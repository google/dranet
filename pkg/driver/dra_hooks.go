/*
Copyright 2024 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/dranet/pkg/apis"
	"github.com/google/dranet/pkg/filter"

	"github.com/Mellanox/rdmamap"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
)

const (
	rdmaCmPath = "/dev/infiniband/rdma_cm"
)

// DRA hooks exposes Network Devices to Kubernetes, the Network devices and its attributes are
// obtained via the netdb to decouple the discovery of the interfaces with the execution.
// The exposed devices can be allocated to one or mod pods via Claim, the Claim lifecycle is
// the ones that defines the lifecycle of a device assigned to a Pod.
// The hooks NodePrepareResources and NodeUnprepareResources are needed to collect the necessary
// information so the NRI hooks can perform the configuration and attachment of Pods at runtime.

func (np *NetworkDriver) PublishResources(ctx context.Context) {
	klog.V(2).Infof("Publishing resources")
	for {
		select {
		case devices := <-np.netdb.GetResources(ctx):
			klog.V(4).Infof("Received %d devices", len(devices))
			devices = filter.FilterDevices(np.celProgram, devices)
			klog.V(4).Infof("After filtering %d devices", len(devices))
			resources := resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					np.nodeName: {Slices: []resourceslice.Slice{{Devices: devices}}}},
			}
			err := np.draPlugin.PublishResources(ctx, resources)
			if err != nil {
				klog.Error(err, "unexpected error trying to publish resources")
			}
		case <-ctx.Done():
			klog.Error(ctx.Err(), "context canceled")
			return
		}
		// poor man rate limit
		time.Sleep(3 * time.Second)
	}
}

func (np *NetworkDriver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	klog.V(2).Infof("PrepareResourceClaims is called: number of claims: %d", len(claims))

	nodePrepareRequestsTotal.Inc()

	if len(claims) == 0 {
		return nil, nil
	}
	result := make(map[types.UID]kubeletplugin.PrepareResult)

	for _, claim := range claims {
		klog.V(2).Infof("NodePrepareResources: Claim Request %s/%s", claim.Namespace, claim.Name)
		result[claim.UID] = np.prepareResourceClaim(ctx, claim)
	}
	return result, nil
}

// prepareResourceClaim gets all the configuration required to be applied at runtime and passes it downs to the handlers.
// This happens in the kubelet so it can be a "slow" operation, so we can execute fast in RunPodsandbox, that happens in the
// container runtime and has strong expectactions to be executed fast (default hook timeout is 2 seconds).
func (np *NetworkDriver) prepareResourceClaim(ctx context.Context, claim *resourceapi.ResourceClaim) kubeletplugin.PrepareResult {
	klog.V(2).Infof("PrepareResourceClaim Claim %s/%s", claim.Namespace, claim.Name)
	start := time.Now()
	defer func() {
		klog.V(2).Infof("PrepareResourceClaim Claim %s/%s  took %v", claim.Namespace, claim.Name, time.Since(start))
	}()
	// TODO: shared devices may allocate the same device to multiple pods, i.e. macvlan, ipvlan, ...
	podUIDs := []types.UID{}
	for _, reserved := range claim.Status.ReservedFor {
		if reserved.Resource != "pods" || reserved.APIGroup != "" {
			klog.Infof("Driver only supports Pods, unsupported reference %#v", reserved)
			continue
		}
		podUIDs = append(podUIDs, reserved.UID)
	}
	if len(podUIDs) == 0 {
		klog.Infof("no pods allocated to claim %s/%s", claim.Namespace, claim.Name)
		return kubeletplugin.PrepareResult{}
	}

	nlHandle, err := netlink.NewHandle()
	if err != nil {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("error creating netlink handle %v", err),
		}
	}

	var errorList []error
	charDevices := sets.New[string]()
	for _, result := range claim.Status.Allocation.Devices.Results {
		// A single ResourceClaim can have devices managed by distinct DRA
		// drivers. One common use case for this is device topology alignment
		// (think NIC and GPU alignment). In such cases, we should ignore the
		// devices which are not managed by our driver.
		//
		// TODO: Test running a different driver alongside DraNet in e2e. This
		//   requires an easy way to spin up a mock DRA driver.
		if result.Driver != np.driverName {
			continue
		}
		requestName := result.Request
		netconf := apis.NetworkConfig{}
		for _, config := range claim.Status.Allocation.Devices.Config {
			// Check there is a config associated to this device
			if config.Opaque == nil ||
				config.Opaque.Driver != np.driverName ||
				len(config.Requests) > 0 && !slices.Contains(config.Requests, requestName) {
				continue
			}
			// Check if there is a custom configuration
			conf, errs := apis.ValidateConfig(&config.Opaque.Parameters)
			if len(errs) > 0 {
				errorList = append(errorList, errs...)
				continue
			}
			// TODO: define a strategy for multiple configs
			if conf != nil {
				netconf = *conf
				break
			}
		}
		klog.V(4).Infof("PrepareResourceClaim %s/%s final Configuration %#v", claim.Namespace, claim.Name, netconf)
		podCfg := PodConfig{
			Claim: types.NamespacedName{
				Namespace: claim.Namespace,
				Name:      claim.Name,
			},
			Network: netconf,
		}
		// TODO(gauravkghildiyal): There's a (self-resolving) race condition
		// here which is triggered when a pod claiming a device is rapidly
		// deleted and recreated. When the pod is deleted,
		// UnprepareResourceClaim deletes the claim and does not wait for the
		// network device to be freed up. Deletion of the claim indicates to the
		// kube-scheduler that the device is available for future use so it can
		// assign the same device to the newly created pod. But it's possible
		// that the old pod's sandbox still exists (and is in the process of
		// being deleted) so the network interface is still in the old pod's
		// network namespace. Although the problem self resolves when kubelet
		// retries PrepareResourceClaims for the new pod, but it should be
		// possible to instrument a better lifecycle.
		ifName, err := np.netdb.GetNetInterfaceName(result.Device)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("failed to get network interface name for device %s: %v", ifName, err))
		}
		// Get Network configuration and merge it
		link, err := nlHandle.LinkByName(ifName)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("failed to get netlink to interface %s: %v", ifName, err))
			continue
		}

		if podCfg.Network.Interface.Name == "" {
			podCfg.Network.Interface.Name = ifName
		}

		// If DHCP is requested, do a DHCP request to gather the network parameters (IPs and Routes)
		// ... but we DO NOT apply them in the root namespace
		if podCfg.Network.Interface.DHCP != nil && *podCfg.Network.Interface.DHCP {
			klog.V(2).Infof("trying to get network configuration via DHCP")
			contextCancel, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			ip, routes, err := getDHCP(contextCancel, ifName)
			if err != nil {
				errorList = append(errorList, fmt.Errorf("fail to get configuration via DHCP for %s: %w", ifName, err))
			} else {
				podCfg.Network.Interface.Addresses = []string{ip}
				podCfg.Network.Routes = append(podCfg.Network.Routes, routes...)
			}
		} else if len(podCfg.Network.Interface.Addresses) == 0 {
			// If there is no custom addresses and no DHCP, then use the existing ones
			// get the existing IP addresses
			nlAddresses, err := nlHandle.AddrList(link, netlink.FAMILY_ALL)
			if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
				errorList = append(errorList, fmt.Errorf("fail to get ip addresses for interface %s : %w", ifName, err))
			} else {
				for _, address := range nlAddresses {
					// Only move IP addresses with global scope because those are not host-specific, auto-configured,
					// or have limited network scope, making them unsuitable inside the container namespace.
					// Ref: https://www.ietf.org/rfc/rfc3549.txt
					if address.Scope != unix.RT_SCOPE_UNIVERSE {
						continue
					}
					podCfg.Network.Interface.Addresses = append(podCfg.Network.Interface.Addresses, address.IPNet.String())
				}
			}
		}

		// Obtain the existing supported ethtool features and validate the config
		if podCfg.Network.Ethtool != nil {
			client, err := newEthtoolClient(0)
			if err != nil {
				errorList = append(errorList, fmt.Errorf("fail to create ethtool client %v", err))
				continue
			}
			defer client.Close()

			ifFeatures, err := client.GetFeatures(ifName)
			if err != nil {
				errorList = append(errorList, fmt.Errorf("fail to get ethtool features %v", err))
				continue
			}

			// translate features to the actual kernel names
			ethtoolFeatures := map[string]bool{}
			for feature, value := range podCfg.Network.Ethtool.Features {
				aliases := ifFeatures.Get(feature)
				if len(aliases) == 0 {
					errorList = append(errorList, fmt.Errorf("feature %s not supported by interface", feature))
					continue
				}
				for _, alias := range aliases {
					ethtoolFeatures[alias] = value
				}
			}
			podCfg.Network.Ethtool.Features = ethtoolFeatures
		}

		// Obtain the routes associated to the interface
		// TODO: only considers outgoing traffic
		filter := &netlink.Route{
			LinkIndex: link.Attrs().Index,
		}
		routes, err := nlHandle.RouteListFiltered(netlink.FAMILY_ALL, filter, netlink.RT_FILTER_OIF)
		if err != nil {
			klog.Infof("fail to get ip routes for interface %s : %v", ifName, err)
		}
		for _, route := range routes {
			routeCfg := apis.RouteConfig{}
			// routes need a destination
			if route.Dst == nil {
				continue
			}
			// Discard IPv6 link-local routes, but allow IPv4 link-local.
			if route.Dst.IP.To4() == nil && route.Dst.IP.IsLinkLocalUnicast() {
				continue
			}
			routeCfg.Destination = route.Dst.String()

			if route.Gw != nil {
				routeCfg.Gateway = route.Gw.String()
			}
			if route.Src != nil {
				routeCfg.Source = route.Src.String()
			}
			routeCfg.Scope = uint8(route.Scope)
			podCfg.Network.Routes = append(podCfg.Network.Routes, routeCfg)
		}

		// Get RDMA configuration: link and char devices
		if rdmaDev, _ := rdmamap.GetRdmaDeviceForNetdevice(ifName); rdmaDev != "" {
			klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", rdmaDev)
			podCfg.RDMADevice.LinkDev = rdmaDev
			// Obtain the char devices associated to the rdma device
			charDevices.Insert(rdmaCmPath)
			charDevices.Insert(rdmamap.GetRdmaCharDevices(rdmaDev)...)
			for _, devpath := range charDevices.UnsortedList() {
				dev, err := GetDeviceInfo(devpath)
				if err != nil {
					klog.Infof("fail to get device info for %s : %v", devpath, err)
				} else {
					podCfg.RDMADevice.DevChars = append(podCfg.RDMADevice.DevChars, dev)
				}
			}
		}

		// Remove the pinned programs before the NRI hooks since it
		// has to walk the entire bpf virtual filesystem and is slow
		// TODO: check if there is some other way to do this
		if podCfg.Network.Interface.DisableEBPFPrograms != nil &&
			*podCfg.Network.Interface.DisableEBPFPrograms {
			err := unpinBPFPrograms(ifName)
			if err != nil {
				klog.Infof("error unpinning ebpf programs for %s : %v", ifName, err)
			}
		}

		// TODO: support for multiple pods sharing the same device
		// we'll create the subinterface here
		for _, uid := range podUIDs {
			np.podConfigStore.Set(uid, result.Device, podCfg)
		}
		klog.V(4).Infof("Claim Resources for pods %v : %#v", podUIDs, podCfg)
	}

	if len(errorList) > 0 {
		klog.Infof("claim %s contain errors: %v", claim.UID, errors.Join(errorList...))
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("claim %s contain errors: %w", claim.UID, errors.Join(errorList...)),
		}
	}
	return kubeletplugin.PrepareResult{}
}

func (np *NetworkDriver) UnprepareResourceClaims(ctx context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	klog.V(2).Infof("UnprepareResourceClaims is called: number of claims: %d", len(claims))
	if len(claims) == 0 {
		return nil, nil
	}

	result := make(map[types.UID]error)
	for _, claim := range claims {
		err := np.unprepareResourceClaim(ctx, claim)
		result[claim.UID] = err
		if err != nil {
			klog.Infof("error unpreparing ressources for claim %s/%s : %v", claim.Namespace, claim.Name, err)
		}
	}
	return result, nil
}

func (np *NetworkDriver) unprepareResourceClaim(_ context.Context, claim kubeletplugin.NamespacedObject) error {
	np.podConfigStore.DeleteClaim(claim.NamespacedName)
	return nil
}

func (np *NetworkDriver) HandleError(ctx context.Context, err error, msg string) {
	// For now we just follow the advice documented in the DRAPlugin API docs.
	// See: https://pkg.go.dev/k8s.io/apimachinery/pkg/util/runtime#HandleErrorWithContext
	runtime.HandleErrorWithContext(ctx, err, msg)
}

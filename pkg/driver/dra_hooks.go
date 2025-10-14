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
	"strings"
	"time"

	"github.com/google/dranet/pkg/apis"
	"github.com/google/dranet/pkg/filter"

	"github.com/Mellanox/rdmamap"
	"github.com/google/dranet/internal/nlwrap"
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
			klog.V(3).Infof("Got %d devices from inventory: %s", len(devices), formatDeviceNames(devices, 15))
			devices = filter.FilterDevices(np.celProgram, devices)
			klog.V(3).Infof("After filtering, publishing %d devices in ResourceSlice(s): %s", len(devices), formatDeviceNames(devices, 15))

			np.publishResourcesPrometheusMetrics(devices)

			resources := resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					np.nodeName: {Slices: []resourceslice.Slice{{Devices: devices}}}},
			}
			err := np.draPlugin.PublishResources(ctx, resources)
			if err != nil {
				klog.Error(err, "unexpected error trying to publish resources")
			} else {
				lastPublishedTime.SetToCurrentTime()
			}
		case <-ctx.Done():
			klog.Error(ctx.Err(), "context canceled")
			return
		}
	}
}

func (np *NetworkDriver) publishResourcesPrometheusMetrics(devices []resourceapi.Device) {
	rdmaCount := 0
	for _, device := range devices {
		if attr, ok := device.Attributes[apis.AttrRDMA]; ok && attr.BoolValue != nil && *attr.BoolValue {
			rdmaCount++
		}
	}
	publishedDevicesTotal.WithLabelValues("rdma").Set(float64(rdmaCount))
	publishedDevicesTotal.WithLabelValues("total").Set(float64(len(devices)))
}

func (np *NetworkDriver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	klog.V(2).Infof("PrepareResourceClaims is called: number of claims: %d", len(claims))
	start := time.Now()
	defer func() {
		draPluginRequestsLatencySeconds.WithLabelValues(methodPrepareResourceClaims).Observe(time.Since(start).Seconds())
	}()
	result, err := np.prepareResourceClaims(ctx, claims)
	if err != nil {
		draPluginRequestsTotal.WithLabelValues(methodPrepareResourceClaims, statusFailed).Inc()
		return result, err
	}
	// identify errors and log metrics
	isError := false
	for _, res := range result {
		if res.Err != nil {
			isError = true
			break
		}
	}
	if isError {
		draPluginRequestsTotal.WithLabelValues(methodPrepareResourceClaims, statusFailed).Inc()
	} else {
		draPluginRequestsTotal.WithLabelValues(methodPrepareResourceClaims, statusSuccess).Inc()
	}
	return result, err
}

func (np *NetworkDriver) prepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
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

	nlHandle, err := nlwrap.NewHandle()
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
			NetworkInterfaceConfigInPod: netconf,
		}
		ifName, err := np.netdb.GetNetInterfaceName(result.Device)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("failed to get network interface name for device %s: %v", result.Device, err))
			continue
		}
		// Get Network configuration and merge it
		link, err := nlHandle.LinkByName(ifName)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("failed to get netlink to interface %s: %v", ifName, err))
			continue
		}
		podCfg.NetworkInterfaceConfigInHost.Interface.Name = ifName

		if podCfg.NetworkInterfaceConfigInPod.Interface.Name == "" {
			// If the interface name was not explicitly overridden, use the same
			// interface name within the pod's network namespace.
			podCfg.NetworkInterfaceConfigInPod.Interface.Name = ifName
		}

		// If DHCP is requested, do a DHCP request to gather the network parameters (IPs and Routes)
		// ... but we DO NOT apply them in the root namespace
		if podCfg.NetworkInterfaceConfigInPod.Interface.DHCP != nil && *podCfg.NetworkInterfaceConfigInPod.Interface.DHCP {
			klog.V(2).Infof("trying to get network configuration via DHCP")
			contextCancel, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			ip, routes, err := getDHCP(contextCancel, ifName)
			if err != nil {
				errorList = append(errorList, fmt.Errorf("fail to get configuration via DHCP for %s: %w", ifName, err))
			} else {
				podCfg.NetworkInterfaceConfigInPod.Interface.Addresses = []string{ip}
				podCfg.NetworkInterfaceConfigInPod.Routes = append(podCfg.NetworkInterfaceConfigInPod.Routes, routes...)
			}
		} else if len(podCfg.NetworkInterfaceConfigInPod.Interface.Addresses) == 0 {
			// If there is no custom addresses and no DHCP, then use the existing ones
			// get the existing IP addresses
			nlAddresses, err := nlHandle.AddrList(link, netlink.FAMILY_ALL)
			if err != nil {
				errorList = append(errorList, fmt.Errorf("fail to get ip addresses for interface %s : %w", ifName, err))
			} else {
				for _, address := range nlAddresses {
					// Only move IP addresses with global scope because those are not host-specific, auto-configured,
					// or have limited network scope, making them unsuitable inside the container namespace.
					// Ref: https://www.ietf.org/rfc/rfc3549.txt
					if address.Scope != unix.RT_SCOPE_UNIVERSE {
						continue
					}
					podCfg.NetworkInterfaceConfigInPod.Interface.Addresses = append(podCfg.NetworkInterfaceConfigInPod.Interface.Addresses, address.IPNet.String())
				}
			}
		}

		// Obtain the existing supported ethtool features and validate the config
		if podCfg.NetworkInterfaceConfigInPod.Ethtool != nil {
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
			for feature, value := range podCfg.NetworkInterfaceConfigInPod.Ethtool.Features {
				aliases := ifFeatures.Get(feature)
				if len(aliases) == 0 {
					errorList = append(errorList, fmt.Errorf("feature %s not supported by interface", feature))
					continue
				}
				for _, alias := range aliases {
					ethtoolFeatures[alias] = value
				}
			}
			podCfg.NetworkInterfaceConfigInPod.Ethtool.Features = ethtoolFeatures
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
			podCfg.NetworkInterfaceConfigInPod.Routes = append(podCfg.NetworkInterfaceConfigInPod.Routes, routeCfg)
		}

		// Obtain the neighbors associated to the interface
		neighs, err := nlHandle.NeighList(link.Attrs().Index, netlink.FAMILY_ALL)
		if err != nil {
			klog.Infof("failed to get neighbors for interface %s: %v", ifName, err)
		}
		for _, neigh := range neighs {
			if neigh.IP == nil || neigh.HardwareAddr == nil {
				continue
			}
			// We are only interested in permanent neighbor entries
			if neigh.State != netlink.NUD_PERMANENT {
				continue
			}
			neighCfg := apis.NeighborConfig{
				Destination:  neigh.IP.String(),
				HardwareAddr: neigh.HardwareAddr.String(),
			}
			podCfg.NetworkInterfaceConfigInPod.Neighbors = append(podCfg.NetworkInterfaceConfigInPod.Neighbors, neighCfg)
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
		if podCfg.NetworkInterfaceConfigInPod.Interface.DisableEBPFPrograms != nil &&
			*podCfg.NetworkInterfaceConfigInPod.Interface.DisableEBPFPrograms {
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
	start := time.Now()
	defer func() {
		draPluginRequestsLatencySeconds.WithLabelValues(methodUnprepareResourceClaims).Observe(time.Since(start).Seconds())
	}()
	result, err := np.unprepareResourceClaims(ctx, claims)
	if err != nil {
		draPluginRequestsTotal.WithLabelValues(methodUnprepareResourceClaims, statusFailed).Inc()
		return result, err
	}
	// identify errors and log metrics
	isError := false
	for _, res := range result {
		if res != nil {
			isError = true
			break
		}
	}
	if isError {
		draPluginRequestsTotal.WithLabelValues(methodUnprepareResourceClaims, statusFailed).Inc()
	} else {
		draPluginRequestsTotal.WithLabelValues(methodUnprepareResourceClaims, statusSuccess).Inc()
	}
	return result, err
}

func (np *NetworkDriver) unprepareResourceClaims(ctx context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
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

func formatDeviceNames(devices []resourceapi.Device, max int) string {
	deviceNames := make([]string, len(devices))
	for i := range devices {
		deviceNames[i] = devices[i].Name
	}

	if len(deviceNames) <= max {
		return strings.Join(deviceNames, ", ")
	}

	return fmt.Sprintf("%s, and %d more", strings.Join(deviceNames[:max], ", "), len(deviceNames)-max)
}

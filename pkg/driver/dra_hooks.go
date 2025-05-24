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
	"github.com/google/dranet/pkg/names"

	"github.com/Mellanox/rdmamap"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
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
			conf, err := apis.ValidateConfig(&config.Opaque.Parameters)
			if err != nil {
				errorList = append(errorList, err)
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
			NetDevice:          netconf.Interface,
			NetNamespaceRoutes: netconf.Routes,
		}
		ifName := names.GetOriginalName(result.Device)
		// Get Network configuration and merge it
		link, err := nlHandle.LinkByName(ifName)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("fail to get network interface %s", ifName))
			continue
		}

		// If there is no custom addresses then use the existing ones
		if len(podCfg.NetDevice.Addresses) == 0 {
			// get the existing IP addresses
			nlAddresses, err := nlHandle.AddrList(link, netlink.FAMILY_ALL)
			if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
				klog.Infof("fail to get ip addresses for interface %s : %v", ifName, err)
			} else {
				for _, address := range nlAddresses {
					// Only move IP addresses with global scope because those are not host-specific, auto-configured,
					// or have limited network scope, making them unsuitable inside the container namespace.
					// Ref: https://www.ietf.org/rfc/rfc3549.txt
					if address.Scope != unix.RT_SCOPE_UNIVERSE {
						continue
					}
					podCfg.NetDevice.Addresses = append(podCfg.NetDevice.Addresses, address.IPNet.String())
				}
			}
		}

		// If there are no addresses configured on the interface and the user is not setting them
		// this may be an interface that uses DHCP, so we bring it up if necessary and do a DHCP
		// request to gather the network parameters (IPs and Routes) ... but we DO NOT apply them
		// in the root namespace
		if len(podCfg.NetDevice.Addresses) == 0 {
			klog.V(2).Infof("trying to get network configuration via DHCP")
			ip, routes, err := getDHCP(ifName)
			if err != nil {
				klog.Infof("fail to get configuration via DHCP: %v", err)
			} else {
				podCfg.NetDevice.Addresses = []string{ip}
				podCfg.NetNamespaceRoutes = append(podCfg.NetNamespaceRoutes, routes...)
			}
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

			if route.Gw == nil || route.Dst == nil {
				continue
			}
			if !route.Dst.IP.IsGlobalUnicast() {
				continue
			}
			routeCfg.Gateway = route.Gw.String()
			routeCfg.Destination = route.Dst.String()
			if route.Src != nil {
				routeCfg.Source = route.Src.String()
			}
			podCfg.NetNamespaceRoutes = append(podCfg.NetNamespaceRoutes, routeCfg)
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

		device := kubeletplugin.Device{
			Requests:   []string{result.Request},
			PoolName:   result.Pool,
			DeviceName: result.Device,
		}
		// TODO: support for multiple pods sharing the same device
		// we'll create the subinterface here
		for _, uid := range podUIDs {
			np.podConfigStore.Set(uid, device.DeviceName, podCfg)
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

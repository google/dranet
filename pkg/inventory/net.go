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

package inventory

import (
	"fmt"
	"net"

	"github.com/Mellanox/rdmamap"
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/google/dranet/pkg/names"
	"github.com/vishvananda/netlink"

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

func getDefaultGwInterfaces() sets.Set[string] {
	interfaces := sets.Set[string]{}
	filter := &netlink.Route{}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, filter, netlink.RT_FILTER_TABLE)
	if err != nil {
		return interfaces
	}

	for _, r := range routes {
		if r.Family != netlink.FAMILY_V4 && r.Family != netlink.FAMILY_V6 {
			continue
		}

		if r.Dst != nil && !r.Dst.IP.IsUnspecified() {
			continue
		}

		// no multipath
		if len(r.MultiPath) == 0 {
			if r.Gw == nil {
				continue
			}
			intfLink, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				klog.Infof("Failed to get interface link for route %v : %v", r, err)
				continue
			}
			interfaces.Insert(intfLink.Attrs().Name)
		}

		for _, nh := range r.MultiPath {
			if nh.Gw == nil {
				continue
			}
			intfLink, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				klog.Infof("Failed to get interface link for route %v : %v", r, err)
				continue
			}
			interfaces.Insert(intfLink.Attrs().Name)
		}
	}
	klog.V(4).Infof("Found following interfaces for the default gateway: %v", interfaces.UnsortedList())
	return interfaces
}

func getTcFilters(link netlink.Link) ([]string, bool) {
	isTcEBPF := false
	filterNames := sets.Set[string]{}
	for _, parent := range []uint32{netlink.HANDLE_MIN_INGRESS, netlink.HANDLE_MIN_EGRESS} {
		filters, err := netlink.FilterList(link, parent)
		if err == nil {
			for _, f := range filters {
				if bpffFilter, ok := f.(*netlink.BpfFilter); ok {
					isTcEBPF = true
					filterNames.Insert(bpffFilter.Name)
				}
			}
		}
	}
	return filterNames.UnsortedList(), isTcEBPF
}

// see https://github.com/cilium/ebpf/issues/1117
func getTcxFilters(device netlink.Link) ([]string, bool) {
	isTcxEBPF := false
	programNames := sets.Set[string]{}
	for _, attach := range []ebpf.AttachType{ebpf.AttachTCXIngress, ebpf.AttachTCXEgress} {
		result, err := link.QueryPrograms(link.QueryOptions{
			Target: int(device.Attrs().Index),
			Attach: attach,
		})
		if err != nil {
			continue
		}

		isTcxEBPF = true
		for _, p := range result.Programs {
			prog, err := ebpf.NewProgramFromID(p.ID)
			if err != nil {
				continue
			}
			defer prog.Close()

			pi, err := prog.Info()
			if err != nil {
				continue
			}
			programNames.Insert(pi.Name)
		}
	}
	return programNames.UnsortedList(), isTcxEBPF
}

// discoverNetlinkDevices scans for kernel network interfaces
func (db *DB) discoverNetlinkDevices() (map[string]*resourceapi.Device, error) {
	klog.V(4).Info("Starting netlink device discovery...")
	devices := make(map[string]*resourceapi.Device)

	// TODO: it is not common but may happen in edge cases that the default gateway changes
	// revisit once we have more evidence this can be a potential problem or break some use
	// cases.
	gwInterfaces := getDefaultGwInterfaces()
	nlHandle, err := netlink.NewHandle()
	if err != nil {
		return devices, fmt.Errorf("error creating netlink handle %v", err)
	}

	// Don't return early - we want print to user at end of function
	// @vsoch This is logic from previous refactored version.
	ifaces, err := nlHandle.LinkList()
	if err != nil {
		klog.Error(err, "unexpected error trying to get system interfaces")
	}

	for _, iface := range ifaces {
		attrs := iface.Attrs()

		klog.V(7).InfoS("Checking network interface", "name", attrs.Name)
		if gwInterfaces.Has(attrs.Name) {
			klog.V(4).Infof("iface %s is an uplink interface", attrs.Name)
			continue
		}

		if ignoredInterfaceNames.Has(attrs.Name) {
			klog.V(4).Infof("iface %s is in the list of ignored interfaces", attrs.Name)
			continue
		}

		// Skip loopback interfaces.
		if attrs.Flags&net.FlagLoopback != 0 {
			continue
		}

		// Publish this network interface.
		device, err := db.netdevToDRAdev(iface)
		if err != nil {
			klog.V(2).Infof("could not obtain attributes for iface %s : %v", attrs.Name, err)
			continue
		}

		// This could be error prone if a missing address leads to a second entry in rdma devices.
		pciAddress, err := bdfAddress(attrs.Name, rdmamap.RdmaClassDir)
		pciAddr := pciAddress.device
		if err != nil {
			klog.Warningf("could not get PCI address for netdev %s, using fallback key. error: %v", attrs.Name, err)
			pciAddr = "netdev-" + attrs.Name
		}
		devices[pciAddr] = device
	}
	klog.V(4).Infof("Finished netlink discovery. Found %d devices.", len(devices))
	return devices, nil
}

// discoverRawRdmaDevices scans for raw RDMA devices using rdmamap listing
func (db *DB) discoverRawRdmaDevices() map[string]*resourceapi.Device {
	klog.V(4).Info("Starting raw RDMA device discovery...")
	devices := make(map[string]*resourceapi.Device)

	// This was tested to work to list an Infiniband device without an associated netlink.
	deviceNames := rdmamap.GetRdmaDeviceList()

	for _, rdmaName := range deviceNames {
		pciAddr, err := bdfAddress(rdmaName, rdmamap.RdmaClassDir)

		// Assume that a missing PCI address would be missing for both netlink and rdma (not sure if this is true).
		// I think there are cases when we wouldn't have one, but I want to be conservative and only
		// allow RDMA interfaces with associated PCI addresses. This can change if needed.
		if err != nil {
			klog.Warningf("could not get PCI address for RDMA device %s, skipping: %v", rdmaName, err)
			continue
		}
		sanitizedName := names.SetDeviceName(rdmaName)

		// Create a new resourceapi device for the RDMA raw device.
		device := &resourceapi.Device{
			Name: sanitizedName,
			Basic: &resourceapi.BasicDevice{
				Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					"dra.net/rdma":   {BoolValue: ptr.To(true)},
					"dra.net/ifName": {StringValue: &rdmaName},
					// https://github.com/vishvananda/netlink/blob/master/nl/nl_linux.go#L143
					// This could also be ib, but "infiniband" is more clear
					"dra.net/type": {StringValue: ptr.To("infiniband")},
				},
			},
		}
		addPCIAttributes(device.Basic, rdmaName, rdmamap.RdmaClassDir)
		devices[pciAddr.device] = device
	}
	klog.V(4).Infof("Finished raw RDMA discovery. Found %d devices.", len(devices))
	return devices
}

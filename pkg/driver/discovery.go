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
	"log"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Mellanox/rdmamap"
	"github.com/vishvananda/netlink"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/klog/v2"
)

const (
	sysnetPath = "/sys/class/net"
	// Each of the entries in this directory is a symbolic link
	// representing one of the real or virtual networking devices
	// that are visible in the network namespace of the process
	// that is accessing the directory.  Each of these symbolic
	// links refers to entries in the /sys/devices directory.
	// https://man7.org/linux/man-pages/man5/sysfs.5.html
	sysdevPath = "/sys/devices"
)

var dns1123LabelNonValid = regexp.MustCompile("[^a-z0-9-]")

// https://man7.org/linux/man-pages/man5/pci.ids.5.html
// The pci.ids file is generated from the PCI ID database, which is
// maintained at ⟨https://pci-ids.ucw.cz/⟩.  If you find any IDs
// missing from the list, please contribute them to the database.

func realpath(ifName string) string {
	linkPath := filepath.Join(sysnetPath, ifName)
	dst, err := os.Readlink(linkPath)
	if err != nil {
		log.Fatal(err)
	}
	var dstAbs string
	if filepath.IsAbs(dst) {
		dstAbs = dst
	} else {
		// Symlink targets are relative to the directory containing the link.
		dstAbs = filepath.Join(filepath.Dir(linkPath), dst)
	}
	return dstAbs
}

func isVirtual(ifName string) bool {
	ok, _ := filepath.Match(filepath.Join(sysnetPath, "virtual/*"), realpath(ifName))
	return ok
}

func netdevToDRAdev(ifName string) (*resourceapi.Device, error) {
	device := resourceapi.Device{
		Name: ifName,
		Basic: &resourceapi.BasicDevice{
			Attributes: make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute),
			Capacity:   make(map[resourceapi.QualifiedName]resourceapi.DeviceCapacity),
		},
	}
	// normalize the name because interface names may contain invalid
	// characters as object names
	if len(validation.IsDNS1123Label(ifName)) > 0 {
		klog.V(2).Infof("normalizing iface %s name", ifName)
		device.Name = "norm-" + dns1123LabelNonValid.ReplaceAllString(ifName, "-")
	}

	device.Basic.Attributes["name"] = resourceapi.DeviceAttribute{StringValue: &ifName}
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		klog.Infof("Error getting link by name %v", err)
		return nil, err
	}
	linkType := link.Type()
	linkAttrs := link.Attrs()
	// TODO we can get more info from the kernel
	// https://www.kernel.org/doc/Documentation/ABI/testing/sysfs-class-net
	// Ref: https://github.com/canonical/lxd/blob/main/lxd/resources/network.go

	// sriov device plugin has a more detailed and better discovery
	// https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/blob/ed1c14dd4c313c7dd9fe4730a60358fbeffbfdd4/cmd/sriovdp/manager.go#L243

	if ips, err := netlink.AddrList(link, netlink.FAMILY_ALL); err == nil && len(ips) > 0 {
		// TODO assume only one addres by now
		ip := ips[0].String()
		device.Basic.Attributes["ip"] = resourceapi.DeviceAttribute{StringValue: &ip}
		mac := link.Attrs().HardwareAddr.String()
		device.Basic.Attributes["mac"] = resourceapi.DeviceAttribute{StringValue: &mac}
		mtu := int64(link.Attrs().MTU)
		device.Basic.Attributes["mtu"] = resourceapi.DeviceAttribute{IntValue: &mtu}
	}

	device.Basic.Attributes["encapsulation"] = resourceapi.DeviceAttribute{StringValue: &linkAttrs.EncapType}
	operState := linkAttrs.OperState.String()
	device.Basic.Attributes["state"] = resourceapi.DeviceAttribute{StringValue: &operState}
	device.Basic.Attributes["alias"] = resourceapi.DeviceAttribute{StringValue: &linkAttrs.Alias}
	device.Basic.Attributes["type"] = resourceapi.DeviceAttribute{StringValue: &linkType}

	isRDMA := rdmamap.IsRDmaDeviceForNetdevice(ifName)
	device.Basic.Attributes["rdma"] = resourceapi.DeviceAttribute{BoolValue: &isRDMA}
	// from https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/blob/ed1c14dd4c313c7dd9fe4730a60358fbeffbfdd4/pkg/netdevice/netDeviceProvider.go#L99
	isSRIOV := sriovTotalVFs(ifName) > 0
	device.Basic.Attributes["sriov"] = resourceapi.DeviceAttribute{BoolValue: &isSRIOV}
	if isSRIOV {
		vfs := int64(sriovNumVFs(ifName))
		device.Basic.Attributes["sriov_vfs"] = resourceapi.DeviceAttribute{IntValue: &vfs}
	}

	// check if is a Cloud instance and add the cloud provider attributes
	if instance == nil {
		return &device, nil
	}

	mac := link.Attrs().HardwareAddr.String()
	// this is bounded and small number O(N) is ok
	for _, cloudInterface := range instance.Interfaces {
		if cloudInterface.Mac == mac {
			device.Basic.Attributes["cloudNetwork"] = resourceapi.DeviceAttribute{StringValue: &cloudInterface.Network}
			break
		}
	}
	return &device, nil
}

func rdmaToDRAdev(ifName string) (*resourceapi.Device, error) {
	device := resourceapi.Device{
		Name: ifName,
		Basic: &resourceapi.BasicDevice{
			Attributes: make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute),
			Capacity:   make(map[resourceapi.QualifiedName]resourceapi.DeviceCapacity),
		},
	}
	// normalize the name because interface names may contain invalid
	// characters as object names
	if len(validation.IsDNS1123Label(ifName)) > 0 {
		klog.V(2).Infof("normalizing iface %s name", ifName)
		device.Name = "norm-" + dns1123LabelNonValid.ReplaceAllString(ifName, "-")
	}

	device.Basic.Attributes["name"] = resourceapi.DeviceAttribute{StringValue: &ifName}
	link, err := netlink.RdmaLinkByName(ifName)
	if err != nil {
		klog.Infof("Error getting link by name %v", err)
		return nil, err
	}

	device.Basic.Attributes["firmwareVersion"] = resourceapi.DeviceAttribute{StringValue: &link.Attrs.FirmwareVersion}
	device.Basic.Attributes["nodeGuid"] = resourceapi.DeviceAttribute{StringValue: &link.Attrs.NodeGuid}

	return &device, nil
}

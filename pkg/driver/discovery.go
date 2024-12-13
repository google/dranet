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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/dranet/pkg/pcidb"

	"github.com/Mellanox/rdmamap"
	"github.com/vishvananda/netlink"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	sysnetPath  = "/sys/class/net"
	sysrdmaPath = "/sys/class/infiniband"
	// Each of the entries in this directory is a symbolic link
	// representing one of the real or virtual networking devices
	// that are visible in the network namespace of the process
	// that is accessing the directory.  Each of these symbolic
	// links refers to entries in the /sys/devices directory.
	// https://man7.org/linux/man-pages/man5/sysfs.5.html
	sysdevPath = "/sys/devices"
)

var (
	dns1123LabelNonValid = regexp.MustCompile("[^a-z0-9-]")

	networkKind = "network"
	rdmaKind    = "rdma"
)

func realpath(ifName string) string {
	linkPath := filepath.Join(sysnetPath, ifName)
	dst, err := os.Readlink(linkPath)
	if err != nil {
		klog.Error(err, "unexpected error trying reading link", "link", linkPath)
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
	device.Basic.Attributes["kind"] = resourceapi.DeviceAttribute{StringValue: &networkKind}
	device.Basic.Attributes["name"] = resourceapi.DeviceAttribute{StringValue: &ifName}
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		klog.Infof("Error getting link by name %v", err)
		return nil, err
	}
	linkType := link.Type()
	linkAttrs := link.Attrs()

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

	sysfsPath := realpath(ifName)
	if ok, _ := filepath.Match(filepath.Join(sysnetPath, "virtual/*"), sysfsPath); ok {
		device.Basic.Attributes["virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	} else {
		device.Basic.Attributes["virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(false)}
		addPCIAttributes(device.Basic, ifName, sysnetPath)
	}

	// check if is a Cloud instance and add the cloud provider attributes
	if instance == nil {
		return &device, nil
	}

	mac := link.Attrs().HardwareAddr.String()
	// this is bounded and small number O(N) is ok
	for _, cloudInterface := range instance.Interfaces {
		if cloudInterface.Mac == mac {
			device.Basic.Attributes["cloud_network"] = resourceapi.DeviceAttribute{StringValue: &cloudInterface.Network}
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

	device.Basic.Attributes["kind"] = resourceapi.DeviceAttribute{StringValue: &rdmaKind}
	device.Basic.Attributes["name"] = resourceapi.DeviceAttribute{StringValue: &ifName}
	device.Basic.Attributes["rdma"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	link, err := netlink.RdmaLinkByName(ifName)
	if err != nil {
		klog.Infof("Error getting link by name %v", err)
		return nil, err
	}

	device.Basic.Attributes["firmwareVersion"] = resourceapi.DeviceAttribute{StringValue: &link.Attrs.FirmwareVersion}
	device.Basic.Attributes["nodeGuid"] = resourceapi.DeviceAttribute{StringValue: &link.Attrs.NodeGuid}
	sysfsPath := realpath(ifName)
	if ok, _ := filepath.Match(filepath.Join(sysrdmaPath, "virtual/*"), sysfsPath); ok {
		device.Basic.Attributes["virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	} else {
		device.Basic.Attributes["virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(false)}
		addPCIAttributes(device.Basic, ifName, sysrdmaPath)
	}
	return &device, nil
}

func addPCIAttributes(draDevice *resourceapi.BasicDevice, ifName string, syspath string) {
	attributes := []resourceapi.DeviceAttribute{}
	// /sys/class/net/<interface>/device/numa_node
	numeNode, err := os.ReadFile(filepath.Join(syspath, ifName, "device/numa_node"))
	if err == nil {
		numa, err := strconv.ParseInt(strings.TrimSpace(string(numeNode)), 10, 32)
		if err == nil {
			attributes = append(attributes)
			draDevice.Attributes["numa_node"] = resourceapi.DeviceAttribute{IntValue: &numa}
		}
	}
	// https://docs.kernel.org/PCI/sysfs-pci.html
	// realpath /sys/class/net/ens4/device
	// /sys/devices/pci0000:00/0000:00:04.0/virtio1
	// The topmost element describes the PCI domain and bus number.
	// PCI domain: 0000 Bus: 00 Device: 04 Function: 0
	s := strings.Split(filepath.Join(sysdevPath, ifName), "/")
	if len(s) == 5 {
		pci := strings.Split(s[3], ":")
		if len(pci) == 3 {
			draDevice.Attributes["pci_domain"] = resourceapi.DeviceAttribute{StringValue: &pci[0]}
			draDevice.Attributes["pci_bus"] = resourceapi.DeviceAttribute{StringValue: &pci[1]}
			f := strings.Split(pci[2], ".")
			if len(f) == 2 {
				draDevice.Attributes["pci_device"] = resourceapi.DeviceAttribute{StringValue: &f[0]}
				draDevice.Attributes["pci_function"] = resourceapi.DeviceAttribute{StringValue: &f[1]}
			}
		}
	}

	// PCI data
	var device, subsystemVendor, subsystemDevice []byte
	vendor, err := os.ReadFile(filepath.Join(sysdevPath, ifName, "device/vendor"))
	if err == nil {
		device, err = os.ReadFile(filepath.Join(sysdevPath, ifName, "device/device"))
		if err == nil {
			subsystemVendor, err = os.ReadFile(filepath.Join(sysdevPath, ifName, "device/subsystem_vendor"))
			if err == nil {
				subsystemDevice, err = os.ReadFile(filepath.Join(sysdevPath, ifName, "device/subsystem_device"))
			}
		}
		vendor := strings.TrimSpace(string(vendor))
		device := strings.TrimSpace(string(device))
		subsystemVendor := strings.TrimSpace(string(subsystemVendor))
		subsystemDevice := strings.TrimSpace(string(subsystemDevice))

		entry, err := pcidb.GetDevice(vendor, device, subsystemVendor, subsystemDevice)
		if err != nil {
			klog.Error(err, "error trying to get pci details")
			return
		}
		if entry.Vendor != "" {
			draDevice.Attributes["pci_vendor"] = resourceapi.DeviceAttribute{StringValue: &entry.Vendor}
		}
		if entry.Device != "" {
			draDevice.Attributes["pci_device"] = resourceapi.DeviceAttribute{StringValue: &entry.Device}
		}
		if entry.Subsystem != "" {
			draDevice.Attributes["pci_subsystem"] = resourceapi.DeviceAttribute{StringValue: &entry.Subsystem}
		}
	}
}

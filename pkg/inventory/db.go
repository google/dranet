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
	"context"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/Mellanox/rdmamap"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/time/rate"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	networkKind = "network"
	rdmaKind    = "rdma"

	// database poll period
	minInterval = 5 * time.Second
	maxInterval = 1 * time.Minute
)

var (
	dns1123LabelNonValid = regexp.MustCompile("[^a-z0-9-]")
)

type DB struct {
	instance *cloudInstance

	mu       sync.RWMutex
	podStore map[int]string // key: netnsid path value: Pod namespace/name

	rateLimiter   *rate.Limiter
	notifications chan []resourceapi.Device
}

type Device struct {
	Kind string
	Name string
}

func New() *DB {
	return &DB{
		rateLimiter:   rate.NewLimiter(rate.Every(minInterval), 1),
		podStore:      map[int]string{},
		notifications: make(chan []resourceapi.Device),
	}
}

func (db *DB) AddPodNetns(pod string, netnsPath string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	ns, err := netns.GetFromPath(netnsPath)
	if err != nil {
		klog.Infof("fail to get pod %s network namespace %s handle: %v", pod, netnsPath, err)
		return
	}
	id, err := netlink.GetNetNsIdByFd(int(ns))
	if err != nil {
		klog.Infof("fail to get pod %s network namespace %s netnsid: %v", pod, netnsPath, err)
		return
	}
	db.podStore[id] = pod
}

func (db *DB) RemovePodNetns(pod string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for k, v := range db.podStore {
		if v == pod {
			delete(db.podStore, k)
			return
		}
	}
}

func (db *DB) GetPodName(netnsid int) string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.podStore[netnsid]
}

func (db *DB) Run(ctx context.Context) error {
	defer close(db.notifications)
	// Resources are published periodically or if there is a netlink notification
	// indicating a new interfaces was added or changed
	nlChannel := make(chan netlink.LinkUpdate)
	doneCh := make(chan struct{})
	defer close(doneCh)
	if err := netlink.LinkSubscribe(nlChannel, doneCh); err != nil {
		klog.Error(err, "error subscribing to netlink interfaces, only syncing periodically", "interval", maxInterval.String())
	}

	// Obtain data that will not change after the startup
	db.instance = getInstanceProperties(ctx)
	// TODO: it is not common but may happen in edge cases that the default gateway changes
	// revisit once we have more evidence this can be a potential problem or break some use
	// cases.
	gwInterfaces := getDefaultGwInterfaces()

	for {
		err := db.rateLimiter.Wait(ctx)
		if err != nil {
			klog.Error(err, "unexpected rate limited error trying to get system interfaces")
		}

		devices := []resourceapi.Device{}
		ifaces, err := net.Interfaces()
		if err != nil {
			klog.Error(err, "unexpected error trying to get system interfaces")
		}
		for _, iface := range ifaces {
			klog.V(7).InfoS("Checking network interface", "name", iface.Name)
			if gwInterfaces.Has(iface.Name) {
				klog.V(4).Infof("iface %s is an uplink interface", iface.Name)
				continue
			}

			// skip loopback interfaces
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			// publish this network interface
			device, err := db.netdevToDRAdev(iface.Name)
			if err != nil {
				klog.V(2).Infof("could not obtain attributes for iface %s : %v", iface.Name, err)
				continue
			}

			devices = append(devices, *device)
			klog.V(4).Infof("Found following network interface %s", iface.Name)
		}

		// List RDMA devices
		rdmaIfaces, err := netlink.RdmaLinkList()
		if err != nil {
			klog.Error(err, "could not obtain the list of RDMA resources")
		}

		for _, iface := range rdmaIfaces {
			klog.V(7).InfoS("Checking rdma interface", "name", iface.Attrs.Name)
			// publish this RDMA interface
			device, err := db.rdmaToDRAdev(iface.Attrs.Name)
			if err != nil {
				klog.V(2).Infof("could not obtain attributes for iface %s : %v", iface.Attrs.Name, err)
				continue
			}

			devices = append(devices, *device)
			klog.V(4).Infof("Found following rdma interface %s", iface.Attrs.Name)
		}

		klog.V(4).Infof("Found %d devices", len(devices))
		if len(devices) > 0 {
			db.notifications <- devices
		}
		select {
		// trigger a reconcile
		case <-nlChannel:
			// drain the channel so we only sync once
			for len(nlChannel) > 0 {
				<-nlChannel
			}
		case <-time.After(maxInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (db *DB) GetResources(ctx context.Context) <-chan []resourceapi.Device {
	return db.notifications
}

func (db *DB) netdevToDRAdev(ifName string) (*resourceapi.Device, error) {
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
		device.Name = "normalized-" + dns1123LabelNonValid.ReplaceAllString(ifName, "-")
	}
	device.Basic.Attributes["kind"] = resourceapi.DeviceAttribute{StringValue: ptr.To(networkKind)}
	device.Basic.Attributes["name"] = resourceapi.DeviceAttribute{StringValue: &ifName}
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		klog.Infof("Error getting link by name %v", err)
		return nil, err
	}
	linkType := link.Type()
	linkAttrs := link.Attrs()

	// identify the namespace holding the link as the other end of a veth pair
	netnsid := link.Attrs().NetNsID
	if podName := db.GetPodName(netnsid); podName != "" {
		device.Basic.Attributes["pod"] = resourceapi.DeviceAttribute{StringValue: &podName}
	}

	if ips, err := netlink.AddrList(link, netlink.FAMILY_ALL); err == nil && len(ips) > 0 {
		// TODO assume only one address by now but prefer the global ones
		var v6found, v4found bool
		for _, address := range ips {
			if v4found && v6found {
				break
			}
			if !address.IP.IsGlobalUnicast() {
				continue
			}

			if address.IP.To4() == nil && !v6found {
				device.Basic.Attributes["ipv6"] = resourceapi.DeviceAttribute{StringValue: ptr.To(address.IP.String())}
			} else if !v4found {
				device.Basic.Attributes["ip"] = resourceapi.DeviceAttribute{StringValue: ptr.To(address.IP.String())}
			}
		}
		if !v4found {
			device.Basic.Attributes["ip"] = resourceapi.DeviceAttribute{StringValue: ptr.To(ips[0].String())}
		}
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

	if isVirtual(ifName, sysnetPath) {
		device.Basic.Attributes["virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	} else {
		addPCIAttributes(device.Basic, ifName, sysnetPath)
	}

	mac := link.Attrs().HardwareAddr.String()
	// this is bounded and small number O(N) is ok
	if network := cloudNetwork(mac, db.instance); network != "" {
		device.Basic.Attributes["cloud_network"] = resourceapi.DeviceAttribute{StringValue: &network}
	}

	return &device, nil
}

func (db *DB) rdmaToDRAdev(ifName string) (*resourceapi.Device, error) {
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

	device.Basic.Attributes["kind"] = resourceapi.DeviceAttribute{StringValue: ptr.To(rdmaKind)}
	device.Basic.Attributes["name"] = resourceapi.DeviceAttribute{StringValue: &ifName}
	device.Basic.Attributes["rdma"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	link, err := netlink.RdmaLinkByName(ifName)
	if err != nil {
		klog.Infof("Error getting link by name %v", err)
		return nil, err
	}

	device.Basic.Attributes["firmwareVersion"] = resourceapi.DeviceAttribute{StringValue: &link.Attrs.FirmwareVersion}
	device.Basic.Attributes["nodeGuid"] = resourceapi.DeviceAttribute{StringValue: &link.Attrs.NodeGuid}

	if isVirtual(ifName, sysrdmaPath) {
		device.Basic.Attributes["virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	} else {
		addPCIAttributes(device.Basic, ifName, sysrdmaPath)
	}
	return &device, nil
}

func addPCIAttributes(device *resourceapi.BasicDevice, ifName string, path string) {
	device.Attributes["virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(false)}

	address, err := bdfAddress(ifName, path)
	if err == nil {
		if address.domain != "" {
			device.Attributes["pci_address_domain"] = resourceapi.DeviceAttribute{StringValue: &address.domain}
		}
		if address.bus != "" {
			device.Attributes["pci_address_bus"] = resourceapi.DeviceAttribute{StringValue: &address.bus}
		}
		if address.device != "" {
			device.Attributes["pci_address_device"] = resourceapi.DeviceAttribute{StringValue: &address.device}
		}
		if address.function != "" {
			device.Attributes["pci_address_function"] = resourceapi.DeviceAttribute{StringValue: &address.function}
		}
	} else {
		klog.Infof("could not get pci address : %v", err)
	}

	entry, err := ids(ifName, path)
	if err == nil {
		if entry.Vendor != "" {
			device.Attributes["pci_vendor"] = resourceapi.DeviceAttribute{StringValue: &entry.Vendor}
		}
		if entry.Device != "" {
			device.Attributes["pci_device"] = resourceapi.DeviceAttribute{StringValue: &entry.Device}
		}
		if entry.Subsystem != "" {
			device.Attributes["pci_subsystem"] = resourceapi.DeviceAttribute{StringValue: &entry.Subsystem}
		}
	} else {
		klog.Infof("could not get pci vendor information : %v", err)
	}

	numa, err := numaNode(ifName, path)
	if err == nil {
		device.Attributes["numa_node"] = resourceapi.DeviceAttribute{IntValue: &numa}
	}
}

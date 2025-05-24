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
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Mellanox/rdmamap"
	"github.com/google/dranet/pkg/cloudprovider"
	"github.com/google/dranet/pkg/names"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/time/rate"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	// database poll period
	minInterval = 5 * time.Second
	maxInterval = 1 * time.Minute
)

var (
	// ignoredInterfaceNames is a set of network interface names that are typically
	// created by CNI plugins or are otherwise not relevant for DRA resource exposure.
	ignoredInterfaceNames = sets.New("cilium_net", "cilium_host")
)

type DB struct {
	instance *cloudprovider.CloudInstance

	mu         sync.RWMutex
	podStore   map[int]string    // key: netnsid path value: Pod namespace/name
	podNsStore map[string]string // key pod value: netns path

	rateLimiter   *rate.Limiter
	notifications chan []resourceapi.Device
}

func New() *DB {
	return &DB{
		rateLimiter:   rate.NewLimiter(rate.Every(minInterval), 1),
		podStore:      map[int]string{},
		podNsStore:    map[string]string{},
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
	defer ns.Close()
	id, err := netlink.GetNetNsIdByFd(int(ns))
	if err != nil {
		klog.Infof("fail to get pod %s network namespace %s netnsid: %v", pod, netnsPath, err)
		return
	}
	db.podStore[id] = pod
	db.podNsStore[pod] = netnsPath
}

func (db *DB) RemovePodNetns(pod string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.podNsStore, pod)
	for k, v := range db.podStore {
		if v == pod {
			delete(db.podStore, k)
			return
		}
	}
}

// GetPodName allows to get the Pod name from the namespace Id
// that comes in the link id from the veth pair interface
func (db *DB) GetPodName(netnsid int) string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.podStore[netnsid]
}

// GetPodNamespace allows to get the Pod network namespace
func (db *DB) GetPodNamespace(pod string) string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.podNsStore[pod]
}

func (db *DB) Run(ctx context.Context) error {
	defer close(db.notifications)

	nlHandle, err := netlink.NewHandle()
	if err != nil {
		return fmt.Errorf("error creating netlink handle %v", err)
	}
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
		ifaces, err := nlHandle.LinkList()
		if err != nil {
			klog.Error(err, "unexpected error trying to get system interfaces")
		}
		for _, iface := range ifaces {
			klog.V(7).InfoS("Checking network interface", "name", iface.Attrs().Name)
			if gwInterfaces.Has(iface.Attrs().Name) {
				klog.V(4).Infof("iface %s is an uplink interface", iface.Attrs().Name)
				continue
			}

			if ignoredInterfaceNames.Has(iface.Attrs().Name) {
				klog.V(4).Infof("iface %s is in the list of ignored interfaces", iface.Attrs().Name)
				continue
			}

			// skip loopback interfaces
			if iface.Attrs().Flags&net.FlagLoopback != 0 {
				continue
			}

			// publish this network interface
			device, err := db.netdevToDRAdev(iface)
			if err != nil {
				klog.V(2).Infof("could not obtain attributes for iface %s : %v", iface.Attrs().Name, err)
				continue
			}

			devices = append(devices, *device)
			klog.V(4).Infof("Found following network interface %s", iface.Attrs().Name)
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

func (db *DB) netdevToDRAdev(link netlink.Link) (*resourceapi.Device, error) {
	ifName := link.Attrs().Name
	device := resourceapi.Device{
		Basic: &resourceapi.BasicDevice{
			Attributes: make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute),
			Capacity:   make(map[resourceapi.QualifiedName]resourceapi.DeviceCapacity),
		},
	}
	// Set the device name. It will be normalized only if necessary.
	device.Name = names.SetDeviceName(ifName)
	// expose the real interface name as an attribute in case it is normalized.
	device.Basic.Attributes["dra.net/ifName"] = resourceapi.DeviceAttribute{StringValue: &ifName}

	linkType := link.Type()
	linkAttrs := link.Attrs()

	// identify the namespace holding the link as the other end of a veth pair
	netnsid := link.Attrs().NetNsID
	if podName := db.GetPodName(netnsid); podName != "" {
		device.Basic.Attributes["dra.net/pod"] = resourceapi.DeviceAttribute{StringValue: &podName}
	}

	v4 := sets.Set[string]{}
	v6 := sets.Set[string]{}
	if ips, err := netlink.AddrList(link, netlink.FAMILY_ALL); err == nil && len(ips) > 0 {
		for _, address := range ips {
			if !address.IP.IsGlobalUnicast() {
				continue
			}

			if address.IP.To4() == nil && address.IP.To16() != nil {
				v6.Insert(address.IP.String())
			} else if address.IP.To4() != nil {
				v4.Insert(address.IP.String())
			}
		}
		if v4.Len() > 0 {
			device.Basic.Attributes["dra.net/ipv4"] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(v4.UnsortedList(), ","))}
		}
		if v6.Len() > 0 {
			device.Basic.Attributes["dra.net/ipv6"] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(v6.UnsortedList(), ","))}
		}
		mac := link.Attrs().HardwareAddr.String()
		device.Basic.Attributes["dra.net/mac"] = resourceapi.DeviceAttribute{StringValue: &mac}
		mtu := int64(link.Attrs().MTU)
		device.Basic.Attributes["dra.net/mtu"] = resourceapi.DeviceAttribute{IntValue: &mtu}
	}

	device.Basic.Attributes["dra.net/encapsulation"] = resourceapi.DeviceAttribute{StringValue: &linkAttrs.EncapType}
	operState := linkAttrs.OperState.String()
	device.Basic.Attributes["dra.net/state"] = resourceapi.DeviceAttribute{StringValue: &operState}
	device.Basic.Attributes["dra.net/alias"] = resourceapi.DeviceAttribute{StringValue: &linkAttrs.Alias}
	device.Basic.Attributes["dra.net/type"] = resourceapi.DeviceAttribute{StringValue: &linkType}

	isRDMA := rdmamap.IsRDmaDeviceForNetdevice(ifName)
	device.Basic.Attributes["dra.net/rdma"] = resourceapi.DeviceAttribute{BoolValue: &isRDMA}
	// from https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/blob/ed1c14dd4c313c7dd9fe4730a60358fbeffbfdd4/pkg/netdevice/netDeviceProvider.go#L99
	isSRIOV := sriovTotalVFs(ifName) > 0
	device.Basic.Attributes["dra.net/sriov"] = resourceapi.DeviceAttribute{BoolValue: &isSRIOV}
	if isSRIOV {
		vfs := int64(sriovNumVFs(ifName))
		device.Basic.Attributes["dra.net/sriovVfs"] = resourceapi.DeviceAttribute{IntValue: &vfs}
	}

	if isVirtual(ifName, sysnetPath) {
		device.Basic.Attributes["dra.net/virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	} else {
		addPCIAttributes(device.Basic, ifName, sysnetPath)
	}

	mac := link.Attrs().HardwareAddr.String()
	// this is bounded and small number O(N) is ok
	if network := cloudNetwork(mac, db.instance); network != "" {
		device.Basic.Attributes["dra.net/cloudNetwork"] = resourceapi.DeviceAttribute{StringValue: &network}
	}

	return &device, nil
}

func addPCIAttributes(device *resourceapi.BasicDevice, ifName string, path string) {
	device.Attributes["dra.net/virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(false)}

	address, err := bdfAddress(ifName, path)
	if err == nil {
		if address.domain != "" {
			device.Attributes["dra.net/pciAddressDomain"] = resourceapi.DeviceAttribute{StringValue: &address.domain}
		}
		if address.bus != "" {
			device.Attributes["dra.net/pciAddressBus"] = resourceapi.DeviceAttribute{StringValue: &address.bus}
		}
		if address.device != "" {
			device.Attributes["dra.net/pciAddressDevice"] = resourceapi.DeviceAttribute{StringValue: &address.device}
		}
		if address.function != "" {
			device.Attributes["dra.net/pciAddressFunction"] = resourceapi.DeviceAttribute{StringValue: &address.function}
		}
	} else {
		klog.Infof("could not get pci address : %v", err)
	}

	entry, err := ids(ifName, path)
	if err == nil {
		if entry.Vendor != "" {
			device.Attributes["dra.net/pciVendor"] = resourceapi.DeviceAttribute{StringValue: &entry.Vendor}
		}
		if entry.Device != "" {
			device.Attributes["dra.net/pciDevice"] = resourceapi.DeviceAttribute{StringValue: &entry.Device}
		}
		if entry.Subsystem != "" {
			device.Attributes["dra.net/pciSubsystem"] = resourceapi.DeviceAttribute{StringValue: &entry.Subsystem}
		}
	} else {
		klog.Infof("could not get pci vendor information : %v", err)
	}

	numa, err := numaNode(ifName, path)
	if err == nil {
		device.Attributes["dra.net/numaNode"] = resourceapi.DeviceAttribute{IntValue: &numa}
	}
}

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
	"maps"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Mellanox/rdmamap"
	"github.com/google/dranet/pkg/cloudprovider"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/time/rate"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/dynamic-resource-allocation/deviceattribute"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	// database poll period
	minInterval             = 5 * time.Second
	simpleDiscoveryInterval = 1 * time.Minute
	fullDiscoveryInterval   = 5 * time.Minute
)

var (
	// ignoredInterfaceNames is a set of network interface names that are typically
	// created by CNI plugins or are otherwise not relevant for DRA resource exposure.
	ignoredInterfaceNames = sets.New("cilium_net", "cilium_host", "docker")
)

type DB struct {
	instance *cloudprovider.CloudInstance

	mu            sync.RWMutex
	podStore      map[int]string // key: netnsid path value: Pod namespace/name
	podNsStore    map[string]string
	deviceStore   map[string]*resourceapi.Device
	rateLimiter   *rate.Limiter
	notifications chan []resourceapi.Device

	nriSyncCh <-chan struct{}

	fullDiscoveryInterval   time.Duration
	simpleDiscoveryInterval time.Duration
}

func New(nriSyncCh <-chan struct{}) *DB {
	return &DB{
		rateLimiter:             rate.NewLimiter(rate.Every(minInterval), 1),
		podStore:                map[int]string{},
		podNsStore:              map[string]string{},
		deviceStore:             map[string]*resourceapi.Device{},
		notifications:           make(chan []resourceapi.Device),
		nriSyncCh:               nriSyncCh,
		fullDiscoveryInterval:   fullDiscoveryInterval,
		simpleDiscoveryInterval: simpleDiscoveryInterval,
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

// GetPodNetNamespace returns the network namespace of the pod.
func (db *DB) GetPodNetNamespace(pod string) string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.podNsStore[pod]
}

func (db *DB) listPodNetNamespaces() map[string]string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return maps.Clone(db.podNsStore)
}

func (db *DB) GetDevice(deviceName string) (*resourceapi.Device, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	dev, ok := db.deviceStore[deviceName]
	return dev, ok
}

// GetInterfaceName returns the interface name for a given normalized device name.
func (db *DB) GetInterfaceName(deviceName string) (string, bool) {
	device, ok := db.GetDevice(deviceName)
	if !ok {
		return "", false
	}
	ifNameAttr, ok := device.Attributes["dra.net/ifName"]
	if !ok || ifNameAttr.StringValue != nil && *ifNameAttr.StringValue == "" {
		return "", false
	}
	return *ifNameAttr.StringValue, true
}

func (db *DB) Run(ctx context.Context) error {
	defer close(db.notifications)

	// Wait for the NRI plugin to be synchronized
	<-db.nriSyncCh

	// Obtain data that will not change after the startup
	db.instance = getInstanceProperties(ctx)
	// TODO: it is not common but may happen in edge cases that the default gateway changes
	// revisit once we have more evidence this can be a potential problem or break some use
	// cases.
	gwInterfaces := getDefaultGwInterfaces()

	// Run initial full discovery
	if err := db.discoverDevices(true, gwInterfaces); err != nil {
		klog.Errorf("Initial full device discovery failed: %v", err)
	}

	fullDiscoveryTicker := time.NewTicker(db.fullDiscoveryInterval)
	defer fullDiscoveryTicker.Stop()

	simpleDiscoveryTicker := time.NewTicker(db.simpleDiscoveryInterval)
	defer simpleDiscoveryTicker.Stop()

	nlChannel := make(chan netlink.LinkUpdate)
	doneCh := make(chan struct{})
	defer close(doneCh)
	if err := netlink.LinkSubscribe(nlChannel, doneCh); err != nil {
		klog.Error(err, "error subscribing to netlink interfaces, only syncing periodically", "interval", db.simpleDiscoveryInterval.String())
	}

	for {
		err := db.rateLimiter.Wait(ctx)
		if err != nil {
			klog.Error(err, "unexpected rate limited error trying to get system interfaces")
		}

		select {
		case <-fullDiscoveryTicker.C:
			if err := db.discoverDevices(true, gwInterfaces); err != nil {
				klog.Errorf("full device discovery failed: %v", err)
			}
		case <-simpleDiscoveryTicker.C:
			if err := db.discoverDevices(false, gwInterfaces); err != nil {
				klog.Errorf("simple device discovery failed: %v", err)
			}
		case <-nlChannel:
			// drain the channel so we only sync once
			for len(nlChannel) > 0 {
				<-nlChannel
			}
			if err := db.discoverDevices(false, gwInterfaces); err != nil {
				klog.Errorf("simple device discovery failed: %v", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (db *DB) discoverDevices(full bool, gwInterfaces sets.Set[string]) error {
	var netNamespaces []netns.NsHandle
	var err error

	// Always scan the host network namespace
	hostNs, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get host network namespace: %w", err)
	}
	netNamespaces = append(netNamespaces, hostNs)

	if full {
		// In case of full device discovery, also scan the pod's network namespaces.
		for pod, nsPath := range db.listPodNetNamespaces() {
			if nsPath == "" {
				continue
			}
			ns, err := netns.GetFromPath(nsPath)
			if err != nil {
				klog.Errorf("Failed to get netns handle for pod %q having network namespace %q: %v", pod, nsPath, err)
				continue
			}
			netNamespaces = append(netNamespaces, ns)
		}
	}

	newDeviceStore := make(map[string]*resourceapi.Device)

	for _, ns := range netNamespaces {
		nlHandle, err := netlink.NewHandleAt(ns)
		if err != nil {
			klog.Errorf("failed to create netlink handle for netns %v: %v", ns, err)
			continue
		}

		ifaces, err := nlHandle.LinkList()
		if err != nil {
			klog.Errorf("failed to list links for netns %v: %v", ns, err)
			continue
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
			device, err := db.netdevToDRAdev(nlHandle, iface)
			if err != nil {
				klog.V(2).Infof("could not obtain attributes for iface %s : %v", iface.Attrs().Name, err)
				continue
			}
			if device == nil {
				continue
			}
			newDeviceStore[device.Name] = device
			klog.V(4).Infof("Found following network interface %s", iface.Attrs().Name)
		}

		ns.Close()
		nlHandle.Close()
	}

	klog.V(4).Infof("Found %d devices (full discovery mode: %t)", len(newDeviceStore), full)

	db.mu.Lock()
	if full {
		db.deviceStore = newDeviceStore
	} else {
		maps.Copy(db.deviceStore, newDeviceStore)
	}
	devices := make([]resourceapi.Device, 0, len(db.deviceStore))
	for _, device := range db.deviceStore {
		devices = append(devices, *device)
	}
	defer db.mu.Unlock()

	db.notifications <- devices

	return nil
}

func (db *DB) GetResources(ctx context.Context) <-chan []resourceapi.Device {
	return db.notifications
}

func (db *DB) netdevToDRAdev(nlHandle *netlink.Handle, link netlink.Link) (*resourceapi.Device, error) {
	permMAC := link.Attrs().PermHWAddr.String()
	if permMAC == "" {
		return nil, nil // Skip interfaces without a permanent MAC address
	}

	ifName := link.Attrs().Name
	device := resourceapi.Device{
		Attributes: make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute),
		Capacity:   make(map[resourceapi.QualifiedName]resourceapi.DeviceCapacity),
	}
	// Set the device name from the permanent MAC address.
	device.Name = "mac-" + strings.ReplaceAll(permMAC, ":", "-")
	// expose the real interface name as an attribute.
	device.Attributes["dra.net/ifName"] = resourceapi.DeviceAttribute{StringValue: &ifName}
	// expose the permanent MAC address as an attribute.
	device.Attributes["dra.net/permMac"] = resourceapi.DeviceAttribute{StringValue: &permMAC}

	linkType := link.Type()
	linkAttrs := link.Attrs()

	// identify the namespace holding the link as the other end of a veth pair
	netnsid := link.Attrs().NetNsID
	if podName := db.GetPodName(netnsid); podName != "" {
		device.Attributes["dra.net/pod"] = resourceapi.DeviceAttribute{StringValue: &podName}
	}

	v4 := sets.Set[string]{}
	v6 := sets.Set[string]{}
	if ips, err := nlHandle.AddrList(link, netlink.FAMILY_ALL); err == nil && len(ips) > 0 {
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
			device.Attributes["dra.net/ipv4"] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(v4.UnsortedList(), ","))}
		}
		if v6.Len() > 0 {
			device.Attributes["dra.net/ipv6"] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(v6.UnsortedList(), ","))}
		}
		mac := link.Attrs().HardwareAddr.String()
		device.Attributes["dra.net/mac"] = resourceapi.DeviceAttribute{StringValue: &mac}
		mtu := int64(link.Attrs().MTU)
		device.Attributes["dra.net/mtu"] = resourceapi.DeviceAttribute{IntValue: &mtu}
	}

	device.Attributes["dra.net/encapsulation"] = resourceapi.DeviceAttribute{StringValue: &linkAttrs.EncapType}
	operState := linkAttrs.OperState.String()
	device.Attributes["dra.net/state"] = resourceapi.DeviceAttribute{StringValue: &operState}
	device.Attributes["dra.net/alias"] = resourceapi.DeviceAttribute{StringValue: &linkAttrs.Alias}
	device.Attributes["dra.net/type"] = resourceapi.DeviceAttribute{StringValue: &linkType}

	// Get eBPF properties from the interface using the legacy tc hooks
	isEbpf := false
	filterNames, ok := getTcFilters(link)
	if ok {
		isEbpf = true
		device.Attributes["dra.net/tcFilterNames"] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(filterNames, ","))}
	}

	// Get eBPF properties from the interface using the tcx hooks
	programNames, ok := getTcxFilters(link)
	if ok {
		isEbpf = true
		device.Attributes["dra.net/tcxProgramNames"] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(programNames, ","))}
	}
	device.Attributes["dra.net/ebpf"] = resourceapi.DeviceAttribute{BoolValue: &isEbpf}

	isRDMA := rdmamap.IsRDmaDeviceForNetdevice(ifName)
	device.Attributes["dra.net/rdma"] = resourceapi.DeviceAttribute{BoolValue: &isRDMA}
	// from https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/blob/ed1c14dd4c313c7dd9fe4730a60358fbeffbfdd4/pkg/netdevice/netDeviceProvider.go#L99
	isSRIOV := sriovTotalVFs(ifName) > 0
	device.Attributes["dra.net/sriov"] = resourceapi.DeviceAttribute{BoolValue: &isSRIOV}
	if isSRIOV {
		vfs := int64(sriovNumVFs(ifName))
		device.Attributes["dra.net/sriovVfs"] = resourceapi.DeviceAttribute{IntValue: &vfs}
	}

	if isVirtual(ifName, sysnetPath) {
		device.Attributes["dra.net/virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	} else {
		addPCIAttributes(&device, link.Attrs())
	}

	for name, attribute := range getProviderAttributes(permMAC, db.instance) {
		device.Attributes[name] = attribute
	}

	return &device, nil
}

func addPCIAttributes(device *resourceapi.Device, netLinkAttrs *netlink.LinkAttrs) {
	device.Attributes["dra.net/virtual"] = resourceapi.DeviceAttribute{BoolValue: ptr.To(false)}

	if err := setPciRootAttr(device, netLinkAttrs.ParentDev); err != nil {
		klog.Infof("Could not get pci root attribute : %v", err)
	}

	parentDevicePath := "/sys/bus/" + netLinkAttrs.ParentDevBus + "/devices/" + netLinkAttrs.ParentDev

	entry, err := ids(parentDevicePath)
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

	numa, err := numaNode(parentDevicePath)
	if err == nil {
		device.Attributes["dra.net/numaNode"] = resourceapi.DeviceAttribute{IntValue: &numa}
	}
}

func setPciRootAttr(device *resourceapi.Device, pciAddr string) error {
	pcieRootAttr, err := deviceattribute.GetPCIeRootAttributeByPCIBusID(pciAddr)
	if err != nil {
		return err
	}
	device.Attributes[pcieRootAttr.Name] = pcieRootAttr.Value
	return nil
}

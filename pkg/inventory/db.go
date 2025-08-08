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
	"strings"
	"sync"
	"time"

	"github.com/google/dranet/pkg/apis"
	"github.com/google/dranet/pkg/cloudprovider"

	"github.com/Mellanox/rdmamap"
	"github.com/jaypipes/ghw"
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
	minInterval = 5 * time.Second
	maxInterval = 1 * time.Minute
)

type DB struct {
	instance *cloudprovider.CloudInstance

	mu sync.RWMutex
	// netNsForPod gives the network namespace for a pod, indexed by the pods
	// "namespaced/name".
	netNsForPod map[string]string
	// deviceStore is an in-memory cache of the available devices on the node.
	// It is keyed by the normalized PCI address of the device. The value is a
	// resourceapi.Device object that contains the device's attributes.
	// The deviceStore is periodically updated by the Run method.
	deviceStore map[string]*resourceapi.Device

	rateLimiter *rate.Limiter
	// syncCh is a channel that signals a request for an immediate device
	// discovery (rateLimiter is NOT ignored.)
	syncCh        chan any
	notifications chan []resourceapi.Device
	hasDevices    bool
}

func New() *DB {
	return &DB{
		rateLimiter:   rate.NewLimiter(rate.Every(minInterval), 1),
		syncCh:        make(chan any, 1),
		netNsForPod:   map[string]string{},
		notifications: make(chan []resourceapi.Device),
	}
}

func (db *DB) Run(ctx context.Context) error {
	defer close(db.notifications)

	// Obtain data that will not change after the startup
	db.instance = getInstanceProperties(ctx)

	for {
		err := db.rateLimiter.Wait(ctx)
		if err != nil {
			klog.Error(err, "unexpected rate limited error trying to get system interfaces")
		}

		pci, err := ghw.PCI()
		if err != nil {
			return fmt.Errorf("error getting PCI info: %v", err)
		}

		devices := []resourceapi.Device{}
		for _, pciDev := range pci.Devices {
			if !isNetworkDevice(pciDev) {
				continue
			}
			// TODO(gauravkghildiyal): Exclude device for default interface.
			devices = append(devices, *db.pciToDRAdev(pciDev))
		}

		// Future improvement: We have identified the relevant physical network
		// devices. If need be, we could now inspect the network namespaces
		// (indicated by `netNsForPod`) to determine the current state of
		// network interfaces. This can be achieved by finding interfaces within
		// those namespaces that have a PCI address matching a device in our
		// deviceStore.

		klog.V(4).Infof("Found %d devices", len(devices))
		if len(devices) > 0 || db.hasDevices {
			db.hasDevices = len(devices) > 0
			db.updateDeviceStore(devices)
			db.notifications <- devices
		}
		select {
		case <-time.After(maxInterval):
		case <-db.syncCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (db *DB) Sync() {
	select {
	case db.syncCh <- struct{}{}:
	default:
	}
}

func (db *DB) GetResources(ctx context.Context) <-chan []resourceapi.Device {
	return db.notifications
}

func (db *DB) pciToDRAdev(pciDev *ghw.PCIDevice) *resourceapi.Device {
	device := &resourceapi.Device{
		Name:       NormalizePCIAddress(pciDev.Address),
		Attributes: make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute),
		Capacity:   make(map[resourceapi.QualifiedName]resourceapi.DeviceCapacity),
	}

	db.addPCIAttributes(device, pciDev)
	db.addNetDevAttributes(device, pciDev.Address)
	db.addRDMADevAttributes(device, pciDev.Address)
	db.addCloudProviderAttributes(device)

	return device
}

func (db *DB) addPCIAttributes(device *resourceapi.Device, pciDev *ghw.PCIDevice) {
	if pciDev.Vendor != nil {
		device.Attributes[apis.AttrPCIVendor] = resourceapi.DeviceAttribute{StringValue: &pciDev.Vendor.Name}
	}
	if pciDev.Product != nil {
		device.Attributes[apis.AttrPCIDevice] = resourceapi.DeviceAttribute{StringValue: &pciDev.Product.Name}
	}
	if pciDev.Subsystem != nil {
		device.Attributes[apis.AttrPCISubsystem] = resourceapi.DeviceAttribute{StringValue: &pciDev.Subsystem.ID}
	}

	if pciDev.Node != nil {
		device.Attributes[apis.AttrNUMANode] = resourceapi.DeviceAttribute{IntValue: ptr.To(int64(pciDev.Node.ID))}
	}

	pcieRootAttr, err := deviceattribute.GetPCIeRootAttributeByPCIBusID(pciDev.Address)
	if err != nil {
		klog.Infof("Could not get pci root attribute: %v", err)
	} else {
		device.Attributes[pcieRootAttr.Name] = pcieRootAttr.Value
	}
}

func (db *DB) addNetDevAttributes(device *resourceapi.Device, pciAddress string) {
	ifName, err := GetNetworkInterface(pciAddress)
	if err != nil {
		klog.Infof("Could not get network interface for pci device %s: %v; Will re-use any existing device attributes.", pciAddress, err)
		prevDevice, exists := db.GetDevice(NormalizePCIAddress(pciAddress))
		if exists {
			// This merging is a best-effort attempt to preserve device attributes
			// when a network interface is not in the root namespace (e.g., inside a
			// pod). It relies entirely on the existing `deviceStore` cache.
			//
			// This approach has a key limitation: if the agent restarts, the cache
			// is empty. If an interface is already in a pod's namespace at startup,
			// its network-related attributes cannot be discovered and will be missing
			// until the interface is returned to the host.
			//
			// This is an acceptable limitation because the missing attributes
			// are mutable (e.g., IP address, Interface Name). Users should base
			// resource selections on stable, immutable device properties (like
			// PCI Root, RDMA type) rather than transient interface state.
			mergeDeviceAttributes(device, prevDevice,
				apis.AttrInterfaceName,
				apis.AttrMac,
				apis.AttrEncapsulation,
				apis.AttrAlias,
				apis.AttrState,
				apis.AttrType,
				apis.AttrIPv4,
				apis.AttrIPv6,
				apis.AttrTCFilterNames,
				apis.AttrTCXProgramNames,
				apis.AttrEBPF,
				apis.AttrSRIOV,
				apis.AttrSRIOVVfs,
				apis.AttrVirtual,
			)
		}
		return
	}
	// Expose the interface name as an attribute.
	device.Attributes[apis.AttrInterfaceName] = resourceapi.DeviceAttribute{StringValue: &ifName}

	link, err := netlink.LinkByName(ifName)
	if err != nil {
		klog.Infof("Could not get link for interface %s: %v", ifName, err)
		return
	}

	device.Attributes[apis.AttrMac] = resourceapi.DeviceAttribute{StringValue: ptr.To(link.Attrs().HardwareAddr.String())}
	device.Attributes[apis.AttrMTU] = resourceapi.DeviceAttribute{IntValue: ptr.To(int64(link.Attrs().MTU))}
	device.Attributes[apis.AttrEncapsulation] = resourceapi.DeviceAttribute{StringValue: ptr.To(link.Attrs().EncapType)}
	device.Attributes[apis.AttrAlias] = resourceapi.DeviceAttribute{StringValue: ptr.To(link.Attrs().Alias)}
	device.Attributes[apis.AttrState] = resourceapi.DeviceAttribute{StringValue: ptr.To(link.Attrs().OperState.String())}
	device.Attributes[apis.AttrType] = resourceapi.DeviceAttribute{StringValue: ptr.To(link.Type())}

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
			device.Attributes[apis.AttrIPv4] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(v4.UnsortedList(), ","))}
		}
		if v6.Len() > 0 {
			device.Attributes[apis.AttrIPv6] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(v6.UnsortedList(), ","))}
		}
	}

	// Get eBPF properties from the interface using the legacy tc hooks
	isEbpf := false
	filterNames, ok := getTcFilters(link)
	if ok {
		isEbpf = true
		device.Attributes[apis.AttrTCFilterNames] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(filterNames, ","))}
	}

	// Get eBPF properties from the interface using the tcx hooks
	programNames, ok := getTcxFilters(link)
	if ok {
		isEbpf = true
		device.Attributes[apis.AttrTCXProgramNames] = resourceapi.DeviceAttribute{StringValue: ptr.To(strings.Join(programNames, ","))}
	}
	device.Attributes[apis.AttrEBPF] = resourceapi.DeviceAttribute{BoolValue: &isEbpf}

	// from https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/blob/ed1c14dd4c313c7dd9fe4730a60358fbeffbfdd4/pkg/netdevice/netDeviceProvider.go#L99
	isSRIOV := sriovTotalVFs(ifName) > 0
	device.Attributes[apis.AttrSRIOV] = resourceapi.DeviceAttribute{BoolValue: &isSRIOV}
	if isSRIOV {
		vfs := int64(sriovNumVFs(ifName))
		device.Attributes[apis.AttrSRIOVVfs] = resourceapi.DeviceAttribute{IntValue: &vfs}
	}

	if isVirtual(ifName, sysnetPath) {
		device.Attributes[apis.AttrVirtual] = resourceapi.DeviceAttribute{BoolValue: ptr.To(true)}
	} else {
		device.Attributes[apis.AttrVirtual] = resourceapi.DeviceAttribute{BoolValue: ptr.To(false)}
	}
}

func (db *DB) addRDMADevAttributes(device *resourceapi.Device, pciAddress string) {
	rdmaDevices := rdmamap.GetRdmaDevicesForPcidev(pciAddress)
	isRDMA := len(rdmaDevices) != 0
	device.Attributes[apis.AttrRDMA] = resourceapi.DeviceAttribute{BoolValue: &isRDMA}
}

func (db *DB) addCloudProviderAttributes(device *resourceapi.Device) {
	mac := device.Attributes[apis.AttrMac]
	if mac.StringValue == nil {
		return
	}
	maps.Copy(device.Attributes, getProviderAttributes(*mac.StringValue, db.instance))
}

func (db *DB) AddPodNetNs(pod string, netNsPath string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	ns, err := netns.GetFromPath(netNsPath)
	if err != nil {
		klog.Infof("fail to get pod %s network namespace %s handle: %v", pod, netNsPath, err)
		return
	}
	defer ns.Close()
	db.netNsForPod[pod] = netNsPath
}

func (db *DB) RemovePodNetNs(pod string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.netNsForPod, pod)
}

// GetPodNamespace allows to get the Pod network namespace
func (db *DB) GetPodNetNs(pod string) string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.netNsForPod[pod]
}

func (db *DB) updateDeviceStore(devices []resourceapi.Device) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.deviceStore = map[string]*resourceapi.Device{}
	for _, device := range devices {
		db.deviceStore[device.Name] = &device
	}
}

func (db *DB) GetDevice(deviceName string) (*resourceapi.Device, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	device, exists := db.deviceStore[deviceName]
	return device, exists
}

func (db *DB) GetNetInterfaceName(deviceName string) (string, error) {
	pciAddress := DeNormalizePCIAddress(deviceName)
	device, exists := db.GetDevice(deviceName)
	if !exists {
		klog.Infof("device %s not found in store, using sysfs to get interface name", deviceName)
		return GetNetworkInterface(pciAddress)
	}
	if device.Attributes[apis.AttrInterfaceName].StringValue == nil {
		klog.Infof("device %s has no interface name in store, using sysfs to get interface name", deviceName)
		return GetNetworkInterface(pciAddress)
	}
	return *device.Attributes[apis.AttrInterfaceName].StringValue, nil
}

// mergeDeviceAttributes copies a selective list of attributes from a source device
// to a destination device. This is useful in scenarios where a device's state
// cannot be fully determined (e.g., a network interface is down), allowing the
// driver to preserve and reuse previously known attributes from the device store.
func mergeDeviceAttributes(dest *resourceapi.Device, src *resourceapi.Device, attrsToCopy ...resourceapi.QualifiedName) {
	if dest == nil || src == nil || src.Attributes == nil {
		return
	}
	if dest.Attributes == nil {
		dest.Attributes = make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute)
	}
	for _, attr := range attrsToCopy {
		if val, ok := src.Attributes[attr]; ok {
			dest.Attributes[attr] = val
		}
	}
}

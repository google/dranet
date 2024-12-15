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
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/dranet/pkg/pcidb"
	"k8s.io/klog/v2"
)

const (
	// https://www.kernel.org/doc/Documentation/ABI/testing/sysfs-class-net
	sysnetPath  = "/sys/class/net/"
	sysrdmaPath = "/sys/class/infiniband"
	// Each of the entries in this directory is a symbolic link
	// representing one of the real or virtual networking devices
	// that are visible in the network namespace of the process
	// that is accessing the directory.  Each of these symbolic
	// links refers to entries in the /sys/devices directory.
	// https://man7.org/linux/man-pages/man5/sysfs.5.html
	sysdevPath = "/sys/devices"
)

func realpath(ifName string, syspath string) string {
	linkPath := filepath.Join(syspath, ifName)
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

// $ realpath /sys/class/net/cilium_host
// /sys/devices/virtual/net/cilium_host
func isVirtual(name string, syspath string) bool {
	sysfsPath := realpath(name, syspath)
	prefix := filepath.Join(sysdevPath, "virtual")
	return strings.HasPrefix(sysfsPath, prefix)
}

func sriovTotalVFs(name string) int {
	totalVfsPath := filepath.Join(sysnetPath, name, "/device/sriov_totalvfs")
	totalBytes, err := os.ReadFile(totalVfsPath)
	if err != nil {
		klog.V(7).Infof("error trying to get total VFs for device %s: %v", name, err)
		return 0
	}
	total := bytes.TrimSpace(totalBytes)
	t, err := strconv.Atoi(string(total))
	if err != nil {
		klog.Errorf("Error in obtaining maximum supported number of virtual functions for network interface: %s: %v", name, err)
		return 0
	}
	return t
}

func sriovNumVFs(name string) int {
	numVfsPath := filepath.Join(sysnetPath, name, "/device/sriov_numvfs")
	numBytes, err := os.ReadFile(numVfsPath)
	if err != nil {
		klog.V(7).Infof("error trying to get number of VFs for device %s: %v", name, err)
		return 0
	}
	num := bytes.TrimSpace(numBytes)
	t, err := strconv.Atoi(string(num))
	if err != nil {
		klog.Errorf("Error in obtaining number of virtual functions for network interface: %s: %v", name, err)
		return 0
	}
	return t
}

func numaNode(ifName string, syspath string) (int64, error) {
	// /sys/class/net/<interface>/device/numa_node
	numeNode, err := os.ReadFile(filepath.Join(syspath, ifName, "device/numa_node"))
	if err != nil {
		return 0, err
	}
	numa, err := strconv.ParseInt(strings.TrimSpace(string(numeNode)), 10, 32)
	if err != nil {
		return 0, err
	}
	return numa, nil
}

// pciAddress BDF Notation
// [domain:]bus:device.function
// https://wiki.xenproject.org/wiki/Bus:Device.Function_(BDF)_Notation
type pciAddress struct {
	// There might be several independent sets of PCI devices
	// (e.g. several host PCI controllers on a mainboard chipset)
	domain string
	bus    string
	device string
	// One PCI device (e.g. pluggable card) may implement several functions
	// (e.g. sound card and joystick controller used to be a common combo),
	// so PCI provides for up to 8 separate functions on a single PCI device.
	function string
}

func bdfAddress(ifName string, path string) (*pciAddress, error) {
	address := &pciAddress{}
	// https://docs.kernel.org/PCI/sysfs-pci.html
	// realpath /sys/class/net/ens4/device
	// /sys/devices/pci0000:00/0000:00:04.0/virtio1
	// The topmost element describes the PCI domain and bus number.
	// PCI domain: 0000 Bus: 00 Device: 04 Function: 0
	sysfsPath := realpath(ifName, path)
	bfd := strings.Split(sysfsPath, "/")
	if len(bfd) < 5 {
		return nil, fmt.Errorf("could not find corresponding PCI address: %v", bfd)
	}

	klog.V(4).Infof("pci address: %s", bfd[4])
	pci := strings.Split(bfd[4], ":")
	// Simple BDF notation
	switch len(pci) {
	case 2:
		address.bus = pci[0]
		f := strings.Split(pci[1], ".")
		if len(f) != 2 {
			return nil, fmt.Errorf("could not find corresponding PCI device and function: %v", pci)
		}
		address.device = f[0]
		address.function = f[1]
	case 3:
		address.domain = pci[0]
		address.bus = pci[1]
		f := strings.Split(pci[2], ".")
		if len(f) != 2 {
			return nil, fmt.Errorf("could not find corresponding PCI device and function: %v", pci)
		}
		address.device = f[0]
		address.function = f[1]
	default:
		return nil, fmt.Errorf("could not find corresponding PCI address: %v", pci)
	}
	return address, nil
}

func ids(ifName string, path string) (*pcidb.Entry, error) {
	// PCI data
	var device, subsystemVendor, subsystemDevice []byte
	vendor, err := os.ReadFile(filepath.Join(path, ifName, "device/vendor"))
	if err != nil {
		return nil, err
	}
	// device, subsystemVendor and subsystemDevice are best effort
	device, err = os.ReadFile(filepath.Join(sysdevPath, ifName, "device/device"))
	if err == nil {
		subsystemVendor, err = os.ReadFile(filepath.Join(sysdevPath, ifName, "device/subsystem_vendor"))
		if err == nil {
			subsystemDevice, _ = os.ReadFile(filepath.Join(sysdevPath, ifName, "device/subsystem_device"))
		}
	}

	// remove the 0x prefix
	entry, err := pcidb.GetDevice(
		strings.TrimPrefix(strings.TrimSpace(string(vendor)), "0x"),
		strings.TrimPrefix(strings.TrimSpace(string(device)), "0x"),
		strings.TrimPrefix(strings.TrimSpace(string(subsystemVendor)), "0x"),
		strings.TrimPrefix(strings.TrimSpace(string(subsystemDevice)), "0x"),
	)

	if err != nil {
		return nil, err
	}
	return entry, nil
}

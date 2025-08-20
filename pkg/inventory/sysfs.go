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
	"regexp"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const (
	// https://www.kernel.org/doc/Documentation/ABI/testing/sysfs-class-net
	sysnetPath = "/sys/class/net/"
	// Each of the entries in this directory is a symbolic link
	// representing one of the real or virtual networking devices
	// that are visible in the network namespace of the process
	// that is accessing the directory.  Each of these symbolic
	// links refers to entries in the /sys/devices directory.
	// https://man7.org/linux/man-pages/man5/sysfs.5.html
	sysdevPath = "/sys/devices"
)

// pciAddressRegex is used to identify a PCI address within a string.
// It matches patterns like "0000:00:04.0" or "00:04.0".
var pciAddressRegex = regexp.MustCompile(`^(?:([0-9a-fA-F]{4}):)?([0-9a-fA-F]{2}):([0-9a-fA-F]{2})\.([0-9a-fA-F])$`)

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

func (a pciAddress) String() string {
	if a.domain == "" {
		return fmt.Sprintf("%s:%s.%s", a.bus, a.device, a.function)
	}
	return fmt.Sprintf("%s:%s:%s.%s", a.domain, a.bus, a.device, a.function)
}

// The PCI root is the root PCI device, derived from the
// pciAddress of a device. Spec is defined from the DRA KEP.
// https://github.com/kubernetes/enhancements/pull/5316
type pciRoot struct {
	domain string
	// The root may have a different host bus than the PCI device.
	// e.g https://uefi.org/specs/UEFI/2.10/14_Protocols_PCI_Bus_Support.html#server-system-with-four-pci-root-bridges
	bus string
}

// parsePCIAddress takes a string and attempts to extract and parse a PCI address from it.
func parsePCIAddress(s string) (*pciAddress, error) {
	matches := pciAddressRegex.FindStringSubmatch(s)
	if matches == nil {
		return nil, fmt.Errorf("could not find PCI address in string: %s", s)
	}
	address := &pciAddress{}

	// When pciAddressRegex matches, it is expected to return 5 elements. (First
	// is the complete matched string itself, and the next 4 are the submatches
	// corresponding to Domain:Bus:Device.Function). Examples:
	// 	- "0000:00:04.0" -> ["0000:00:04.0" "0000" "00" "04" "0"]
	// 	- "00:05.0" -> ["0000:00:05.0" "" "00" "05" "0"]
	if len(matches) == 5 {
		address.domain = matches[1]
		address.bus = matches[2]
		address.device = matches[3]
		address.function = matches[4]
	} else {
		return nil, fmt.Errorf("invalid PCI address format: %s", s)
	}

	return address, nil
}

// pciAddressFromPath takes a full sysfs path and traverses it upwards to find
// the first component that contains a valid PCI address.
func pciAddressFromPath(path string) (*pciAddress, error) {
	parts := strings.Split(path, "/")
	for len(parts) > 0 {
		current := parts[len(parts)-1]
		addr, err := parsePCIAddress(current)
		if err == nil {
			return addr, nil
		}
		parts = parts[:len(parts)-1]
	}
	return nil, fmt.Errorf("could not find PCI address in path: %s", path)
}

// pciAddressForNetInterface finds the PCI address for a given network interface name.
func pciAddressForNetInterface(ifName string) (*pciAddress, error) {
	// First, find the absolute path of the device in the sysfs, which typically
	// looks like:
	// /sys/devices/pci0000:8c/0000:8c:00.0/0000:8d:00.0/0000:8e:02.0/0000:91:00.0/net/eth0
	// Then, use pciAddressFromPath() to traverse the path upwards, checking
	// each component to find the first one that matches the format of a PCI
	// address.
	sysfsPath := realpath(ifName, sysnetPath)
	address, err := pciAddressFromPath(sysfsPath)
	if err != nil {
		return nil, fmt.Errorf("could not find PCI address for interface %q: %w", ifName, err)
	}
	return address, nil
}

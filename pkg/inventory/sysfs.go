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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/dranet/pkg/pcidb"
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

func numaNode(devicePath string) (int64, error) {
	numeNode, err := os.ReadFile(filepath.Join(devicePath, "numa_node"))
	if err != nil {
		return 0, err
	}
	numa, err := strconv.ParseInt(strings.TrimSpace(string(numeNode)), 10, 32)
	if err != nil {
		return 0, err
	}
	return numa, nil
}

func ids(devicePath string) (*pcidb.Entry, error) {
	// PCI data
	var device, subsystemVendor, subsystemDevice []byte
	vendor, err := os.ReadFile(filepath.Join(devicePath, "vendor"))
	if err != nil {
		return nil, err
	}

	// TODO(#193): pcidb.GetDevice does not currently work if we
	// correctly derive the following values and use them in the GetDevice.
	// Previously, we were parsing the incorrect path which returned an
	// incorrect value here that somehow made the GetDevice work partially. Need
	// to investigate on how this can be fixed.
	//
	// device, subsystemVendor and subsystemDevice are best effort
	// device, err = os.ReadFile(filepath.Join(devicePath, "device"))
	// if err == nil {
	// 	subsystemVendor, err = os.ReadFile(filepath.Join(devicePath, "subsystem_vendor"))
	// 	if err == nil {
	// 		subsystemDevice, _ = os.ReadFile(filepath.Join(devicePath, "subsystem_device"))
	// 	}
	// }

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

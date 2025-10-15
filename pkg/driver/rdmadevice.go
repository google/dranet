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
	"fmt"
	"os"
	"syscall"

	"github.com/google/dranet/internal/nlwrap"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// Based on existing RDMA CNI plugin
// https://github.com/k8snetworkplumbingwg/rdma-cni

func nsAttachRdmadev(hostIfName string, containerNsPAth string) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, hostIfName, err)
	}

	hostDev, err := nlwrap.RdmaLinkByName(hostIfName)
	if err != nil {
		return err
	}

	if err = netlink.RdmaLinkSetNsFd(hostDev, uint32(containerNs)); err != nil {
		return fmt.Errorf("failed to move %q to container ns: %v", hostDev.Attrs.Name, err)
	}

	return nil
}

func nsDetachRdmadev(containerNsPAth string, ifName string) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, ifName, err)
	}

	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := nlwrap.NewHandleAt(containerNs)
	if err != nil {
		return fmt.Errorf("could not get network namespace handle: %w", err)
	}

	dev, err := nhNs.RdmaLinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to find %q: %v", ifName, err)
	}

	rootNs, err := netns.Get()
	if err != nil {
		return err
	}
	defer rootNs.Close()

	if err = nhNs.RdmaLinkSetNsFd(dev, uint32(rootNs)); err != nil {
		return fmt.Errorf("failed to move %q to host netns: %v", dev.Attrs.Name, err)
	}
	return nil

}

// GetDeviceInfo retrieves device type, major, and minor numbers for a given path.
// It returns an error if the path does not exist or if it's not a device file.
func GetDeviceInfo(path string) (LinuxDevice, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return LinuxDevice{}, fmt.Errorf("failed to stat path %s: %w", path, err)
	}

	// Check if it's a device file
	if fileInfo.Mode()&os.ModeDevice == 0 {
		return LinuxDevice{}, fmt.Errorf("path %s is not a device file", path)
	}

	// Determine device type ('c' for character, 'b' for block)
	deviceType := ""
	if fileInfo.Mode()&os.ModeCharDevice != 0 {
		deviceType = "c" // Character device
	} else if fileInfo.Mode()&os.ModeDevice != 0 && fileInfo.Mode()&os.ModeCharDevice == 0 {
		deviceType = "b" // Block device (not a character device but is a device)
	}

	// Type assert to syscall.Stat_t to get Rdev (raw device number)
	statT, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return LinuxDevice{}, fmt.Errorf("failed to assert FileInfo.Sys() to syscall.Stat_t for path %s", path)
	}

	// Extract major and minor numbers from Rdev
	majorVal := unix.Major(statT.Rdev)
	minorVal := unix.Minor(statT.Rdev)

	return LinuxDevice{
		Path:  path,
		Type:  deviceType,
		Major: int64(majorVal),
		Minor: int64(minorVal),
	}, nil
}

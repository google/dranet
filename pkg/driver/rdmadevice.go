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

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// Based on existing RDMA CNI plugin
// https://github.com/k8snetworkplumbingwg/rdma-cni

func nsAttachRdmadev(hostIfName string, containerNsPAth string) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, hostIfName, err)
	}

	hostDev, err := netlink.RdmaLinkByName(hostIfName)
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
	nhNs, err := netlink.NewHandleAt(containerNs)
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

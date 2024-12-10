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

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func MoveLinkIn(hostIfName string, containerNsPAth string, ifName string) error {
	hostDev, err := netlink.LinkByName(hostIfName)
	if err != nil {
		return err
	}
	attrs := netlink.NewLinkAttrs()
	attrs.Index = hostDev.Attrs().Index
	attrs.Flags = hostDev.Attrs().Flags
	attrs.MTU = hostDev.Attrs().MTU
	attrs.HardwareAddr = hostDev.Attrs().HardwareAddr
	attrs.Name = ifName
	// Store the original name
	attrs.Alias = hostIfName

	// Devices can be renamed only when down
	if err = netlink.LinkSetDown(hostDev); err != nil {
		return fmt.Errorf("failed to set %q down: %v", hostDev.Attrs().Name, err)
	}

	containerNs, err := ns.GetNS(containerNsPAth)
	if err != nil {
		return err
	}
	attrs.Namespace = containerNs.Fd()

	dev := &netlink.Device{
		LinkAttrs: attrs,
	}

	err = netlink.LinkModify(dev)
	if err != nil {
		return fmt.Errorf("could not modify network device %s : %w", hostIfName, err)
	}

	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := netlink.NewHandleAt(netns.NsHandle(containerNs.Fd()))
	if err != nil {
		return err
	}

	nsLink, err := nhNs.LinkByName(dev.Name)
	if err != nil {
		return fmt.Errorf("link not found for interface %s on namespace %s: %w", dev.Name, containerNsPAth, err)
	}

	err = nhNs.LinkSetUp(nsLink)
	if err != nil {
		return fmt.Errorf("failt to set up interface %s on namespace %s: %w", nsLink.Attrs().Name, containerNsPAth, err)
	}

	return nil
}

func MoveLinkOut(containerNsPAth string, devName string) error {
	defaultNs, err := ns.GetCurrentNS()
	if err != nil {
		return err
	}
	defer defaultNs.Close()

	ns, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, devName, err)
	}
	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := netlink.NewHandleAt(ns)
	if err != nil {
		return fmt.Errorf("could not get network namespace handle: %w", err)
	}

	nsLink, err := nhNs.LinkByName(devName)
	if err != nil {
		return fmt.Errorf("link not found for interface %s on namespace %s: %w", devName, containerNsPAth, err)
	}

	// set the device down to avoid network conflicts
	// when it is restored to the original namespace
	err = nhNs.LinkSetDown(nsLink)
	if err != nil {
		return err
	}

	attrs := netlink.NewLinkAttrs()
	attrs.Index = nsLink.Attrs().Index
	attrs.Name = devName
	// restore the original name if it was renamed
	if nsLink.Attrs().Alias != "" {
		attrs.Name = nsLink.Attrs().Alias
	}

	rootNs, err := netns.Get()
	if err != nil {
		return err
	}
	defer rootNs.Close()

	attrs.Namespace = int(netlink.NsFd(rootNs))

	dev := &netlink.Device{
		LinkAttrs: attrs,
	}

	err = netlink.LinkModify(dev)
	if err != nil {
		return fmt.Errorf("could not modify network device %s : %w", devName, err)
	}
	return nil
}

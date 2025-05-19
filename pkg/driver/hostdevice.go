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
	"errors"
	"fmt"
	"net"

	"github.com/google/dranet/pkg/apis"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"

	resourceapi "k8s.io/api/resource/v1beta1"
)

func nsAttachNetdev(hostIfName string, containerNsPAth string, interfaceConfig apis.InterfaceConfig) (*resourceapi.NetworkDeviceData, error) {
	hostDev, err := netlink.LinkByName(hostIfName)
	// recover same behavior on vishvananda/netlink@1.2.1 and do not fail when the kernel returns NLM_F_DUMP_INTR.
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, err
	}

	// Devices can be renamed only when down
	if err = netlink.LinkSetDown(hostDev); err != nil {
		return nil, fmt.Errorf("failed to set %q down: %v", hostDev.Attrs().Name, err)
	}

	addresses := []*net.IPNet{}
	if len(interfaceConfig.Addresses) == 0 {
		// get the existing IP addresses
		nlAddresses, err := netlink.AddrList(hostDev, netlink.FAMILY_ALL)
		if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
			return nil, fmt.Errorf("fail to get ip addresses: %w", err)
		}
		for _, address := range nlAddresses {
			// Only move permanent IP addresses configured by the user, dynamic addresses are excluded because
			// their validity may rely on the original network namespace's context and they may have limited
			// lifetimes and are not guaranteed to be available in a new namespace.
			// Ref: https://www.ietf.org/rfc/rfc3549.txt
			if address.Flags&unix.IFA_F_PERMANENT == 0 {
				continue
			}
			// Only move IP addresses with global scope because those are not host-specific, auto-configured,
			// or have limited network scope, making them unsuitable inside the container namespace.
			// Ref: https://www.ietf.org/rfc/rfc3549.txt
			if address.Scope != unix.RT_SCOPE_UNIVERSE {
				continue
			}
			// remove the interface attribute of the original address
			// to avoid issues when the interface is renamed.
			addresses = append(addresses, address.IPNet)
		}
	} else {
		for _, addr := range interfaceConfig.Addresses {
			_, ipnet, _ := net.ParseCIDR(addr) // already validated
			addresses = append(addresses, ipnet)
		}
	}

	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return nil, err
	}
	defer containerNs.Close()

	attrs := hostDev.Attrs()
	// Store the original name
	attrs.Alias = hostIfName

	// copy from netlink.LinkModify(dev) using only the parts needed
	flags := unix.NLM_F_REQUEST | unix.NLM_F_ACK
	req := nl.NewNetlinkRequest(unix.RTM_NEWLINK, flags)
	// Get a netlink socket in current namespace
	s, err := nl.GetNetlinkSocketAt(netns.None(), netns.None(), unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("could not get network namespace handle: %w", err)
	}
	defer s.Close()

	req.Sockets = map[int]*nl.SocketHandle{
		unix.NETLINK_ROUTE: {Socket: s},
	}

	msg := nl.NewIfInfomsg(unix.AF_UNSPEC)
	msg.Index = int32(attrs.Index)
	req.AddData(msg)

	ifName := attrs.Name
	if interfaceConfig.Name != "" {
		ifName = interfaceConfig.Name
	}
	nameData := nl.NewRtAttr(unix.IFLA_IFNAME, nl.ZeroTerminated(ifName))
	req.AddData(nameData)

	ifMtu := uint32(attrs.MTU)
	if interfaceConfig.MTU > 0 {
		ifMtu = uint32(interfaceConfig.MTU)
	}
	mtu := nl.NewRtAttr(unix.IFLA_MTU, nl.Uint32Attr(ifMtu))
	req.AddData(mtu)

	val := nl.Uint32Attr(uint32(containerNs))
	attr := nl.NewRtAttr(unix.IFLA_NET_NS_FD, val)
	req.AddData(attr)

	_, err = req.Execute(unix.NETLINK_ROUTE, 0)
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, err
	}

	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := netlink.NewHandleAt(containerNs)
	if err != nil {
		return nil, err
	}
	defer nhNs.Close()

	nsLink, err := nhNs.LinkByName(ifName)
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("link not found for interface %s on namespace %s: %w", ifName, containerNsPAth, err)
	}

	networkData := &resourceapi.NetworkDeviceData{
		InterfaceName:   nsLink.Attrs().Name,
		HardwareAddress: string(nsLink.Attrs().HardwareAddr.String()),
	}

	for _, address := range addresses {
		err = nhNs.AddrAdd(nsLink, &netlink.Addr{IPNet: address})
		if err != nil {
			return nil, fmt.Errorf("fail to set up address %s on namespace %s: %w", address.String(), containerNsPAth, err)
		}
		networkData.IPs = append(networkData.IPs, address.String())

	}

	err = nhNs.LinkSetUp(nsLink)
	if err != nil {
		return nil, fmt.Errorf("failt to set up interface %s on namespace %s: %w", nsLink.Attrs().Name, containerNsPAth, err)
	}

	return networkData, nil
}

func nsDetachNetdev(containerNsPAth string, devName string, outName string) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, devName, err)
	}
	defer containerNs.Close()
	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := netlink.NewHandleAt(containerNs)
	if err != nil {
		return fmt.Errorf("could not get network namespace handle: %w", err)
	}
	defer nhNs.Close()

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

	attrs := nsLink.Attrs()
	// restore the original name if it was renamed
	if nsLink.Attrs().Alias != "" {
		attrs.Name = nsLink.Attrs().Alias
	}

	rootNs, err := netns.Get()
	if err != nil {
		return err
	}
	defer rootNs.Close()

	s, err := nl.GetNetlinkSocketAt(containerNs, rootNs, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("could not get network namespace handle: %w", err)
	}
	defer s.Close()

	// copy from netlink.LinkModify(dev) using only the parts needed
	flags := unix.NLM_F_REQUEST | unix.NLM_F_ACK
	req := nl.NewNetlinkRequest(unix.RTM_NEWLINK, flags)
	req.Sockets = map[int]*nl.SocketHandle{
		unix.NETLINK_ROUTE: {Socket: s},
	}
	msg := nl.NewIfInfomsg(unix.AF_UNSPEC)
	msg.Index = int32(attrs.Index)
	req.AddData(msg)

	ifName := attrs.Name
	if outName != "" {
		ifName = outName
	}
	nameData := nl.NewRtAttr(unix.IFLA_IFNAME, nl.ZeroTerminated(ifName))
	req.AddData(nameData)

	val := nl.Uint32Attr(uint32(rootNs))
	attr := nl.NewRtAttr(unix.IFLA_NET_NS_FD, val)
	req.AddData(attr)

	_, err = req.Execute(unix.NETLINK_ROUTE, 0)
	if err != nil {
		return err
	}

	return nil
}

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

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/klog/v2"
)

func nsAttachNetdev(hostIfName string, containerNsPAth string, interfaceConfig apis.InterfaceConfig) (*resourceapi.NetworkDeviceData, error) {
	hostDev, err := netlink.LinkByName(hostIfName)
	// recover same behavior on vishvananda/netlink@1.2.1 and do not fail when the kernel returns NLM_F_DUMP_INTR.
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("failed to get link for interface %s: %w", hostIfName, err)
	}

	// Devices can be renamed only when down
	if err = netlink.LinkSetDown(hostDev); err != nil {
		return nil, fmt.Errorf("failed to set %q down: %w", hostDev.Attrs().Name, err)
	}

	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return nil, fmt.Errorf("failed to get container network namespace %s: %w", containerNsPAth, err)
	}
	defer containerNs.Close()

	attrs := hostDev.Attrs()

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

	// Configuration values
	if interfaceConfig.MTU != nil {
		ifMtu := uint32(*interfaceConfig.MTU)
		mtu := nl.NewRtAttr(unix.IFLA_MTU, nl.Uint32Attr(ifMtu))
		req.AddData(mtu)
	}

	if interfaceConfig.HardwareAddr != nil {
		if hardwareAddr, err := net.ParseMAC(*interfaceConfig.HardwareAddr); err == nil {
			hwaddr := nl.NewRtAttr(unix.IFLA_ADDRESS, []byte(hardwareAddr))
			req.AddData(hwaddr)
		}
	}

	if interfaceConfig.GSOMaxSize != nil {
		gsoMaxSize := uint32(*interfaceConfig.GSOMaxSize)
		gsoAttr := nl.NewRtAttr(unix.IFLA_GSO_MAX_SIZE, nl.Uint32Attr(gsoMaxSize))
		req.AddData(gsoAttr)
	}

	if interfaceConfig.GROMaxSize != nil {
		groMaxSize := uint32(*interfaceConfig.GROMaxSize)
		groAttr := nl.NewRtAttr(unix.IFLA_GRO_MAX_SIZE, nl.Uint32Attr(groMaxSize))
		req.AddData(groAttr)
	}

	if interfaceConfig.GSOIPv4MaxSize != nil {
		gsoMaxSize := uint32(*interfaceConfig.GSOIPv4MaxSize)
		gsoV4Attr := nl.NewRtAttr(unix.IFLA_GSO_IPV4_MAX_SIZE, nl.Uint32Attr(gsoMaxSize))
		req.AddData(gsoV4Attr)
	}

	if interfaceConfig.GROIPv4MaxSize != nil {
		groMaxSize := uint32(*interfaceConfig.GROIPv4MaxSize)
		groV4Attr := nl.NewRtAttr(unix.IFLA_GRO_IPV4_MAX_SIZE, nl.Uint32Attr(groMaxSize))
		req.AddData(groV4Attr)
	}

	val := nl.Uint32Attr(uint32(containerNs))
	attr := nl.NewRtAttr(unix.IFLA_NET_NS_FD, val)
	req.AddData(attr)

	_, err = req.Execute(unix.NETLINK_ROUTE, 0)
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("failed to move interface %s to container namespace %s: %w", hostIfName, containerNsPAth, err)
	}

	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := netlink.NewHandleAt(containerNs)
	if err != nil {
		return nil, fmt.Errorf("failed to get netlink handle in container namespace %s: %w", containerNsPAth, err)
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

	for _, address := range interfaceConfig.Addresses {
		ip, ipnet, err := net.ParseCIDR(address)
		if err != nil {
			klog.Infof("fail to parse address %s : %v", address, err)
			continue // this should not happen since it has been already validated
		}
		err = nhNs.AddrAdd(nsLink, &netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: ipnet.Mask}})
		if err != nil {
			return nil, fmt.Errorf("fail to set up address %s on namespace %s: %w", address, containerNsPAth, err)
		}
		networkData.IPs = append(networkData.IPs, address)
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

	// Set up the interface in case host network workloads depend on it
	hostDev, err := netlink.LinkByName(ifName)
	// recover same behavior on vishvananda/netlink@1.2.1 and do not fail when the kernel returns NLM_F_DUMP_INTR.
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return err
	}

	if err = netlink.LinkSetUp(hostDev); err != nil {
		return fmt.Errorf("failed to set %q down: %v", hostDev.Attrs().Name, err)
	}
	return nil
}

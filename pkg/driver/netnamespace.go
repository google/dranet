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
	"slices"
	"syscall"

	"github.com/google/dranet/pkg/apis"

	"github.com/google/dranet/internal/nlwrap"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func applyRoutingConfig(containerNsPAth string, ifName string, routeConfig []apis.RouteConfig) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return err
	}
	defer containerNs.Close()

	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := nlwrap.NewHandleAt(containerNs)
	if err != nil {
		return fmt.Errorf("can not get netlink handle: %v", err)
	}
	defer nhNs.Close()

	nsLink, err := nhNs.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("link not found for interface %s on namespace %s: %w", ifName, containerNsPAth, err)
	}

	errorList := []error{}
	// Sort routes to process link-local routes before universe routes.
	// This is important because universe routes might depend on link-local ones.
	// For example, in GCE VMs:
	// # ip addr show eth0
	//   inet 10.0.5.8/32 scope global dynamic eth0
	// # ip route show dev eth0
	//   10.0.5.0/24 via 10.0.5.1 proto dhcp src 10.0.5.8
	//   10.0.5.1 proto dhcp scope link src 10.0.5.8
	slices.SortFunc(routeConfig, func(a, b apis.RouteConfig) int {
		// Routes with scope RT_SCOPE_LINK (253) should come before RT_SCOPE_UNIVERSE (0)
		// A higher scope value means it's processed earlier.
		return int(b.Scope) - int(a.Scope)
	})

	for _, route := range routeConfig {
		r := netlink.Route{
			LinkIndex: nsLink.Attrs().Index,
			Scope:     netlink.Scope(route.Scope),
		}

		_, dst, err := net.ParseCIDR(route.Destination)
		if err != nil {
			errorList = append(errorList, err)
			continue
		}
		r.Dst = dst
		r.Gw = net.ParseIP(route.Gateway)
		if route.Source != "" {
			r.Src = net.ParseIP(route.Source)
		}
		if err := nhNs.RouteAdd(&r); err != nil && !errors.Is(err, syscall.EEXIST) {
			errorList = append(errorList, fmt.Errorf("fail to add route %s for interface %s on namespace %s: %w", r.String(), ifName, containerNsPAth, err))
		}

	}
	return errors.Join(errorList...)
}

func applyNeighborConfig(containerNsPAth string, ifName string, neighConfig []apis.NeighborConfig) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s: %w", containerNsPAth, err)
	}
	defer containerNs.Close()

	nhNs, err := nlwrap.NewHandleAt(containerNs)
	if err != nil {
		return fmt.Errorf("can not get netlink handle: %v", err)
	}
	defer nhNs.Close()

	nsLink, err := nhNs.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("link not found for interface %s on namespace %s: %w", ifName, containerNsPAth, err)
	}

	var errorList []error
	for _, neigh := range neighConfig {
		ip := net.ParseIP(neigh.Destination)
		if ip == nil {
			errorList = append(errorList, fmt.Errorf("invalid ip address: %s", neigh.Destination))
			continue
		}
		mac, err := net.ParseMAC(neigh.HardwareAddr)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("invalid mac address: %s", neigh.HardwareAddr))
			continue
		}
		n := netlink.Neigh{
			LinkIndex:    nsLink.Attrs().Index,
			State:        netlink.NUD_PERMANENT,
			IP:           ip,
			HardwareAddr: mac,
		}
		if err := nhNs.NeighAdd(&n); err != nil && !errors.Is(err, syscall.EEXIST) {
			errorList = append(errorList, fmt.Errorf("failed to add permanent neighbor entry %s (%s) for interface %s: %w", neigh.Destination, neigh.HardwareAddr, ifName, err))
		}
	}
	return errors.Join(errorList...)
}

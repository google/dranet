/*
Copyright 2025 Google LLC

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

	"github.com/google/dranet/internal/nlwrap"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func addMacVlan(containerNsPAth string, devName string, mode netlink.MacvlanMode) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, devName, err)
	}
	defer containerNs.Close()

	parentLink, err := nlwrap.LinkByName(devName)
	if err != nil {
		return fmt.Errorf("could not find parent interface %s : %w", devName, err)
	}

	macvlan := &netlink.Macvlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        "mavlan-" + devName,
			ParentIndex: parentLink.Attrs().Index,
			NetNsID:     int(containerNs),
		},
		Mode: mode,
	}
	if err := netlink.LinkAdd(macvlan); err != nil {
		// If a user creates a macvlan and ipvlan on same parent, only one slave iface can be active at a time.
		return fmt.Errorf("failed to create the %s macvlan interface: %v", macvlan.Name, err)
	}

	return nil
}

func addIPVlan(containerNsPAth string, devName string, mode netlink.IPVlanMode) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, devName, err)
	}
	defer containerNs.Close()

	parentLink, err := nlwrap.LinkByName(devName)
	if err != nil {
		return fmt.Errorf("could not find parent interface %s : %w", devName, err)
	}

	ipvlan := &netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        "ipvaln-" + devName,
			ParentIndex: parentLink.Attrs().Index,
			NetNsID:     int(containerNs),
		},
		Mode: mode,
	}

	if err := netlink.LinkAdd(ipvlan); err != nil {
		// If a user creates a macvlan and ipvlan on same parent, only one slave iface can be active at a time.
		return fmt.Errorf("failed to create the %s ipvlan interface: %v", ipvlan.Name, err)
	}

	return nil
}

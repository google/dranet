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
	"context"
	"fmt"
	"net"

	"github.com/google/dranet/pkg/apis"

	"github.com/google/dranet/internal/nlwrap"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"
)

func getDHCP(ctx context.Context, ifName string) (ip string, routes []apis.RouteConfig, err error) {
	link, err := nlwrap.LinkByName(ifName)
	if err != nil {
		return "", nil, err
	}
	if link.Attrs().OperState != netlink.OperUp {
		if err := netlink.LinkSetUp(link); err != nil {
			return "", nil, fmt.Errorf("fail to set interface %s up: %v", ifName, err)
		}
	}
	dhclient, err := nclient4.New(ifName)
	if err != nil {
		return "", nil, fmt.Errorf("fail to create DHCP client on interface %s  up: %v", ifName, err)
	}
	defer dhclient.Close()

	lease, err := dhclient.Request(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("fail to obtain DHCP lease on interface %s  up: %v", ifName, err)
	}
	if lease.ACK == nil {
		return "", nil, fmt.Errorf("fail to obtain DHCP lease on interface %s  up: %v", ifName, err)
	}
	ip = (&net.IPNet{
		IP:   lease.ACK.YourIPAddr,
		Mask: lease.ACK.SubnetMask(),
	}).String()

	// only support opt 121 (ignore 33)
	for _, route := range lease.ACK.ClasslessStaticRoute() {
		routeCfg := apis.RouteConfig{
			Destination: route.Dest.String(),
			Gateway:     route.Router.String(),
		}
		routes = append(routes, routeCfg)
	}
	return
}

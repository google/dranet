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
	"runtime"
	"testing"

	"github.com/google/dranet/internal/nlwrap"
	"github.com/google/dranet/pkg/apis"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func TestApplyRoutingConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-linux platform")
	}

	tests := []struct {
		name          string
		preAddrs      []string
		routeConfig   []apis.RouteConfig
		family        int
		expectedDest  string
		expectedCount int
		expectErr     bool
	}{
		{
			name:     "IPv6 route already exists",
			preAddrs: []string{"fd36::3:0:e:0:0/96"},
			routeConfig: []apis.RouteConfig{
				{Destination: "fd36::3:0:e:0:0/96"},
			},
			family:        netlink.FAMILY_V6,
			expectedDest:  "fd36::3:0:e:0:0/96",
			expectedCount: 1,
		},
		{
			name:     "IPv6 route does not exist",
			preAddrs: []string{},
			routeConfig: []apis.RouteConfig{
				{Destination: "fd36:3:0:f::/64"},
			},
			family:        netlink.FAMILY_V6,
			expectedDest:  "fd36:3:0:f::/64",
			expectedCount: 1,
		},
		{
			name:     "IPv4 route already exists",
			preAddrs: []string{"192.168.1.1/24"},
			routeConfig: []apis.RouteConfig{
				{Destination: "192.168.1.0/24"},
			},
			family:        netlink.FAMILY_V4,
			expectedDest:  "192.168.1.0/24",
			expectedCount: 1,
		},
		{
			name:     "IPv4 route does not exist",
			preAddrs: []string{},
			routeConfig: []apis.RouteConfig{
				{Destination: "10.0.0.0/8"},
			},
			family:        netlink.FAMILY_V4,
			expectedDest:  "10.0.0.0/8",
			expectedCount: 1,
		},
		{
			name:          "Empty route config",
			preAddrs:      []string{"192.168.1.1/24"},
			routeConfig:   []apis.RouteConfig{},
			family:        netlink.FAMILY_V4,
			expectedDest:  "192.168.1.0/24",
			expectedCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns, err := netns.New()
			if err != nil {
				t.Fatalf("failed to create new netns: %v", err)
			}
			defer ns.Close()

			ifName := "test-eth0"
			dummy := &netlink.Dummy{
				LinkAttrs: netlink.LinkAttrs{
					Name:      ifName,
					Namespace: netlink.NsFd(ns),
				},
			}
			if err := netlink.LinkAdd(dummy); err != nil {
				t.Fatalf("failed to add dummy link: %v", err)
			}

			nhNs, err := nlwrap.NewHandleAt(ns)
			if err != nil {
				t.Fatalf("failed to get netlink handle in namespace: %v", err)
			}
			defer nhNs.Close()

			nsLink, err := nhNs.LinkByName(ifName)
			if err != nil {
				t.Fatalf("failed to find link %q in namespace: %v", ifName, err)
			}

			if err := nhNs.LinkSetUp(nsLink); err != nil {
				t.Fatalf("failed to set link up: %v", err)
			}

			for _, addrStr := range tc.preAddrs {
				addr, err := netlink.ParseAddr(addrStr)
				if err != nil {
					t.Fatalf("failed to parse address %q: %v", addrStr, err)
				}
				if err := nhNs.AddrAdd(nsLink, addr); err != nil {
					t.Fatalf("failed to add address to link: %v", err)
				}
			}

			err = applyRoutingConfig(fmt.Sprintf("/proc/self/fd/%d", ns), ifName, tc.routeConfig)
			if (err != nil) != tc.expectErr {
				t.Fatalf("applyRoutingConfig() error = %v, wantErr %v", err, tc.expectErr)
			}
			if err != nil {
				return
			}

			routes, err := nhNs.RouteList(nsLink, tc.family)
			if err != nil {
				t.Fatalf("failed to list routes: %v", err)
			}

			routeCount := 0
			for _, r := range routes {
				if r.Dst != nil && r.Dst.String() == tc.expectedDest {
					routeCount++
				}
			}

			if routeCount != tc.expectedCount {
				t.Errorf("found %d routes for dest %s, expected %d", routeCount, tc.expectedDest, tc.expectedCount)
			}
		})
	}
}
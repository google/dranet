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
	"k8s.io/klog/v2"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func netnsRouting(containerNsPAth string, routeConfig []apis.RouteConfig) error {
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return err
	}
	defer containerNs.Close()

	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := netlink.NewHandleAt(containerNs)
	if err != nil {
		return fmt.Errorf("can not get netlink handle: %v", err)
	}
	defer nhNs.Close()

	errorList := []error{}
	for _, route := range routeConfig {
		r := netlink.Route{}

		_, dst, err := net.ParseCIDR(route.Destination) // nolint:errcheck already validated
		if err != nil {
			errorList = append(errorList, err)
			continue
		}
		r.Dst = dst
		r.Gw = net.ParseIP(route.Gateway) // already validated
		if route.Source != "" {
			r.Src = net.ParseIP(route.Source)
		}
		klog.V(4).Infof("Configuring route %#v", route)
		if err := nhNs.RouteAdd(&r); err != nil {
			errorList = append(errorList, err)
		}

	}
	return errors.Join(errorList...)
}

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

package inventory

import (
	"github.com/vishvananda/netlink"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
)

func getDefaultGwInterfaces() sets.Set[string] {
	interfaces := sets.Set[string]{}
	routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return interfaces
	}

	for _, r := range routes {
		// no multipath
		if len(r.MultiPath) == 0 {
			if r.Gw == nil {
				continue
			}
			intfLink, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				klog.Infof("Failed to get interface link for route %v : %v", r, err)
				continue
			}
			interfaces.Insert(intfLink.Attrs().Name)
		}

		for _, nh := range r.MultiPath {
			if nh.Gw == nil {
				continue
			}
			intfLink, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				klog.Infof("Failed to get interface link for route %v : %v", r, err)
				continue
			}
			interfaces.Insert(intfLink.Attrs().Name)
		}
	}
	return interfaces
}

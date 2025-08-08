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
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/vishvananda/netlink"

	"k8s.io/apimachinery/pkg/util/sets"
)

func getTcFilters(link netlink.Link) ([]string, bool) {
	isTcEBPF := false
	filterNames := sets.Set[string]{}
	for _, parent := range []uint32{netlink.HANDLE_MIN_INGRESS, netlink.HANDLE_MIN_EGRESS} {
		filters, err := netlink.FilterList(link, parent)
		if err == nil {
			for _, f := range filters {
				if bpffFilter, ok := f.(*netlink.BpfFilter); ok {
					isTcEBPF = true
					filterNames.Insert(bpffFilter.Name)
				}
			}
		}
	}
	return filterNames.UnsortedList(), isTcEBPF
}

// see https://github.com/cilium/ebpf/issues/1117
func getTcxFilters(device netlink.Link) ([]string, bool) {
	isTcxEBPF := false
	programNames := sets.Set[string]{}
	for _, attach := range []ebpf.AttachType{ebpf.AttachTCXIngress, ebpf.AttachTCXEgress} {
		result, err := link.QueryPrograms(link.QueryOptions{
			Target: int(device.Attrs().Index),
			Attach: attach,
		})
		if err != nil {
			continue
		}

		isTcxEBPF = true
		for _, p := range result.Programs {
			prog, err := ebpf.NewProgramFromID(p.ID)
			if err != nil {
				continue
			}
			defer prog.Close()

			pi, err := prog.Info()
			if err != nil {
				continue
			}
			programNames.Insert(pi.Name)
		}
	}
	return programNames.UnsortedList(), isTcxEBPF
}

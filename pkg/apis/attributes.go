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

package apis

const (
	AttrPrefix = "dra.net"

	// TODO: Document meaning of these attributes and re-evaluate if all are needed.
	AttrInterfaceName   = AttrPrefix + "/" + "ifName"
	AttrPCIAddress      = AttrPrefix + "/" + "pciAddress"
	AttrMac             = AttrPrefix + "/" + "mac"
	AttrPCIVendor       = AttrPrefix + "/" + "pciVendor"
	AttrPCIDevice       = AttrPrefix + "/" + "pciDevice"
	AttrPCISubsystem    = AttrPrefix + "/" + "pciSubsystem"
	AttrNUMANode        = AttrPrefix + "/" + "numaNode"
	AttrMTU             = AttrPrefix + "/" + "mtu"
	AttrEncapsulation   = AttrPrefix + "/" + "encapsulation"
	AttrAlias           = AttrPrefix + "/" + "alias"
	AttrState           = AttrPrefix + "/" + "state"
	AttrType            = AttrPrefix + "/" + "type"
	AttrIPv4            = AttrPrefix + "/" + "ipv4"
	AttrIPv6            = AttrPrefix + "/" + "ipv6"
	AttrTCFilterNames   = AttrPrefix + "/" + "tcFilterNames"
	AttrTCXProgramNames = AttrPrefix + "/" + "tcxProgramNames"
	AttrEBPF            = AttrPrefix + "/" + "ebpf"
	AttrSRIOV           = AttrPrefix + "/" + "sriov"
	AttrSRIOVVfs        = AttrPrefix + "/" + "sriovVfs"
	AttrVirtual         = AttrPrefix + "/" + "virtual"
	AttrRDMA            = AttrPrefix + "/" + "rdma"
)

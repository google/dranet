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
	// rdmaNetnsModeShared and rdmaNetnsModeExclusive define the RDMA subsystem
	// network namespace mode. An RDMA device can only be assigned to a network
	// namespace when the RDMA subsystem is set to an "exclusive" network
	// namespace mode. When the subsystem is set to "shared" mode, an attempt to
	// assign an RDMA device to a network namespace will result in failure.
	// Additionally, "If there are active network namespaces and if one or more
	// RDMA devices exist, changing mode from shared to exclusive returns error
	// code EBUSY."
	//
	// Ref. https://man7.org/linux/man-pages/man8/rdma-system.8.html
	RdmaNetnsModeShared    = "shared"
	RdmaNetnsModeExclusive = "exclusive"
)

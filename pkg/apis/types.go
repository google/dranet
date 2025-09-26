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

// NetworkConfig represents the desired state of all network interfaces and their associated routes,
// along with ethtool and sysctl configurations to be applied within the Pod's network namespace.
type NetworkConfig struct {
	// Interface defines core properties of the network interface.
	// Settings here are typically managed by `ip link` commands.
	Interface InterfaceConfig `json:"interface"`

	// Routes defines static routes to be configured for this interface.
	Routes []RouteConfig `json:"routes,omitempty"`

	// Neighbors defines permanent neighbor (ARP/NDP) entries to be added for this interface.
	Neighbors []NeighborConfig `json:"neighbors,omitempty"`

	// Ethtool defines hardware offload features and other settings managed by `ethtool`.
	Ethtool *EthtoolConfig `json:"ethtool,omitempty"`
}

// InterfaceConfig represents the configuration for a single network interface.
// These are fundamental properties, often managed using `ip link` commands.
type InterfaceConfig struct {
	// Name is the desired logical name of the interface inside the Pod (e.g., "net0", "eth_app").
	// If not specified, DraNet may use or derive a name from the original interface.
	Name string `json:"name,omitempty"`

	// Addresses is a list of IP addresses in CIDR format (e.g., "192.168.1.10/24")
	// to be assigned to the interface.
	Addresses []string `json:"addresses,omitempty"`

	// DHCP, if true, indicates that the interface should be configured via DHCP.
	// This is mutually exclusive with the 'addresses' field.
	DHCP *bool `json:"dhcp,omitempty"`

	// MTU is the Maximum Transmission Unit for the interface.
	MTU *int32 `json:"mtu,omitempty"`

	// HardwareAddr is the MAC address of the interface.
	HardwareAddr *string `json:"hardwareAddr,omitempty"`

	// GSOMaxSize sets the maximum Generic Segmentation Offload size for IPv6.
	// Managed by `ip link set <dev> gso_max_size <val>`. For enabling Big TCP.
	GSOMaxSize *int32 `json:"gsoMaxSize,omitempty"`

	// GROMaxSize sets the maximum Generic Receive Offload size for IPv6.
	// Managed by `ip link set <dev> gro_max_size <val>`. For enabling Big TCP.
	GROMaxSize *int32 `json:"groMaxSize,omitempty"`

	// GSOv4MaxSize sets the maximum Generic Segmentation Offload size.
	// Managed by `ip link set <dev> gso_ipv4_max_size <val>`. For enabling Big TCP.
	GSOIPv4MaxSize *int32 `json:"gsoIPv4MaxSize,omitempty"`

	// GROv4MaxSize sets the maximum Generic Receive Offload size.
	// Managed by `ip link set <dev> gro_ipv4_max_size <val>`. For enabling Big TCP.
	GROIPv4MaxSize *int32 `json:"groIPv4MaxSize,omitempty"`

	// DisableEBPFPrograms, if true, attempts to detach all eBPF programs
	// (both TC and TCX) from the network interface assigned to the Pod.
	DisableEBPFPrograms *bool `json:"disableEbpfPrograms,omitempty"`
}

// RouteConfig represents a network route configuration.
type RouteConfig struct {
	// Destination is the target network in CIDR format (e.g., "0.0.0.0/0", "10.0.0.0/8").
	Destination string `json:"destination,omitempty"`
	// Gateway is the IP address of the gateway for this route.
	Gateway string `json:"gateway,omitempty"`
	// Source is an optional source IP address for policy routing.
	Source string `json:"source,omitempty"`
	// Scope is the scope of the route (e.g., link, host, global).
	// Refers to Linux route scopes (e.g., 0 for RT_SCOPE_UNIVERSE, 253 for RT_SCOPE_LINK).
	Scope uint8 `json:"scope,omitempty"`
}

// NeighborConfig represents a neighbor (ARP/NDP) entry.
type NeighborConfig struct {
	// Destination is the target IP address.
	Destination string `json:"destination,omitempty"`
	// HardwareAddr is the MAC address of the neighbor.
	HardwareAddr string `json:"hardwareAddr,omitempty"`
}

// EthtoolConfig defines ethtool-based optimizations for a network interface.
// These settings correspond to features typically toggled using `ethtool -K <dev> <feature> on|off`.
type EthtoolConfig struct {
	// Features is a map of ethtool feature names to their desired state (true for on, false for off).
	// Example: {"tcp-segmentation-offload": true, "rx-checksum": true}
	Features map[string]bool `json:"features,omitempty"`

	// PrivateFlags is a map of device-specific private flag names to their desired state.
	// Example: {"my-custom-flag": true}
	PrivateFlags map[string]bool `json:"privateFlags,omitempty"`
}

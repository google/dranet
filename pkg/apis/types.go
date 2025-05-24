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

// NetworkConfig represents the desired state of all network interfaces and their associated routes.
type NetworkConfig struct {
	Interface InterfaceConfig `json:"interface"` // Changed to a slice to support multiple interfaces
	Routes    []RouteConfig   `json:"routes"`
}

// InterfaceConfig represents the configuration for a single network interface.
type InterfaceConfig struct {
	Name         string   `json:"name,omitempty"`         // Logical name of the interface (e.g., "eth0", "enp0s3")
	Addresses    []string `json:"addresses,omitempty"`    // IP addresses and their CIDR masks
	MTU          int32    `json:"mtu,omitempty"`          // Maximum Transmission Unit, optional
	HardwareAddr string   `json:"hardwareAddr,omitempty"` // Read-only: Current hardware address (might be useful for GET)
}

// RouteConfig represents a network route configuration.
type RouteConfig struct {
	Destination string `json:"destination,omitempty"` // e.g., "0.0.0.0/0" for default, "10.0.0.0/8"
	Gateway     string `json:"gateway,omitempty"`     // The "gateway" address, e.g., "192.168.1.1"
	Source      string `json:"source,omitempty"`      // Optional source address for policy routing
	Scope       uint8  `json:"scope,omitempty"`       // Optional scope of the route, only Link (253) or Universe (0) allowed
}

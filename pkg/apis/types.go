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

// TODO Generate code and keep in sync golang types on schema
type NetworkConfig struct {
	Name    string         `json:"name"`
	IPs     []string       `json:"ips"`
	Routes  []Route        `json:"routes"`
	MTU     int            `json:"mtu"`
	Mode    Mode           `json:"mode"`
	VLAN    *VLANConfig    `json:"vlan,omitempty"`
	Macvlan *MacvlanConfig `json:"macvlan,omitempty"`
	IPvlan  *IPvlanConfig  `json:"ipvlan,omitempty"`
}

// Route represents a route configuration.
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
}

// Mode represents the network mode.
type Mode string

// Enumerated Mode values.
const (
	ModeVLAN    Mode = "vlan"
	ModeMacvlan Mode = "macvlan"
	ModeIPvlan  Mode = "ipvlan"
)

// VLANConfig represents the VLAN configuration.
type VLANConfig struct {
	VLANID int `json:"vlanId"`
}

// MacvlanConfig represents the Macvlan configuration.
type MacvlanConfig struct {
	Mode MacvlanMode `json:"macvlanMode"`
}

// MacvlanMode represents the macvlan mode.
type MacvlanMode string

// Enumerated Macvlan mode values.
const (
	MacvlanModeBridge   MacvlanMode = "bridge"
	MacvlanModePrivate  MacvlanMode = "private"
	MacvlanModeVepa     MacvlanMode = "vepa"
	MacvlanModePassthru MacvlanMode = "passthru"
)

// IPvlanConfig represents the IPvlan configuration.
type IPvlanConfig struct {
	Mode IPvlanMode `json:"ipvlanMode"`
}

// IPvlanMode represents the ipvlan mode.
type IPvlanMode string

// Enumerated IPvlan mode values.
const (
	IPvlanModeL2 IPvlanMode = "l2"
	IPvlanModeL3 IPvlanMode = "l3"
)

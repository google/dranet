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

package cloudprovider

type CloudInstance struct {
	Name                string
	Type                string
	Provider            CloudProvider
	AcceleratorProtocol string
	Interfaces          []NetworkInterface
	Topology            string
}

type NetworkInterface struct {
	IPv4      string   `json:"ip,omitempty"`
	IPv6      []string `json:"ipv6,omitempty"`
	Mac       string   `json:"mac,omitempty"`
	MTU       int      `json:"mtu,omitempty"`
	Network   string   `json:"network,omitempty"`
	IPAliases []string `json:"ipAliases,omitempty"`
}

// CloudProvider represents the type of cloud provider.
type CloudProvider string

const (
	CloudProviderGCE   CloudProvider = "GCE"
	CloudProviderAWS   CloudProvider = "AWS"
	CloudProviderAzure CloudProvider = "Azure"
)

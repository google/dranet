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

import (
	"errors"
	"fmt"
	"net/netip"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// ValidateConfig validates the data in a runtime.RawExtension against the OpenAPI schema.
func ValidateConfig(raw *runtime.RawExtension) (*NetworkConfig, error) {
	if raw == nil || raw.Raw == nil {
		return nil, nil
	}
	// Check if raw.Raw is empty
	if len(raw.Raw) == 0 {
		return nil, nil
	}
	var errorsList []error
	var config NetworkConfig
	if err := yaml.Unmarshal(raw.Raw, &config, yaml.DisallowUnknownFields); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML data: %w", err)
	}

	switch config.Mode {
	case ModeMacvtap:
		if config.Macvtap == nil {
			return nil, fmt.Errorf("vlan config is missing")
		}
	case ModeMacvlan:
		if config.Macvlan == nil {
			errorsList = append(errorsList, fmt.Errorf("macvlan config is missing"))
		}
	case ModeIPvlan:
		if config.IPvlan == nil {
			errorsList = append(errorsList, fmt.Errorf("ipvlan config is missing"))
		}
	default:
		// No mode specified or ModeDedicated
	}

	for _, ip := range config.IPs {
		if _, err := netip.ParsePrefix(ip); err != nil {
			errorsList = append(errorsList, fmt.Errorf("invalid IP in CIDR format %s", ip))
		}
	}

	for _, route := range config.Routes {
		if route.Destination == "" || route.Gateway == "" {
			errorsList = append(errorsList, fmt.Errorf("invalid route %v", route))
		}
		if _, err := netip.ParsePrefix(route.Destination); err != nil {
			errorsList = append(errorsList, fmt.Errorf("invalid CIDR %s", route.Destination))
		}
		if _, err := netip.ParseAddr(route.Gateway); err != nil {
			errorsList = append(errorsList, fmt.Errorf("invalid IP address %s", route.Gateway))
		}
	}
	return &config, errors.Join(errorsList...)
}

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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"k8s.io/apimachinery/pkg/runtime"
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
	if err := json.Unmarshal(raw.Raw, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML data: %w", err)
	}

	for _, ip := range config.Interface.Addresses {
		if _, err := netip.ParsePrefix(ip); err != nil {
			errorsList = append(errorsList, fmt.Errorf("invalid IP in CIDR format %s", ip))
		}
	}

	// Validate routes
	for i, route := range config.Routes {
		if route.Destination == "" {
			errorsList = append(errorsList, fmt.Errorf("route %d: destination cannot be empty", i))
		} else {
			// Validate Destination as CIDR or IP
			if _, _, err := net.ParseCIDR(route.Destination); err != nil {
				if net.ParseIP(route.Destination) == nil {
					errorsList = append(errorsList, fmt.Errorf("route %d: invalid destination IP or CIDR '%s'", i, route.Destination))
				}
			}
		}

		if route.Gateway != "" {
			if net.ParseIP(route.Gateway) == nil {
				errorsList = append(errorsList, fmt.Errorf("route %d: invalid gateway IP '%s'", i, route.Gateway))
			}
		} else {
			errorsList = append(errorsList, fmt.Errorf("route %d: for destination '%s' must have a gateway", i, route.Destination))
		}
	}
	return &config, errors.Join(errorsList...)
}

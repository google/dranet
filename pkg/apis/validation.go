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
	"fmt"
	"net"
	"net/netip"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/json"
)

const (
	// MinMTU is the minimum practical MTU (e.g., for IPv4).
	MinMTU = 68
	// MaxInterfaceNameLen is typically IFNAMSIZ-1 (usually 15 on Linux).
	MaxInterfaceNameLen = 15
)

// ValidateConfig unmarshals and validates the NetworkConfig from a runtime.RawExtension.
// It performs strict unmarshalling and then calls specific validation functions for each part of the config.
// Returns the parsed NetworkConfig and a slice of errors if any validation fails.
func ValidateConfig(raw *runtime.RawExtension) (*NetworkConfig, []error) {
	if raw == nil || raw.Raw == nil || len(raw.Raw) == 0 {
		return nil, nil // No configuration provided, so no validation errors.
	}

	var config NetworkConfig
	var allErrors []error

	// Strict unmarshalling to catch unknown fields.
	strictErrs, err := json.UnmarshalStrict(raw.Raw, &config)
	if err != nil {
		allErrors = append(allErrors, fmt.Errorf("failed to unmarshal JSON data: %w", err))
		// If basic unmarshalling fails, we can't proceed with further validation.
		return nil, allErrors
	}
	if len(strictErrs) > 0 {
		for _, e := range strictErrs {
			allErrors = append(allErrors, fmt.Errorf("failed to unmarshal strict JSON data: %w", e))
		}
	}

	// Validate InterfaceConfig
	allErrors = append(allErrors, validateInterfaceConfig(&config.Interface, "interface")...)

	// Validate Routes
	if len(config.Routes) > 0 {
		allErrors = append(allErrors, validateRoutes(config.Routes, "routes")...)
	}

	// Validate EthtoolConfig if present
	if config.Ethtool != nil {
		allErrors = append(allErrors, validateEthtoolConfig(config.Ethtool, "ethtool")...)
	}

	// Validate Neighbors
	if len(config.Neighbors) > 0 {
		allErrors = append(allErrors, validateNeighborConfig(config.Neighbors, "neighbors")...)
	}

	if len(allErrors) > 0 {
		return &config, allErrors // Return partially parsed config with errors
	}

	return &config, nil
}

// isValidLinuxInterfaceName checks if the provided name is a valid Linux interface name.
// Basic checks: length, no '/', no whitespace, not '.' or '..'.
func isValidLinuxInterfaceName(name string, fieldPath string) (allErrors []error) {
	if name == "" {
		// Allow empty name, as DraNet might derive it. If a name is provided, it must be valid.
		return nil
	}
	if len(name) > MaxInterfaceNameLen {
		allErrors = append(allErrors, fmt.Errorf("%s: name '%s' exceeds maximum length of %d characters", fieldPath, name, MaxInterfaceNameLen))
	}
	if strings.Contains(name, "/") {
		allErrors = append(allErrors, fmt.Errorf("%s: name '%s' cannot contain '/'", fieldPath, name))
	}
	if strings.ContainsAny(name, " \t\n\v\f\r") { // Check for any whitespace
		allErrors = append(allErrors, fmt.Errorf("%s: name '%s' cannot contain whitespace", fieldPath, name))
	}
	if name == "." || name == ".." {
		allErrors = append(allErrors, fmt.Errorf("%s: name '%s' cannot be '.' or '..'", fieldPath, name))
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			// If '/' or whitespace, it's already covered by more specific checks above.
			// Avoid adding a generic "invalid character" error for these specific cases.
			if r == '/' || unicode.IsSpace(r) {
				continue
			}
			// This is a more restrictive set for safety, Linux itself might allow more.
			// Common practice avoids special characters other than '-', '_', '.'
			// The kernel function dev_valid_name also disallows non-printable chars.
			allErrors = append(allErrors, fmt.Errorf("%s: name '%s' contains invalid character '%c'. Only letters, digits, '-', '_', '.' are recommended", fieldPath, name, r))
		}
	}
	return allErrors
}

// validateInterfaceConfig validates the InterfaceConfig part of the NetworkConfig.
func validateInterfaceConfig(cfg *InterfaceConfig, fieldPath string) (allErrors []error) {
	if cfg == nil {
		return
	}

	allErrors = append(allErrors, isValidLinuxInterfaceName(cfg.Name, fieldPath+".name")...)

	for i, addr := range cfg.Addresses {
		if _, err := netip.ParsePrefix(addr); err != nil {
			allErrors = append(allErrors, fmt.Errorf("%s.addresses[%d]: invalid IP CIDR format '%s': %w", fieldPath, i, addr, err))
		}
	}

	if cfg.DHCP != nil && *cfg.DHCP && len(cfg.Addresses) > 0 {
		allErrors = append(allErrors, fmt.Errorf("%s: dhcp and addresses are mutually exclusive", fieldPath))
	}

	if cfg.MTU != nil {
		if *cfg.MTU < MinMTU {
			allErrors = append(allErrors, fmt.Errorf("%s.mtu: must be at least %d, got %d", fieldPath, MinMTU, *cfg.MTU))
		}
	}

	if cfg.HardwareAddr != nil {
		if _, err := net.ParseMAC(*cfg.HardwareAddr); err != nil {
			allErrors = append(allErrors, fmt.Errorf("%s.hardwareAddress: invalid Hardware Address format '%s': %w", fieldPath, *cfg.HardwareAddr, err))
		}
	}

	if cfg.GSOMaxSize != nil && *cfg.GSOMaxSize <= 0 {
		allErrors = append(allErrors, fmt.Errorf("%s.gsoMaxSize: must be positive, got %d", fieldPath, *cfg.GSOMaxSize))
	}

	if cfg.GROMaxSize != nil && *cfg.GROMaxSize <= 0 {
		allErrors = append(allErrors, fmt.Errorf("%s.groMaxSize: must be positive, got %d", fieldPath, *cfg.GROMaxSize))
	}

	if cfg.GSOIPv4MaxSize != nil && *cfg.GSOIPv4MaxSize <= 0 {
		allErrors = append(allErrors, fmt.Errorf("%s.gsov4MaxSize: must be positive, got %d", fieldPath, *cfg.GSOIPv4MaxSize))
	}

	if cfg.GROIPv4MaxSize != nil && *cfg.GROIPv4MaxSize <= 0 {
		allErrors = append(allErrors, fmt.Errorf("%s.grov4MaxSize: must be positive, got %d", fieldPath, *cfg.GROIPv4MaxSize))
	}

	return allErrors
}

// validateRoutes validates a slice of RouteConfig.
func validateRoutes(routes []RouteConfig, fieldPath string) (allErrors []error) {
	for i, route := range routes {
		currentFieldPath := fmt.Sprintf("%s[%d]", fieldPath, i)

		if route.Destination == "" {
			allErrors = append(allErrors, fmt.Errorf("%s.destination: cannot be empty", currentFieldPath))
		} else {
			if _, _, err := net.ParseCIDR(route.Destination); err != nil {
				if net.ParseIP(route.Destination) == nil {
					allErrors = append(allErrors, fmt.Errorf("%s.destination: invalid IP or CIDR format '%s'", currentFieldPath, route.Destination))
				}
			}
		}

		scopeIsLink := false
		if route.Scope != unix.RT_SCOPE_UNIVERSE && route.Scope != unix.RT_SCOPE_LINK {
			allErrors = append(allErrors, fmt.Errorf("%s.scope: invalid scope '%d', only Link (%d) or Universe (%d) allowed", currentFieldPath, route.Scope, unix.RT_SCOPE_LINK, unix.RT_SCOPE_UNIVERSE))
		}
		if route.Scope == unix.RT_SCOPE_LINK {
			scopeIsLink = true
		}

		if route.Gateway != "" {
			if net.ParseIP(route.Gateway) == nil {
				allErrors = append(allErrors, fmt.Errorf("%s.gateway: invalid IP address format '%s'", currentFieldPath, route.Gateway))
			}
		} else if !scopeIsLink { // Gateway is required if scope is Universe
			allErrors = append(allErrors, fmt.Errorf("%s.gateway: must be specified for Universe scope routes", currentFieldPath))
		}

		if route.Source != "" {
			if net.ParseIP(route.Source) == nil {
				allErrors = append(allErrors, fmt.Errorf("%s.source: invalid IP address format '%s'", currentFieldPath, route.Source))
			}
		}
	}
	return allErrors
}

// validateEthtoolConfig validates the EthtoolConfig part of the NetworkConfig.
func validateEthtoolConfig(cfg *EthtoolConfig, fieldPath string) (allErrors []error) {
	return allErrors
}

// validateNeighborConfig validates a slice of NeighborConfig.
func validateNeighborConfig(neighbors []NeighborConfig, fieldPath string) (allErrors []error) {
	for i, neighbor := range neighbors {
		currentFieldPath := fmt.Sprintf("%s[%d]", fieldPath, i)

		if neighbor.Destination == "" {
			allErrors = append(allErrors, fmt.Errorf("%s.destination: cannot be empty", currentFieldPath))
		} else if net.ParseIP(neighbor.Destination) == nil {
			allErrors = append(allErrors, fmt.Errorf("%s.destination: invalid IP address format '%s'", currentFieldPath, neighbor.Destination))
		}

		if neighbor.HardwareAddr == "" {
			allErrors = append(allErrors, fmt.Errorf("%s.hardwareAddress: cannot be empty", currentFieldPath))
		} else if _, err := net.ParseMAC(neighbor.HardwareAddr); err != nil {
			allErrors = append(allErrors, fmt.Errorf("%s.hardwareAddress: invalid Hardware Address format '%s': %w", currentFieldPath, neighbor.HardwareAddr, err))
		}
	}
	return allErrors
}

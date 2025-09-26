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
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

// Helper to create a *runtime.RawExtension from a struct
func newRawExtension(t *testing.T, data interface{}) *runtime.RawExtension {
	t.Helper()
	rawJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}
	return &runtime.RawExtension{Raw: rawJSON}
}

// Helper to create a *runtime.RawExtension from a JSON string
func newRawExtensionFromString(t *testing.T, jsonStr string) *runtime.RawExtension {
	t.Helper()
	return &runtime.RawExtension{Raw: []byte(jsonStr)}
}

func TestValidateConfig(t *testing.T) {
	validConfig := NetworkConfig{
		Interface: InterfaceConfig{Name: "eth0", Addresses: []string{"192.168.1.1/24"}, MTU: ptr.To[int32](1500)},
		Routes: []RouteConfig{
			{Destination: "0.0.0.0/0", Gateway: "192.168.1.254", Scope: unix.RT_SCOPE_UNIVERSE},
		},
		Ethtool: &EthtoolConfig{Features: map[string]bool{"tso": true}},
	}
	invalidInterfaceConf := NetworkConfig{Interface: InterfaceConfig{Name: "eth/0"}}
	invalidRouteConf := NetworkConfig{Interface: InterfaceConfig{Name: "eth0"}, Routes: []RouteConfig{{Destination: "invalid-cidr"}}}

	tests := []struct {
		name        string
		raw         *runtime.RawExtension
		expectErr   bool
		expectedCfg *NetworkConfig
		errContains []string
	}{
		{
			name:        "valid full config",
			raw:         newRawExtension(t, validConfig),
			expectErr:   false,
			expectedCfg: &validConfig,
		},
		{
			name:        "nil raw extension",
			raw:         nil,
			expectErr:   false,
			expectedCfg: nil,
		},
		{
			name:        "empty raw data in extension",
			raw:         &runtime.RawExtension{Raw: []byte{}},
			expectErr:   false,
			expectedCfg: nil,
		},
		{
			name:        "malformed json",
			raw:         newRawExtensionFromString(t, `{"interface": {"name": "eth0"`),
			expectErr:   true,
			expectedCfg: nil, // Unmarshal itself fails, cfg should be nil or zero
			errContains: []string{"failed to unmarshal JSON data"},
		},
		{
			name:        "unknown field (strict unmarshal error)",
			raw:         newRawExtensionFromString(t, `{"interface": {"name": "eth0", "unknownField": "test"}}`),
			expectErr:   true,
			expectedCfg: &NetworkConfig{Interface: InterfaceConfig{Name: "eth0"}}, // sigs.k8s.io/json unmarshals known fields first
			errContains: []string{"failed to unmarshal strict JSON data", "unknownField"},
		},
		{
			name:        "config with interface validation error",
			raw:         newRawExtension(t, invalidInterfaceConf),
			expectErr:   true,
			expectedCfg: &invalidInterfaceConf,
			errContains: []string{"interface.name: name 'eth/0' cannot contain '/'"},
		},
		{
			name:        "config with route validation error",
			raw:         newRawExtension(t, invalidRouteConf),
			expectErr:   true,
			expectedCfg: &invalidRouteConf,
			errContains: []string{"routes[0].destination: invalid IP or CIDR format 'invalid-cidr'"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, errs := ValidateConfig(tt.raw)
			hasErrs := len(errs) > 0

			if hasErrs != tt.expectErr {
				t.Errorf("ValidateConfig() error = %v, expectErr %v. Errors: %v", hasErrs, tt.expectErr, errs)
			}

			if tt.expectErr {
				for _, substr := range tt.errContains {
					found := false
					for _, err := range errs {
						if strings.Contains(err.Error(), substr) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("ValidateConfig() expected error to contain '%s', but it didn't. Errors: %v", substr, errs)
					}
				}
			}

			if !reflect.DeepEqual(cfg, tt.expectedCfg) {
				t.Errorf("ValidateConfig() cfg = %+v, expectedCfg %+v", cfg, tt.expectedCfg)
			}
		})
	}
}

func TestIsValidLinuxInterfaceName(t *testing.T) {
	tests := []struct {
		name      string
		ifName    string
		fieldPath string
		expectErr bool
	}{
		{"valid short", "eth0", "iface.name", false},
		{"valid with hyphen", "my-nic", "iface.name", false},
		{"valid with underscore", "my_nic", "iface.name", false},
		{"valid with period", "my.nic", "iface.name", false},
		{"valid max length", strings.Repeat("a", MaxInterfaceNameLen), "iface.name", false},
		{"empty name (allowed)", "", "iface.name", false},
		{"too long", strings.Repeat("a", MaxInterfaceNameLen+1), "iface.name", true},
		{"contains slash", "eth/0", "iface.name", true},
		{"contains space", "eth 0", "iface.name", true},
		{"is dot", ".", "iface.name", true},
		{"is dotdot", "..", "iface.name", true},
		{"contains invalid char", "eth!", "iface.name", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := isValidLinuxInterfaceName(tt.ifName, tt.fieldPath)
			if (len(errs) > 0) != tt.expectErr {
				t.Errorf("isValidLinuxInterfaceName(%s) expectErr %v, got errors: %v", tt.ifName, tt.expectErr, errs)
			}
		})
	}
}

func TestValidateInterfaceConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *InterfaceConfig
		fieldPath string
		expectErr bool
		errCount  int // Expected number of errors
	}{
		{
			name:      "valid",
			cfg:       &InterfaceConfig{Name: "eth0", Addresses: []string{"10.0.0.1/24"}, MTU: ptr.To[int32](1500), GSOMaxSize: ptr.To[int32](65536)},
			fieldPath: "iface",
			expectErr: false,
		},
		{
			name:      "invalid name",
			cfg:       &InterfaceConfig{Name: "eth/"},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid address",
			cfg:       &InterfaceConfig{Name: "eth0", Addresses: []string{"10.0.0/24"}},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid MTU (zero)",
			cfg:       &InterfaceConfig{Name: "eth0", MTU: ptr.To[int32](0)},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid MTU (too small)",
			cfg:       &InterfaceConfig{Name: "eth0", MTU: ptr.To[int32](MinMTU - 1)},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid GSO MaxSize",
			cfg:       &InterfaceConfig{Name: "eth0", GSOMaxSize: ptr.To[int32](0)},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid GRO MaxSize",
			cfg:       &InterfaceConfig{Name: "eth0", GROMaxSize: ptr.To[int32](-1)},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid GSO V4 MaxSize",
			cfg:       &InterfaceConfig{Name: "eth0", GSOIPv4MaxSize: ptr.To[int32](0)},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid GRO V4 MaxSize",
			cfg:       &InterfaceConfig{Name: "eth0", GROIPv4MaxSize: ptr.To[int32](-1)},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "valid hardware address",
			cfg:       &InterfaceConfig{Name: "eth0", HardwareAddr: ptr.To("00:1A:2B:3C:4D:5E")},
			fieldPath: "iface",
			expectErr: false,
		},
		{
			name:      "invalid hardware address",
			cfg:       &InterfaceConfig{Name: "eth0", HardwareAddr: ptr.To("00-1A-2B-3C-4D-5E-XX")},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "valid with dhcp",
			cfg:       &InterfaceConfig{Name: "eth0", DHCP: ptr.To(true)},
			fieldPath: "iface",
			expectErr: false,
		},
		{
			name:      "invalid with dhcp and addresses",
			cfg:       &InterfaceConfig{Name: "eth0", DHCP: ptr.To(true), Addresses: []string{"10.0.0.1/24"}},
			fieldPath: "iface",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "valid with dhcp false and addresses",
			cfg:       &InterfaceConfig{Name: "eth0", DHCP: ptr.To(false), Addresses: []string{"10.0.0.1/24"}},
			fieldPath: "iface",
			expectErr: false,
		},
		{
			name:      "multiple errors",
			cfg:       &InterfaceConfig{Name: "eth/0", Addresses: []string{"badip"}, MTU: ptr.To[int32](0)},
			fieldPath: "iface",
			expectErr: true,
			errCount:  3,
		},
		{
			name:      "nil config",
			cfg:       nil,
			fieldPath: "iface",
			expectErr: false, // Function should handle nil gracefully
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateInterfaceConfig(tt.cfg, tt.fieldPath)
			if (len(errs) > 0) != tt.expectErr {
				t.Errorf("validateInterfaceConfig() expectErr %v, got errors: %v", tt.expectErr, errs)
			}
			if tt.expectErr && len(errs) != tt.errCount {
				t.Errorf("validateInterfaceConfig() expected %d errors, got %d: %v", tt.errCount, len(errs), errs)
			}
		})
	}
}

func TestValidateRoutes(t *testing.T) {
	scopeLink := uint8(unix.RT_SCOPE_LINK)
	scopeUniverse := uint8(unix.RT_SCOPE_UNIVERSE)
	invalidScope := uint8(100)

	tests := []struct {
		name      string
		routes    []RouteConfig
		fieldPath string
		expectErr bool
		errCount  int
	}{
		{
			name:      "valid default route",
			routes:    []RouteConfig{{Destination: "0.0.0.0/0", Gateway: "192.168.1.1", Scope: scopeUniverse}},
			fieldPath: "routes",
			expectErr: false,
		},
		{
			name:      "valid link-local route",
			routes:    []RouteConfig{{Destination: "169.254.0.0/16", Scope: scopeLink}},
			fieldPath: "routes",
			expectErr: false,
		},
		{
			name:      "empty destination",
			routes:    []RouteConfig{{Gateway: "192.168.1.1"}},
			fieldPath: "routes",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid destination CIDR",
			routes:    []RouteConfig{{Destination: "10.0.0/24", Gateway: "192.168.1.1"}},
			fieldPath: "routes",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "universe scope no gateway",
			routes:    []RouteConfig{{Destination: "10.0.0.0/8", Scope: scopeUniverse}},
			fieldPath: "routes",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "default scope (universe) no gateway",
			routes:    []RouteConfig{{Destination: "10.0.0.0/8"}}, // Scope defaults to Universe
			fieldPath: "routes",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid gateway IP",
			routes:    []RouteConfig{{Destination: "0.0.0.0/0", Gateway: "not-an-ip"}},
			fieldPath: "routes",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid scope value",
			routes:    []RouteConfig{{Destination: "10.0.0.0/8", Scope: invalidScope, Gateway: "192.168.1.1"}},
			fieldPath: "routes",
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid source IP",
			routes:    []RouteConfig{{Destination: "0.0.0.0/0", Gateway: "192.168.1.1", Source: "not-an-ip"}},
			fieldPath: "routes",
			expectErr: true,
			errCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateRoutes(tt.routes, tt.fieldPath)
			if (len(errs) > 0) != tt.expectErr {
				t.Errorf("validateRoutes() expectErr %v, got errors: %v", tt.expectErr, errs)
			}
			if tt.expectErr && len(errs) != tt.errCount {
				t.Errorf("validateRoutes() expected %d errors, got %d: %v", tt.errCount, len(errs), errs)
			}
		})
	}
}

func TestValidateNeighborConfig(t *testing.T) {
	tests := []struct {
		name      string
		neighbors []NeighborConfig
		expectErr bool
		errCount  int
	}{
		{
			name:      "valid neighbor",
			neighbors: []NeighborConfig{{Destination: "192.168.1.1", HardwareAddr: "00:1A:2B:3C:4D:5E"}},
			expectErr: false,
		},
		{
			name:      "empty destination",
			neighbors: []NeighborConfig{{HardwareAddr: "00:1A:2B:3C:4D:5E"}},
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid destination IP",
			neighbors: []NeighborConfig{{Destination: "invalid-ip", HardwareAddr: "00:1A:2B:3C:4D:5E"}},
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "empty hardware address",
			neighbors: []NeighborConfig{{Destination: "192.168.1.1"}},
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "invalid hardware address",
			neighbors: []NeighborConfig{{Destination: "192.168.1.1", HardwareAddr: "invalid-mac"}},
			expectErr: true,
			errCount:  1,
		},
		{
			name:      "multiple errors",
			neighbors: []NeighborConfig{{Destination: "bad-ip", HardwareAddr: "bad-mac"}},
			expectErr: true,
			errCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateNeighborConfig(tt.neighbors, "neighbors")
			if (len(errs) > 0) != tt.expectErr {
				t.Errorf("validateNeighborConfig() got errors: %v, want %v", errs, tt.expectErr)
			}
			if tt.expectErr && len(errs) != tt.errCount {
				t.Errorf("validateNeighborConfig() got %d errors (%v), want %d", len(errs), errs, tt.errCount)
			}
		})
	}
}

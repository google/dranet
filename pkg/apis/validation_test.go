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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		raw     *runtime.RawExtension
		wantErr bool
		errMsgs []string
	}{
		{
			name: "valid config",
			raw: &runtime.RawExtension{Raw: []byte(`{
				"interface": {
					"name": "eth0",
					"addresses": ["192.168.1.10/24", "2001:db8::1/64"],
					"mtu": 1500
				},
				"routes": [
					{
						"destination": "0.0.0.0/0",
						"gateway": "192.168.1.1"
					},
					{
						"destination": "2001:db8:abcd::/48",
						"gateway": "2001:db::1"
					}
				]
			}`)},
			wantErr: false,
		},
		{
			name:    "nil raw extension",
			raw:     nil,
			wantErr: false,
		},
		{
			name:    "nil raw field in raw extension",
			raw:     &runtime.RawExtension{Raw: nil},
			wantErr: false,
		},
		{
			name:    "empty raw field in raw extension",
			raw:     &runtime.RawExtension{Raw: []byte{}},
			wantErr: false,
		},
		{
			name:    "malformed json",
			raw:     &runtime.RawExtension{Raw: []byte(`{"interface": {"name": "eth0"`)}, // Missing closing brace
			wantErr: true,
			errMsgs: []string{"failed to unmarshal YAML data"},
		},
		{
			name: "invalid interface IP CIDR",
			raw: &runtime.RawExtension{Raw: []byte(`{
				"interface": {
					"name": "eth0",
					"addresses": ["192.168.1.10/240"]
				}
			}`)},
			wantErr: true,
			errMsgs: []string{"invalid IP in CIDR format 192.168.1.10/240"},
		},
		{
			name: "route with empty destination",
			raw: &runtime.RawExtension{Raw: []byte(`{
				"interface": {"name": "eth0", "addresses": ["192.168.1.10/24"]},
				"routes": [{"gateway": "192.168.1.1"}]
			}`)},
			wantErr: true,
			errMsgs: []string{"route 0: destination cannot be empty"},
		},
		{
			name: "route with invalid destination",
			raw: &runtime.RawExtension{Raw: []byte(`{
				"interface": {"name": "eth0", "addresses": ["192.168.1.10/24"]},
				"routes": [{"destination": "not-an-ip", "gateway": "192.168.1.1"}]
			}`)},
			wantErr: true,
			errMsgs: []string{"route 0: invalid destination IP or CIDR 'not-an-ip'"},
		},
		{
			name: "route with no gateway",
			raw: &runtime.RawExtension{Raw: []byte(`{
				"interface": {"name": "eth0", "addresses": ["192.168.1.10/24"]},
				"routes": [{"destination": "10.0.0.0/8"}]
			}`)},
			wantErr: true,
			errMsgs: []string{"route 0: for destination '10.0.0.0/8' must have a gateway"},
		},
		{
			name: "route with invalid gateway IP",
			raw: &runtime.RawExtension{Raw: []byte(`{
				"interface": {"name": "eth0", "addresses": ["192.168.1.10/24"]},
				"routes": [{"destination": "10.0.0.0/8", "gateway": "not-a-gateway"}]
			}`)},
			wantErr: true,
			errMsgs: []string{"route 0: invalid gateway IP 'not-a-gateway'"},
		},
		{
			name: "multiple errors",
			raw: &runtime.RawExtension{Raw: []byte(`{
				"interface": {
					"name": "eth0",
					"addresses": ["192.168.1.10/240", "10.0.0.1/invalid"]
				},
				"routes": [
					{"destination": "", "gateway": "192.168.1.1"},
					{"destination": "not-an-ip", "gateway": "192.168.1.1"},
					{"destination": "10.0.0.0/8"},
					{"destination": "10.0.1.0/24", "gateway": "not-a-gateway"}
				]
			}`)},
			wantErr: true,
			errMsgs: []string{
				"invalid IP in CIDR format 192.168.1.10/240",
				"invalid IP in CIDR format 10.0.0.1/invalid",
				"route 0: destination cannot be empty",
				"route 1: invalid destination IP or CIDR 'not-an-ip'",
				"route 3: invalid gateway IP 'not-a-gateway'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateConfig(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				for _, errMsg := range tt.errMsgs {
					if !strings.Contains(err.Error(), errMsg) {
						t.Errorf("ValidateConfig() error = %v, want to contain %v", err, errMsg)
					}
				}
			}
		})
	}
}

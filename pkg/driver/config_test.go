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

package driver

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestValidateConfig(t *testing.T) {
	testCases := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name: "valid config",
			config: `
ips:
- 192.168.1.10/24
routes:
- destination: 10.0.0.0/8
  gateway: 192.168.1.1
mtu: 1500
`,
			wantErr: false,
		},
		{
			name: "invalid ip",
			config: `
ips:
- a.b.c.d/24
routes:
- destination: 10.0.0.0/8
  gateway: 192.168.1.1
mtu: 1500
`,
			wantErr: true,
		},
		{
			name: "invalid route destination",
			config: `
ips:
- 192.168.1.10/24
routes:
- destination: a.b.c.d/8
  gateway: 192.168.1.1
mtu: 1500
`,
			wantErr: true,
		},
		{
			name:    "Empty config",
			config:  ``,
			wantErr: false,
		},
		{
			name: "invalid route",
			config: `
ips:
- 192.168.1.10/24
routes:
- destination: 10.0.0.0/8
mtu: 1500
`,
			wantErr: true,
		},
		{
			name: "invalid route",
			config: `
ips:
- 192.168.1.10/24
routes:
- gateway: 192.168.1.1
mtu: 1500
`,
			wantErr: true,
		},
		{
			name: "invalid route gateway",
			config: `
ips:
- 192.168.1.10/24
routes:
- destination: 10.0.0.0/8
  gateway: a.b.c.d
mtu: 1500
`,
			wantErr: true,
		},
		{
			name: "invalid yaml",
			config: `
ips:
- 192.168.1.10/24
routes:
- destination: 10.0.0.0/8
  gateway: 192.168.1.1
mtu: 1500
foo:
- bar
`,
			wantErr: true,
		},
		{
			name: "valid config with name",
			config: `
ips:
- 192.168.1.10/24
routes:
- destination: 10.0.0.0/8
  gateway: 192.168.1.1
name: eth1
mtu: 1500
`,
			wantErr: false,
		},
		{
			name: "valid config with ipv6",
			config: `
ips:
- 2001:db8::1/64
routes:
- destination: 2001:db8:1::/64
  gateway: 2001:db8::2
name: eth1
mtu: 1500
`,
			wantErr: false,
		},
		{
			name: "invalid config with ipv6",
			config: `
ips:
- 2001:db8::1/64
routes:
- destination: 2001:db8:1::/64
  gateway: 2001:db8::z
name: eth1
mtu: 1500
`,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := &runtime.RawExtension{}
			raw.Raw = []byte(tc.config)

			_, err := ValidateConfig(raw)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
		})
	}
}

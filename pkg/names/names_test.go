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

package names

import (
	"strings"
	"testing"
)

func TestSetDeviceName(t *testing.T) {
	tests := []struct {
		name   string
		ifName string
		want   string
	}{
		{"already compliant", "eth0", "eth0"},
		{"already compliant with hyphen", "my-device-1", "my-device-1"},
		{"needs normalization colons", "eth:0", NormalizedPrefix + "mv2gqorq"},
		{"needs normalization uppercase", "ETH0", NormalizedPrefix + "ivkeqma"},
		{"needs normalization underscore", "eth_int", NormalizedPrefix + "mv2gqx3jnz2a"},
		{"empty string", "", ""},
		{
			name:   "long name needs normalization",
			ifName: "very_long_interface_name_that_is_not_dns_compliant_at_all_and_exceeds_limits",
			// base32 of the above is much longer, this is just to check prefixing
			want: NormalizedPrefix + "ozsxe6k7nrxw4z27nfxhizlsmzqwgzk7nzqw2zk7orugc5c7nfzv63tporpwi3ttl5rw63lqnruwc3tul5qxix3bnrwf6ylomrpwk6ddmvswi427nruw22luom",
		},
		{"already compliant max length", strings.Repeat("a", 63), strings.Repeat("a", 63)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SetDeviceName(tt.ifName); got != tt.want {
				t.Errorf("SetDeviceName(%q) = %q, want %q", tt.ifName, got, tt.want)
			}
		})
	}
}

func TestSetAndGetOriginalName(t *testing.T) {
	testIfNames := []string{
		"eth0",
		"my-nic",
		"eth:0:1",
		"veth_pair_A",
		"UPPERCASE_NIC",
		"a.b.c.d.e.f",
		strings.Repeat("a_b", 30), // long non-compliant name
		"",
	}

	for _, ifName := range testIfNames {
		t.Run(ifName, func(t *testing.T) {
			deviceName := SetDeviceName(ifName)
			originalName := GetOriginalName(deviceName)
			if originalName != ifName {
				t.Errorf("SetDeviceName -> GetOriginalName round trip failed for %q: SetDeviceName returned %q, GetOriginalName returned %q",
					ifName, deviceName, originalName)
			}
		})
	}
}

/*
Copyright 2024 Google LLC

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

package inventory

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParsePCIAddress(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		want    *pciAddress
		wantErr bool
	}{
		{
			name:  "valid with domain",
			input: "0000:00:04.0",
			want: &pciAddress{
				domain:   "0000",
				bus:      "00",
				device:   "04",
				function: "0",
			},
			wantErr: false,
		},
		{
			name:  "valid without domain",
			input: "00:04.0",
			want: &pciAddress{
				domain:   "",
				bus:      "00",
				device:   "04",
				function: "0",
			},
			wantErr: false,
		},
		{
			name:    "invalid format",
			input:   "not-a-pci-address",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "embedded in string",
			input:   "pci-0000:8c:00.0-device",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePCIAddress(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("pciAddressFromString() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(pciAddress{})); diff != "" {
				t.Errorf("pciAddressFromString() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPCIAddressFromPath(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		want    *pciAddress
		wantErr bool
	}{
		{
			name:  "simple path",
			input: "/sys/devices/pci0000:00/0000:00:04.0/virtio1/net/eth0",
			want: &pciAddress{
				domain:   "0000",
				bus:      "00",
				device:   "04",
				function: "0",
			},
			wantErr: false,
		},
		{
			name:  "hierarchical path",
			input: "/sys/devices/pci0000:8c/0000:8c:00.0/0000:8d:00.0/0000:8e:02.0/0000:91:00.0/net/eth3",
			want: &pciAddress{
				domain:   "0000",
				bus:      "91",
				device:   "00",
				function: "0",
			},
			wantErr: false,
		},
		{
			name:    "no pci address in path",
			input:   "/sys/devices/virtual/net/lo",
			wantErr: true,
		},
		{
			name:    "empty path",
			input:   "",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pciAddressFromPath(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("pciAddressFromPath() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(pciAddress{})); diff != "" {
				t.Errorf("pciAddressFromPath() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

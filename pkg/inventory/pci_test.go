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
)

func TestNormalizePCIAddress(t *testing.T) {
	testCases := []struct {
		name       string
		pciAddress string
		want       string
	}{
		{
			name:       "Standard PCI Address",
			pciAddress: "0000:8a:00.0",
			want:       "net1-0000-8a-00-0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePCIAddress(tc.pciAddress); got != tc.want {
				t.Errorf("NormalizePCIAddress(%v) = %v, want %v", tc.pciAddress, got, tc.want)
			}
		})
	}
}

func TestDeNormalizePCIAddress(t *testing.T) {
	testCases := []struct {
		name              string
		normalizedAddress string
		want              string
	}{
		{
			name:              "Standard Normalized Address",
			normalizedAddress: "net1-0000-8a-00-0",
			want:              "0000:8a:00.0",
		},
		{
			name:              "Empty Normalized Address",
			normalizedAddress: "",
			want:              "",
		},
		{
			name:              "Invalid Format - No Prefix",
			normalizedAddress: "0000-8a-00-0",
			want:              "",
		},
		{
			name:              "Invalid Format - Wrong Number of Parts",
			normalizedAddress: "net1-0000-8a-00",
			want:              "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeNormalizePCIAddress(tc.normalizedAddress); got != tc.want {
				t.Errorf("DeNormalizePCIAddress(%v) = %v, want %v", tc.normalizedAddress, got, tc.want)
			}
		})
	}
}

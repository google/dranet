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

package pcidb

import (
	_ "embed"
	"reflect"
	"testing"
)

func Test_getPCI(t *testing.T) {

	tests := []struct {
		name      string
		vendor    string
		device    string
		subvendor string
		subdevice string
		want      *Entry
		wantErr   bool
	}{
		{
			name:    "empty",
			wantErr: true,
		},
		{
			name:    "wrong vendor - 5 digits",
			vendor:  "12345",
			wantErr: true,
		},
		{
			name:    "device and no vendor",
			device:  "1234",
			wantErr: true,
		},
		{
			name:   "vendor 001c",
			vendor: "001c",
			want: &Entry{
				Vendor: "PEAK-System Technik GmbH",
			},
		},
		{
			name:   "vendor 001c device 0001",
			vendor: "001c",
			device: "0001",
			want: &Entry{
				Vendor: "PEAK-System Technik GmbH",
				Device: "PCAN-PCI CAN-Bus controller",
			},
		},
		{
			name:      "vendor 001c device 0001 subsystem 001c 0005",
			vendor:    "001c",
			device:    "0001",
			subvendor: "001c",
			subdevice: "0005",
			want: &Entry{
				Vendor:    "PEAK-System Technik GmbH",
				Device:    "PCAN-PCI CAN-Bus controller",
				Subsystem: "2 Channel CAN Bus SJC1000 (Optically Isolated)",
			},
		},
		{
			name:      "subsystem does not exist",
			vendor:    "001c",
			device:    "0001",
			subvendor: "001c",
			subdevice: "fd05",
			want: &Entry{
				Vendor: "PEAK-System Technik GmbH",
				Device: "PCAN-PCI CAN-Bus controller",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetDevice(tt.vendor, tt.device, tt.subvendor, tt.subdevice)
			if (err != nil) != tt.wantErr {
				t.Errorf("getPCI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getPCI() = %v, want %v", got, tt.want)
			}
		})
	}
}

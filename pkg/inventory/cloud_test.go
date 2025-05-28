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
	"github.com/google/go-cmp/cmp"
	"testing"

	"github.com/google/dranet/pkg/cloudprovider"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/utils/ptr"
)

func TestGetProviderAttributes(t *testing.T) {
	tests := []struct {
		name     string
		mac      string
		instance *cloudprovider.CloudInstance
		want     map[resourceapi.QualifiedName]resourceapi.DeviceAttribute
	}{
		{
			name:     "nil instance",
			mac:      "00:11:22:33:44:55",
			instance: nil,
			want:     nil,
		},
		{
			name: "instance with no interfaces",
			mac:  "00:11:22:33:44:55",
			instance: &cloudprovider.CloudInstance{
				Provider:   cloudprovider.CloudProviderGCE,
				Interfaces: []cloudprovider.NetworkInterface{},
			},
			want: nil,
		},
		{
			name: "MAC not found in instance interfaces",
			mac:  "00:11:22:33:44:FF", // MAC that won't be found
			instance: &cloudprovider.CloudInstance{
				Provider: cloudprovider.CloudProviderGCE,
				Interfaces: []cloudprovider.NetworkInterface{
					{Mac: "00:11:22:33:44:55", Network: "projects/12345/networks/test-network"},
				},
			},
			want: nil,
		},
		{
			name: "GCE provider, MAC found, valid network",
			mac:  "00:11:22:33:44:55",
			instance: &cloudprovider.CloudInstance{
				Provider: cloudprovider.CloudProviderGCE,
				Interfaces: []cloudprovider.NetworkInterface{
					{Mac: "00:11:22:33:44:55", Network: "projects/12345/networks/test-network"},
					{Mac: "AA:BB:CC:DD:EE:FF", Network: "projects/67890/networks/other-network"},
				},
			},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"gce.dra.net/networkName":          {StringValue: ptr.To("test-network")},
				"gce.dra.net/networkProjectNumber": {IntValue: ptr.To(int64(12345))},
			},
		},
		{
			name: "GCE provider, MAC found, invalid network string for GCE parsing",
			mac:  "00:11:22:33:44:55",
			instance: &cloudprovider.CloudInstance{
				Provider: cloudprovider.CloudProviderGCE,
				Interfaces: []cloudprovider.NetworkInterface{
					{Mac: "00:11:22:33:44:55", Network: "invalid-gce-network-string"},
				},
			},
			want: nil, // gce.GetGCEAttributes returns nil for invalid network string
		},
		{
			name: "Unsupported provider, MAC found",
			mac:  "00:11:22:33:44:55",
			instance: &cloudprovider.CloudInstance{
				Provider: cloudprovider.CloudProviderAWS, // Unsupported provider
				Interfaces: []cloudprovider.NetworkInterface{
					{Mac: "00:11:22:33:44:55", Network: "aws-network-info"},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getProviderAttributes(tt.mac, tt.instance)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("getProviderAttributes() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getLastSegmentAndTruncate(t *testing.T) {
	type args struct {
		s         string
		maxLength int
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "shorter than max",
			args: args{s: "/path/to/segment", maxLength: 20},
			want: "segment",
		},
		{
			name: "longer than max",
			args: args{s: "/path/to/verylongsegmentname", maxLength: 10},
			want: "verylongse",
		},
		{
			name: "equal to max",
			args: args{s: "/path/to/segmentname", maxLength: 12},
			want: "segmentname",
		},
		{
			name: "empty string",
			args: args{s: "", maxLength: 10},
			want: "",
		},
		{
			name: "no path separators",
			args: args{s: "justafilename", maxLength: 20},
			want: "justafilename",
		},
		{
			name: "no path separators, needs truncation",
			args: args{s: "justaverylongfilename", maxLength: 10},
			want: "justaveryl",
		},
		{
			name: "trailing slash",
			args: args{s: "/path/to/segment/", maxLength: 10},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getLastSegmentAndTruncate(tt.args.s, tt.args.maxLength); got != tt.want {
				t.Errorf("getLastSegmentAndTruncate() = %v, want %v", got, tt.want)
			}
		})
	}
}

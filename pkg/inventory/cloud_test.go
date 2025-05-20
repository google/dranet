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

	"github.com/google/dranet/pkg/cloudprovider"
)

func Test_cloudNetwork(t *testing.T) {
	type args struct {
		mac      string
		instance *cloudprovider.CloudInstance
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "nil instance",
			args: args{
				mac:      "00:11:22:33:44:55",
				instance: nil,
			},
			want: "",
		},
		{
			name: "empty interfaces",
			args: args{
				mac: "00:11:22:33:44:55",
				instance: &cloudprovider.CloudInstance{
					Interfaces: []cloudprovider.NetworkInterface{},
				},
			},
			want: "",
		},
		{
			name: "mac match",
			args: args{
				mac: "00:11:22:33:44:55",
				instance: &cloudprovider.CloudInstance{
					Interfaces: []cloudprovider.NetworkInterface{
						{Mac: "aa:bb:cc:dd:ee:ff", Network: "/projects/proj1/global/networks/net1"},
						{Mac: "00:11:22:33:44:55", Network: "/projects/proj2/global/networks/my-super-long-network-name-that-will-be-truncated-and-then-some-more"},
					},
				},
			},
			want: "my-super-long-network-name-that-will-be-truncated-and-then-some-", // Expected 64 char truncation
		},
		{
			name: "mac no match",
			args: args{
				mac: "11:22:33:44:55:66",
				instance: &cloudprovider.CloudInstance{
					Interfaces: []cloudprovider.NetworkInterface{
						{Mac: "aa:bb:cc:dd:ee:ff", Network: "/projects/proj1/global/networks/net1"},
						{Mac: "00:11:22:33:44:55", Network: "/projects/proj2/global/networks/net2"},
					},
				},
			},
			want: "",
		},
		{
			name: "mac match, network needs no truncation",
			args: args{
				mac: "aa:bb:cc:dd:ee:ff",
				instance: &cloudprovider.CloudInstance{
					Interfaces: []cloudprovider.NetworkInterface{
						{Mac: "aa:bb:cc:dd:ee:ff", Network: "global/networks/short-net"},
					},
				},
			},
			want: "short-net",
		},
		{
			name: "mac match, empty network string",
			args: args{
				mac:      "00:11:22:33:44:55",
				instance: &cloudprovider.CloudInstance{Interfaces: []cloudprovider.NetworkInterface{{Mac: "00:11:22:33:44:55", Network: ""}}},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cloudNetwork(tt.args.mac, tt.args.instance); got != tt.want {
				t.Errorf("cloudNetwork() = %v, want %v", got, tt.want)
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

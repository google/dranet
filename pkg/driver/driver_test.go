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

	"github.com/google/dranet/pkg/apis"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/utils/ptr"
)

func TestGetDeviceName(t *testing.T) {
	type args struct {
		device *resourceapi.Device
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "nil device",
			args: args{device: nil},
			want: "",
		},
		{
			name: "nil basic",
			args: args{device: &resourceapi.Device{Name: "test-dev"}},
			want: "test-dev",
		},
		{
			name: "nil attributes",
			args: args{device: &resourceapi.Device{Name: "test-dev", Basic: &resourceapi.BasicDevice{}}},
			want: "test-dev",
		},
		{
			name: "missing kind attribute",
			args: args{device: &resourceapi.Device{
				Name: "test-dev",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{},
				},
			}},
			want: "test-dev",
		},
		{
			name: "kind attribute not a string",
			args: args{device: &resourceapi.Device{
				Name: "test-dev",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"dra.net/kind": {BoolValue: ptr.To(true)},
					},
				},
			}},
			want: "test-dev",
		},
		{
			name: "network kind, ifName present",
			args: args{device: &resourceapi.Device{
				Name: "normalized-eth0",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"dra.net/kind":   {StringValue: ptr.To(apis.NetworkKind)},
						"dra.net/ifName": {StringValue: ptr.To("eth0")},
					},
				},
			}},
			want: "eth0",
		},
		{
			name: "network kind, ifName missing",
			args: args{device: &resourceapi.Device{
				Name: "normalized-eth0",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"dra.net/kind": {StringValue: ptr.To(apis.NetworkKind)},
					},
				},
			}},
			want: "normalized-eth0",
		},
		{
			name: "network kind, ifName not a string",
			args: args{device: &resourceapi.Device{
				Name: "normalized-eth0",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"dra.net/kind":   {StringValue: ptr.To(apis.NetworkKind)},
						"dra.net/ifName": {IntValue: ptr.To[int64](123)},
					},
				},
			}},
			want: "normalized-eth0",
		},
		{
			name: "rdma kind, rdmaDevName present",
			args: args{device: &resourceapi.Device{
				Name: "normalized-rdma0",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"dra.net/kind":        {StringValue: ptr.To(apis.RdmaKind)},
						"dra.net/rdmaDevName": {StringValue: ptr.To("mlx5_0")},
					},
				},
			}},
			want: "mlx5_0",
		},
		{
			name: "rdma kind, rdmaDevName missing",
			args: args{device: &resourceapi.Device{
				Name: "normalized-rdma0",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"dra.net/kind": {StringValue: ptr.To(apis.RdmaKind)},
					},
				},
			}},
			want: "normalized-rdma0",
		},
		{
			name: "rdma kind, rdmaDevName not a string",
			args: args{device: &resourceapi.Device{
				Name: "normalized-rdma0",
				Basic: &resourceapi.BasicDevice{
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"dra.net/kind":        {StringValue: ptr.To(apis.RdmaKind)},
						"dra.net/rdmaDevName": {BoolValue: ptr.To(false)},
					},
				},
			}},
			want: "normalized-rdma0",
		},
		{
			name: "unknown kind",
			args: args{device: &resourceapi.Device{Name: "test-dev", Basic: &resourceapi.BasicDevice{Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{"dra.net/kind": {StringValue: ptr.To("unknown")}}}}},
			want: "test-dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getDeviceName(tt.args.device); got != tt.want {
				t.Errorf("GetDeviceName() = %v, want %v", got, tt.want)
			}
		})
	}
}

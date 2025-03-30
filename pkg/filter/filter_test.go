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

package filter

import (
	"reflect"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	resourcev1beta1 "k8s.io/api/resource/v1beta1"
	"k8s.io/utils/ptr"
)

func Test_filterDevices(t *testing.T) {
	tests := []struct {
		name           string
		celProgram     cel.Program
		devices        []resourcev1beta1.Device
		expectedLength int
	}{
		{
			name:           "nil program",
			celProgram:     nil,
			devices:        []resourcev1beta1.Device{{Name: "dev1"}},
			expectedLength: 1,
		},
		{
			name:       "filter by attribute",
			celProgram: mustCompileCEL(t, `attributes["kind"].StringValue == "network"`),
			devices: []resourcev1beta1.Device{
				{
					Name: "dev1",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("network")},
						},
					},
				},
				{
					Name: "dev2",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("rdma")},
						},
					},
				},
			},
			expectedLength: 1,
		},
		{
			name:       "filter by multiple attributes",
			celProgram: mustCompileCEL(t, `attributes["kind"].StringValue == "network" && attributes["name"].StringValue == "eth0"`),
			devices: []resourcev1beta1.Device{
				{
					Name: "dev1",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("network")},
							"name": {StringValue: ptr.To("eth0")},
						},
					},
				},
				{
					Name: "dev2",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("rdma")},
							"name": {StringValue: ptr.To("eth1")},
						},
					},
				},
			},
			expectedLength: 1,
		},
		{
			name:       "not veth",
			celProgram: mustCompileCEL(t, `attributes["type"].StringValue  != "veth"`),
			devices: []resourcev1beta1.Device{
				{
					Name: "dev1",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("network")},
							"name": {StringValue: ptr.To("eth0")},
							"type": {StringValue: ptr.To("veth")},
						},
					},
				},
				{
					Name: "dev2",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("rdma")},
							"name": {StringValue: ptr.To("eth1")},
							"type": {StringValue: ptr.To("veth")},
						},
					},
				},
			},
			expectedLength: 0,
		},
		{
			name:       "not virtual",
			celProgram: mustCompileCEL(t, `attributes["virtual"].BoolValue`),
			devices: []resourcev1beta1.Device{
				{
					Name: "dev1",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind":    {StringValue: ptr.To("network")},
							"name":    {StringValue: ptr.To("eth0")},
							"type":    {StringValue: ptr.To("veth")},
							"virtual": {BoolValue: ptr.To(true)},
						},
					},
				},
				{
					Name: "dev2",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind":    {StringValue: ptr.To("rdma")},
							"name":    {StringValue: ptr.To("eth1")},
							"type":    {StringValue: ptr.To("veth")},
							"virtual": {BoolValue: ptr.To(true)},
						},
					},
				},
			},
			expectedLength: 2,
		},
		{
			name:           "empty devices",
			celProgram:     mustCompileCEL(t, `attributes["kind"].StringValue == "network"`),
			devices:        []resourcev1beta1.Device{},
			expectedLength: 0,
		},
		{
			name:       "all devices filtered",
			celProgram: mustCompileCEL(t, `attributes["kind"].StringValue == "network"`),
			devices: []resourcev1beta1.Device{
				{
					Name: "dev1",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("rdma")},
						},
					},
				},
				{
					Name: "dev2",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("rdma")},
						},
					},
				},
			},
			expectedLength: 0,
		},
		{
			name:       "no filter",
			celProgram: mustCompileCEL(t, `true`),
			devices: []resourcev1beta1.Device{
				{
					Name: "dev1",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("rdma")},
						},
					},
				},
				{
					Name: "dev2",
					Basic: &resourcev1beta1.BasicDevice{
						Attributes: map[resourcev1beta1.QualifiedName]resourcev1beta1.DeviceAttribute{
							"kind": {StringValue: ptr.To("network")},
						},
					},
				},
			},
			expectedLength: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devices := FilterDevices(tt.celProgram, tt.devices)
			if len(devices) != tt.expectedLength {
				t.Errorf("filterDevices() length = %v, want %v", len(devices), tt.expectedLength)
			}
		})
	}
}

func mustCompileCEL(t *testing.T, expression string) cel.Program {
	t.Helper()
	env, err := cel.NewEnv(
		ext.NativeTypes(
			reflect.ValueOf(resourcev1beta1.DeviceAttribute{}),
		),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.ObjectType("v1beta1.DeviceAttribute"))),
	)
	if err != nil {
		t.Fatalf("error creating CEL environment: %v", err)
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		t.Fatalf("type-check error: %s", issues.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program construction error: %s", err)
	}
	return prg
}

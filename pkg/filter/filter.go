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
	"github.com/google/cel-go/cel"
	celtypes "github.com/google/cel-go/common/types"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/klog/v2"
)

func FilterDevices(celProgram cel.Program, devices []resourcev1.Device) []resourcev1.Device {
	if celProgram == nil {
		return devices
	}
	// filter in place
	var filteredDevices []resourcev1.Device
	for _, dev := range devices {
		out, _, err := celProgram.Eval(map[string]interface{}{"attributes": dev.Attributes})
		if err != nil {
			klog.Infof("prg.Eval() failed: %v", err)
			filteredDevices = append(filteredDevices, dev)
			continue
		}
		// The result should be a boolean.
		result, ok := out.(celtypes.Bool)
		if !ok {
			klog.Infof("CEL expression did not evaluate to a boolean got: %T", out)
			continue
		}
		if result == celtypes.True {
			filteredDevices = append(filteredDevices, dev)
		}
	}
	return filteredDevices
}

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

package driver

import (
	"context"
	"testing"

	"github.com/containerd/nri/pkg/api"
	"k8s.io/apimachinery/pkg/types"
)

func TestCreateContainerNoDuplicateDevices(t *testing.T) {
	np := &NetworkDriver{
		podConfigStore: NewPodConfigStore(),
	}

	podUID := types.UID("test-pod")
	pod := &api.PodSandbox{
		Uid:       string(podUID),
		Name:      "test-pod",
		Namespace: "test-ns",
	}
	ctr := &api.Container{
		Name: "test-container",
	}

	// Setup pod config with duplicate RDMA devices
	rdmaDevChars := []LinuxDevice{
		{Path: "/dev/infiniband/uverbs0", Type: "c", Major: 231, Minor: 192},
	}
	podConfig := PodConfig{
		RDMADevice: RDMAConfig{
			DevChars: rdmaDevChars,
		},
	}
	np.podConfigStore.Set(podUID, "eth0", podConfig)
	np.podConfigStore.Set(podUID, "eth1", podConfig)

	adjust, _, err := np.CreateContainer(context.Background(), pod, ctr)
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	if len(adjust.Linux.Devices) != 1 {
		t.Errorf("CreateContainer should not adjust the same device multiple times\n%v", adjust.Linux.Devices)
	}
}

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
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/google/dranet/pkg/apis"
	"k8s.io/apimachinery/pkg/types"
)

func TestNewPodConfigStore(t *testing.T) {
	store := NewPodConfigStore()
	if store == nil {
		t.Fatal("NewPodConfigStore() returned nil")
	}
	if store.configs == nil {
		t.Error("NewPodConfigStore() did not initialize configs map")
	}
}

func TestPodConfigStore_SetAndGet(t *testing.T) {
	store := NewPodConfigStore()
	podUID := types.UID("test-pod-uid-1")
	deviceName := "eth0"
	config := Config{
		NetDevice: apis.InterfaceConfig{Name: "eth0-pod"},
		NetNamespaceRoutes: []apis.RouteConfig{
			{Destination: "0.0.0.0/0", Gateway: "192.168.1.1"},
		},
		RDMADevice: RDMAConfig{LinkDev: "mlx5_0"},
	}

	// Test Get on non-existent item
	_, found := store.Get(podUID, deviceName)
	if found {
		t.Errorf("Get() found a config before Set(), expected not found")
	}

	store.Set(podUID, deviceName, config)

	retrievedConfig, found := store.Get(podUID, deviceName)
	if !found {
		t.Fatalf("Get() did not find config after Set(), expected found")
	}
	if !reflect.DeepEqual(retrievedConfig, config) {
		t.Errorf("Get() retrieved %+v, want %+v", retrievedConfig, config)
	}

	// Test Get with different deviceName
	_, found = store.Get(podUID, "eth1")
	if found {
		t.Errorf("Get() found config for wrong deviceName 'eth1', expected not found")
	}

	// Test Get with different podUID
	_, found = store.Get(types.UID("other-pod-uid"), deviceName)
	if found {
		t.Errorf("Get() found config for wrong podUID, expected not found")
	}

	// Test overwriting
	newConfig := Config{NetDevice: apis.InterfaceConfig{Name: "eth0-new"}}
	store.Set(podUID, deviceName, newConfig)
	retrievedConfig, found = store.Get(podUID, deviceName)
	if !found {
		t.Fatalf("Get() did not find config after overwrite, expected found")
	}
	if !reflect.DeepEqual(retrievedConfig, newConfig) {
		t.Errorf("Get() retrieved %+v after overwrite, want %+v", retrievedConfig, newConfig)
	}
}

func TestPodConfigStore_DeletePod(t *testing.T) {
	store := NewPodConfigStore()
	podUID1 := types.UID("test-pod-uid-1")
	podUID2 := types.UID("test-pod-uid-2")
	dev1 := "eth0"
	dev2 := "eth1"
	config1 := Config{NetDevice: apis.InterfaceConfig{Name: "p1eth0"}}
	config2 := Config{NetDevice: apis.InterfaceConfig{Name: "p1eth1"}}
	config3 := Config{NetDevice: apis.InterfaceConfig{Name: "p2eth0"}}

	store.Set(podUID1, dev1, config1)
	store.Set(podUID1, dev2, config2)
	store.Set(podUID2, dev1, config3)

	store.DeletePod(podUID1)

	_, found := store.Get(podUID1, dev1)
	if found {
		t.Errorf("Get() found config for podUID1 device %s after DeletePod(), expected not found", dev1)
	}
	_, found = store.Get(podUID1, dev2)
	if found {
		t.Errorf("Get() found config for podUID1 device %s after DeletePod(), expected not found", dev2)
	}

	retrievedConfig3, found := store.Get(podUID2, dev1)
	if !found {
		t.Errorf("Get() did not find config for podUID2 after deleting podUID1, expected found")
	}
	if !reflect.DeepEqual(retrievedConfig3, config3) {
		t.Errorf("Get() for podUID2 retrieved %+v, want %+v", retrievedConfig3, config3)
	}

	// Test deleting non-existent pod
	store.DeletePod(types.UID("non-existent-pod")) // Should not panic
}

func TestPodConfigStore_GetPodConfigs(t *testing.T) {
	store := NewPodConfigStore()
	podUID1 := types.UID("test-pod-uid-1")
	podUID2 := types.UID("test-pod-uid-2")
	dev1 := "eth0"
	dev2 := "eth1"
	config1 := Config{NetDevice: apis.InterfaceConfig{Name: "p1eth0"}}
	config2 := Config{NetDevice: apis.InterfaceConfig{Name: "p1eth1"}}
	config3 := Config{NetDevice: apis.InterfaceConfig{Name: "p2eth0"}}

	store.Set(podUID1, dev1, config1)
	store.Set(podUID1, dev2, config2)
	store.Set(podUID2, dev1, config3)

	expectedPod1Configs := map[string]Config{
		dev1: config1,
		dev2: config2,
	}

	pod1Configs, found := store.GetPodConfigs(podUID1)
	if !found {
		t.Fatalf("GetPodConfigs() did not find configs for podUID1, expected found")
	}
	if !reflect.DeepEqual(pod1Configs, expectedPod1Configs) {
		t.Errorf("GetPodConfigs() for podUID1 returned %+v, want %+v", pod1Configs, expectedPod1Configs)
	}

	// Test GetPodConfigs for non-existent pod
	_, found = store.GetPodConfigs(types.UID("non-existent-pod"))
	if found {
		t.Errorf("GetPodConfigs() found configs for non-existent pod, expected not found")
	}

	// Modify returned map and check if original is unchanged
	pod1Configs["newDev"] = Config{}
	originalPod1Configs, _ := store.GetPodConfigs(podUID1)
	if !reflect.DeepEqual(originalPod1Configs, expectedPod1Configs) {
		t.Errorf("Original map in store was modified after GetPodConfigs() returned map was changed. Original: %+v, Expected: %+v", originalPod1Configs, expectedPod1Configs)
	}
}

func TestPodConfigStore_ThreadSafety(t *testing.T) {
	store := NewPodConfigStore()
	numGoroutines := 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			podUID := types.UID(fmt.Sprintf("pod-%d", i))
			deviceName := fmt.Sprintf("eth%d", i%2)
			config := Config{NetDevice: apis.InterfaceConfig{Name: fmt.Sprintf("dev-%d", i)}}
			store.Set(podUID, deviceName, config)
			retrieved, _ := store.Get(podUID, deviceName)
			if !reflect.DeepEqual(retrieved, config) {
				t.Errorf("goroutine %d: Get() retrieved %+v, want %+v", i, retrieved, config)
			}
			if i%10 == 0 {
				store.DeletePod(podUID)
				_, found := store.Get(podUID, deviceName)
				if found {
					t.Errorf("goroutine %d: Get() found config after DeletePod()", i)
				}
			}
		}(i)
	}
	wg.Wait()
}

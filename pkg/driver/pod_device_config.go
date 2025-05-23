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
	"sync"

	"github.com/google/dranet/pkg/apis"
	"k8s.io/apimachinery/pkg/types"
)

// PodConfig holds the set of configurations to be applied for a single
// network device allocated to a Pod. This includes network interface settings,
// routes for the Pod's network namespace, and RDMA configurations.
type PodConfig struct {
	Claim types.NamespacedName
	// NetDevice specifies the configuration for the network interface itself,
	// such as its desired name within the Pod, IP addresses, and MTU.
	NetDevice apis.InterfaceConfig

	// NetNamespaceRoutes lists the routes to be configured within the Pod's
	// network namespace, associated with this network device.
	NetNamespaceRoutes []apis.RouteConfig

	// RDMADevice holds RDMA-specific configurations if the network device
	// has associated RDMA capabilities.
	RDMADevice RDMAConfig
}

// RDMAConfig contains parameters for setting up an RDMA device associated
// with a network interface.
type RDMAConfig struct {
	// LinkDev is the name of the RDMA link device (e.g., "mlx5_0") that
	// corresponds to the allocated network device.
	LinkDev string

	// DevChars is a list of absolute paths to the user-space RDMA character
	// devices (e.g., "/dev/infiniband/uverbs0", "/dev/infiniband/rdma_cm")
	// that should be made available to the Pod.
	DevChars []string
}

// PodConfigStore provides a thread-safe, centralized store for all network device configurations
// across multiple Pods. It is indexed by the Pod's UID, and for each Pod, it maps
// network device names (as allocated) to their specific Config.
type PodConfigStore struct {
	mu      sync.RWMutex
	configs map[types.UID]map[string]PodConfig
}

// NewPodConfigStore creates and returns a new instance of PodConfigStore.
func NewPodConfigStore() *PodConfigStore {
	return &PodConfigStore{
		configs: make(map[types.UID]map[string]PodConfig),
	}
}

// Set stores the configuration for a specific device under a given Pod UID.
// If a configuration for the Pod UID or device name already exists, it will be overwritten.
func (s *PodConfigStore) Set(podUID types.UID, deviceName string, config PodConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.configs[podUID]; !ok {
		s.configs[podUID] = make(map[string]PodConfig)
	}
	s.configs[podUID][deviceName] = config
}

// Get retrieves the configuration for a specific device under a given Pod UID.
// It returns the Config and true if found, otherwise an empty Config and false.
func (s *PodConfigStore) Get(podUID types.UID, deviceName string) (PodConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if podConfigs, ok := s.configs[podUID]; ok {
		config, found := podConfigs[deviceName]
		return config, found
	}
	return PodConfig{}, false
}

// DeletePod removes all configurations associated with a given Pod UID.
func (s *PodConfigStore) DeletePod(podUID types.UID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.configs, podUID)
}

// GetPodConfigs retrieves all device configurations for a given Pod UID.
// It is indexed by the Pod's UID, and for each Pod, it maps network device names (as allocated)
// to their specific Config.
func (s *PodConfigStore) GetPodConfigs(podUID types.UID) (map[string]PodConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	podConfigs, found := s.configs[podUID]
	if !found {
		return nil, false
	}
	// Return a copy to prevent external modification of the internal map
	configsCopy := make(map[string]PodConfig, len(podConfigs))
	for k, v := range podConfigs {
		configsCopy[k] = v
	}
	return configsCopy, true
}

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
	"strings"
	"testing"

	"github.com/containerd/nri/pkg/api"
	"github.com/google/dranet/pkg/inventory"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

func TestCreateContainerMetrics(t *testing.T) {
	testCases := []struct {
		name           string
		podConfigStore *PodConfigStore
		expectSuccess  bool
	}{
		{
			name:           "Success",
			podConfigStore: NewPodConfigStore(),
			expectSuccess:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nriPluginRequestsTotal.Reset()
			nriPluginRequestsLatencySeconds.Reset()
			np := &NetworkDriver{
				podConfigStore: tc.podConfigStore,
				netdb:          inventory.New(),
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

			np.CreateContainer(context.Background(), pod, ctr)
			expected := `
						# HELP dranet_driver_nri_plugin_requests_latency_seconds NRI plugin request latency in seconds.
						# TYPE dranet_driver_nri_plugin_requests_latency_seconds histogram
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="0.005"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="0.01"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="0.025"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="0.05"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="0.1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="0.25"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="0.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="2.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="10"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="CreateContainer",status="noop",le="+Inf"} 1
						`
			if err := testutil.CollectAndCompare(nriPluginRequestsLatencySeconds, strings.NewReader(expected), "dranet_driver_nri_plugin_requests_latency_seconds_bucket"); err != nil {
				t.Fatalf("CollectAndCompare failed: %v", err)
			}
			if tc.expectSuccess {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodCreateContainer, statusNoop)); got != float64(1) {
					t.Errorf("Expected 1 success, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodCreateContainer, statusFailed)); got != float64(0) {
					t.Errorf("Expected 0 failures, got %f", got)
				}
			} else {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodCreateContainer, statusSuccess)); got != float64(0) {
					t.Errorf("Expected 0 successes, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodCreateContainer, statusFailed)); got != float64(1) {
					t.Errorf("Expected 1 failure, got %f", got)
				}
			}
		})
	}
}

func TestRunPodSandboxMetrics(t *testing.T) {
	podUID := types.UID("test-pod")
	podUIDHostNetwork := types.UID("test-pod-host-network")

	testCases := []struct {
		name           string
		podConfigStore *PodConfigStore
		pod            *api.PodSandbox
		expectSuccess  bool
	}{
		{
			name:           "Success",
			podConfigStore: NewPodConfigStore(),
			pod: &api.PodSandbox{
				Uid:       string(podUID),
				Name:      "test-pod",
				Namespace: "test-ns",
				Linux: &api.LinuxPodSandbox{
					Namespaces: []*api.LinuxNamespace{
						{
							Type: "network",
							Path: "/var/run/netns/test",
						},
					},
				},
			},
			expectSuccess: true,
		},
		{
			name:           "Failure - Host Network",
			podConfigStore: NewPodConfigStore(),
			pod: &api.PodSandbox{
				Uid:       string(podUIDHostNetwork),
				Name:      "test-pod-host-network",
				Namespace: "test-ns",
				Linux:     &api.LinuxPodSandbox{}, // No network namespace
			},
			expectSuccess: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nriPluginRequestsTotal.Reset()
			nriPluginRequestsLatencySeconds.Reset()
			np := &NetworkDriver{
				podConfigStore: tc.podConfigStore,
				netdb:          inventory.New(),
			}

			// For the failure case, a pod config must exist.
			if !tc.expectSuccess {
				tc.podConfigStore.Set(podUIDHostNetwork, "eth0", PodConfig{})
			}

			np.RunPodSandbox(context.Background(), tc.pod)
			status := statusSuccess
			if !tc.expectSuccess {
				status = statusFailed
			}
			expected := `
						# HELP dranet_driver_nri_plugin_requests_latency_seconds NRI plugin request latency in seconds.
						# TYPE dranet_driver_nri_plugin_requests_latency_seconds histogram
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="0.005"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="0.01"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="0.025"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="0.05"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="0.1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="0.25"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="0.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="2.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="10"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RunPodSandbox",le="+Inf"} 1
						`
			expected = strings.Replace(expected, `method="RunPodSandbox"`, `method="RunPodSandbox",status="`+status+`"`, -1)
			if err := testutil.CollectAndCompare(nriPluginRequestsLatencySeconds, strings.NewReader(expected), "dranet_driver_nri_plugin_requests_latency_seconds_bucket"); err != nil {
				t.Fatalf("CollectAndCompare failed: %v", err)
			}
			if tc.expectSuccess {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRunPodSandbox, statusNoop)); got != float64(1) {
					t.Errorf("Expected 1 success, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRunPodSandbox, statusFailed)); got != float64(0) {
					t.Errorf("Expected 0 failures, got %f", got)
				}
			} else {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRunPodSandbox, statusSuccess)); got != float64(0) {
					t.Errorf("Expected 0 successes, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRunPodSandbox, statusFailed)); got != float64(1) {
					t.Errorf("Expected 1 failure, got %f", got)
				}
			}
		})
	}
}

func TestStopPodSandboxMetrics(t *testing.T) {
	testCases := []struct {
		name           string
		podConfigStore *PodConfigStore
		expectSuccess  bool
	}{
		{
			name:           "Success",
			podConfigStore: NewPodConfigStore(),
			expectSuccess:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nriPluginRequestsTotal.Reset()
			nriPluginRequestsLatencySeconds.Reset()
			np := &NetworkDriver{
				podConfigStore: tc.podConfigStore,
				netdb:          inventory.New(),
			}
			podUID := types.UID("test-pod")
			pod := &api.PodSandbox{
				Uid:       string(podUID),
				Name:      "test-pod",
				Namespace: "test-ns",
			}

			np.StopPodSandbox(context.Background(), pod)
			expected := `
						# HELP dranet_driver_nri_plugin_requests_latency_seconds NRI plugin request latency in seconds.
						# TYPE dranet_driver_nri_plugin_requests_latency_seconds histogram
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="0.005"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="0.01"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="0.025"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="0.05"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="0.1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="0.25"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="0.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="2.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="10"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="StopPodSandbox",status="success",le="+Inf"} 1
						`
			if err := testutil.CollectAndCompare(nriPluginRequestsLatencySeconds, strings.NewReader(expected), "dranet_driver_nri_plugin_requests_latency_seconds_bucket"); err != nil {
				t.Fatalf("CollectAndCompare failed: %v", err)
			}
			if tc.expectSuccess {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodStopPodSandbox, statusNoop)); got != float64(1) {
					t.Errorf("Expected 1 success, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodStopPodSandbox, statusFailed)); got != float64(0) {
					t.Errorf("Expected 0 failures, got %f", got)
				}
			} else {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodStopPodSandbox, statusSuccess)); got != float64(0) {
					t.Errorf("Expected 0 successes, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodStopPodSandbox, statusFailed)); got != float64(1) {
					t.Errorf("Expected 1 failure, got %f", got)
				}
			}
		})
	}
}

func TestRemovePodSandboxMetrics(t *testing.T) {
	testCases := []struct {
		name           string
		podConfigStore *PodConfigStore
		expectSuccess  bool
	}{
		{
			name:           "Success",
			podConfigStore: NewPodConfigStore(),
			expectSuccess:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nriPluginRequestsTotal.Reset()
			nriPluginRequestsLatencySeconds.Reset()
			np := &NetworkDriver{
				podConfigStore: tc.podConfigStore,
				netdb:          inventory.New(),
			}
			podUID := types.UID("test-pod")
			pod := &api.PodSandbox{
				Uid:       string(podUID),
				Name:      "test-pod",
				Namespace: "test-ns",
			}

			np.RemovePodSandbox(context.Background(), pod)
			expected := `
						# HELP dranet_driver_nri_plugin_requests_latency_seconds NRI plugin request latency in seconds.
						# TYPE dranet_driver_nri_plugin_requests_latency_seconds histogram
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="0.005"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="0.01"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="0.025"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="0.05"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="0.1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="0.25"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="0.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="1"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="2.5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="5"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="10"} 1
						dranet_driver_nri_plugin_requests_latency_seconds_bucket{method="RemovePodSandbox",status="success",le="+Inf"} 1
						`
			if err := testutil.CollectAndCompare(nriPluginRequestsLatencySeconds, strings.NewReader(expected), "dranet_driver_nri_plugin_requests_latency_seconds_bucket"); err != nil {
				t.Fatalf("CollectAndCompare failed: %v", err)
			}
			if tc.expectSuccess {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRemovePodSandbox, statusNoop)); got != float64(1) {
					t.Errorf("Expected 1 success, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRemovePodSandbox, statusFailed)); got != float64(0) {
					t.Errorf("Expected 0 failures, got %f", got)
				}
			} else {
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRemovePodSandbox, statusSuccess)); got != float64(0) {
					t.Errorf("Expected 0 successes, got %f", got)
				}
				if got := testutil.ToFloat64(nriPluginRequestsTotal.WithLabelValues(methodRemovePodSandbox, statusFailed)); got != float64(1) {
					t.Errorf("Expected 1 failure, got %f", got)
				}
			}
		})
	}
}

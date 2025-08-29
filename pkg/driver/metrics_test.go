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
	"context"
	"strings"
	"testing"

	"github.com/containerd/nri/pkg/api"
	"github.com/google/dranet/pkg/inventory"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"
)

func Test_NRIHooksMetrics(t *testing.T) {
	testcases := []struct {
		name          string
		podConfig     bool
		hostNetwork   bool
		expectedError bool
	}{
		{
			name:          "success",
			podConfig:     true,
			hostNetwork:   false,
			expectedError: false,
		},
		{
			name:          "no pod config",
			podConfig:     false,
			hostNetwork:   false,
			expectedError: true,
		},
		{
			name:          "host network",
			podConfig:     true,
			hostNetwork:   true,
			expectedError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			np := &NetworkDriver{
				podConfigStore: NewPodConfigStore(),
				netdb:          inventory.New(),
			}
			podUID := types.UID("test-pod")
			pod := &api.PodSandbox{
				Uid:       string(podUID),
				Name:      "test-pod",
				Namespace: "test-ns",
				Linux: &api.LinuxPodSandbox{
					Namespaces: []*api.LinuxNamespace{
						{
							Type: "network",
							Path: "test",
						},
					},
				},
			}
			ctr := &api.Container{
				Name: "test-container",
			}

			if tc.podConfig {
				np.podConfigStore.Set(podUID, "eth0", PodConfig{})
			}
			if tc.hostNetwork {
				pod.Linux.Namespaces[0].Path = ""
			}

			// NRI CreateContainer
			_, _, _ = np.CreateContainer(context.Background(), pod, ctr)
			metric := "nri_plugin_requests_total"
			method := "CreateContainer"
			if tc.expectedError {
				if err := testutil.CollectAndCompare(nriPluginRequestsTotal, strings.NewReader(`
				# HELP dranet_driver_nri_plugin_requests_total Total number of NRI plugin requests.
				# TYPE dranet_driver_nri_plugin_requests_total counter
				dranet_driver_nri_plugin_requests_total{method="`+method+`",status="failed"} 1
				`), metric); err != nil {
					t.Errorf("unexpected metric value for %s: %v", method, err)
				}
			} else {
				if err := testutil.CollectAndCompare(nriPluginRequestsTotal, strings.NewReader(`
				# HELP dranet_driver_nri_plugin_requests_total Total number of NRI plugin requests.
				# TYPE dranet_driver_nri_plugin_requests_total counter
				dranet_driver_nri_plugin_requests_total{method="`+method+`",status="success"} 1
				`), metric); err != nil {
					t.Errorf("unexpected metric value for %s: %v", method, err)
				}
			}
			nriPluginRequestsTotal.Reset()

			// NRI RunPodSandbox
			_ = np.RunPodSandbox(context.Background(), pod)
			method = "RunPodSandbox"
			if tc.expectedError {
				if err := testutil.CollectAndCompare(nriPluginRequestsTotal, strings.NewReader(`
				# HELP dranet_driver_nri_plugin_requests_total Total number of NRI plugin requests.
				# TYPE dranet_driver_nri_plugin_requests_total counter
				dranet_driver_nri_plugin_requests_total{method="`+method+`",status="failed"} 1
				`), metric); err != nil {
					t.Errorf("unexpected metric value for %s: %v", method, err)
				}
			} else {
				if err := testutil.CollectAndCompare(nriPluginRequestsTotal, strings.NewReader(`
				# HELP dranet_driver_nri_plugin_requests_total Total number of NRI plugin requests.
				# TYPE dranet_driver_nri_plugin_requests_total counter
				dranet_driver_nri_plugin_requests_total{method="`+method+`",status="success"} 1
				`), metric); err != nil {
					t.Errorf("unexpected metric value for %s: %v", method, err)
				}
			}
			nriPluginRequestsTotal.Reset()

			// NRI StopPodSandbox
			_ = np.StopPodSandbox(context.Background(), pod)
			method = "StopPodSandbox"
			if tc.expectedError {
				if err := testutil.CollectAndCompare(nriPluginRequestsTotal, strings.NewReader(`
				# HELP dranet_driver_nri_plugin_requests_total Total number of NRI plugin requests.
				# TYPE dranet_driver_nri_plugin_requests_total counter
				dranet_driver_nri_plugin_requests_total{method="`+method+`",status="failed"} 1
				`), metric); err != nil {
					t.Errorf("unexpected metric value for %s: %v", method, err)
				}
			} else {
				if err := testutil.CollectAndCompare(nriPluginRequestsTotal, strings.NewReader(`
				# HELP dranet_driver_nri_plugin_requests_total Total number of NRI plugin requests.
				# TYPE dranet_driver_nri_plugin_requests_total counter
				dranet_driver_nri_plugin_requests_total{method="`+method+`",status="success"} 1
				`), metric); err != nil {
					t.Errorf("unexpected metric value for %s: %v", method, err)
				}
			}
			nriPluginRequestsTotal.Reset()

			// NRI RemovePodSandbox
			_ = np.RemovePodSandbox(context.Background(), pod)
			method = "RemovePodSandbox"
			if err := testutil.CollectAndCompare(nriPluginRequestsTotal, strings.NewReader(`
			# HELP dranet_driver_nri_plugin_requests_total Total number of NRI plugin requests.
			# TYPE dranet_driver_nri_plugin_requests_total counter
			dranet_driver_nri_plugin_requests_total{method="`+method+`",status="success"} 1
			`), metric); err != nil {
				t.Errorf("unexpected metric value for %s: %v", method, err)
			}
			nriPluginRequestsTotal.Reset()
		})
	}
}
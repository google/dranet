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
	"fmt"
	"strings"
	"testing"

	"github.com/google/dranet/pkg/apis"
	"github.com/prometheus/client_golang/prometheus/testutil"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"

)

func TestPublishResourcesPrometheusMetrics(t *testing.T) {
	testCases := []struct {
		name          string
		devices       []resourcev1.Device
		expectedRdma  float64
		expectedTotal float64
	}{
		{
			name:          "No devices",
			devices:       []resourcev1.Device{},
			expectedRdma:  0,
			expectedTotal: 0,
		},
		{
			name: "Only RDMA devices",
			devices: []resourcev1.Device{
				{Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					apis.AttrRDMA: {BoolValue: func() *bool { b := true; return &b }()},
				}},
			},
			expectedRdma:  1,
			expectedTotal: 1,
		},
		{
			name: "Only non-RDMA devices",
			devices: []resourcev1.Device{
				{Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					apis.AttrRDMA: {BoolValue: func() *bool { b := false; return &b }()},
				}},
			},
			expectedRdma:  0,
			expectedTotal: 1,
		},
		{
			name: "Mixed RDMA and non-RDMA devices",
			devices: []resourcev1.Device{
				{Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					apis.AttrRDMA: {BoolValue: func() *bool { b := true; return &b }()},
				}},
				{Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					apis.AttrRDMA: {BoolValue: func() *bool { b := true; return &b }()},
				}},
				{Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					apis.AttrRDMA: {BoolValue: func() *bool { b := false; return &b }()},
				}},
			},
			expectedRdma:  2,
			expectedTotal: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			publishedDevicesTotal.Reset()
			np := &NetworkDriver{}
			np.publishResourcesPrometheusMetrics(tc.devices)

			if got := testutil.ToFloat64(publishedDevicesTotal.WithLabelValues("rdma")); got != tc.expectedRdma {
				t.Errorf("Expected %f for RDMA devices, got %f", tc.expectedRdma, got)
			}
			if got := testutil.ToFloat64(publishedDevicesTotal.WithLabelValues("total")); got != tc.expectedTotal {
				t.Errorf("Expected %f for Total devices, got %f", tc.expectedTotal, got)
			}
		})
	}
}

func TestPrepareResourceClaimsMetrics(t *testing.T) {
	ctx := context.Background()

	t.Run("Success Case", func(t *testing.T) {
		draPluginRequestsTotal.Reset()
		draPluginRequestsLatencySeconds.Reset()

		np := &NetworkDriver{}
		if _, err := np.PrepareResourceClaims(ctx, []*resourcev1.ResourceClaim{}); err != nil {
			t.Fatalf("PrepareResourceClaims failed: %v", err)
		}

		if got := testutil.ToFloat64(draPluginRequestsTotal.WithLabelValues(methodPrepareResourceClaims, statusSuccess)); got != float64(1) {
			t.Errorf("Expected 1 success, got %f", got)
		}
		if got := testutil.ToFloat64(draPluginRequestsTotal.WithLabelValues(methodPrepareResourceClaims, statusFailed)); got != float64(0) {
			t.Errorf("Expected 0 failures, got %f", got)
		}

		expected := `
			# HELP dranet_driver_dra_plugin_requests_latency_seconds DRA plugin request latency in seconds.
			# TYPE dranet_driver_dra_plugin_requests_latency_seconds histogram
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="0.005"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="0.01"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="0.025"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="0.05"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="0.1"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="0.25"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="0.5"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="1"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="2.5"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="5"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="10"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="PrepareResourceClaims",le="+Inf"} 1
		`
		if err := testutil.CollectAndCompare(draPluginRequestsLatencySeconds, strings.NewReader(expected), "dranet_driver_dra_plugin_requests_latency_seconds_bucket"); err != nil {
			t.Fatalf("CollectAndCompare failed: %v", err)
		}
	})

	t.Run("Failure Case", func(t *testing.T) {
		draPluginRequestsTotal.Reset()
		draPluginRequestsLatencySeconds.Reset()

		np := &NetworkDriver{
			netdb:      newFakeInventoryDB(),
			driverName: "test.driver",
		}

		claims := []*resourcev1.ResourceClaim{
			{
				ObjectMeta: metav1.ObjectMeta{UID: "claim-uid-1"},
				Status: resourcev1.ResourceClaimStatus{
					ReservedFor: []resourcev1.ResourceClaimConsumerReference{
						{APIGroup: "", Resource: "pods", Name: "test-pod", UID: "pod-uid-1"},
					},
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{
								{Driver: "test.driver", Device: "device-does-not-exist"},
							},
						},
					},
				},
			},
		}

		res, err := np.PrepareResourceClaims(ctx, claims)
		if err != nil {
			t.Fatalf("PrepareResourceClaims failed: %v", err)
		}
		if res["claim-uid-1"].Err == nil {
			t.Errorf("Expected an error for claim-uid-1, but got none")
		}

		if got := testutil.ToFloat64(draPluginRequestsTotal.WithLabelValues(methodPrepareResourceClaims, statusSuccess)); got != float64(0) {
			t.Errorf("Expected 0 successes, got %f", got)
		}
		if got := testutil.ToFloat64(draPluginRequestsTotal.WithLabelValues(methodPrepareResourceClaims, statusFailed)); got != float64(1) {
			t.Errorf("Expected 1 failure, got %f", got)
		}

		if count := testutil.CollectAndCount(draPluginRequestsLatencySeconds); count != 1 {
			t.Errorf("Expected 1 latency metric, got %d", count)
		}
	})
}

func TestUnprepareResourceClaimsMetrics(t *testing.T) {
	ctx := context.Background()

	t.Run("Success Case", func(t *testing.T) {
		draPluginRequestsTotal.Reset()
		draPluginRequestsLatencySeconds.Reset()

		np := &NetworkDriver{
			podConfigStore: NewPodConfigStore(),
		}
		claimName := types.NamespacedName{Name: "test-claim", Namespace: "test-ns"}
		np.podConfigStore.Set("pod-uid-1", "device-a", PodConfig{Claim: claimName})

		claims := []kubeletplugin.NamespacedObject{
			{NamespacedName: claimName, UID: "claim-uid-1"},
		}

		if _, err := np.UnprepareResourceClaims(ctx, claims); err != nil {
			t.Fatalf("UnprepareResourceClaims failed: %v", err)
		}

		// Verify the claim was removed from the store
		if _, ok := np.podConfigStore.GetPodConfigs("pod-uid-1"); ok {
			t.Errorf("Pod config should have been removed, but was found")
		}

		if got := testutil.ToFloat64(draPluginRequestsTotal.WithLabelValues(methodUnprepareResourceClaims, statusSuccess)); got != float64(1) {
			t.Errorf("Expected 1 success, got %f", got)
		}
		if got := testutil.ToFloat64(draPluginRequestsTotal.WithLabelValues(methodUnprepareResourceClaims, statusFailed)); got != float64(0) {
			t.Errorf("Expected 0 failures, got %f", got)
		}

		expected := `
			# HELP dranet_driver_dra_plugin_requests_latency_seconds DRA plugin request latency in seconds.
			# TYPE dranet_driver_dra_plugin_requests_latency_seconds histogram
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="0.005"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="0.01"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="0.025"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="0.05"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="0.1"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="0.25"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="0.5"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="1"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="2.5"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="5"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="10"} 1
			dranet_driver_dra_plugin_requests_latency_seconds_bucket{method="UnprepareResourceClaims",le="+Inf"} 1
		`
		if err := testutil.CollectAndCompare(draPluginRequestsLatencySeconds, strings.NewReader(expected), "dranet_driver_dra_plugin_requests_latency_seconds_bucket"); err != nil {
			t.Fatalf("CollectAndCompare failed: %v", err)
		}
	})
}

func TestPublishResourcesMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	fakeDraPlugin := newFakePluginHelper()
	fakeNetDB := newFakeInventoryDB()

	np := &NetworkDriver{
		draPlugin: fakeDraPlugin,
		netdb:     fakeNetDB,
		nodeName:  "test-node",
	}

	go np.PublishResources(ctx)

	t.Run("Success", func(t *testing.T) {
		lastPublishedTime.Set(0)
		fakeNetDB.resources <- []resourcev1.Device{}
		<-fakeDraPlugin.publishCalled

		if testutil.ToFloat64(lastPublishedTime) == 0 {
			t.Errorf("lastPublishedTime should have been updated, but it is 0")
		}
	})

	t.Run("Failure", func(t *testing.T) {
		lastPublishedTime.Set(0)
		fakeDraPlugin.publishErr = fmt.Errorf("mock publish error")
		fakeNetDB.resources <- []resourcev1.Device{}
		<-fakeDraPlugin.publishCalled

		if testutil.ToFloat64(lastPublishedTime) != 0 {
			t.Errorf("lastPublishedTime should not have been updated, but it is %f", testutil.ToFloat64(lastPublishedTime))
		}
	})
}


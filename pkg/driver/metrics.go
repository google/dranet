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

	"github.com/prometheus/client_golang/prometheus"
)

var registerMetricsOnce sync.Once

func registerMetrics() {
	registerMetricsOnce.Do(func() {
		prometheus.MustRegister(nodePrepareRequestsTotal)
	})
}

var (
	nodePrepareRequestsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "dranet",
		Subsystem: "driver",
		Name:      "node_prepare_requests_total",
		Help:      "Total number of NodePrepareResources requests received.",
	})
)

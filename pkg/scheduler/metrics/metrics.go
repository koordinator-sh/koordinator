/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
)

// All the histogram based metrics have 1ms as size for the smallest bucket.
var (
	SchedulingTimeout = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      schedulermetrics.SchedulerSubsystem,
			Name:           "scheduling_timeout",
			Help:           "The currently scheduled Pod exceeds the maximum acceptable time interval",
			StabilityLevel: metrics.STABLE,
		}, []string{"profile"})
	SchedulingTracePointElapsedTime = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem: schedulermetrics.SchedulerSubsystem,
			Name:      "scheduling_trace_point_elapsed_time",
			Help:      "Duration for between different trace points in scheduling timeline",
			// Start with 0.01ms with the last bucket being [~22ms, Inf). We use a small factor (1.2)
			// so that we have better granularity since plugin latency is very sensitive.
			Buckets:        metrics.ExponentialBuckets(0.00001, 1.2, 100),
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"tracePoint"})

	metricsList = []metrics.Registerable{
		SchedulingTimeout,
		SchedulingTracePointElapsedTime,
	}
)

var registerMetrics sync.Once

// Register all metrics.
func Register() {
	// Register the metrics.
	registerMetrics.Do(func() {
		RegisterMetrics(metricsList...)
	})
}

// RegisterMetrics registers a list of metrics.
// This function is exported because it is intended to be used by out-of-tree plugins to register their custom metrics.
func RegisterMetrics(extraMetrics ...metrics.Registerable) {
	for _, metric := range extraMetrics {
		legacyregistry.MustRegister(metric)
	}
}

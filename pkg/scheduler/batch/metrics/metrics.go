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

// Package metrics owns the metrics emitted by the reusable batch scheduling engine
// (pkg/scheduler/batch). They are defined here so that the shared engine has no dependency on
// other scheduler packages. The metric names use the batch_* prefix to reflect that they belong
// to the batch scheduling pipeline.
package metrics

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
)

var (
	registerMetrics sync.Once

	BatchAlgorithmByNodeLatency = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      schedulermetrics.SchedulerSubsystem,
			Name:           "batch_algorithm_by_node_duration_seconds",
			Help:           "Batch scheduling algorithm by node latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 15),
			StabilityLevel: metrics.ALPHA,
		},
	)
	BatchAlgorithmByPodLatency = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      schedulermetrics.SchedulerSubsystem,
			Name:           "batch_algorithm_by_pod_duration_seconds",
			Help:           "Batch scheduling algorithm by pod latency in seconds",
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 15),
			StabilityLevel: metrics.ALPHA,
		},
	)

	// Partial bind success metrics
	PartialBindJobsTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      schedulermetrics.SchedulerSubsystem,
			Name:           "batch_partial_bind_jobs_total",
			Help:           "Number of jobs with partial bind success (some pods bound, some failed)",
			StabilityLevel: metrics.ALPHA,
		},
	)
	PartialBindPodsTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      schedulermetrics.SchedulerSubsystem,
			Name:           "batch_partial_bind_pods_total",
			Help:           "Number of pods in partial bind scenarios by status (success/failure)",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"status"},
	)
	BindFailuresByReason = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      schedulermetrics.SchedulerSubsystem,
			Name:           "batch_bind_failures_total",
			Help:           "Number of bind failures by reason (apiserver_error, network_error, etc)",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"reason", "phase"},
	)
	AssumedPodsCleanupTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      schedulermetrics.SchedulerSubsystem,
			Name:           "batch_assumed_pods_cleanup_total",
			Help:           "Number of assumed pods cleaned up in failure scenarios",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"reason"},
	)

	metricsList = []metrics.Registerable{
		BatchAlgorithmByNodeLatency,
		BatchAlgorithmByPodLatency,
		PartialBindJobsTotal,
		PartialBindPodsTotal,
		BindFailuresByReason,
		AssumedPodsCleanupTotal,
	}
)

// Register registers the shared batch scheduling metrics. Safe to call multiple times.
func Register() {
	registerMetrics.Do(func() {
		RegisterMetrics(metricsList...)
	})
}

// RegisterMetrics registers a list of metrics.
func RegisterMetrics(extraMetrics ...metrics.Registerable) {
	for _, metric := range extraMetrics {
		legacyregistry.MustRegister(metric)
	}
}

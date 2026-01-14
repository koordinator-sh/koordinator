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

package defragmentation

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// fragmentationRate is the GPU fragmentation rate per node
	fragmentationRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "gpu_fragmentation_rate",
			Help:      "GPU fragmentation rate per node (0-100)",
		},
		[]string{"node"},
	)

	// fragmentedGPUCount is the number of fragmented GPUs per node
	fragmentedGPUCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "fragmented_gpu_count",
			Help:      "Number of fragmented GPUs per node",
		},
		[]string{"node"},
	)

	// defragmentationExecutions is the total number of defragmentation executions
	defragmentationExecutions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "defragmentation_executions_total",
			Help:      "Total number of defragmentation executions",
		},
		[]string{"status"}, // success, failed, skipped
	)

	// migrationTasks is the total number of migration tasks
	migrationTasks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "migration_tasks_total",
			Help:      "Total number of migration tasks",
		},
		[]string{"status", "reason"}, // status: success/failed, reason: compact/balance
	)

	// migrationDuration is the duration of pod migrations
	migrationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "migration_duration_seconds",
			Help:      "Duration of pod migrations in seconds",
			Buckets:   []float64{10, 30, 60, 120, 300, 600, 1800},
		},
		[]string{"status"},
	)

	// defragmentationImprovement is the improvement in fragmentation rate
	defragmentationImprovement = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "defragmentation_improvement",
			Help:      "Improvement in fragmentation rate after defragmentation",
		},
	)

	// clusterFragmentationRate is the average cluster fragmentation rate
	clusterFragmentationRate = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "cluster_fragmentation_rate",
			Help:      "Average cluster GPU fragmentation rate (0-100)",
		},
	)

	// totalFragmentedGPUs is the total number of fragmented GPUs in the cluster
	totalFragmentedGPUs = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "total_fragmented_gpus",
			Help:      "Total number of fragmented GPUs in the cluster",
		},
	)

	// ongoingMigrations is the number of ongoing migrations
	ongoingMigrations = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Subsystem: "koordinator_descheduler",
			Name:      "ongoing_migrations",
			Help:      "Number of ongoing pod migrations",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		fragmentationRate,
		fragmentedGPUCount,
		defragmentationExecutions,
		migrationTasks,
		migrationDuration,
		defragmentationImprovement,
		clusterFragmentationRate,
		totalFragmentedGPUs,
		ongoingMigrations,
	)
}

// MetricsCollector collects and records metrics
type MetricsCollector struct{}

// NewMetricsCollector creates a new MetricsCollector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

// RecordDefragmentation records defragmentation execution metrics
func (mc *MetricsCollector) RecordDefragmentation(
	plan *DefragmentationPlan,
	report *FragmentationReport,
) {
	// Record node fragmentation rates
	for nodeName, nodeInfo := range report.NodeFragmentations {
		fragmentationRate.WithLabelValues(nodeName).Set(nodeInfo.FragmentationRate)

		fragmentedCount := 0
		for _, device := range nodeInfo.Devices {
			if device.IsFragmented {
				fragmentedCount++
			}
		}
		fragmentedGPUCount.WithLabelValues(nodeName).Set(float64(fragmentedCount))
	}

	// Record cluster-level metrics
	clusterFragmentationRate.Set(report.AverageFragmentation)
	totalFragmentedGPUs.Set(float64(report.FragmentedGPUs))

	// Record execution status
	if len(plan.Migrations) > 0 {
		defragmentationExecutions.WithLabelValues("success").Inc()
	} else {
		defragmentationExecutions.WithLabelValues("skipped").Inc()
	}

	// Record expected improvement
	defragmentationImprovement.Set(plan.ExpectedImprovement)
}

// RecordMigration records migration task metrics
func (mc *MetricsCollector) RecordMigration(
	task *MigrationTask,
	status string,
	duration time.Duration,
) {
	migrationTasks.WithLabelValues(status, task.Reason).Inc()
	migrationDuration.WithLabelValues(status).Observe(duration.Seconds())
}

// RecordOngoingMigrations records the number of ongoing migrations
func (mc *MetricsCollector) RecordOngoingMigrations(count int) {
	ongoingMigrations.Set(float64(count))
}

// RecordDefragmentationError records defragmentation errors
func (mc *MetricsCollector) RecordDefragmentationError() {
	defragmentationExecutions.WithLabelValues("failed").Inc()
}

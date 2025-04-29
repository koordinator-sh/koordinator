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
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"

	utilmetrics "github.com/koordinator-sh/koordinator/pkg/util/metrics"
)

const (
	NodeNameKey = "node_name"
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
	ReservationStatusPhase = utilmetrics.NewGCGaugeVec("reservation_status_phase", prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: schedulermetrics.SchedulerSubsystem,
			Name:      "reservation_status_phase",
			Help:      `The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)`,
		}, []string{"name", "phase"}))
	ElasticQuotaProcessLatency = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem: schedulermetrics.SchedulerSubsystem,
			Name:      "elastic_quota_process_latency",
			Help:      "elastic quota process latency in second",
			Buckets:   metrics.ExponentialBuckets(0.00001, 2, 24),
		},
		[]string{"operation"},
	)
	SecondaryDeviceNotWellPlannedNodes = utilmetrics.NewGCGaugeVec("secondary_device_not_well_planned", prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: schedulermetrics.SchedulerSubsystem,
			Name:      "secondary_device_not_well_planned",
			Help:      "The number of secondary device not well planned",
		}, []string{NodeNameKey}))

	metricsList = []metrics.Registerable{
		SchedulingTimeout,
		ElasticQuotaProcessLatency,
	}

	gcMetricsList = []prometheus.Collector{
		ReservationStatusPhase.GetGaugeVec(),
		SecondaryDeviceNotWellPlannedNodes.GetGaugeVec(),
	}
)

const (
	reservationNameKey  = "name"
	reservationPhaseKey = "phase"
)

// RecordReservationPhase records the phase of a reservation as a metric.
// It uses the provided name, phase, and value to set the metric with specific labels.
func RecordReservationPhase(name string, phase string, value float64) {
	labels := prometheus.Labels{
		reservationNameKey:  name,
		reservationPhaseKey: phase,
	}
	ReservationStatusPhase.WithSet(labels, value)
}

var registerMetrics sync.Once

// Register all metrics.
func Register() {
	// Register the metrics.
	registerMetrics.Do(func() {
		RegisterMetrics(metricsList...)
		RegisterGCMetrics(gcMetricsList...)
	})
}

// RegisterMetrics registers a list of metrics.
// This function is exported because it is intended to be used by out-of-tree plugins to register their custom metrics.
func RegisterMetrics(extraMetrics ...metrics.Registerable) {
	for _, metric := range extraMetrics {
		legacyregistry.MustRegister(metric)
	}
}

// RegisterGCMetrics registers garbage collection metrics.
func RegisterGCMetrics(gcMetrics ...prometheus.Collector) {
	for _, metric := range gcMetrics {
		legacyregistry.RawMustRegister(metric)
	}
}

func RecordElasticQuotaProcessLatency(operation string, latency time.Duration) {
	ElasticQuotaProcessLatency.WithLabelValues(operation).Observe(latency.Seconds())
}

func RecordSecondaryDeviceNotWellPlanned(nodeName string) {
	SecondaryDeviceNotWellPlannedNodes.WithSet(prometheus.Labels{NodeNameKey: nodeName}, 1.0)
}

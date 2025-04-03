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

	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"

	utilmetrics "github.com/koordinator-sh/koordinator/pkg/util/metrics"
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

	metricsList = []metrics.Registerable{
		SchedulingTimeout,
	}

	gcMetricsList = []prometheus.Collector{
		ReservationStatusPhase.GetGaugeVec(),
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
		RegisterStandardAndGCMetrics(metricsList, gcMetricsList)
	})
}

// RegisterStandardAndGCMetrics registers both standard and garbage collection metrics.
func RegisterStandardAndGCMetrics(standardMetrics []metrics.Registerable, gcMetrics []prometheus.Collector) {
	for _, metric := range standardMetrics {
		legacyregistry.MustRegister(metric)
	}
	for _, metric := range gcMetrics {
		legacyregistry.RawMustRegister(metric)
	}
}

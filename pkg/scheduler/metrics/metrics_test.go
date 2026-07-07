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
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"
)

func TestGangJobSizeBucket(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want string
	}{
		{"negative", -1, "0"},
		{"zero", 0, "0"},
		{"lower_boundary_of_first_bucket", 1, "1-100"},
		{"upper_boundary_of_first_bucket", 100, "1-100"},
		{"start_of_second_bucket", 101, "101-200"},
		{"mid_bucket", 250, "201-300"},
		{"exact_bucket_edge_300", 300, "201-300"},
		{"exact_bucket_edge_301", 301, "301-400"},
		{"bucket_401_500", 500, "401-500"},
		{"bucket_501_600", 501, "501-600"},
		{"bucket_901_1000_lower", 901, "901-1000"},
		{"bucket_901_1000_upper", 1000, "901-1000"},
		{"overflow_just_above", 1001, "1000+"},
		{"overflow_large", 100000, "1000+"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GangJobSizeBucket(tt.n))
		})
	}
}

func TestRecordGangScheduleCycleDuration(t *testing.T) {
	// Ensure the metric is registered on the legacy registry.
	Register()
	GangScheduleCycleDuration.Reset()

	RecordGangScheduleCycleDuration("gang_is_succeed", "1-100", 1500*time.Millisecond)
	RecordGangScheduleCycleDuration("gang_is_succeed", "1-100", 9*time.Second)
	RecordGangScheduleCycleDuration("gang_is_nil", "101-200", 35*time.Second)

	// Verify per-label aggregate count and sum: two observations at 1.5s and 9s.
	vec, err := testutil.GetHistogramVecFromGatherer(legacyregistry.DefaultGatherer,
		"scheduler_gang_schedule_cycle_duration_seconds",
		map[string]string{"reason": "gang_is_succeed", "job_size": "1-100"})
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), vec.GetAggregatedSampleCount())
	assert.InDelta(t, 10.5, vec.GetAggregatedSampleSum(), 1e-9)

	// The other label combination should have one observation of 35s.
	vec2, err := testutil.GetHistogramVecFromGatherer(legacyregistry.DefaultGatherer,
		"scheduler_gang_schedule_cycle_duration_seconds",
		map[string]string{"reason": "gang_is_nil", "job_size": "101-200"})
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), vec2.GetAggregatedSampleCount())
	assert.InDelta(t, 35.0, vec2.GetAggregatedSampleSum(), 1e-9)
}

func TestRecordReservationPhaseAndReset(t *testing.T) {
	Register()
	ReservationStatusPhase.Reset()

	RecordReservationPhase("reservation-a", "Pending", 1.0)
	value, ok := getGaugeValue(t, "scheduler_reservation_status_phase", map[string]string{
		reservationNameKey:  "reservation-a",
		reservationPhaseKey: "Pending",
	})
	assert.True(t, ok)
	assert.Equal(t, 1.0, value)

	ResetReservationPhase()
	_, ok = getGaugeValue(t, "scheduler_reservation_status_phase", map[string]string{
		reservationNameKey:  "reservation-a",
		reservationPhaseKey: "Pending",
	})
	assert.False(t, ok)
}

func TestRecordReservationResourceByTypeWithUnit(t *testing.T) {
	Register()
	ReservationResource.GetGaugeVec().Reset()

	RecordReservationResourceByTypeWithUnit("reservation-a", "cpu", TypeAllocatable, UnitCore, 4.0)
	value, ok := getGaugeValue(t, "scheduler_reservation_resource", map[string]string{
		reservationResourceTypeKey: TypeAllocatable,
		reservationNameKey:         "reservation-a",
		reservationResourceKey:     "cpu",
		reservationResourceUnitKey: UnitCore,
	})
	assert.True(t, ok)
	assert.Equal(t, 4.0, value)
}

func TestRecordElasticQuotaLatencies(t *testing.T) {
	Register()
	ElasticQuotaProcessLatency.Reset()
	ElasticQuotaHookPluginLatency.Reset()

	RecordElasticQuotaProcessLatency("update", 1500*time.Millisecond)
	RecordElasticQuotaProcessLatency("update", 500*time.Millisecond)

	count, sum, ok := getHistogramCountSum(t, "scheduler_elastic_quota_process_latency", map[string]string{
		"operation": "update",
	})
	assert.True(t, ok)
	assert.Equal(t, uint64(2), count)
	assert.InDelta(t, 2.0, sum, 1e-9)

	RecordElasticQuotaHookPluginLatency("plugin-a", "reserve", 2*time.Second)
	count, sum, ok = getHistogramCountSum(t, "scheduler_elastic_quota_hook_plugin_latency", map[string]string{
		"plugin":    "plugin-a",
		"operation": "reserve",
	})
	assert.True(t, ok)
	assert.Equal(t, uint64(1), count)
	assert.InDelta(t, 2.0, sum, 1e-9)
}

func TestRecordSecondaryDeviceNotWellPlanned(t *testing.T) {
	Register()
	SecondaryDeviceNotWellPlannedNodes.Reset()

	originalMetricVec := SecondaryDeviceNotWellPlannedNodes.MetricVec
	SecondaryDeviceNotWellPlannedNodes.MetricVec = nil
	RecordSecondaryDeviceNotWellPlanned("node-a", true)
	SecondaryDeviceNotWellPlannedNodes.MetricVec = originalMetricVec

	RecordSecondaryDeviceNotWellPlanned("node-a", true)
	value, ok := getGaugeValue(t, "scheduler_secondary_device_not_well_planned", map[string]string{
		NodeNameKey: "node-a",
	})
	assert.True(t, ok)
	assert.Equal(t, 1.0, value)

	RecordSecondaryDeviceNotWellPlanned("node-a", false)
	_, ok = getGaugeValue(t, "scheduler_secondary_device_not_well_planned", map[string]string{
		NodeNameKey: "node-a",
	})
	assert.False(t, ok)
}

func TestRecordQueueAndPreemptionMetrics(t *testing.T) {
	Register()
	NextPodDeleteFromQueueLatency.Reset()
	JobPreemptionDuration.GetHistogramVec().Reset()

	RecordNextPodPluginsDeletePodFromQueue(500 * time.Millisecond)
	count, sum, ok := getHistogramCountSum(t, "scheduler_next_pod_delete_from_queue_latency", nil)
	assert.True(t, ok)
	assert.Equal(t, uint64(1), count)
	assert.InDelta(t, 0.5, sum, 1e-9)

	RecordJobPreemptionDuration("job-a", "success", 250*time.Millisecond)
	RecordJobPreemptionDuration("job-a", "success", 250*time.Millisecond)
	count, sum, ok = getHistogramCountSum(t, "scheduler_job_preemption_duration_seconds", map[string]string{
		"jobName": "job-a",
		"result":  "success",
	})
	assert.True(t, ok)
	assert.Equal(t, uint64(2), count)
	assert.InDelta(t, 0.5, sum, 1e-9)
}

func getGaugeValue(t *testing.T, name string, labels map[string]string) (float64, bool) {
	t.Helper()

	mf, ok := getMetricFamily(t, name)
	if !ok {
		return 0, false
	}
	metric, ok := findMetricWithLabels(mf, labels)
	if !ok || metric.GetGauge() == nil {
		return 0, false
	}
	return metric.GetGauge().GetValue(), true
}

func getHistogramCountSum(t *testing.T, name string, labels map[string]string) (uint64, float64, bool) {
	t.Helper()

	mf, ok := getMetricFamily(t, name)
	if !ok {
		return 0, 0, false
	}
	metric, ok := findMetricWithLabels(mf, labels)
	if !ok || metric.GetHistogram() == nil {
		return 0, 0, false
	}
	hist := metric.GetHistogram()
	return hist.GetSampleCount(), hist.GetSampleSum(), true
}

func getMetricFamily(t *testing.T, name string) (*dto.MetricFamily, bool) {
	t.Helper()

	metricsFamilies, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range metricsFamilies {
		if mf.GetName() == name {
			return mf, true
		}
	}
	return nil, false
}

func findMetricWithLabels(mf *dto.MetricFamily, labels map[string]string) (*dto.Metric, bool) {
	if labels == nil {
		labels = map[string]string{}
	}
	for _, metric := range mf.GetMetric() {
		if labelsMatch(metric, labels) {
			return metric, true
		}
	}
	return nil, false
}

func labelsMatch(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.GetLabel()) != len(labels) {
		return false
	}
	for _, label := range metric.GetLabel() {
		if labels[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}

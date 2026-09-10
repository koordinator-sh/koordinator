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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"
)

func TestRecordSandboxBindingSlotWaitDuration(t *testing.T) {
	Register()
	SandboxBindingSlotWaitDuration.Reset()
	t.Cleanup(SandboxBindingSlotWaitDuration.Reset)

	RecordSandboxBindingSlotWaitDuration("koord-scheduler", 250*time.Millisecond)
	RecordSandboxBindingSlotWaitDuration("koord-scheduler", 750*time.Millisecond)
	RecordSandboxBindingSlotWaitDuration("other-scheduler", 1500*time.Millisecond)

	for _, tt := range []struct {
		profile string
		count   uint64
		sum     float64
	}{
		{profile: "koord-scheduler", count: 2, sum: 1},
		{profile: "other-scheduler", count: 1, sum: 1.5},
	} {
		t.Run(tt.profile, func(t *testing.T) {
			vec, err := testutil.GetHistogramVecFromGatherer(legacyregistry.DefaultGatherer,
				"scheduler_sandbox_binding_slot_wait_duration_seconds",
				map[string]string{"profile": tt.profile})
			require.NoError(t, err)
			assert.Equal(t, tt.count, vec.GetAggregatedSampleCount())
			assert.InDelta(t, tt.sum, vec.GetAggregatedSampleSum(), 1e-9)
		})
	}
}

func TestRecordSandboxEquivalenceClassCacheEntries(t *testing.T) {
	Register()
	SandboxEquivalenceClassCacheEntries.Set(0)
	t.Cleanup(func() { SandboxEquivalenceClassCacheEntries.Set(0) })

	for _, tt := range []struct {
		delta int
		want  float64
	}{
		{delta: 1, want: 1},
		{delta: 3, want: 4},
		{delta: -1, want: 3},
		{delta: -3, want: 0},
		{delta: 0, want: 0},
	} {
		RecordSandboxEquivalenceClassCacheEntries(tt.delta)
		value, err := testutil.GetGaugeMetricValue(SandboxEquivalenceClassCacheEntries)
		require.NoError(t, err)
		assert.Equal(t, tt.want, value)
	}
}

func TestRecordSandboxEquivalenceClassFlush(t *testing.T) {
	Register()
	SandboxEquivalenceClassFlushes.Reset()
	t.Cleanup(SandboxEquivalenceClassFlushes.Reset)

	RecordSandboxEquivalenceClassFlush("node_event")
	RecordSandboxEquivalenceClassFlush("node_event")
	RecordSandboxEquivalenceClassFlush("bind_failure")

	for reason, want := range map[string]float64{"node_event": 2, "bind_failure": 1} {
		value, err := testutil.GetCounterMetricValue(SandboxEquivalenceClassFlushes.WithLabelValues(reason))
		require.NoError(t, err)
		assert.Equal(t, want, value, reason)
	}
}

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

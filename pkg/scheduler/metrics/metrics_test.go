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

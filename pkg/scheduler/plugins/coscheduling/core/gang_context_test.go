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

package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"

	schedulermetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

func newTestFirstPod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
			UID:       types.UID("uid-" + name),
		},
	}
}

func TestSetGangSchedulingContext_InitializesStartTimeOnce(t *testing.T) {
	h := &GangSchedulingContextHolder{}

	ctx := &GangSchedulingContext{
		firstPod:  newTestFirstPod("default", "pod-leader"),
		gangGroup: sets.New[string]("default/gangA"),
	}

	before := time.Now()
	h.setGangSchedulingContext(ctx, "first-pod-passed-prefilter")
	after := time.Now()

	got := h.getCurrentGangSchedulingContext()
	assert.NotNil(t, got)
	assert.False(t, got.startTime.IsZero(), "startTime should be set on first init")
	assert.GreaterOrEqual(t, got.startTime.UnixNano(), before.UnixNano())
	assert.LessOrEqual(t, got.startTime.UnixNano(), after.UnixNano())
	assert.Equal(t, 1, got.alreadyAttemptedPods.Len())

	// Calling set again on the same context must not reset the startTime,
	// because alreadyAttemptedPods is already populated.
	originalStart := got.startTime
	time.Sleep(2 * time.Millisecond)
	h.setGangSchedulingContext(got, "re-enter")
	assert.Equal(t, originalStart, h.getCurrentGangSchedulingContext().startTime)
}

func TestClearGangSchedulingContext_RecordsMetricWithReasonAndJobSize(t *testing.T) {
	schedulermetrics.Register()
	schedulermetrics.GangScheduleCycleDuration.Reset()

	h := &GangSchedulingContextHolder{}
	ctx := &GangSchedulingContext{
		firstPod:  newTestFirstPod("default", "pod-leader"),
		gangGroup: sets.New[string]("default/gangA"),
	}
	h.setGangSchedulingContext(ctx, "first-pod-passed-prefilter")

	// Add extra attempted pods so the job size bucket is deterministic.
	for i := 0; i < 25; i++ {
		ctx.alreadyAttemptedPods.Insert(string(rune('a' + i)))
	}
	// 1 (pod-leader) + 25 = 26 attempted pods => bucket "1-100".
	assert.Equal(t, 26, ctx.alreadyAttemptedPods.Len())

	// Simulate some cycle time.
	time.Sleep(5 * time.Millisecond)
	h.clearGangSchedulingContext(ReasonGangIsSucceed)

	// The context reference must be cleared.
	assert.Nil(t, h.getCurrentGangSchedulingContext())

	// The metric must have been recorded with the right labels.
	vec, err := testutil.GetHistogramVecFromGatherer(legacyregistry.DefaultGatherer,
		"scheduler_gang_schedule_cycle_duration_seconds",
		map[string]string{"reason": ReasonGangIsSucceed, "job_size": "1-100"})
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), vec.GetAggregatedSampleCount())
	assert.Positive(t, vec.GetAggregatedSampleSum(), "recorded latency must be positive")
}

func TestClearGangSchedulingContext_NilContextIsNoOp(t *testing.T) {
	schedulermetrics.Register()
	schedulermetrics.GangScheduleCycleDuration.Reset()

	h := &GangSchedulingContextHolder{}
	// No panic, no metric emitted when the context is nil.
	h.clearGangSchedulingContext(ReasonGangIsNil)

	// When the metric has no samples at all, GetHistogramVecFromGatherer returns
	// a "metric not found" error. Treat that as equivalent to "zero samples".
	assertNoGangCycleSamples(t, ReasonGangIsNil, "0")
}

func TestClearGangSchedulingContext_ZeroStartTimeSkipsMetric(t *testing.T) {
	schedulermetrics.Register()
	schedulermetrics.GangScheduleCycleDuration.Reset()

	h := &GangSchedulingContextHolder{}
	// Install a context that was never fully initialized (alreadyAttemptedPods
	// was seeded externally, so setGangSchedulingContext would not set startTime).
	ctx := &GangSchedulingContext{
		firstPod:             newTestFirstPod("default", "pod-leader"),
		gangGroup:            sets.New[string]("default/gangA"),
		alreadyAttemptedPods: sets.New[string]("default/pod-leader"),
	}
	h.setGangSchedulingContext(ctx, "unit-test")
	assert.True(t, h.getCurrentGangSchedulingContext().startTime.IsZero())

	h.clearGangSchedulingContext(ReasonAllPendingPodsIsAlreadyAttempted)

	// Metric must not be emitted because startTime was zero.
	assertNoGangCycleSamples(t, ReasonAllPendingPodsIsAlreadyAttempted, "1-100")
}

// assertNoGangCycleSamples asserts that no samples were recorded for the given
// label combination. A "metric not found" error from the gatherer means the
// metric family has no samples at all, which also satisfies the assertion.
func assertNoGangCycleSamples(t *testing.T, reason, jobSize string) {
	t.Helper()
	vec, err := testutil.GetHistogramVecFromGatherer(legacyregistry.DefaultGatherer,
		"scheduler_gang_schedule_cycle_duration_seconds",
		map[string]string{"reason": reason, "job_size": jobSize})
	if err != nil {
		assert.Contains(t, err.Error(), "not found")
		return
	}
	assert.Equal(t, uint64(0), vec.GetAggregatedSampleCount())
}

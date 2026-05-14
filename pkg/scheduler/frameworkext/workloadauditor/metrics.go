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

package workloadauditor

import (
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
)

const (
	outcomeScheduled     = "scheduled"
	outcomeDeleted       = "deleted"
	outcomeGangScheduled = "gangScheduled"
	outcomeGangDeleted   = "gangDeleted"
)

var (
	initOnce             sync.Once
	configuredLabelNames sets.Set[string]

	// RepeatedPreemptionTotal repeated preemption counter.
	RepeatedPreemptionTotal *metrics.CounterVec

	// PreemptionInvalidationsTotal preemption invalidation counter.
	PreemptionInvalidationsTotal *metrics.CounterVec

	// VictimRescheduleDurationSeconds histogram of time from victimAllDeleted to next scheduling result.
	VictimRescheduleDurationSeconds *metrics.HistogramVec

	// PreemptionVictimDeletingRetries histogram of preemptVictimDeleting retry count per preemption cycle.
	PreemptionVictimDeletingRetries *metrics.HistogramVec

	// PreemptionToVictimDeletedSeconds histogram of time from preemptNominated to victimAllDeleted (normal cycle completion).
	PreemptionToVictimDeletedSeconds *metrics.HistogramVec

	// PreemptionCycleInterruptedSeconds histogram of time from preemptNominated to cycle interruption
	// (next preemptNominated, scheduled, preemptFailure, scheduleFailure, or workload deletion).
	// These durations may be affected by scheduling backlog and are less accurate.
	PreemptionCycleInterruptedSeconds *metrics.HistogramVec

	// SchedulingEventsBeforeOutcome histogram of scheduling event counts before workload outcome.
	SchedulingEventsBeforeOutcome *metrics.HistogramVec

	// SchedulingEventIntervalSeconds histogram of time between consecutive scheduling events
	// within a single dequeue-attempt round.
	SchedulingEventIntervalSeconds *metrics.HistogramVec

	// RecordMethodDurationSeconds histogram of RecordAttemptPod / RecordDiagnosis call duration.
	RecordMethodDurationSeconds *metrics.HistogramVec
)

// InitMetrics creates and registers all workload auditor metrics with the given label names.
// Must be called once after flag parsing (typically from NewWorkloadAuditor).
func InitMetrics(labelNames []string) {
	initOnce.Do(func() {
		configuredLabelNames = sets.New[string](labelNames...)
		RepeatedPreemptionTotal = metrics.NewCounterVec(
			&metrics.CounterOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_repeated_preemptions_total",
				Help:           "Total occurrences of a workload being preempted more than once",
				StabilityLevel: metrics.ALPHA,
			},
			labelNames,
		)

		PreemptionInvalidationsTotal = metrics.NewCounterVec(
			&metrics.CounterOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_preemption_invalidations_total",
				Help:           "Total occurrences of a preemption result being invalidated",
				StabilityLevel: metrics.ALPHA,
			},
			labelNames,
		)

		VictimRescheduleDurationSeconds = metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_victim_reschedule_duration_seconds",
				Help:           "Time from victimAllDeleted to the next scheduling result, in seconds",
				Buckets:        metrics.ExponentialBuckets(0.1, 2, 15),
				StabilityLevel: metrics.ALPHA,
			},
			labelNames,
		)

		PreemptionVictimDeletingRetries = metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_preemption_victim_deleting_retries",
				Help:           "Count of preemptVictimDeleting events per preemption cycle",
				Buckets:        metrics.LinearBuckets(0, 1, 20),
				StabilityLevel: metrics.ALPHA,
			},
			labelNames,
		)

		PreemptionToVictimDeletedSeconds = metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_preemption_to_victim_deleted_seconds",
				Help:           "Time from preemptNominated to victimAllDeleted (normal cycle completion), in seconds",
				Buckets:        metrics.ExponentialBuckets(0.1, 2, 15),
				StabilityLevel: metrics.ALPHA,
			},
			labelNames,
		)

		PreemptionCycleInterruptedSeconds = metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_preemption_cycle_interrupted_seconds",
				Help:           "Time from preemptNominated to cycle interruption (next preemption, scheduled, failure, or deletion), in seconds. May be affected by scheduling backlog.",
				Buckets:        metrics.ExponentialBuckets(0.1, 2, 15),
				StabilityLevel: metrics.ALPHA,
			},
			labelNames,
		)

		SchedulingEventsBeforeOutcome = metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_scheduling_events_before_outcome",
				Help:           "Distribution of per-event-type counts before workload completion",
				Buckets:        metrics.LinearBuckets(0, 1, 30),
				StabilityLevel: metrics.ALPHA,
			},
			append(labelNames, "event_type", "outcome"),
		)

		SchedulingEventIntervalSeconds = metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_scheduling_event_interval_seconds",
				Help:           "Time between consecutive scheduling events within a dequeue-attempt round, in seconds",
				Buckets:        metrics.ExponentialBuckets(0.1, 2, 20), // 0.1s … ~52428s (~14.5h)
				StabilityLevel: metrics.ALPHA,
			},
			labelNames,
		)

		RecordMethodDurationSeconds = metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Subsystem:      schedulermetrics.SchedulerSubsystem,
				Name:           "workload_auditor_record_method_duration_seconds",
				Help:           "Latency of WorkloadAuditor record methods (RecordAttemptPod, RecordDiagnosis)",
				Buckets:        metrics.ExponentialBuckets(0.000001, 4, 10), // 1µs … ~0.26s
				StabilityLevel: metrics.ALPHA,
			},
			[]string{"method"},
		)

		for _, m := range []metrics.Registerable{
			RepeatedPreemptionTotal,
			PreemptionInvalidationsTotal,
			VictimRescheduleDurationSeconds,
			PreemptionVictimDeletingRetries,
			PreemptionToVictimDeletedSeconds,
			PreemptionCycleInterruptedSeconds,
			SchedulingEventsBeforeOutcome,
			SchedulingEventIntervalSeconds,
			RecordMethodDurationSeconds,
		} {
			legacyregistry.MustRegister(m)
		}
	})
}

// DeleteMetricsByLabel deletes all metric time series where labelName equals labelValue.
// This is a no-op if labelName is not among the configured metric labels or metrics are not initialized.
func DeleteMetricsByLabel(labelName, labelValue string) {
	if !configuredLabelNames.Has(labelName) {
		return
	}
	labels := map[string]string{labelName: labelValue}
	RepeatedPreemptionTotal.DeletePartialMatch(labels)
	PreemptionInvalidationsTotal.DeletePartialMatch(labels)
	VictimRescheduleDurationSeconds.DeletePartialMatch(labels)
	PreemptionVictimDeletingRetries.DeletePartialMatch(labels)
	PreemptionToVictimDeletedSeconds.DeletePartialMatch(labels)
	PreemptionCycleInterruptedSeconds.DeletePartialMatch(labels)
	SchedulingEventsBeforeOutcome.DeletePartialMatch(labels)
	SchedulingEventIntervalSeconds.DeletePartialMatch(labels)
}

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

// Anomaly detection rules for workload scheduling lifecycle:
//
// Repeated Preemption:
//   A workload with more than one preemptNominated in its lifetime indicates
//   repeated preemption. Each additional occurrence increments
//   RepeatedPreemptionTotal and emits an ALERT log.
//
// Preemption Invalidation:
//   After a preemptNominated, a subsequent preemptFailure, scheduleFailure,
//   or another preemptNominated means the previous preemption result was
//   invalidated. Each preemptNominated can only be invalidated once.
//   Increments PreemptionInvalidationsTotal and emits an ALERT log.
//
// Victim Reschedule Latency:
//   After victimAllDeleted, the workload should be rescheduled promptly.
//   The next scheduling result (scheduleFailure, preemptFailure,
//   preemptNominated, or scheduled — but NOT preemptVictimDeleting) is
//   measured against victimAllDeleted time. Observed in
//   VictimRescheduleDurationSeconds; ALERT if exceeding the configured
//   VictimRescheduleDuration threshold.
//   If a gatedByQueueAdmission event arrives while waiting, the wait is
//   canceled because the pod cannot be dequeued for scheduling.
//
// Preemption Lifecycle:
//   A preemption cycle starts at preemptNominated and ends at
//   victimAllDeleted, the next preemptNominated, scheduled,
//   preemptFailure, scheduleFailure, or workload deletion.
//   Within the cycle, preemptVictimDeleting events are counted as retries.
//   Observed in PreemptionVictimDeletingRetries (retry count distribution).
//   Cycle duration is observed in two separate metrics:
//   - PreemptionToVictimDeletedSeconds: normal completion (victimAllDeleted)
//   - PreemptionCycleInterruptedSeconds: interrupted by other events
//     (next preemptNominated, scheduled, failure, or deletion), whose
//     timing may be skewed by scheduling backlog.
//   ALERT if retries exceed VictimDeletingRetries or duration exceeds
//   VictimDeletionDuration.
//
// Scheduling Event Interval:
//   Within a dequeue-attempt round, the time between consecutive scheduling
//   events is measured and observed into SchedulingEventIntervalSeconds.
//   A round is started by Create, admissionPassed, or gangMinMemberSatisfied
//   (which set the initial timer) and ended by gatedByQueueAdmission (which
//   clears the timer without observing). All other scheduling
//   events (scheduled, preemptNominated, preemptVictimDeleting, preemptFailure,
//   scheduleFailure, gangAllPodsAlreadyAttempted) measure the interval since
//   the previous event and update the timer. victimAllDeleted is excluded
//   because it is not a scheduling event driven by dequeue.
//   ALERT if any interval exceeds the configured SchedulingEventInterval
//   threshold (default 5 min).
//
// Scheduling Event Counts Before Outcome:
//   Counts of all record-type events accumulated over the workload's lifetime
//   are observed into SchedulingEventsBeforeOutcome when the workload is
//   scheduled or deleted, labeled by event_type and outcome
//   (scheduled / deleted / gangScheduled / gangDeleted).

import (
	"strings"
	"time"

	"k8s.io/klog/v2"
)

// checkRecordAnomaly is called after every appendRecord to detect anomalies
// based on the newly appended record type and the workload's accumulated state.
func checkRecordAnomaly(config *WorkloadAuditorConfig, wr *WorkloadRecord, recordType RecordType, message string) {
	now := time.Now()

	switch recordType {
	case RecordTypePreemptNominated:
		handlePreemptNominated(config, wr, now, recordType, message)
	case RecordTypePreemptFailure:
		handlePreemptionInvalidation(wr, recordType, message)
		handleVictimRescheduleCheck(config, wr, now, recordType, message)
		finalizePreemptionCycleIfActive(config, wr, now, recordType, message)
	case RecordTypeScheduleFailure:
		handlePreemptionInvalidation(wr, recordType, message)
		handleVictimRescheduleCheck(config, wr, now, recordType, message)
		finalizePreemptionCycleIfActive(config, wr, now, recordType, message)
	case RecordTypeScheduled:
		handleVictimRescheduleCheck(config, wr, now, recordType, message)
		finalizePreemptionCycleIfActive(config, wr, now, recordType, message)
	case RecordTypeVictimAllDeleted:
		handleVictimAllDeleted(config, wr, now, recordType, message)
	case RecordTypePreemptVictimDeleting:
		wr.preemptVictimDeletingCountSinceNom++
	case RecordTypeGatedByQueueAdmission:
		// Pod is gated and cannot be dequeued; cancel any pending reschedule wait.
		wr.lastVictimAllDeletedTime = nil
	}

	// Inter-event interval tracking within the current dequeue-attempt round.
	trackSchedulingEventInterval(config, wr, recordType, now, message)
}

// handlePreemptNominated handles all rules triggered by a preemptNominated event.
func handlePreemptNominated(config *WorkloadAuditorConfig, wr *WorkloadRecord, now time.Time, triggerType RecordType, triggerMsg string) {
	// Finalize the previous preemption cycle if one was active (interrupted)
	if wr.lastPreemptNominatedTime != nil {
		finalizePreemptionCycle(config, wr, now, false, triggerType, triggerMsg)
	}

	// Repeated preemption detection (count already incremented by appendRecord)
	nomCount := wr.recordTypeCounts[RecordTypePreemptNominated]
	if nomCount > 1 {
		RepeatedPreemptionTotal.WithLabelValues(wr.labelValues...).Inc()
		klog.Infof("workloadauditor: ALERT: repeated preemption for workload %s %s, count=%d, trigger=%s, message=%q",
			wr.WorkloadKey, wr.labelDetail, nomCount, triggerType, triggerMsg)
	}

	// Preemption invalidation: a new preemptNominated after a previous one
	if nomCount >= 2 && !wr.lastPreemptNominatedInvalidated {
		wr.lastPreemptNominatedInvalidated = true
		PreemptionInvalidationsTotal.WithLabelValues(wr.labelValues...).Inc()
		klog.Infof("workloadauditor: ALERT: preemption invalidated by new preemption for workload %s %s, trigger=%s, message=%q",
			wr.WorkloadKey, wr.labelDetail, triggerType, triggerMsg)
	}

	// Reset state for the new preemption cycle
	wr.lastPreemptNominatedInvalidated = false
	wr.lastPreemptNominatedTime = &now
	wr.preemptVictimDeletingCountSinceNom = 0

	// Also check if we were waiting for a reschedule after victim deletion
	handleVictimRescheduleCheck(config, wr, now, triggerType, triggerMsg)
}

// handlePreemptionInvalidation checks whether a failure event invalidates
// the most recent preemptNominated result.
func handlePreemptionInvalidation(wr *WorkloadRecord, recordType RecordType, message string) {
	if wr.recordTypeCounts[RecordTypePreemptNominated] < 1 || wr.lastPreemptNominatedInvalidated {
		return
	}
	wr.lastPreemptNominatedInvalidated = true
	PreemptionInvalidationsTotal.WithLabelValues(wr.labelValues...).Inc()
	klog.Infof("workloadauditor: ALERT: preemption invalidated by %s for workload %s %s, message=%q",
		recordType, wr.WorkloadKey, wr.labelDetail, message)
}

// handleVictimRescheduleCheck checks if a scheduling result arrives after
// victimAllDeleted, measure the latency and alert if it exceeds the threshold.
func handleVictimRescheduleCheck(config *WorkloadAuditorConfig, wr *WorkloadRecord, now time.Time, triggerType RecordType, triggerMsg string) {
	if wr.lastVictimAllDeletedTime == nil {
		return
	}
	duration := now.Sub(*wr.lastVictimAllDeletedTime)
	VictimRescheduleDurationSeconds.WithLabelValues(wr.labelValues...).Observe(duration.Seconds())
	if duration > config.VictimRescheduleDuration {
		klog.Infof("workloadauditor: ALERT: slow reschedule after victim deletion for workload %s %s, duration=%v (threshold %v), trigger=%s, message=%q",
			wr.WorkloadKey, wr.labelDetail, duration, config.VictimRescheduleDuration, triggerType, triggerMsg)
	}
	wr.lastVictimAllDeletedTime = nil
}

// finalizePreemptionCycleIfActive finalizes the current preemption cycle if one
// is active, then clears the cycle state.
func finalizePreemptionCycleIfActive(config *WorkloadAuditorConfig, wr *WorkloadRecord, now time.Time, triggerType RecordType, triggerMsg string) {
	if wr.lastPreemptNominatedTime == nil {
		return
	}
	finalizePreemptionCycle(config, wr, now, false, triggerType, triggerMsg)
	wr.lastPreemptNominatedTime = nil
}

// handleVictimAllDeleted handles state setup and cycle finalization
// when all victims have been deleted.
func handleVictimAllDeleted(config *WorkloadAuditorConfig, wr *WorkloadRecord, now time.Time, triggerType RecordType, triggerMsg string) {
	// Finalize the preemption cycle (preemptNominated -> victimAllDeleted, normal completion)
	if wr.lastPreemptNominatedTime != nil {
		finalizePreemptionCycle(config, wr, now, true, triggerType, triggerMsg)
		wr.lastPreemptNominatedTime = nil
	}

	// Mark that we are now waiting for a reschedule result
	wr.lastVictimAllDeletedTime = &now
}

// finalizePreemptionCycle observes metrics for a completed preemption cycle.
// victimDeleted indicates whether the cycle completed normally (victimAllDeleted)
// or was interrupted by another event (next preemption, scheduled, failure, deletion).
func finalizePreemptionCycle(config *WorkloadAuditorConfig, wr *WorkloadRecord, endTime time.Time, victimDeleted bool, triggerType RecordType, triggerMsg string) {
	duration := endTime.Sub(*wr.lastPreemptNominatedTime)
	retries := wr.preemptVictimDeletingCountSinceNom

	if victimDeleted {
		PreemptionToVictimDeletedSeconds.WithLabelValues(wr.labelValues...).Observe(duration.Seconds())
	} else {
		PreemptionCycleInterruptedSeconds.WithLabelValues(wr.labelValues...).Observe(duration.Seconds())
	}
	PreemptionVictimDeletingRetries.WithLabelValues(wr.labelValues...).Observe(float64(retries))

	if retries > config.VictimDeletingRetries {
		klog.Infof("workloadauditor: ALERT: excessive victim deleting retries for workload %s %s, retries=%d (threshold %d), trigger=%s, message=%q",
			wr.WorkloadKey, wr.labelDetail, retries, config.VictimDeletingRetries, triggerType, triggerMsg)
	}
	if victimDeleted {
		if duration > config.VictimDeletionDuration {
			klog.Infof("workloadauditor: ALERT: slow victim deletion for workload %s %s, duration=%v (threshold %v), trigger=%s, message=%q",
				wr.WorkloadKey, wr.labelDetail, duration, config.VictimDeletionDuration, triggerType, triggerMsg)
		}
	} else {
		if duration > config.VictimDeletionDuration {
			klog.Infof("workloadauditor: ALERT: preemption cycle interrupted (victim not fully deleted) for workload %s %s, duration=%v (threshold %v), trigger=%s, message=%q",
				wr.WorkloadKey, wr.labelDetail, duration, config.VictimDeletionDuration, triggerType, triggerMsg)
		}
	}
}

// isRoundStarter returns true if the record type starts a new dequeue-attempt round.
func isRoundStarter(recordType RecordType) bool {
	return recordType == RecordTypeCreate ||
		recordType == RecordTypeAdmissionPassed ||
		strings.HasPrefix(string(recordType), "gangMinMemberSatisfied")
}

// trackSchedulingEventInterval measures the time between consecutive scheduling
// events within a dequeue-attempt round.
// Round starters (Create, admissionPassed, gangMinMemberSatisfied) set the initial timer.
// Round ender (gatedByQueueAdmission) clears the timer.
// All other scheduling events measure and update the timer.
func trackSchedulingEventInterval(config *WorkloadAuditorConfig, wr *WorkloadRecord, recordType RecordType, now time.Time, message string) {
	if isRoundStarter(recordType) {
		// Round starter: begin a new timer, no measurement.
		wr.lastSchedulingEventTime = &now
		return
	}

	if recordType == RecordTypeGatedByQueueAdmission {
		// Round ender: clear the timer. No metric observation needed.
		wr.lastSchedulingEventTime = nil
		return
	}

	// victimAllDeleted is not a dequeue-driven scheduling event; skip interval tracking.
	if recordType == RecordTypeVictimAllDeleted {
		return
	}

	// Mid-round scheduling event: measure and update the timer.
	if wr.lastSchedulingEventTime != nil {
		duration := now.Sub(*wr.lastSchedulingEventTime)
		SchedulingEventIntervalSeconds.WithLabelValues(wr.labelValues...).Observe(duration.Seconds())
		if duration > config.SchedulingEventInterval {
			klog.Infof("workloadauditor: ALERT: long scheduling event interval for workload %s %s, duration=%v (threshold %v), trigger=%s, message=%q",
				wr.WorkloadKey, wr.labelDetail, duration, config.SchedulingEventInterval, recordType, message)
		}
	}
	wr.lastSchedulingEventTime = &now
}

// finalizeWorkloadRecord is called just before a workload record is removed from
// the map. It flushes any remaining preemption cycle and observes event-count histograms.
func finalizeWorkloadRecord(wr *WorkloadRecord, outcome string) {
	klog.V(4).Infof("WorkloadAuditor finalize: workloadKey=%s %s, outcome=%s, recordTypeCounts=%v",
		wr.WorkloadKey, wr.labelDetail, outcome, wr.recordTypeCounts)
	now := time.Now()

	// Finalize any open preemption cycle (interrupted — workload deleted before victimAllDeleted)
	if wr.lastPreemptNominatedTime != nil {
		PreemptionCycleInterruptedSeconds.WithLabelValues(wr.labelValues...).Observe(now.Sub(*wr.lastPreemptNominatedTime).Seconds())
		PreemptionVictimDeletingRetries.WithLabelValues(wr.labelValues...).Observe(float64(wr.preemptVictimDeletingCountSinceNom))
	}

	// Observe event counts by record type
	for recordType, count := range wr.recordTypeCounts {
		SchedulingEventsBeforeOutcome.WithLabelValues(append(wr.labelValues, string(recordType), outcome)...).Observe(float64(count))
	}
}

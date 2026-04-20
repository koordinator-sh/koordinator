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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTestRecord(key string) *WorkloadRecord {
	return &WorkloadRecord{
		WorkloadKey:      key,
		recordTypeCounts: map[RecordType]int{RecordTypeCreate: 1},
		labelValues:      []string{"koord-prod"},
		labelDetail:      "{koord-prod}",
	}
}

func newTestConfigPtr() *WorkloadAuditorConfig {
	c := newTestConfig()
	return &c
}

// --- checkRecordAnomaly: PreemptNominated ---

func TestCheckRecordAnomaly_PreemptNominated_First(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	checkRecordAnomaly(config, wr, RecordTypePreemptNominated, "")
	assert.NotNil(t, wr.lastPreemptNominatedTime)
	assert.Equal(t, 0, wr.preemptVictimDeletingCountSinceNom)
	assert.False(t, wr.lastPreemptNominatedInvalidated)
}

func TestCheckRecordAnomaly_PreemptNominated_Repeated(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now().Add(-5 * time.Second)
	wr.lastPreemptNominatedTime = &now
	wr.recordTypeCounts[RecordTypePreemptNominated] = 2
	checkRecordAnomaly(config, wr, RecordTypePreemptNominated, "")
	// repeated preemption: count > 1 => metric emitted (no crash)
	assert.NotNil(t, wr.lastPreemptNominatedTime)
	assert.False(t, wr.lastPreemptNominatedInvalidated) // reset for the new cycle
}

func TestCheckRecordAnomaly_PreemptNominated_InvalidatesPrevious(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now().Add(-5 * time.Second)
	wr.lastPreemptNominatedTime = &now
	wr.recordTypeCounts[RecordTypePreemptNominated] = 2
	wr.lastPreemptNominatedInvalidated = false
	checkRecordAnomaly(config, wr, RecordTypePreemptNominated, "")
	// After a new preemptNominated the invalidated flag is reset for the NEW cycle
	assert.False(t, wr.lastPreemptNominatedInvalidated)
}

// --- checkRecordAnomaly: PreemptFailure ---

func TestCheckRecordAnomaly_PreemptFailure_InvalidatesPreemption(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now().Add(-2 * time.Second)
	wr.lastPreemptNominatedTime = &now
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	wr.lastPreemptNominatedInvalidated = false
	checkRecordAnomaly(config, wr, RecordTypePreemptFailure, "")
	assert.True(t, wr.lastPreemptNominatedInvalidated)
	// preemption cycle finalized
	assert.Nil(t, wr.lastPreemptNominatedTime)
}

func TestCheckRecordAnomaly_PreemptFailure_AlreadyInvalidated(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now().Add(-2 * time.Second)
	wr.lastPreemptNominatedTime = &now
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	wr.lastPreemptNominatedInvalidated = true
	checkRecordAnomaly(config, wr, RecordTypePreemptFailure, "")
	assert.True(t, wr.lastPreemptNominatedInvalidated) // still true, no double invalidation
}

func TestCheckRecordAnomaly_PreemptFailure_NoPreemptionActive(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	checkRecordAnomaly(config, wr, RecordTypePreemptFailure, "")
	// no preemptNominated count, so no invalidation
	assert.False(t, wr.lastPreemptNominatedInvalidated)
}

// --- checkRecordAnomaly: ScheduleFailure ---

func TestCheckRecordAnomaly_ScheduleFailure_InvalidatesPreemption(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now().Add(-2 * time.Second)
	wr.lastPreemptNominatedTime = &now
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	wr.lastPreemptNominatedInvalidated = false
	checkRecordAnomaly(config, wr, RecordTypeScheduleFailure, "")
	assert.True(t, wr.lastPreemptNominatedInvalidated)
	assert.Nil(t, wr.lastPreemptNominatedTime)
}

// --- checkRecordAnomaly: Scheduled ---

func TestCheckRecordAnomaly_Scheduled_FinalizesPreemptionCycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now().Add(-3 * time.Second)
	wr.lastPreemptNominatedTime = &now
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	checkRecordAnomaly(config, wr, RecordTypeScheduled, "")
	assert.Nil(t, wr.lastPreemptNominatedTime)
}

func TestCheckRecordAnomaly_Scheduled_MeasuresVictimReschedule(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	deleted := time.Now().Add(-1 * time.Second)
	wr.lastVictimAllDeletedTime = &deleted
	checkRecordAnomaly(config, wr, RecordTypeScheduled, "")
	assert.Nil(t, wr.lastVictimAllDeletedTime)
}

// --- checkRecordAnomaly: VictimAllDeleted ---

func TestCheckRecordAnomaly_VictimAllDeleted(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	nom := time.Now().Add(-5 * time.Second)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 2
	checkRecordAnomaly(config, wr, RecordTypeVictimAllDeleted, "")
	// cycle finalized
	assert.Nil(t, wr.lastPreemptNominatedTime)
	// now waiting for reschedule
	assert.NotNil(t, wr.lastVictimAllDeletedTime)
}

func TestCheckRecordAnomaly_VictimAllDeleted_NoCycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	checkRecordAnomaly(config, wr, RecordTypeVictimAllDeleted, "")
	// No cycle active, just start waiting
	assert.NotNil(t, wr.lastVictimAllDeletedTime)
}

// --- checkRecordAnomaly: PreemptVictimDeleting ---

func TestCheckRecordAnomaly_PreemptVictimDeleting(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	wr.preemptVictimDeletingCountSinceNom = 0
	checkRecordAnomaly(config, wr, RecordTypePreemptVictimDeleting, "")
	assert.Equal(t, 1, wr.preemptVictimDeletingCountSinceNom)
}

// --- checkRecordAnomaly: GatedByQueueAdmission ---

func TestCheckRecordAnomaly_Gated_CancelsRescheduleWait(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	deleted := time.Now().Add(-2 * time.Second)
	wr.lastVictimAllDeletedTime = &deleted
	checkRecordAnomaly(config, wr, RecordTypeGatedByQueueAdmission, "")
	assert.Nil(t, wr.lastVictimAllDeletedTime)
}

func TestCheckRecordAnomaly_Gated_NoRescheduleWait(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	checkRecordAnomaly(config, wr, RecordTypeGatedByQueueAdmission, "")
	assert.Nil(t, wr.lastVictimAllDeletedTime) // remains nil, no crash
}

// --- handlePreemptNominated ---

func TestHandlePreemptNominated_NewCycleAfterPrevious(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	old := time.Now().Add(-10 * time.Second)
	wr.lastPreemptNominatedTime = &old
	wr.preemptVictimDeletingCountSinceNom = 5
	wr.recordTypeCounts[RecordTypePreemptNominated] = 2
	wr.lastPreemptNominatedInvalidated = false

	now := time.Now()
	handlePreemptNominated(config, wr, now, RecordTypePreemptNominated, "")

	// Old cycle finalized, new cycle started
	assert.NotNil(t, wr.lastPreemptNominatedTime)
	assert.Equal(t, 0, wr.preemptVictimDeletingCountSinceNom)
	assert.False(t, wr.lastPreemptNominatedInvalidated) // reset for new cycle
}

func TestHandlePreemptNominated_WithVictimRescheduleWait(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	deleted := time.Now().Add(-1 * time.Second)
	wr.lastVictimAllDeletedTime = &deleted
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1

	handlePreemptNominated(config, wr, time.Now(), RecordTypePreemptNominated, "")

	// victim reschedule check done, cleared
	assert.Nil(t, wr.lastVictimAllDeletedTime)
	assert.NotNil(t, wr.lastPreemptNominatedTime)
}

// --- handlePreemptionInvalidation ---

func TestHandlePreemptionInvalidation_NoPreemption(t *testing.T) {
	wr := newTestRecord("w1")
	handlePreemptionInvalidation(wr, RecordTypeScheduleFailure, "")
	assert.False(t, wr.lastPreemptNominatedInvalidated)
}

func TestHandlePreemptionInvalidation_AlreadyDone(t *testing.T) {
	wr := newTestRecord("w1")
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	wr.lastPreemptNominatedInvalidated = true
	handlePreemptionInvalidation(wr, RecordTypePreemptFailure, "")
	assert.True(t, wr.lastPreemptNominatedInvalidated)
}

func TestHandlePreemptionInvalidation_Success(t *testing.T) {
	wr := newTestRecord("w1")
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	wr.lastPreemptNominatedInvalidated = false
	handlePreemptionInvalidation(wr, RecordTypeScheduleFailure, "")
	assert.True(t, wr.lastPreemptNominatedInvalidated)
}

// --- handleVictimRescheduleCheck ---

func TestHandleVictimRescheduleCheck_NotWaiting(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	handleVictimRescheduleCheck(config, wr, time.Now(), RecordTypeScheduled, "")
	assert.Nil(t, wr.lastVictimAllDeletedTime)
}

func TestHandleVictimRescheduleCheck_WithinThreshold(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	deleted := time.Now().Add(-1 * time.Second)
	wr.lastVictimAllDeletedTime = &deleted
	handleVictimRescheduleCheck(config, wr, time.Now(), RecordTypeScheduled, "")
	assert.Nil(t, wr.lastVictimAllDeletedTime)
}

func TestHandleVictimRescheduleCheck_ExceedsThreshold(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	deleted := time.Now().Add(-20 * time.Second) // > VictimRescheduleDuration(10s)
	wr.lastVictimAllDeletedTime = &deleted
	handleVictimRescheduleCheck(config, wr, time.Now(), RecordTypeScheduled, "")
	// Should still clear the wait
	assert.Nil(t, wr.lastVictimAllDeletedTime)
}

// --- finalizePreemptionCycleIfActive ---

func TestFinalizePreemptionCycleIfActive_NoCycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	finalizePreemptionCycleIfActive(config, wr, time.Now(), RecordTypeScheduleFailure, "")
	assert.Nil(t, wr.lastPreemptNominatedTime)
}

func TestFinalizePreemptionCycleIfActive_WithCycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	nom := time.Now().Add(-5 * time.Second)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 1
	finalizePreemptionCycleIfActive(config, wr, time.Now(), RecordTypeScheduleFailure, "")
	assert.Nil(t, wr.lastPreemptNominatedTime)
}

// --- handleVictimAllDeleted ---

func TestHandleVictimAllDeleted_WithActiveCycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	nom := time.Now().Add(-5 * time.Second)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 2
	handleVictimAllDeleted(config, wr, time.Now(), RecordTypeVictimAllDeleted, "")
	assert.Nil(t, wr.lastPreemptNominatedTime)
	assert.NotNil(t, wr.lastVictimAllDeletedTime)
}

func TestHandleVictimAllDeleted_NoCycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	handleVictimAllDeleted(config, wr, time.Now(), RecordTypeVictimAllDeleted, "")
	assert.NotNil(t, wr.lastVictimAllDeletedTime)
}

// --- finalizePreemptionCycle ---

func TestFinalizePreemptionCycle_NormalDuration(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	nom := time.Now().Add(-2 * time.Second)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 1
	finalizePreemptionCycle(config, wr, time.Now(), true, RecordTypeVictimAllDeleted, "") // normal completion, no panic, metrics observed
}

func TestFinalizePreemptionCycle_ExcessiveRetries(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	nom := time.Now().Add(-2 * time.Second)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 10                                            // > config.VictimDeletingRetries(3)
	finalizePreemptionCycle(config, wr, time.Now(), true, RecordTypeVictimAllDeleted, "") // triggers ALERT log
}

func TestFinalizePreemptionCycle_SlowDeletion(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	nom := time.Now().Add(-60 * time.Second) // > config.VictimDeletionDuration(30s)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 1
	finalizePreemptionCycle(config, wr, time.Now(), false, RecordTypeScheduleFailure, "test") // interrupted, triggers ALERT log
}

func TestFinalizePreemptionCycle_SlowVictimDeletion(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	nom := time.Now().Add(-60 * time.Second) // > config.VictimDeletionDuration(30s)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 1
	finalizePreemptionCycle(config, wr, time.Now(), true, RecordTypeVictimAllDeleted, "test") // normal completion but slow, triggers ALERT log
}

// --- finalizeWorkloadRecord ---

func TestFinalizeWorkloadRecord_NoOpenCycle(t *testing.T) {
	wr := newTestRecord("w1")
	wr.recordTypeCounts[RecordTypeScheduleFailure] = 3
	finalizeWorkloadRecord(wr, outcomeDeleted)
	// No crash; event counts observed
}

func TestFinalizeWorkloadRecord_WithOpenCycle(t *testing.T) {
	wr := newTestRecord("w1")
	nom := time.Now().Add(-5 * time.Second)
	wr.lastPreemptNominatedTime = &nom
	wr.preemptVictimDeletingCountSinceNom = 2
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	finalizeWorkloadRecord(wr, outcomeScheduled)
	// Open cycle metrics observed, no crash
}

func TestFinalizeWorkloadRecord_GangDeleted(t *testing.T) {
	wr := newTestRecord("gang-1")
	wr.recordTypeCounts[RecordTypeGangMinMemberSatisfied] = 1
	finalizeWorkloadRecord(wr, outcomeGangDeleted)
}

func TestFinalizeWorkloadRecord_GangScheduled(t *testing.T) {
	wr := newTestRecord("gang-1")
	wr.recordTypeCounts[RecordTypePreemptNominated] = 2
	nom := time.Now().Add(-1 * time.Second)
	wr.lastPreemptNominatedTime = &nom
	finalizeWorkloadRecord(wr, outcomeGangScheduled)
}

// --- Full lifecycle integration ---

func TestFullPreemptionLifecycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")

	// 1. preemptNominated
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	checkRecordAnomaly(config, wr, RecordTypePreemptNominated, "")
	assert.NotNil(t, wr.lastPreemptNominatedTime)

	// 2. preemptVictimDeleting (retries)
	checkRecordAnomaly(config, wr, RecordTypePreemptVictimDeleting, "")
	checkRecordAnomaly(config, wr, RecordTypePreemptVictimDeleting, "")
	assert.Equal(t, 2, wr.preemptVictimDeletingCountSinceNom)

	// 3. victimAllDeleted
	wr.recordTypeCounts[RecordTypeVictimAllDeleted] = 1
	checkRecordAnomaly(config, wr, RecordTypeVictimAllDeleted, "")
	assert.Nil(t, wr.lastPreemptNominatedTime)
	assert.NotNil(t, wr.lastVictimAllDeletedTime)

	// 4. scheduled (clears reschedule wait)
	checkRecordAnomaly(config, wr, RecordTypeScheduled, "")
	assert.Nil(t, wr.lastVictimAllDeletedTime)
}

func TestPreemptionInvalidationByFailureThenNewPreemption(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")

	// First preemption cycle
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	checkRecordAnomaly(config, wr, RecordTypePreemptNominated, "")
	assert.NotNil(t, wr.lastPreemptNominatedTime)

	// Invalidated by scheduleFailure
	checkRecordAnomaly(config, wr, RecordTypeScheduleFailure, "")
	assert.True(t, wr.lastPreemptNominatedInvalidated)
	assert.Nil(t, wr.lastPreemptNominatedTime) // cycle finalized

	// Second preemption cycle
	wr.recordTypeCounts[RecordTypePreemptNominated] = 2
	checkRecordAnomaly(config, wr, RecordTypePreemptNominated, "")
	assert.NotNil(t, wr.lastPreemptNominatedTime)
	assert.False(t, wr.lastPreemptNominatedInvalidated) // reset for new cycle
}

func TestGatingCancelsVictimRescheduleWaitLifecycle(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")

	// Preemption cycle -> victimAllDeleted -> gating cancels wait
	wr.recordTypeCounts[RecordTypePreemptNominated] = 1
	checkRecordAnomaly(config, wr, RecordTypePreemptNominated, "")
	wr.recordTypeCounts[RecordTypeVictimAllDeleted] = 1
	checkRecordAnomaly(config, wr, RecordTypeVictimAllDeleted, "")
	assert.NotNil(t, wr.lastVictimAllDeletedTime)

	// Gating event cancels the wait
	checkRecordAnomaly(config, wr, RecordTypeGatedByQueueAdmission, "")
	assert.Nil(t, wr.lastVictimAllDeletedTime)
}

// --- isRoundStarter ---

func TestIsRoundStarter(t *testing.T) {
	assert.True(t, isRoundStarter(RecordTypeCreate))
	assert.True(t, isRoundStarter(RecordTypeAdmissionPassed))
	assert.True(t, isRoundStarter(RecordType("gangMinMemberSatisfied, gang-1")))
	assert.False(t, isRoundStarter(RecordTypeScheduled))
	assert.False(t, isRoundStarter(RecordTypePreemptNominated))
	assert.False(t, isRoundStarter(RecordTypeGatedByQueueAdmission))
}

// --- trackSchedulingEventInterval ---

func TestTrackSchedulingEventInterval_RoundStarter_SetsTimer(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	assert.Nil(t, wr.lastSchedulingEventTime)

	now := time.Now()
	trackSchedulingEventInterval(config, wr, RecordTypeCreate, now, "")
	assert.NotNil(t, wr.lastSchedulingEventTime)
	assert.Equal(t, now, *wr.lastSchedulingEventTime)
}

func TestTrackSchedulingEventInterval_AdmissionPassed_SetsTimer(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now()
	trackSchedulingEventInterval(config, wr, RecordTypeAdmissionPassed, now, "")
	assert.NotNil(t, wr.lastSchedulingEventTime)
	assert.Equal(t, now, *wr.lastSchedulingEventTime)
}

func TestTrackSchedulingEventInterval_GangMinMember_SetsTimer(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	now := time.Now()
	trackSchedulingEventInterval(config, wr, RecordType("gangMinMemberSatisfied, gang-1"), now, "")
	assert.NotNil(t, wr.lastSchedulingEventTime)
	assert.Equal(t, now, *wr.lastSchedulingEventTime)
}

func TestTrackSchedulingEventInterval_RoundEnder_ClearsTimer(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	prev := time.Now().Add(-2 * time.Second)
	wr.lastSchedulingEventTime = &prev

	trackSchedulingEventInterval(config, wr, RecordTypeGatedByQueueAdmission, time.Now(), "")
	assert.Nil(t, wr.lastSchedulingEventTime)
}

func TestTrackSchedulingEventInterval_RoundEnder_NoTimer(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	trackSchedulingEventInterval(config, wr, RecordTypeGatedByQueueAdmission, time.Now(), "")
	assert.Nil(t, wr.lastSchedulingEventTime) // remains nil, no crash
}

func TestTrackSchedulingEventInterval_VictimAllDeleted_Skipped(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	prev := time.Now().Add(-1 * time.Second)
	wr.lastSchedulingEventTime = &prev

	trackSchedulingEventInterval(config, wr, RecordTypeVictimAllDeleted, time.Now(), "")
	// Timer unchanged: victimAllDeleted does not participate in interval tracking
	assert.Equal(t, prev, *wr.lastSchedulingEventTime)
}

func TestTrackSchedulingEventInterval_MidRound_MeasuresAndUpdates(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	prev := time.Now().Add(-1 * time.Second)
	wr.lastSchedulingEventTime = &prev

	now := time.Now()
	trackSchedulingEventInterval(config, wr, RecordTypeScheduleFailure, now, "")
	// Timer updated to now
	assert.NotNil(t, wr.lastSchedulingEventTime)
	assert.Equal(t, now, *wr.lastSchedulingEventTime)
}

func TestTrackSchedulingEventInterval_MidRound_ExceedsThreshold(t *testing.T) {
	config := newTestConfigPtr()
	config.SchedulingEventInterval = 1 * time.Second
	wr := newTestRecord("w1")
	prev := time.Now().Add(-10 * time.Second) // > 1s threshold
	wr.lastSchedulingEventTime = &prev

	now := time.Now()
	trackSchedulingEventInterval(config, wr, RecordTypePreemptNominated, now, "slow pod")
	// Timer updated, ALERT logged
	assert.Equal(t, now, *wr.lastSchedulingEventTime)
}

func TestTrackSchedulingEventInterval_MidRound_NoActiveTimer(t *testing.T) {
	config := newTestConfigPtr()
	wr := newTestRecord("w1")
	assert.Nil(t, wr.lastSchedulingEventTime)

	now := time.Now()
	trackSchedulingEventInterval(config, wr, RecordTypeScheduleFailure, now, "")
	// No measurement, but timer set
	assert.Equal(t, now, *wr.lastSchedulingEventTime)
}

// --- Full lifecycle: inter-event interval tracking ---

func TestSchedulingEventIntervalLifecycle(t *testing.T) {
	config := newTestConfigPtr()
	config.SchedulingEventInterval = 5 * time.Minute
	wr := newTestRecord("w1")

	// 1. Create (round starter) → sets timer
	now := time.Now()
	trackSchedulingEventInterval(config, wr, RecordTypeCreate, now, "")
	assert.Equal(t, now, *wr.lastSchedulingEventTime)

	// 2. scheduleFailure → measures interval, updates timer
	now2 := now.Add(2 * time.Second)
	trackSchedulingEventInterval(config, wr, RecordTypeScheduleFailure, now2, "")
	assert.Equal(t, now2, *wr.lastSchedulingEventTime)

	// 3. preemptNominated → measures interval, updates timer
	now3 := now2.Add(3 * time.Second)
	trackSchedulingEventInterval(config, wr, RecordTypePreemptNominated, now3, "")
	assert.Equal(t, now3, *wr.lastSchedulingEventTime)

	// 4. victimAllDeleted → skipped, timer unchanged
	now4 := now3.Add(5 * time.Second)
	trackSchedulingEventInterval(config, wr, RecordTypeVictimAllDeleted, now4, "")
	assert.Equal(t, now3, *wr.lastSchedulingEventTime) // unchanged

	// 5. scheduled → measures interval, updates timer
	now5 := now4.Add(1 * time.Second)
	trackSchedulingEventInterval(config, wr, RecordTypeScheduled, now5, "")
	assert.Equal(t, now5, *wr.lastSchedulingEventTime)

	// 6. gatedByQueueAdmission → clears timer
	trackSchedulingEventInterval(config, wr, RecordTypeGatedByQueueAdmission, now5.Add(1*time.Second), "")
	assert.Nil(t, wr.lastSchedulingEventTime)

	// 7. admissionPassed → new round, sets timer
	now7 := now5.Add(10 * time.Second)
	trackSchedulingEventInterval(config, wr, RecordTypeAdmissionPassed, now7, "")
	assert.Equal(t, now7, *wr.lastSchedulingEventTime)
}

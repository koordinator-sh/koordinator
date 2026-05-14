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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestMain(m *testing.M) {
	InitMetrics([]string{"priority"})
	os.Exit(m.Run())
}

func newTestConfig() WorkloadAuditorConfig {
	return WorkloadAuditorConfig{
		Enabled:                  true,
		VictimRescheduleDuration: 10 * time.Second,
		VictimDeletionDuration:   30 * time.Second,
		VictimDeletingRetries:    3,
		SchedulingEventInterval:  5 * time.Minute,
		MetricLabelNames:         []string{"priority"},
	}
}

func newTestAuditor() *workloadAuditorImpl {
	return &workloadAuditorImpl{Config: newTestConfig()}
}

func newPod(ns, name, uid string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
			UID:       types.UID(uid),
		},
	}
}

func newGangPod(ns, name, uid string) *corev1.Pod {
	pod := newPod(ns, name, uid)
	pod.Annotations = map[string]string{extension.AnnotationGangName: "g"}
	return pod
}

// --- GetPodKey ---

func TestGetPodKey(t *testing.T) {
	pod := newPod("ns", "name", "uid1")
	assert.Equal(t, "ns/name/uid1", GetPodKey(pod))
}

// --- AddPod ---

func TestAddPod(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	record := w.getRecord("ns/p1/u1")
	assert.NotNil(t, record)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeCreate])
	assert.True(t, record.labelsExtracted)
	assert.False(t, record.gated)
}

func TestAddPod_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.AddPod(newPod("ns", "p1", "u1"))
	assert.Nil(t, w.getRecord("ns/p1/u1"))
}

func TestAddPod_GangSkipped(t *testing.T) {
	w := newTestAuditor()
	w.AddPod(newGangPod("ns", "p1", "u1"))
	assert.Nil(t, w.getRecord("ns/p1/u1"))
}

func TestAddPod_Duplicate(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.AddPod(pod) // no-op
	record := w.getRecord("ns/p1/u1")
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeCreate])
}

func TestAddPod_Gated(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	pod.Spec.SchedulingGates = []corev1.PodSchedulingGate{{Name: "test"}}
	w.AddPod(pod)
	record := w.getRecord("ns/p1/u1")
	assert.True(t, record.gated)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeGatedByQueueAdmission])
}

// --- DeletePod ---

func TestDeletePod(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.DeletePod(pod)
	assert.Nil(t, w.getRecord("ns/p1/u1"))
}

func TestDeletePod_ScheduledAlsoDeleted(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	pod.Spec.NodeName = "node1"
	w.DeletePod(pod)
	// NodeName guard removed; FilteringResourceEventHandler handles filtering.
	assert.Nil(t, w.getRecord("ns/p1/u1"))
}

func TestDeletePod_GangSkipped(t *testing.T) {
	w := newTestAuditor()
	w.DeletePod(newGangPod("ns", "p1", "u1")) // no panic
}

func TestDeletePod_Disabled(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.Config.Enabled = false
	w.DeletePod(pod)
	assert.NotNil(t, w.getRecord("ns/p1/u1"))
}

func TestDeletePod_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.DeletePod(newPod("ns", "p1", "u1")) // no panic
}

// --- RecordAttemptPod ---

func TestRecordAttemptPod(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordAttemptPod(pod)
	w.RecordAttemptPod(pod)
	record := w.getRecord("ns/p1/u1")
	assert.Equal(t, 2, record.Attempts)
}

func TestRecordAttemptPod_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordAttemptPod(newPod("ns", "p1", "u1")) // no panic
}

func TestRecordAttemptPod_GangSkipped(t *testing.T) {
	w := newTestAuditor()
	w.RecordAttemptPod(newGangPod("ns", "p1", "u1")) // no panic
}

func TestRecordAttemptPod_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordAttemptPod(newPod("ns", "p1", "u1")) // no panic
}

// --- RecordPodScheduleResult ---

func TestRecordPodScheduleResult_Failure(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordAttemptPod(pod)
	w.RecordPodScheduleResult(pod, RecordTypeScheduleFailure, "no fit")
	record := w.getRecord("ns/p1/u1")
	assert.NotNil(t, record)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeScheduleFailure])
}

func TestRecordPodScheduleResult_FailureDedup(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordAttemptPod(pod)
	w.RecordPodScheduleResult(pod, RecordTypeScheduleFailure, "no fit")
	w.RecordPodScheduleResult(pod, RecordTypeScheduleFailure, "no fit2")
	record := w.getRecord("ns/p1/u1")
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeScheduleFailure])
}

func TestRecordPodScheduleResult_FailureNewAttempt(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordAttemptPod(pod)
	w.RecordPodScheduleResult(pod, RecordTypeScheduleFailure, "no fit")
	// New attempt allows new failure
	w.RecordAttemptPod(pod)
	w.RecordPodScheduleResult(pod, RecordTypeScheduleFailure, "no fit2")
	record := w.getRecord("ns/p1/u1")
	assert.Equal(t, 2, record.recordTypeCounts[RecordTypeScheduleFailure])
}

func TestRecordPodScheduleResult_Scheduled(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordAttemptPod(pod)
	w.RecordPodScheduleResult(pod, RecordTypeScheduled, "")
	assert.Nil(t, w.getRecord("ns/p1/u1"))
}

func TestRecordPodScheduleResult_PreemptNominated(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordAttemptPod(pod)
	w.RecordPodScheduleResult(pod, RecordTypePreemptNominated, "preempting node1")
	record := w.getRecord("ns/p1/u1")
	assert.NotNil(t, record)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypePreemptNominated])
}

func TestRecordPodScheduleResult_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordPodScheduleResult(newPod("ns", "p1", "u1"), RecordTypeScheduled, "") // no panic
}

func TestRecordPodScheduleResult_GangSkipped(t *testing.T) {
	w := newTestAuditor()
	w.RecordPodScheduleResult(newGangPod("ns", "p1", "u1"), RecordTypeScheduled, "") // no panic
}

func TestRecordPodScheduleResult_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordPodScheduleResult(newPod("ns", "p1", "u1"), RecordTypeScheduled, "") // no panic
}

// --- RecordPodGating ---

func TestRecordPodGating_StateChange(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod) // not gated

	w.RecordPodGating(pod, true)
	record := w.getRecord("ns/p1/u1")
	assert.True(t, record.gated)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeGatedByQueueAdmission])

	w.RecordPodGating(pod, false)
	assert.False(t, record.gated)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeAdmissionPassed])
}

func TestRecordPodGating_NoChange(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordPodGating(pod, false) // same state
	record := w.getRecord("ns/p1/u1")
	assert.Equal(t, 0, record.recordTypeCounts[RecordTypeAdmissionPassed])
}

func TestRecordPodGating_GangSkipped(t *testing.T) {
	w := newTestAuditor()
	w.RecordPodGating(newGangPod("ns", "p1", "u1"), true)
}

func TestRecordPodGating_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordPodGating(newPod("ns", "p1", "u1"), true)
}

func TestRecordPodGating_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordPodGating(newPod("ns", "p1", "u1"), true)
}

// --- Gang Group methods ---

func TestAddGangGroup(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	record := w.getRecord("gang-1")
	assert.NotNil(t, record)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeCreate])
	assert.False(t, record.labelsExtracted)
}

func TestAddGangGroup_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.AddGangGroup("gang-1")
	assert.Nil(t, w.getRecord("gang-1"))
}

func TestAddGangGroup_Duplicate(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	w.AddGangGroup("gang-1")
	record := w.getRecord("gang-1")
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeCreate])
}

func TestDeleteGangGroup(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	w.DeleteGangGroup("gang-1")
	assert.Nil(t, w.getRecord("gang-1"))
}

func TestDeleteGangGroup_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	w.Config.Enabled = false
	w.DeleteGangGroup("gang-1")
	assert.NotNil(t, w.getRecord("gang-1"))
}

func TestDeleteGangGroup_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.DeleteGangGroup("nonexist") // no panic
}

func TestRecordGangGroup(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	pod := newPod("ns", "p1", "u1")
	w.RecordGangGroup("gang-1", pod, RecordTypeGangMinMemberSatisfied, "test")
	record := w.getRecord("gang-1")
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeGangMinMemberSatisfied])
	assert.True(t, record.labelsExtracted)
}

func TestRecordGangGroup_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordGangGroup("gang-1", newPod("ns", "p1", "u1"), RecordTypeGangMinMemberSatisfied, "")
}

func TestRecordGangGroup_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordGangGroup("nonexist", newPod("ns", "p1", "u1"), RecordTypeGangMinMemberSatisfied, "")
}

func TestRecordGangGating(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	pod := newPod("ns", "p1", "u1")

	w.RecordGangGating("gang-1", pod, true)
	record := w.getRecord("gang-1")
	assert.True(t, record.gated)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeGatedByQueueAdmission])
	assert.True(t, record.labelsExtracted)

	w.RecordGangGating("gang-1", pod, false)
	assert.False(t, record.gated)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeAdmissionPassed])
}

func TestRecordGangGating_NoChange(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	w.RecordGangGating("gang-1", newPod("ns", "p1", "u1"), false)
	record := w.getRecord("gang-1")
	assert.Equal(t, 0, record.recordTypeCounts[RecordTypeAdmissionPassed])
}

func TestRecordGangGating_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordGangGating("gang-1", newPod("ns", "p1", "u1"), true)
}

func TestRecordGangGating_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordGangGating("nonexist", newPod("ns", "p1", "u1"), true)
}

func TestRecordGangScheduleResult_Scheduled(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	w.RecordGangScheduleResult("gang-1", RecordTypeScheduleFailure, "no fit")
	w.RecordGangScheduleResult("gang-1", RecordTypeScheduled, "")
	// Record is deleted on scheduled.
	assert.Nil(t, w.getRecord("gang-1"))
}

func TestRecordGangScheduleResult_Failure(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-1")
	w.RecordGangScheduleResult("gang-1", RecordTypeScheduleFailure, "")
	record := w.getRecord("gang-1")
	assert.NotNil(t, record)
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeScheduleFailure])
}

func TestRecordGangScheduleResult_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordGangScheduleResult("gang-1", RecordTypeScheduled, "")
}

func TestRecordGangScheduleResult_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordGangScheduleResult("nonexist", RecordTypeScheduled, "")
}

// --- RecordDiagnosis ---

func TestRecordDiagnosis_NonGang(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordAttemptPod(pod)
	w.RecordDiagnosis(pod, "question-1", RecordTypeScheduleFailure, "filter failed")
	record := w.getRecord("ns/p1/u1")
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeScheduleFailure])
	assert.Equal(t, 1, record.lastRecordedAttempt)
}

func TestRecordDiagnosis_Gang(t *testing.T) {
	w := newTestAuditor()
	w.AddGangGroup("gang-key")
	pod := newGangPod("ns", "p1", "u1")
	w.RecordDiagnosis(pod, "gang-key", RecordTypeScheduleFailure, "")
	record := w.getRecord("gang-key")
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypeScheduleFailure])
}

func TestRecordDiagnosis_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordDiagnosis(newPod("ns", "p1", "u1"), "q", RecordTypeScheduleFailure, "")
}

func TestRecordDiagnosis_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordDiagnosis(newPod("ns", "p1", "u1"), "q", RecordTypeScheduleFailure, "")
}

// --- RecordPod ---

func TestRecordPod(t *testing.T) {
	w := newTestAuditor()
	pod := newPod("ns", "p1", "u1")
	w.AddPod(pod)
	w.RecordPod(pod, RecordTypePreemptVictimDeleting, "victim deleting")
	record := w.getRecord("ns/p1/u1")
	assert.Equal(t, 1, record.recordTypeCounts[RecordTypePreemptVictimDeleting])
}

func TestRecordPod_GangSkipped(t *testing.T) {
	w := newTestAuditor()
	w.RecordPod(newGangPod("ns", "p1", "u1"), RecordTypePreemptVictimDeleting, "")
}

func TestRecordPod_Disabled(t *testing.T) {
	w := newTestAuditor()
	w.Config.Enabled = false
	w.RecordPod(newPod("ns", "p1", "u1"), RecordTypePreemptVictimDeleting, "")
}

func TestRecordPod_NotFound(t *testing.T) {
	w := newTestAuditor()
	w.RecordPod(newPod("ns", "p1", "u1"), RecordTypePreemptVictimDeleting, "")
}

// --- tryExtractLabels ---

func TestTryExtractLabels_Idempotent(t *testing.T) {
	w := newTestAuditor()
	wr := &WorkloadRecord{labelValues: make([]string, 1)}
	pod := newPod("ns", "p1", "u1")

	w.tryExtractLabels(wr, pod)
	assert.True(t, wr.labelsExtracted)

	wr.labelValues[0] = "modified"
	w.tryExtractLabels(wr, pod) // no-op
	assert.Equal(t, "modified", wr.labelValues[0])
}

func TestTryExtractLabels_NilPod(t *testing.T) {
	w := newTestAuditor()
	wr := &WorkloadRecord{labelValues: make([]string, 1)}
	w.tryExtractLabels(wr, nil)
	assert.False(t, wr.labelsExtracted)
}

// --- extractLabels ---

func TestExtractLabels(t *testing.T) {
	w := &workloadAuditorImpl{
		Config: WorkloadAuditorConfig{
			MetricLabelNames: []string{"priority", "gpu"},
			PodLabelKeys:     []string{"hw.gpu"},
		},
	}
	pod := newPod("ns", "p1", "u1")
	pod.Labels = map[string]string{"hw.gpu": "A100"}
	vals := w.extractLabels(pod)
	assert.Equal(t, 2, len(vals))
	assert.Equal(t, "A100", vals[1])
}

// --- formatLabelDetail ---

func TestFormatLabelDetail(t *testing.T) {
	tests := []struct {
		names  []string
		values []string
		want   string
	}{
		{nil, nil, "{}"},
		{[]string{"priority"}, []string{"koord-batch"}, "{koord-batch}"},
		{[]string{"priority", "gpu"}, []string{"koord-prod", "A100"}, "{koord-prod, gpu=A100}"},
		{[]string{"priority", "gpu", "tenant"}, []string{"koord-prod", "A100", "team-a"},
			"{koord-prod, gpu=A100, tenant=team-a}"},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			assert.Equal(t, tt.want, formatLabelDetail(tt.names, tt.values))
		})
	}
}

// --- DeleteMetricsByLabel ---

func TestDeleteMetricsByLabel_ConfiguredLabel(t *testing.T) {
	// "priority" was configured in TestMain via InitMetrics
	DeleteMetricsByLabel("priority", "koord-batch") // should not panic
}

func TestDeleteMetricsByLabel_UnconfiguredLabel(t *testing.T) {
	DeleteMetricsByLabel("nonexist", "value") // no-op, no panic
}

// --- Enabled ---

func TestEnabled_True(t *testing.T) {
	w := newTestAuditor()
	assert.True(t, w.Enabled())
}

func TestEnabled_False(t *testing.T) {
	w := &workloadAuditorImpl{Config: WorkloadAuditorConfig{Enabled: false}}
	assert.False(t, w.Enabled())
}

// --- NewWorkloadAuditor ---

func TestNewWorkloadAuditor(t *testing.T) {
	// Save and restore package-level vars
	oldEnabled := WorkloadAuditorEnabled
	oldLabels := WorkloadAuditorMetricLabels
	defer func() {
		WorkloadAuditorEnabled = oldEnabled
		WorkloadAuditorMetricLabels = oldLabels
	}()

	WorkloadAuditorEnabled = true
	WorkloadAuditorMetricLabels = ""
	wa := NewWorkloadAuditor()
	assert.NotNil(t, wa)
	assert.True(t, wa.Enabled())
}

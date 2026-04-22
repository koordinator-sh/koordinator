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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

type RecordType string

const (
	RecordTypeGatedByQueueAdmission RecordType = "gatedByQueueAdmission"
	RecordTypeAdmissionPassed       RecordType = "admissionPassed"

	RecordTypeCreate                      RecordType = "create"
	RecordTypeScheduled                   RecordType = "scheduled"
	RecordTypePreemptNominated            RecordType = "preemptNominated"
	RecordTypePreemptVictimDeleting       RecordType = "preemptVictimDeleting"
	RecordTypePreemptFailure              RecordType = "preemptFailure"
	RecordTypeScheduleFailure             RecordType = "scheduleFailure"
	RecordTypeGangAllPodsAlreadyAttempted RecordType = "gangAllPodsAlreadyAttempted"

	RecordTypeVictimAllDeleted       RecordType = "victimAllDeleted"
	RecordTypeGangMinMemberSatisfied RecordType = "gangMinMemberSatisfied"
)

// WorkloadRecord tracks the full lifecycle of a workload in the scheduler.
type WorkloadRecord struct {
	mu          sync.Mutex // protects all mutable fields below
	WorkloadKey string
	QuestionKey string
	Attempts    int
	// lastRecordedAttempt tracks the attempt number for which a Diagnosis or Failure
	// has already been recorded, used to deduplicate ScheduleFailure records.
	lastRecordedAttempt int

	// Preemption state tracking (for anomaly detection)
	lastPreemptNominatedInvalidated    bool
	lastPreemptNominatedTime           *time.Time
	preemptVictimDeletingCountSinceNom int
	lastVictimAllDeletedTime           *time.Time

	// Inter-event interval tracking: time of the last scheduling event
	// within the current dequeue attempt round.
	lastSchedulingEventTime *time.Time

	// recordTypeCounts tracks the total count of each RecordType over the workload's lifetime.
	recordTypeCounts map[RecordType]int

	// gated tracks the current gating state; events are only produced on state change.
	gated bool

	// labelsExtracted indicates whether labelValues/labelDetail have been populated from a pod.
	labelsExtracted bool
	// labelValues holds ordered metric label values matching config.MetricLabelNames.
	labelValues []string
	// labelDetail is a pre-formatted compact string of labels for ALERT logs.
	labelDetail string
}

func GetPodKey(pod *corev1.Pod) string {
	return fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, pod.UID)
}

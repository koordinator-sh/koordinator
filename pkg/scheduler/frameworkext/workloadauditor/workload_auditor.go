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
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

// PodIsGated determines whether a pod is considered gated.
// Other packages may override this function to customize the gating check.
var PodIsGated = func(pod *corev1.Pod) bool {
	return len(pod.Spec.SchedulingGates) > 0
}

type WorkloadAuditor interface {
	Enabled() bool
	AddPod(pod *corev1.Pod)
	DeletePod(pod *corev1.Pod)
	RecordPod(pod *corev1.Pod, recordType RecordType, message string)
	RecordPodGating(pod *corev1.Pod, gated bool)
	RecordAttemptPod(pod *corev1.Pod)
	RecordPodScheduleResult(pod *corev1.Pod, recordType RecordType, message string)

	AddGangGroup(gangGroupID string)
	DeleteGangGroup(gangGroupID string)
	RecordGangGroup(gangGroupID string, pod *corev1.Pod, recordType RecordType, message string)
	RecordGangGating(gangGroupID string, pod *corev1.Pod, gated bool)
	RecordGangScheduleResult(gangGroupID string, recordType RecordType, message string)

	RecordDiagnosis(pod *corev1.Pod, questionKey string, recordType RecordType, message string)
}

// workloadAuditorImpl tracks the scheduling lifecycle of workloads.
// It is a singleton at the FrameworkExtenderFactory level.
// records is a sync.Map for lock-free reads; each WorkloadRecord has its own mu
// for field-level operations, so different workloads never contend.
type workloadAuditorImpl struct {
	records sync.Map // workloadKey -> *WorkloadRecord
	Config  WorkloadAuditorConfig
}

func (w *workloadAuditorImpl) Enabled() bool {
	return w.Config.Enabled
}

// getRecord returns the WorkloadRecord for the given key, or nil if not found.
func (w *workloadAuditorImpl) getRecord(key string) *WorkloadRecord {
	val, ok := w.records.Load(key)
	if !ok {
		return nil
	}
	return val.(*WorkloadRecord)
}

func (w *workloadAuditorImpl) AddGangGroup(gangGroupID string) {
	if !w.Config.Enabled {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("AddGangGroup").Observe(time.Since(start).Seconds())
	}()
	record := &WorkloadRecord{
		WorkloadKey:      gangGroupID,
		recordTypeCounts: map[RecordType]int{RecordTypeCreate: 1},
		labelValues:      make([]string, len(w.Config.MetricLabelNames)),
		labelDetail:      formatLabelDetail(w.Config.MetricLabelNames, make([]string, len(w.Config.MetricLabelNames))),
	}
	w.records.LoadOrStore(gangGroupID, record)
}

func (w *workloadAuditorImpl) DeleteGangGroup(gangGroupID string) {
	if !w.Config.Enabled {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("DeleteGangGroup").Observe(time.Since(start).Seconds())
	}()
	val, ok := w.records.LoadAndDelete(gangGroupID)
	if !ok {
		return
	}
	record := val.(*WorkloadRecord)
	record.mu.Lock()
	defer record.mu.Unlock()
	klog.V(4).Infof("WorkloadAuditor delete(gangDeleted): workloadKey=%s %s, attempts=%d",
		record.WorkloadKey, record.labelDetail, record.Attempts)
	finalizeWorkloadRecord(record, outcomeGangDeleted)
}

func (w *workloadAuditorImpl) RecordGangGroup(gangGroupID string, pod *corev1.Pod, recordType RecordType, message string) {
	if !w.Config.Enabled {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordGangGroup").Observe(time.Since(start).Seconds())
	}()
	record := w.getRecord(gangGroupID)
	if record == nil {
		return
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	w.tryExtractLabels(record, pod)
	w.appendRecord(record, recordType, message)
}

func (w *workloadAuditorImpl) RecordDiagnosis(pod *corev1.Pod, questionKey string, recordType RecordType, message string) {
	if !w.Config.Enabled {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordDiagnosis").Observe(time.Since(start).Seconds())
	}()
	workloadKey := questionKey
	if !extension.IsGangPod(pod) {
		workloadKey = GetPodKey(pod)
	}
	record := w.getRecord(workloadKey)
	if record == nil {
		return
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	w.tryExtractLabels(record, pod)
	record.lastRecordedAttempt = record.Attempts
	w.appendRecord(record, recordType, message)
}

func (w *workloadAuditorImpl) AddPod(pod *corev1.Pod) {
	if !w.Config.Enabled {
		return
	}
	if extension.IsGangPod(pod) {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("AddPod").Observe(time.Since(start).Seconds())
	}()
	workloadKey := GetPodKey(pod)
	labelValues := w.extractLabels(pod)
	gated := PodIsGated(pod)

	record := &WorkloadRecord{
		WorkloadKey:      workloadKey,
		recordTypeCounts: map[RecordType]int{RecordTypeCreate: 1},
		gated:            gated,
		labelsExtracted:  true,
		labelValues:      labelValues,
		labelDetail:      formatLabelDetail(w.Config.MetricLabelNames, labelValues),
	}
	if _, loaded := w.records.LoadOrStore(workloadKey, record); loaded {
		return
	}

	if gated {
		record.mu.Lock()
		w.appendRecord(record, RecordTypeGatedByQueueAdmission, "")
		record.mu.Unlock()
	}
}

func (w *workloadAuditorImpl) RecordPod(pod *corev1.Pod, recordType RecordType, message string) {
	if !w.Config.Enabled {
		return
	}
	if extension.IsGangPod(pod) {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordPod").Observe(time.Since(start).Seconds())
	}()
	record := w.getRecord(GetPodKey(pod))
	if record == nil {
		return
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	w.appendRecord(record, recordType, message)
}

func (w *workloadAuditorImpl) RecordPodGating(pod *corev1.Pod, gated bool) {
	if !w.Config.Enabled {
		return
	}
	if extension.IsGangPod(pod) {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordPodGating").Observe(time.Since(start).Seconds())
	}()
	record := w.getRecord(GetPodKey(pod))
	if record == nil {
		return
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.gated == gated {
		return
	}
	record.gated = gated
	recordType := RecordTypeAdmissionPassed
	if gated {
		recordType = RecordTypeGatedByQueueAdmission
	}
	w.appendRecord(record, recordType, "")
}

func (w *workloadAuditorImpl) RecordGangGating(gangGroupID string, pod *corev1.Pod, gated bool) {
	if !w.Config.Enabled {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordGangGating").Observe(time.Since(start).Seconds())
	}()
	record := w.getRecord(gangGroupID)
	if record == nil {
		return
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	w.tryExtractLabels(record, pod)
	if record.gated == gated {
		return
	}
	record.gated = gated
	recordType := RecordTypeAdmissionPassed
	if gated {
		recordType = RecordTypeGatedByQueueAdmission
	}
	w.appendRecord(record, recordType, "")
}

func (w *workloadAuditorImpl) DeletePod(pod *corev1.Pod) {
	if !w.Config.Enabled {
		return
	}
	if extension.IsGangPod(pod) {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("DeletePod").Observe(time.Since(start).Seconds())
	}()
	workloadKey := GetPodKey(pod)
	val, ok := w.records.LoadAndDelete(workloadKey)
	if !ok {
		return
	}
	record := val.(*WorkloadRecord)
	record.mu.Lock()
	defer record.mu.Unlock()
	klog.V(4).Infof("WorkloadAuditor delete(podDeleted): workloadKey=%s %s, attempts=%d",
		record.WorkloadKey, record.labelDetail, record.Attempts)
	finalizeWorkloadRecord(record, outcomeDeleted)
}

func (w *workloadAuditorImpl) RecordAttemptPod(pod *corev1.Pod) {
	if !w.Config.Enabled {
		return
	}
	if extension.IsGangPod(pod) {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordAttemptPod").Observe(time.Since(start).Seconds())
	}()
	record := w.getRecord(GetPodKey(pod))
	if record == nil {
		return
	}
	record.mu.Lock()
	record.Attempts++
	record.mu.Unlock()
}

func (w *workloadAuditorImpl) RecordPodScheduleResult(pod *corev1.Pod, recordType RecordType, message string) {
	if !w.Config.Enabled {
		return
	}
	if extension.IsGangPod(pod) {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordPodScheduleResult").Observe(time.Since(start).Seconds())
	}()
	workloadKey := GetPodKey(pod)
	record := w.getRecord(workloadKey)
	if record == nil {
		return
	}
	record.mu.Lock()
	// When recording a ScheduleFailure, skip if a Diagnosis or other Failure
	// has already been recorded for the current attempt.
	if recordType == RecordTypeScheduleFailure && record.lastRecordedAttempt >= record.Attempts {
		record.mu.Unlock()
		return
	}
	record.lastRecordedAttempt = record.Attempts
	w.appendRecord(record, recordType, message)
	// On successful schedule, remove the workload record.
	if recordType == RecordTypeScheduled {
		klog.V(4).Infof("WorkloadAuditor delete(podScheduled): workloadKey=%s %s, attempts=%d",
			record.WorkloadKey, record.labelDetail, record.Attempts)
		finalizeWorkloadRecord(record, outcomeScheduled)
		record.mu.Unlock()
		w.records.Delete(workloadKey)
		return
	}
	record.mu.Unlock()
}

func (w *workloadAuditorImpl) RecordGangScheduleResult(gangKey string, recordType RecordType, message string) {
	if !w.Config.Enabled {
		return
	}
	start := time.Now()
	defer func() {
		RecordMethodDurationSeconds.WithLabelValues("RecordGangScheduleResult").Observe(time.Since(start).Seconds())
	}()
	record := w.getRecord(gangKey)
	if record == nil {
		return
	}
	record.mu.Lock()
	w.appendRecord(record, recordType, message)
	// On successful gang schedule, remove the gang record.
	if recordType == RecordTypeScheduled {
		klog.V(4).Infof("WorkloadAuditor delete(gangScheduled): workloadKey=%s %s, attempts=%d",
			record.WorkloadKey, record.labelDetail, record.Attempts)
		finalizeWorkloadRecord(record, outcomeGangScheduled)
		record.mu.Unlock()
		w.records.Delete(gangKey)
		return
	}
	record.mu.Unlock()
}

// appendRecord increments the record-type count, logs the event, and runs anomaly detection.
func (w *workloadAuditorImpl) appendRecord(wr *WorkloadRecord, recordType RecordType, message string) {
	wr.recordTypeCounts[recordType]++
	klog.V(4).Infof("WorkloadAuditor record: workloadKey=%s %s, type=%s, message=%q, attempts=%d",
		wr.WorkloadKey, wr.labelDetail, recordType, message, wr.Attempts)
	checkRecordAnomaly(&w.Config, wr, recordType, message)
}

// NewWorkloadAuditor creates a new workloadAuditorImpl reading config from package-level vars.
func NewWorkloadAuditor() WorkloadAuditor {
	config := DefaultWorkloadAuditorConfig()
	InitMetrics(config.MetricLabelNames)
	return &workloadAuditorImpl{
		Config: config,
	}
}

// tryExtractLabels populates the WorkloadRecord's label values from the given pod,
// but only on the first call with a non-nil pod.
func (w *workloadAuditorImpl) tryExtractLabels(wr *WorkloadRecord, pod *corev1.Pod) {
	if wr.labelsExtracted || pod == nil {
		return
	}
	wr.labelsExtracted = true
	wr.labelValues = w.extractLabels(pod)
	wr.labelDetail = formatLabelDetail(w.Config.MetricLabelNames, wr.labelValues)
}

// extractLabels reads metric label values from a pod.
// The first value is always the priority class; subsequent values come from pod labels
// as configured by PodLabelKeys.
func (w *workloadAuditorImpl) extractLabels(pod *corev1.Pod) []string {
	vals := make([]string, len(w.Config.MetricLabelNames))
	vals[0] = string(extension.GetPodPriorityClassRaw(pod))
	for i, podLabelKey := range w.Config.PodLabelKeys {
		vals[i+1] = pod.Labels[podLabelKey]
	}
	return vals
}

// formatLabelDetail returns a compact string of label values for ALERT logs.
func formatLabelDetail(names []string, values []string) string {
	if len(names) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		if i == 0 {
			// priority printed as bare value
			b.WriteString(values[i])
		} else {
			fmt.Fprintf(&b, "%s=%s", name, values[i])
		}
	}
	b.WriteByte('}')
	return b.String()
}

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

// Package framework holds the generic job/pod request and result types consumed by the reusable
// batch scheduling engine (pkg/scheduler/batch). These types are intentionally free of any
// dependency on the engine's callers so that the shared engine can be built on its own; callers
// may re-export these types via aliases.
package framework

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	fwktype "k8s.io/kube-scheduler/framework"
)

type JobRequest struct {
	Version       int64
	SchedulerName string
	Namespace     string
	JobName       string
	MinMember     int

	Active   bool
	Requeued bool

	PodsByNode map[string]map[string]PodRequest // node name -> pod requests
}

func (j *JobRequest) Clone() *JobRequest {
	r := &JobRequest{
		Version:       j.Version,
		SchedulerName: j.SchedulerName,
		Namespace:     j.Namespace,
		JobName:       j.JobName,
		Active:        j.Active,
		MinMember:     j.MinMember,
		Requeued:      j.Requeued,
	}
	podsByNode := make(map[string]map[string]PodRequest, len(j.PodsByNode))
	for node, pods := range j.PodsByNode {
		if pods == nil {
			podsByNode[node] = nil
			continue
		}
		podsByNode[node] = make(map[string]PodRequest, len(pods))
		for i, podRequest := range pods {
			podsByNode[node][i] = *podRequest.Clone()
		}
	}
	r.PodsByNode = podsByNode
	return r
}

func (j *JobRequest) String() string {
	if j == nil {
		return ""
	}
	return j.Namespace + "/" + j.JobName
}

func (j *JobRequest) ID() string {
	if j == nil {
		return ""
	}
	return j.Namespace + "/" + j.JobName
}

// Sub subtracts pods from the job request.
func (j *JobRequest) Sub(req *JobRequest) bool {
	updated := false
	for node, pods := range j.PodsByNode {
		if len(pods) <= 0 {
			continue
		}
		if req.PodsByNode[node] == nil {
			delete(j.PodsByNode, node)
			updated = true
			continue
		}
		curPodsOnNode := j.PodsByNode[node]
		for podKey := range curPodsOnNode {
			if _, ok := req.PodsByNode[node][podKey]; ok {
				delete(curPodsOnNode, podKey)
				updated = true
			}
		}
		if len(curPodsOnNode) <= 0 {
			delete(j.PodsByNode, node)
		}
	}
	return updated
}

// Merge adds pods to the job request.
func (j *JobRequest) Merge(req *JobRequest) bool {
	updated := false
	if req.MinMember > j.MinMember {
		j.MinMember = req.MinMember
		updated = true
	}
	if !j.Active && req.Active {
		j.Active = req.Active
		updated = true
	}
	for node, pods := range req.PodsByNode {
		if len(pods) <= 0 {
			continue
		}
		if j.PodsByNode[node] == nil {
			j.PodsByNode[node] = pods
			updated = true
			continue
		}
		for podKey, podRequest := range pods {
			if _, ok := j.PodsByNode[node][podKey]; !ok {
				j.PodsByNode[node][podKey] = podRequest
				updated = true // mark job request as updated if a new pod is added
			} else {
				j.PodsByNode[node][podKey] = podRequest
			}
		}
	}
	return updated
}

func (j *JobRequest) Update(req *JobRequest) bool {
	if j.Namespace != req.Namespace || j.JobName != req.JobName { // not a same request
		return false
	}
	if j.SchedulerName != req.SchedulerName {
		return false
	}
	if req.Version < j.Version { // ignore older version
		return false
	}
	if req.Version > j.Version { // a new version will invalidate the old one.
		*j = *req.Clone()
		return true
	}
	// merge with same version
	return j.Merge(req)
}

func (j *JobRequest) IsActive() bool {
	if j.Active {
		return j.Active
	}
	count := 0
	for _, pods := range j.PodsByNode {
		count += len(pods)
	}
	if count >= j.MinMember {
		j.Active = true
	}
	return j.Active
}

func (j *JobRequest) IsEmpty() bool {
	return len(j.PodsByNode) <= 0
}

type PodRequest struct {
	NodeName string
	Pod      *corev1.Pod // read-only
}

func (p *PodRequest) Clone() *PodRequest {
	return &PodRequest{
		NodeName: p.NodeName,
		Pod:      p.Pod,
	}
}

func (p *PodRequest) ID() string {
	if p == nil || p.Pod == nil {
		return ""
	}
	return p.Pod.Namespace + "/" + p.Pod.Name + "/" + p.NodeName
}

func (p *PodRequest) String() string {
	if p == nil {
		return ""
	}
	return p.Pod.Namespace + "/" + p.Pod.Name + "/" + p.NodeName
}

func (p *PodRequest) IsUpdated(podRequest *PodRequest) bool {
	if p == nil || podRequest == nil {
		return false
	}
	if p.NodeName == podRequest.NodeName &&
		p.Pod != nil && podRequest.Pod != nil && p.Pod.ResourceVersion == podRequest.Pod.ResourceVersion {
		return false
	}
	return true
}

type JobResult struct {
	lock        sync.RWMutex
	Version     int64
	Status      *fwktype.Status
	PodStatuses map[string]*fwktype.Status
}

func (j *JobResult) SetPodStatus(pod *corev1.Pod, status *fwktype.Status) {
	j.lock.Lock()
	defer j.lock.Unlock()
	if j.PodStatuses == nil {
		j.PodStatuses = make(map[string]*fwktype.Status)
	}
	j.PodStatuses[pod.Namespace+"/"+pod.Name] = status
}

// PodStatus returns the recorded status for the given pod key (namespace/name), or nil if absent.
// Safe for concurrent use.
func (j *JobResult) PodStatus(key string) *fwktype.Status {
	j.lock.RLock()
	defer j.lock.RUnlock()
	return j.PodStatuses[key]
}

func (j *JobResult) Message() string {
	if j == nil {
		return ""
	}
	j.lock.RLock()
	defer j.lock.RUnlock()
	if j.Status == nil || j.Status.IsSuccess() {
		return ""
	}
	var b strings.Builder
	b.WriteString("job failed due to ")
	b.WriteString(j.Status.Message())
	b.WriteString(", pods: ")
	for podKey, status := range j.PodStatuses {
		if status != nil && (status.IsSuccess() || status.IsSkip()) {
			continue
		}
		b.WriteString(podKey)
		b.WriteString(" failed due to ")
		b.WriteString(status.Message())
		b.WriteString(", ")
	}
	return b.String()
}

// ExampleMessage returns a compact failure message for the job. Message lists every failed pod, which
// can grow very large for a big job; ExampleMessage instead reports the overall assumed/failed pod
// counts plus only a single failed pod (the one with the smallest PodKey, so the result is
// deterministic regardless of the order of podRequestsByNode) together with the pods already assumed
// on that same node. That is enough to explain and triage the failure (e.g. the node the pod targeted
// was already consumed by its assumed siblings) while keeping the message bounded.
//
// It must be called before the job's assumed pods are cleaned up: cleanup overwrites every assumed
// pod's per-pod status with the cleanup status, which would otherwise make an assumed (originally
// successful) pod look like the first failed pod.
func (j *JobResult) ExampleMessage(podRequestsByNode [][]PodRequest, assumedPods []*AssumeContext) string {
	if j == nil {
		return ""
	}
	j.lock.RLock()
	defer j.lock.RUnlock()
	if j.Status == nil || j.Status.IsSuccess() {
		return ""
	}
	var b strings.Builder
	b.WriteString("job failed due to ")
	b.WriteString(j.Status.Message())
	// Pick the failed pod with the smallest PodKey so the reported example is stable regardless of the
	// (possibly map-derived) order of podRequestsByNode and assumedPods.
	var firstFailed *PodRequest
	var firstFailedKey string
	failedCount := 0
	for i := range podRequestsByNode {
		for k := range podRequestsByNode[i] {
			req := &podRequestsByNode[i][k]
			key := GetPodKey(req.Pod)
			status := j.PodStatuses[key]
			if status == nil || status.IsSuccess() || status.IsSkip() {
				continue
			}
			failedCount++
			if firstFailed == nil || key < firstFailedKey {
				firstFailed = req
				firstFailedKey = key
			}
		}
	}
	b.WriteString(", assumed ")
	b.WriteString(strconv.Itoa(len(assumedPods)))
	b.WriteString(" pods, failed ")
	b.WriteString(strconv.Itoa(failedCount))
	b.WriteString(" pods")
	if firstFailed == nil {
		return b.String()
	}
	b.WriteString(", first failed pod ")
	b.WriteString(firstFailedKey)
	b.WriteString("@")
	b.WriteString(firstFailed.NodeName)
	b.WriteString(" failed due to ")
	b.WriteString(j.PodStatuses[firstFailedKey].Message())
	assumedOnNode := make([]string, 0, len(assumedPods))
	for _, ac := range assumedPods {
		if ac.NodeName == firstFailed.NodeName {
			assumedOnNode = append(assumedOnNode, GetPodKey(ac.Pod))
		}
	}
	if len(assumedOnNode) > 0 {
		sort.Strings(assumedOnNode)
		b.WriteString(", assumed pods on node ")
		b.WriteString(firstFailed.NodeName)
		b.WriteString(": ")
		b.WriteString(strings.Join(assumedOnNode, ", "))
	}
	return b.String()
}

type AssumeContext struct {
	Pod        *corev1.Pod
	NodeName   string
	CycleState fwktype.CycleState
}

func GetPodKey(pod *corev1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}

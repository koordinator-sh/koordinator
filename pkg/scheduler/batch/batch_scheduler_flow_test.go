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

package batch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/batch/framework"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func newExtWithScheduler(nodeNames ...string) *fakeExtender {
	return &fakeExtender{
		snapshotLister: newNodeLister(nodeNames...),
		scheduler:      frameworkext.NewFakeScheduler(),
	}
}

// ---- BatchSchedule ----

func TestBatchScheduleSuccess(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	c := newFakeCache()
	bs := NewBatchScheduler(c, nil)
	triggerPod := newPod("ns", "p1")
	plan := &frameworkext.BatchScheduleResult{
		Pods:          []*corev1.Pod{triggerPod},
		PodToNodeName: map[string]string{"ns/p1": "node-a"},
	}
	status := bs.BatchSchedule(context.Background(), ext, schedulerframework.NewCycleState(), triggerPod, plan)
	assert.Nil(t, status)
	// binding runs asynchronously
	eventuallyEqual(t, int32(1), func() int32 { return ext.postBindCalled.Load() })
}

func TestBatchScheduleValidateError(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	bs := NewBatchScheduler(newFakeCache(), nil)
	triggerPod := newPod("ns", "p1")
	// no node mapping -> empty grouping -> validation error
	plan := &frameworkext.BatchScheduleResult{
		Pods:          []*corev1.Pod{triggerPod},
		PodToNodeName: map[string]string{},
	}
	status := bs.BatchSchedule(context.Background(), ext, schedulerframework.NewCycleState(), triggerPod, plan)
	assert.NotNil(t, status)
	assert.False(t, status.IsSuccess())
}

func TestBatchScheduleSchedulingFail(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	ext.reserveStatus = fwktype.NewStatus(fwktype.Error, "reserve")
	bs := NewBatchScheduler(newFakeCache(), nil)
	triggerPod := newPod("ns", "p1")
	plan := &frameworkext.BatchScheduleResult{
		Pods:          []*corev1.Pod{triggerPod},
		PodToNodeName: map[string]string{"ns/p1": "node-a"},
	}
	status := bs.BatchSchedule(context.Background(), ext, schedulerframework.NewCycleState(), triggerPod, plan)
	assert.NotNil(t, status)
	assert.False(t, status.IsSuccess())
	// cleanup unreserves the assumed pod
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
}

func TestBatchSchedulePermitFail(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	ext.permitStatus = fwktype.NewStatus(fwktype.Unschedulable, "permit")
	bs := NewBatchScheduler(newFakeCache(), nil)
	triggerPod := newPod("ns", "p1")
	plan := &frameworkext.BatchScheduleResult{
		Pods:          []*corev1.Pod{triggerPod},
		PodToNodeName: map[string]string{"ns/p1": "node-a"},
	}
	status := bs.BatchSchedule(context.Background(), ext, schedulerframework.NewCycleState(), triggerPod, plan)
	assert.NotNil(t, status)
	assert.False(t, status.IsSuccess())
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
}

// ---- runPermit ----

func newBS() *BatchScheduler {
	return NewBatchScheduler(newFakeCache(), nil)
}

func TestRunPermitSuccess(t *testing.T) {
	ext := &fakeExtender{}
	bs := newBS()
	assumed := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	status := bs.runPermit(context.Background(), ext, assumed, &sync.Map{})
	assert.Nil(t, status)
}

func TestRunPermitWaitThenSuccess(t *testing.T) {
	ext := &fakeExtender{
		permitStatusPerPod: map[string]*fwktype.Status{
			"ns/p1": fwktype.NewStatus(fwktype.Wait),
			"ns/p2": fwktype.NewStatus(fwktype.Success),
		},
	}
	bs := newBS()
	assumed := []*framework.AssumeContext{
		assumeCtx("node-a", "ns", "p1"),
		assumeCtx("node-a", "ns", "p2"),
	}
	status := bs.runPermit(context.Background(), ext, assumed, &sync.Map{})
	assert.Nil(t, status)
}

func TestRunPermitFail(t *testing.T) {
	ext := &fakeExtender{permitStatus: fwktype.NewStatus(fwktype.Unschedulable, "permit")}
	bs := newBS()
	assumed := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	status := bs.runPermit(context.Background(), ext, assumed, &sync.Map{})
	assert.NotNil(t, status)
	assert.False(t, status.IsSuccess())
}

func TestRunPermitAllWaiting(t *testing.T) {
	ext := &fakeExtender{permitStatus: fwktype.NewStatus(fwktype.Wait)}
	bs := newBS()
	assumed := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	status := bs.runPermit(context.Background(), ext, assumed, &sync.Map{})
	assert.NotNil(t, status)
	assert.Equal(t, fwktype.Unschedulable, status.Code())
}

// ---- bindingCycleOne ----

func TestBindingCycleOneSuccess(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	bs := newBS()
	ac := assumeCtx("node-a", "ns", "p1")
	bs.bindingCycleOne(context.Background(), ext, newJobRequest(), ac, &sync.Map{}, podBindMetrics{start: time.Now()})
	assert.Equal(t, int32(1), ext.postBindCalled.Load())
	// WaitOnPermit is called once, only to clean up the framework's waitingPods map.
	assert.Equal(t, int32(1), ext.waitPermitCalls.Load())
}

// TestBindingCycleOneIgnoresWaitOnPermitFailure verifies the binding cycle calls WaitOnPermit only for
// cleanup and ignores a non-success result, so a stale/rejected permit state (e.g. a sibling's
// failure rejecting the shared gang group) cannot fail and thereby reject an otherwise bindable pod.
func TestBindingCycleOneIgnoresWaitOnPermitFailure(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	ext.waitPermitState = fwktype.NewStatus(fwktype.Unschedulable, "wait")
	bs := newBS()
	ac := assumeCtx("node-a", "ns", "p1")
	bs.bindingCycleOne(context.Background(), ext, newJobRequest(), ac, &sync.Map{}, podBindMetrics{start: time.Now()})
	assert.Equal(t, int32(1), ext.waitPermitCalls.Load())
	assert.Equal(t, int32(0), ext.unreserveCalled.Load())
	assert.Equal(t, int32(1), ext.postBindCalled.Load())
}

func TestBindingCycleOnePreBindFail(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	ext.preBindStatus = fwktype.NewStatus(fwktype.Error, "prebind")
	bs := newBS()
	ac := assumeCtx("node-a", "ns", "p1")
	bs.bindingCycleOne(context.Background(), ext, newJobRequest(), ac, &sync.Map{}, podBindMetrics{start: time.Now()})
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
}

func TestBindingCycleOneBindFail(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	ext.bindStatus = fwktype.NewStatus(fwktype.Error, "bind")
	bs := newBS()
	ac := assumeCtx("node-a", "ns", "p1")
	bs.bindingCycleOne(context.Background(), ext, newJobRequest(), ac, &sync.Map{}, podBindMetrics{start: time.Now()})
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
}

// ---- handleBindingCycleError ----

func TestHandleBindingCycleErrorWithFailureHandler(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	var fhCalled atomic.Int32
	fh := func(ctx context.Context, fwk schedulerframework.Framework, podInfo *schedulerframework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
		fhCalled.Add(1)
	}
	bs := NewBatchScheduler(newFakeCache(), fh)
	ac := assumeCtx("node-a", "ns", "p1")
	status := fwktype.NewStatus(fwktype.Unschedulable, "boom")
	bs.handleBindingCycleError(context.Background(), ext, ac, status, time.Now())
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
	assert.Equal(t, int32(1), bs.cache.(*fakeCache).forgetCalled.Load())
	assert.Equal(t, int32(1), fhCalled.Load())
}

func TestHandleBindingCycleErrorNoFailureHandler(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	bs := NewBatchScheduler(newFakeCache(), nil)
	ac := assumeCtx("node-a", "ns", "p1")
	// non-unschedulable status hits the other MoveAll branch
	status := fwktype.NewStatus(fwktype.Error, "boom")
	bs.handleBindingCycleError(context.Background(), ext, ac, status, time.Now())
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
	assert.Equal(t, int32(1), bs.cache.(*fakeCache).forgetCalled.Load())
}

func TestHandleBindingCycleErrorForgetError(t *testing.T) {
	ext := newExtWithScheduler("node-a")
	c := newFakeCache()
	c.forgetPodError = errors.New("forget failed")
	bs := NewBatchScheduler(c, nil)
	ac := assumeCtx("node-a", "ns", "p1")
	status := fwktype.NewStatus(fwktype.Unschedulable, "boom")
	bs.handleBindingCycleError(context.Background(), ext, ac, status, time.Now())
	assert.Equal(t, int32(1), c.forgetCalled.Load())
}

// ---- skipPod ----

func TestSkipPod(t *testing.T) {
	logger := klog.Background()

	// deletion timestamp
	bs := NewBatchScheduler(newFakeCache(), nil)
	deleting := newPod("ns", "p1")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	assert.True(t, bs.skipPod(logger, deleting))

	// nil cache
	bsNilCache := &BatchScheduler{}
	assert.False(t, bsNilCache.skipPod(logger, newPod("ns", "p2")))

	// cached and already scheduled
	c := newFakeCache()
	scheduled := newPod("ns", "p3")
	scheduled.Spec.NodeName = "node-a"
	_ = c.AssumePod(logger, scheduled)
	bs2 := NewBatchScheduler(c, nil)
	assert.True(t, bs2.skipPod(logger, newPod("ns", "p3")))

	// not scheduled / not in cache
	assert.False(t, bs2.skipPod(logger, newPod("ns", "p4")))
}

// ---- ExampleMessage ----

func TestExampleMessage(t *testing.T) {
	reqs := [][]framework.PodRequest{{
		nodePodRequest("node-a", "ns", "p1"),
		nodePodRequest("node-a", "ns", "p2"),
	}}
	jobResult := &framework.JobResult{Status: fwktype.NewStatus(fwktype.Unschedulable, "job unschedulable")}
	jobResult.SetPodStatus(newPod("ns", "p1"), fwktype.NewStatus(fwktype.Success))
	jobResult.SetPodStatus(newPod("ns", "p2"), fwktype.NewStatus(fwktype.Unschedulable, "failed p2"))
	assumed := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	got := jobResult.ExampleMessage(reqs, assumed)
	// reports the first failed pod on its node and the pods already assumed on that node.
	assert.Contains(t, got, "first failed pod ns/p2@node-a")
	assert.Contains(t, got, "failed p2")
	assert.Contains(t, got, "assumed pods on node node-a: ns/p1")
	// does not enumerate every failed pod like Message does.
	assert.NotContains(t, got, "ns/p1 failed due to")

	// all success -> only the job-level status, no first-failed-pod section.
	jobResult2 := &framework.JobResult{Status: fwktype.NewStatus(fwktype.Unschedulable, "job unschedulable")}
	jobResult2.SetPodStatus(newPod("ns", "p1"), fwktype.NewStatus(fwktype.Success))
	got2 := jobResult2.ExampleMessage([][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}, nil)
	assert.NotContains(t, got2, "first failed pod")
}

// ---- cleanup ----

func TestCleanupEmpty(t *testing.T) {
	ext := &fakeExtender{}
	bs := newBS()
	bs.cleanup(context.Background(), klog.Background(), ext.Parallelizer(), ext,
		newJobRequest(), &framework.JobResult{}, nil, &sync.Map{}, fwktype.NewStatus(fwktype.Unschedulable, "reason"), "reason")
	assert.Equal(t, int32(0), ext.unreserveCalled.Load())
}

func TestCleanupNonEmpty(t *testing.T) {
	ext := &fakeExtender{}
	c := newFakeCache()
	bs := NewBatchScheduler(c, nil)
	assumed := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	bs.cleanup(context.Background(), klog.Background(), ext.Parallelizer(), ext,
		newJobRequest(), &framework.JobResult{}, assumed, &sync.Map{}, fwktype.NewStatus(fwktype.Unschedulable, "reason"), "reason")
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
	assert.Equal(t, int32(1), c.forgetCalled.Load())
}

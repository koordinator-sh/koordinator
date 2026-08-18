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

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
)

const (
	testBindPluginName   = "SandboxTestBind"
	testPermitPluginName = "SandboxTestPermit"
)

type testBindPlugin struct {
	status  *fwktype.Status
	bound   chan<- string
	started chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (p *testBindPlugin) Name() string {
	return testBindPluginName
}

func (p *testBindPlugin) Bind(_ context.Context, _ fwktype.CycleState, pod *corev1.Pod, _ string) *fwktype.Status {
	p.once.Do(func() {
		if p.started != nil {
			p.started <- struct{}{}
		}
	})
	if p.release != nil {
		<-p.release
	}
	if p.bound != nil {
		p.bound <- pod.Name
	}
	return p.status
}

type thresholdPermitPlugin struct {
	handle    fwktype.Handle
	threshold int
	timeout   time.Duration

	lock  sync.Mutex
	calls int
}

func (p *thresholdPermitPlugin) Name() string {
	return testPermitPluginName
}

func (p *thresholdPermitPlugin) Permit(_ context.Context, _ fwktype.CycleState, _ *corev1.Pod, _ string) (*fwktype.Status, time.Duration) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.calls++
	if p.calls < p.threshold {
		return fwktype.NewStatus(fwktype.Wait), p.timeout
	}
	p.handle.IterateOverWaitingPods(func(waitingPod fwktype.WaitingPod) {
		waitingPod.Allow(p.Name())
	})
	return nil, 0
}

func (p *thresholdPermitPlugin) callCount() int {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.calls
}

type namedWaitPermitPlugin struct {
	podName string
	timeout time.Duration
	called  chan<- string
}

func (p *namedWaitPermitPlugin) Name() string {
	return testPermitPluginName
}

func (p *namedWaitPermitPlugin) Permit(_ context.Context, _ fwktype.CycleState, pod *corev1.Pod, _ string) (*fwktype.Status, time.Duration) {
	if pod.Name != p.podName {
		return nil, 0
	}
	if p.called != nil {
		p.called <- pod.Name
	}
	return fwktype.NewStatus(fwktype.Wait), p.timeout
}

type bindingWorkflowTestHarness struct {
	workflow *SandboxCustomWorkflow
	sched    *scheduler.Scheduler
	queue    internalqueue.SchedulingQueue
	logger   klog.Logger
	recorder *events.FakeRecorder
}

func newBindingWorkflowTestHarness(
	t *testing.T,
	ctx context.Context,
	maxConcurrentBindings int,
	registerPlugins ...schedulertesting.RegisterPluginFunc,
) *bindingWorkflowTestHarness {
	t.Helper()

	logger, _ := ktesting.NewTestContext(t)
	metrics.Register()
	queue := internalqueue.NewTestQueue(ctx, (&queuesort.PrioritySort{}).Less)
	plugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}
	plugins = append(plugins, registerPlugins...)
	recorder := events.NewFakeRecorder(100)
	fwk, err := schedulertesting.NewFramework(
		ctx,
		plugins,
		"koord-scheduler",
		frameworkruntime.WithEventRecorder(recorder),
		frameworkruntime.WithPodNominator(queue),
		frameworkruntime.WithWaitingPods(frameworkruntime.NewWaitingPodsMap()),
	)
	require.NoError(t, err)

	schedulerCache := cache.New(ctx, 30*time.Second, nil)
	schedulerCache.AddNode(logger, makeNode("node-1", "1000", "1Ti"))
	sched := &scheduler.Scheduler{
		Cache:           schedulerCache,
		NextPod:         queue.Pop,
		SchedulingQueue: queue,
		Profiles: profile.Map{
			"koord-scheduler": fwk,
		},
		SchedulePod: func(context.Context, framework.Framework, fwktype.CycleState, *corev1.Pod) (scheduler.ScheduleResult, error) {
			return scheduler.ScheduleResult{
				SuggestedHost:  "node-1",
				EvaluatedNodes: 1,
				FeasibleNodes:  1,
			}, nil
		},
	}
	schedulingWorkflow := NewWorkflow(sched, nil)
	schedulingWorkflow.nominatedNodeNameForExpectationEnabled = false
	workflow := &SandboxCustomWorkflow{
		workflow:     schedulingWorkflow,
		bindingSlots: make(chan struct{}, maxConcurrentBindings),
	}
	return &bindingWorkflowTestHarness{
		workflow: workflow,
		sched:    sched,
		queue:    queue,
		logger:   logger,
		recorder: recorder,
	}
}

func (h *bindingWorkflowTestHarness) enqueue(name string) {
	h.queue.Add(h.logger, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
			UID:       types.UID(name),
		},
		Spec: corev1.PodSpec{
			SchedulerName: "koord-scheduler",
		},
	})
}

func TestScheduleOneDoesNotLimitBindingsWhenSlotsAreDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bound := make(chan string, 1)
	h := newBindingWorkflowTestHarness(t, ctx, 1,
		schedulertesting.RegisterBindPlugin(testBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testBindPlugin{bound: bound}, nil
		}),
	)
	h.workflow.bindingSlots = nil
	h.sched.FailureHandler = func(_ context.Context, _ framework.Framework, podInfo *framework.QueuedPodInfo, _ *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		h.queue.Done(podInfo.Pod.UID)
	}
	h.enqueue("binding-slots-disabled")

	scheduleReturned := make(chan struct{})
	go func() {
		h.workflow.ScheduleOne(ctx)
		close(scheduleReturned)
	}()

	select {
	case <-scheduleReturned:
	case <-time.After(time.Second):
		t.Fatal("ScheduleOne blocked while binding slots were disabled")
	}
	select {
	case name := <-bound:
		assert.Equal(t, "binding-slots-disabled", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Bind while binding slots were disabled")
	}
	assert.Nil(t, h.workflow.bindingSlots)
}

func TestScheduleOneReleasesBindingSlotAfterBindError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const podCount = 64
	bindErr := errors.New("bind failed")
	bound := make(chan string, podCount)
	h := newBindingWorkflowTestHarness(t, ctx, 1,
		schedulertesting.RegisterBindPlugin(testBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testBindPlugin{
				status: fwktype.AsStatus(bindErr),
				bound:  bound,
			}, nil
		}),
	)
	type bindingFailure struct {
		name string
		err  error
	}
	failed := make(chan bindingFailure, podCount)
	h.sched.FailureHandler = func(_ context.Context, _ framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		h.queue.Done(podInfo.Pod.UID)
		failed <- bindingFailure{name: podInfo.Pod.Name, err: status.AsError()}
	}

	for i := 0; i < podCount; i++ {
		h.enqueue(fmt.Sprintf("bind-error-%d", i))
	}
	for i := 0; i < podCount; i++ {
		h.workflow.ScheduleOne(ctx)
	}

	gotFailures := map[string]bool{}
	for len(gotFailures) < podCount {
		select {
		case failure := <-failed:
			require.ErrorIs(t, failure.err, bindErr)
			gotFailures[failure.name] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for Bind failures: got %d/%d", len(gotFailures), podCount)
		}
	}
	assert.Len(t, gotFailures, podCount)
	assert.Len(t, bound, podCount)
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestScheduleOneReleasesBindingSlotBeforeFailureCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bindErr := errors.New("bind failed")
	bound := make(chan string, 2)
	cleanupStarted := make(chan string, 2)
	releaseCleanup := make(chan struct{})
	h := newBindingWorkflowTestHarness(t, ctx, 1,
		schedulertesting.RegisterBindPlugin(testBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testBindPlugin{
				status: fwktype.AsStatus(bindErr),
				bound:  bound,
			}, nil
		}),
	)
	h.sched.FailureHandler = func(_ context.Context, _ framework.Framework, podInfo *framework.QueuedPodInfo, _ *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		h.queue.Done(podInfo.Pod.UID)
		cleanupStarted <- podInfo.Pod.Name
		<-releaseCleanup
	}

	h.enqueue("cleanup-1")
	h.workflow.ScheduleOne(ctx)
	select {
	case name := <-bound:
		assert.Equal(t, "cleanup-1", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first Bind failure")
	}
	select {
	case name := <-cleanupStarted:
		assert.Equal(t, "cleanup-1", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first failure cleanup")
	}
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)

	h.enqueue("cleanup-2")
	scheduleReturned := make(chan struct{})
	go func() {
		h.workflow.ScheduleOne(ctx)
		close(scheduleReturned)
	}()

	select {
	case <-scheduleReturned:
	case <-time.After(time.Second):
		t.Fatal("the next scheduling cycle did not progress while failure cleanup was blocked")
	}
	select {
	case name := <-bound:
		assert.Equal(t, "cleanup-2", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the second Bind failure")
	}

	close(releaseCleanup)
	select {
	case name := <-cleanupStarted:
		assert.Equal(t, "cleanup-2", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the second failure cleanup")
	}
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestScheduleOneHoldsBindingSlotUntilBindReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	bound := make(chan string, 1)
	h := newBindingWorkflowTestHarness(t, ctx, 1,
		schedulertesting.RegisterBindPlugin(testBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testBindPlugin{
				bound:   bound,
				started: started,
				release: release,
			}, nil
		}),
	)
	h.sched.FailureHandler = func(_ context.Context, _ framework.Framework, podInfo *framework.QueuedPodInfo, _ *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		h.queue.Done(podInfo.Pod.UID)
	}
	h.enqueue("blocked-bind")

	h.workflow.ScheduleOne(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Bind to start")
	}
	assert.Len(t, h.workflow.bindingSlots, 1)

	close(release)
	select {
	case name := <-bound:
		assert.Equal(t, "blocked-bind", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Bind to finish")
	}
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestScheduleOnePreservesPermitTimeoutWhileWaitingForBindingSlot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const permitTimeout = 20 * time.Millisecond
	bindStarted := make(chan struct{}, 1)
	releaseBind := make(chan struct{})
	bound := make(chan string, 2)
	permitCalled := make(chan string, 1)
	h := newBindingWorkflowTestHarness(t, ctx, 1,
		schedulertesting.RegisterPermitPlugin(testPermitPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &namedWaitPermitPlugin{
				podName: "permit-timeout",
				timeout: permitTimeout,
				called:  permitCalled,
			}, nil
		}),
		schedulertesting.RegisterBindPlugin(testBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testBindPlugin{
				bound:   bound,
				started: bindStarted,
				release: releaseBind,
			}, nil
		}),
	)
	type bindingFailure struct {
		name string
		err  error
	}
	failed := make(chan bindingFailure, 1)
	h.sched.FailureHandler = func(_ context.Context, _ framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		h.queue.Done(podInfo.Pod.UID)
		failed <- bindingFailure{name: podInfo.Pod.Name, err: status.AsError()}
	}

	h.enqueue("blocked-bind")
	h.workflow.ScheduleOne(ctx)
	select {
	case <-bindStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first Bind to occupy the slot")
	}
	assert.Len(t, h.workflow.bindingSlots, 1)

	h.enqueue("permit-timeout")
	scheduleReturned := make(chan struct{})
	go func() {
		h.workflow.ScheduleOne(ctx)
		close(scheduleReturned)
	}()
	select {
	case name := <-permitCalled:
		assert.Equal(t, "permit-timeout", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the Permit plugin")
	}
	select {
	case <-scheduleReturned:
		t.Fatal("the waiting pod acquired a binding slot before the first Bind completed")
	case <-time.After(5 * permitTimeout):
	}

	close(releaseBind)
	select {
	case name := <-bound:
		assert.Equal(t, "blocked-bind", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first Bind to finish")
	}
	select {
	case <-scheduleReturned:
	case <-time.After(time.Second):
		t.Fatal("the timed-out pod did not leave binding-slot backpressure")
	}
	select {
	case failure := <-failed:
		assert.Equal(t, "permit-timeout", failure.name)
		require.ErrorContains(t, failure.err, "rejected due to timeout")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the Permit timeout failure")
	}
	select {
	case name := <-bound:
		t.Fatalf("Permit-timed-out pod unexpectedly reached Bind: %q", name)
	case <-time.After(50 * time.Millisecond):
	}
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestScheduleOneReleasesBindingSlotBeforeEventRecording(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bound := make(chan string, 2)
	h := newBindingWorkflowTestHarness(t, ctx, 1,
		schedulertesting.RegisterBindPlugin(testBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testBindPlugin{bound: bound}, nil
		}),
	)
	h.recorder.Events = make(chan string)
	h.sched.FailureHandler = func(_ context.Context, _ framework.Framework, podInfo *framework.QueuedPodInfo, _ *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		h.queue.Done(podInfo.Pod.UID)
	}

	h.enqueue("event-1")
	h.workflow.ScheduleOne(ctx)
	select {
	case name := <-bound:
		assert.Equal(t, "event-1", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first Bind")
	}

	h.enqueue("event-2")
	scheduleReturned := make(chan struct{})
	go func() {
		h.workflow.ScheduleOne(ctx)
		close(scheduleReturned)
	}()
	select {
	case <-scheduleReturned:
	case <-time.After(time.Second):
		t.Fatal("the next scheduling cycle did not progress while event recording was blocked")
	}
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)

	select {
	case name := <-bound:
		assert.Equal(t, "event-2", name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the second Bind")
	}
	select {
	case <-h.recorder.Events:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first Scheduled event")
	}
	select {
	case <-h.recorder.Events:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the second Scheduled event")
	}
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestScheduleOneReleasesBindingSlotWhileWaitingOnPermit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bound := make(chan string, 2)
	var permitPlugin *thresholdPermitPlugin
	h := newBindingWorkflowTestHarness(t, ctx, 1,
		schedulertesting.RegisterPermitPlugin(testPermitPluginName, func(_ context.Context, _ runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
			permitPlugin = &thresholdPermitPlugin{
				handle:    handle,
				threshold: 2,
				timeout:   time.Minute,
			}
			return permitPlugin, nil
		}),
		schedulertesting.RegisterBindPlugin(testBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testBindPlugin{bound: bound}, nil
		}),
	)
	failed := make(chan error, 1)
	h.sched.FailureHandler = func(_ context.Context, _ framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		h.queue.Done(podInfo.Pod.UID)
		failed <- status.AsError()
	}

	h.enqueue("permit-1")
	h.workflow.ScheduleOne(ctx)
	require.Eventually(t, func() bool {
		return permitPlugin.callCount() == 1 && len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond, "a Permit waiter must not occupy the only binding slot")

	h.enqueue("permit-2")
	scheduleReturned := make(chan struct{})
	go func() {
		h.workflow.ScheduleOne(ctx)
		close(scheduleReturned)
	}()
	select {
	case <-scheduleReturned:
	case err := <-failed:
		t.Fatalf("unexpected binding failure: %v", err)
	case <-time.After(time.Second):
		t.Fatal("the next gang member could not enter the scheduling cycle")
	}

	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case name := <-bound:
			got[name] = true
		case err := <-failed:
			t.Fatalf("unexpected binding failure: %v", err)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for both permitted pods to bind")
		}
	}
	assert.Equal(t, map[string]bool{"permit-1": true, "permit-2": true}, got)
	assert.Eventually(t, func() bool {
		return len(h.workflow.bindingSlots) == 0
	}, time.Second, 10*time.Millisecond)
}

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
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/batch/framework"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

func init() {
	metrics.Register()
	schedulermetrics.Register()
}

// fakeExtender is a trimmed frameworkext.FrameworkExtender used to drive the batch Engine and
// BatchScheduler. Only the methods exercised by the batch package are overridden; the embedded
// interface is nil, so calling an un-overridden method panics (surfacing an unexpected code path).
type fakeExtender struct {
	frameworkext.FrameworkExtender

	mu sync.Mutex

	preFilterStatus *fwktype.Status
	filterStatus    *fwktype.Status
	reserveStatus   *fwktype.Status
	preBindStatus   *fwktype.Status
	bindStatus      *fwktype.Status
	postBindStatus  *fwktype.Status
	permitStatus    *fwktype.Status
	waitPermitState *fwktype.Status

	// per-pod overrides keyed by pod key "ns/name"
	permitStatusPerPod map[string]*fwktype.Status
	bindStatusPerPod   map[string]*fwktype.Status

	forgetErr error

	snapshotLister fwktype.SharedLister
	scheduler      frameworkext.Scheduler

	reserveCalled   atomic.Int32
	unreserveCalled atomic.Int32
	preBindCalled   atomic.Int32
	bindCalled      atomic.Int32
	postBindCalled  atomic.Int32
	forgetCalled    atomic.Int32
	waitPermitCalls atomic.Int32
	doneCalled      atomic.Int32
}

func (f *fakeExtender) Parallelizer() fwktype.Parallelizer {
	return parallelize.NewParallelizer(1)
}

func (f *fakeExtender) SnapshotSharedLister() fwktype.SharedLister {
	return f.snapshotLister
}

func (f *fakeExtender) ProfileName() string { return "fake" }

func (f *fakeExtender) EventRecorder() events.EventRecorder { return &events.FakeRecorder{} }

func (f *fakeExtender) DeleteNominatedPodIfExists(pod *corev1.Pod) {}

func (f *fakeExtender) RunPreFilterPlugins(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod) (*fwktype.PreFilterResult, *fwktype.Status, sets.Set[string]) {
	return &fwktype.PreFilterResult{NodeNames: nil}, f.preFilterStatus, nil
}

func (f *fakeExtender) RunFilterPluginsWithNominatedPods(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) *fwktype.Status {
	return f.filterStatus
}

func (f *fakeExtender) RunReservePluginsReserve(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	f.reserveCalled.Add(1)
	return f.reserveStatus
}

func (f *fakeExtender) RunReservePluginsUnreserve(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) {
	f.unreserveCalled.Add(1)
}

func (f *fakeExtender) RunPreBindPlugins(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	f.preBindCalled.Add(1)
	return f.preBindStatus
}

func (f *fakeExtender) RunBindPlugins(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	f.bindCalled.Add(1)
	if f.bindStatusPerPod != nil {
		if s, ok := f.bindStatusPerPod[podKey(pod)]; ok {
			return s
		}
	}
	return f.bindStatus
}

func (f *fakeExtender) RunPostBindPlugins(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) {
	f.postBindCalled.Add(1)
}

func (f *fakeExtender) RunPermitPlugins(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	if f.permitStatusPerPod != nil {
		if s, ok := f.permitStatusPerPod[podKey(pod)]; ok {
			return s
		}
	}
	return f.permitStatus
}

func (f *fakeExtender) WaitOnPermit(ctx context.Context, pod *corev1.Pod) *fwktype.Status {
	f.waitPermitCalls.Add(1)
	return f.waitPermitState
}

func (f *fakeExtender) Scheduler() frameworkext.Scheduler {
	return f.scheduler
}

func (f *fakeExtender) ForgetPod(logger klog.Logger, pod *corev1.Pod) error {
	f.forgetCalled.Add(1)
	return f.forgetErr
}

func podKey(pod *corev1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}

// fakeNodeInfoLister is a trimmed NodeInfoLister/SharedLister used by the batch tests.
type fakeNodeInfoLister struct {
	infos []fwktype.NodeInfo
}

func (c *fakeNodeInfoLister) List() ([]fwktype.NodeInfo, error) { return c.infos, nil }

func (c *fakeNodeInfoLister) HavePodsWithAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (c *fakeNodeInfoLister) HavePodsWithRequiredAntiAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (c *fakeNodeInfoLister) Get(nodeName string) (fwktype.NodeInfo, error) {
	for _, ni := range c.infos {
		if ni.Node() != nil && ni.Node().Name == nodeName {
			return ni, nil
		}
	}
	return nil, fmt.Errorf("nodeinfo not found for node %q", nodeName)
}

func (c *fakeNodeInfoLister) NodeInfos() fwktype.NodeInfoLister { return c }

func (c *fakeNodeInfoLister) StorageInfos() fwktype.StorageInfoLister { return c }

func (c *fakeNodeInfoLister) IsPVCUsedByPods(key string) bool { return false }

func newNodeLister(nodeNames ...string) *fakeNodeInfoLister {
	var infos []fwktype.NodeInfo
	for _, name := range nodeNames {
		ni := schedulerframework.NewNodeInfo()
		ni.SetNode(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})
		infos = append(infos, ni)
	}
	return &fakeNodeInfoLister{infos: infos}
}

var _ cache.Cache = &fakeCache{}

// fakeCache is a trimmed cache.Cache implementation for the batch tests.
type fakeCache struct {
	mu             sync.RWMutex
	pods           map[string]*corev1.Pod
	assumedPods    map[string]*corev1.Pod
	assumePodError error
	forgetPodError error
	forgetCalled   atomic.Int32
}

func newFakeCache() *fakeCache {
	return &fakeCache{
		pods:        map[string]*corev1.Pod{},
		assumedPods: map[string]*corev1.Pod{},
	}
}

func (f *fakeCache) NodeCount() int         { return 0 }
func (f *fakeCache) PodCount() (int, error) { return len(f.pods), nil }

func (f *fakeCache) AssumePod(logger klog.Logger, pod *corev1.Pod) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.assumePodError != nil {
		return f.assumePodError
	}
	key := podKey(pod)
	f.assumedPods[key] = pod.DeepCopy()
	f.pods[key] = pod.DeepCopy()
	return nil
}

func (f *fakeCache) FinishBinding(logger klog.Logger, pod *corev1.Pod) error { return nil }

func (f *fakeCache) ForgetPod(logger klog.Logger, pod *corev1.Pod) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgetCalled.Add(1)
	if f.forgetPodError != nil {
		return f.forgetPodError
	}
	key := podKey(pod)
	delete(f.assumedPods, key)
	delete(f.pods, key)
	return nil
}

func (f *fakeCache) AddPod(logger klog.Logger, pod *corev1.Pod) error { return nil }

func (f *fakeCache) UpdatePod(logger klog.Logger, oldPod, newPod *corev1.Pod) error { return nil }

func (f *fakeCache) RemovePod(logger klog.Logger, pod *corev1.Pod) error { return nil }

func (f *fakeCache) GetPod(pod *corev1.Pod) (*corev1.Pod, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if p, ok := f.pods[podKey(pod)]; ok {
		return p, nil
	}
	return nil, errors.New("pod not found")
}

func (f *fakeCache) IsAssumedPod(pod *corev1.Pod) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.assumedPods[podKey(pod)]
	return ok, nil
}

func (f *fakeCache) AddNode(logger klog.Logger, node *corev1.Node) *schedulerframework.NodeInfo {
	return nil
}

func (f *fakeCache) UpdateNode(logger klog.Logger, oldNode, newNode *corev1.Node) *schedulerframework.NodeInfo {
	return nil
}

func (f *fakeCache) RemoveNode(logger klog.Logger, node *corev1.Node) error { return nil }

func (f *fakeCache) UpdateSnapshot(logger klog.Logger, nodeSnapshot *cache.Snapshot) error {
	return nil
}

func (f *fakeCache) Dump() *cache.Dump {
	return &cache.Dump{Nodes: map[string]*schedulerframework.NodeInfo{}, AssumedPods: sets.New[string]()}
}

func (f *fakeCache) BindPod(binding *corev1.Binding) (<-chan error, error) {
	ch := make(chan error, 1)
	ch <- nil
	return ch, nil
}

// ---- helpers ----

func nodePodRequest(node, ns, name string) framework.PodRequest {
	return framework.PodRequest{NodeName: node, Pod: newPod(ns, name)}
}

func newJobRequest() *framework.JobRequest {
	return &framework.JobRequest{SchedulerName: "koord-scheduler", Namespace: "ns", JobName: "job"}
}

// ---- Engine.Assume ----

func TestEngineAssume(t *testing.T) {
	c := newFakeCache()
	e := NewEngine(c)
	ext := &fakeExtender{}
	pod := newPod("ns", "p1")
	err := e.Assume(klog.Background(), pod, "node-a", ext)
	assert.NoError(t, err)
	assert.Equal(t, "node-a", pod.Spec.NodeName)
	assumed, _ := c.IsAssumedPod(pod)
	assert.True(t, assumed)
}

func TestEngineAssumeError(t *testing.T) {
	c := newFakeCache()
	c.assumePodError = errors.New("assume failed")
	e := NewEngine(c)
	ext := &fakeExtender{}
	pod := newPod("ns", "p1")
	err := e.Assume(klog.Background(), pod, "node-a", ext)
	assert.Error(t, err)
}

// ---- Engine.RunSchedulingCycle ----

func runCycle(t *testing.T, ext *fakeExtender, c *fakeCache, reqs [][]framework.PodRequest, skip func(*corev1.Pod) bool) (*framework.JobResult, []*framework.AssumeContext) {
	t.Helper()
	e := NewEngine(c)
	jobResult := &framework.JobResult{}
	var assumed []*framework.AssumeContext
	lock := &sync.Mutex{}
	snap := &sync.Map{}
	e.RunSchedulingCycle(context.Background(), klog.Background(), ext.Parallelizer(), ext,
		newJobRequest(), jobResult, reqs, &assumed, lock, snap, skip)
	return jobResult, assumed
}

func TestRunSchedulingCycleSuccess(t *testing.T) {
	ext := &fakeExtender{snapshotLister: newNodeLister("node-a")}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	jobResult, assumed := runCycle(t, ext, c, reqs, nil)
	assert.True(t, jobResult.Status.IsSuccess())
	assert.Len(t, assumed, 1)
	assert.Equal(t, int32(1), ext.reserveCalled.Load())
}

func TestRunSchedulingCycleEmptyNodeGroup(t *testing.T) {
	ext := &fakeExtender{snapshotLister: newNodeLister("node-a")}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{}}
	jobResult, assumed := runCycle(t, ext, c, reqs, nil)
	assert.True(t, jobResult.Status.IsSuccess())
	assert.Len(t, assumed, 0)
}

func TestRunSchedulingCycleNodeNotFound(t *testing.T) {
	ext := &fakeExtender{snapshotLister: newNodeLister("other")}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	jobResult, _ := runCycle(t, ext, c, reqs, nil)
	assert.False(t, jobResult.Status.IsSuccess())
}

func TestRunSchedulingCycleSkip(t *testing.T) {
	ext := &fakeExtender{snapshotLister: newNodeLister("node-a")}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	jobResult, assumed := runCycle(t, ext, c, reqs, func(pod *corev1.Pod) bool { return true })
	assert.True(t, jobResult.Status.IsSuccess())
	assert.Len(t, assumed, 0)
	st := jobResult.PodStatus("ns/p1")
	assert.True(t, st.IsSkip())
}

func TestRunSchedulingCyclePreFilterFail(t *testing.T) {
	ext := &fakeExtender{
		snapshotLister:  newNodeLister("node-a"),
		preFilterStatus: fwktype.NewStatus(fwktype.Unschedulable, "prefilter"),
	}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	jobResult, _ := runCycle(t, ext, c, reqs, nil)
	assert.False(t, jobResult.Status.IsSuccess())
}

func TestRunSchedulingCyclePreFilterNodeOut(t *testing.T) {
	ext := &fakeExtender{snapshotLister: newNodeLister("node-a")}
	// override RunPreFilterPlugins to return a node set that excludes node-a
	ext2 := &fakeExtenderNodeOut{fakeExtender: ext}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	e := NewEngine(c)
	jobResult := &framework.JobResult{}
	var assumed []*framework.AssumeContext
	lock := &sync.Mutex{}
	snap := &sync.Map{}
	e.RunSchedulingCycle(context.Background(), klog.Background(), ext.Parallelizer(), ext2,
		newJobRequest(), jobResult, reqs, &assumed, lock, snap, nil)
	assert.False(t, jobResult.Status.IsSuccess())
}

type fakeExtenderNodeOut struct {
	*fakeExtender
}

func (f *fakeExtenderNodeOut) RunPreFilterPlugins(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod) (*fwktype.PreFilterResult, *fwktype.Status, sets.Set[string]) {
	return &fwktype.PreFilterResult{NodeNames: sets.New("some-other-node")}, nil, nil
}

func TestRunSchedulingCycleFilterFail(t *testing.T) {
	ext := &fakeExtender{
		snapshotLister: newNodeLister("node-a"),
		filterStatus:   fwktype.NewStatus(fwktype.Unschedulable, "filter"),
	}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	jobResult, _ := runCycle(t, ext, c, reqs, nil)
	assert.False(t, jobResult.Status.IsSuccess())
}

func TestRunSchedulingCycleAssumeFail(t *testing.T) {
	ext := &fakeExtender{snapshotLister: newNodeLister("node-a")}
	c := newFakeCache()
	c.assumePodError = errors.New("assume failed")
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	jobResult, assumed := runCycle(t, ext, c, reqs, nil)
	assert.False(t, jobResult.Status.IsSuccess())
	assert.Len(t, assumed, 0)
}

func TestRunSchedulingCycleReserveFail(t *testing.T) {
	ext := &fakeExtender{
		snapshotLister: newNodeLister("node-a"),
		reserveStatus:  fwktype.NewStatus(fwktype.Error, "reserve"),
	}
	c := newFakeCache()
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1")}}
	jobResult, assumed := runCycle(t, ext, c, reqs, nil)
	assert.False(t, jobResult.Status.IsSuccess())
	// pod was assumed before reserve, so it must be recorded for cleanup
	assert.Len(t, assumed, 1)
}

// ---- Engine.BindPods ----

func assumeCtx(node, ns, name string) *framework.AssumeContext {
	p := newPod(ns, name)
	p.Spec.NodeName = node
	return &framework.AssumeContext{CycleState: schedulerframework.NewCycleState(), NodeName: node, Pod: p}
}

// ---- Engine.CleanupAssumedPods ----

func TestCleanupAssumedPods(t *testing.T) {
	ext := &fakeExtender{}
	c := newFakeCache()
	e := NewEngine(c)
	assumed := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	jobResult := &framework.JobResult{}
	snap := &sync.Map{}
	status := fwktype.NewStatus(fwktype.Unschedulable, "cleanup")
	e.CleanupAssumedPods(context.Background(), klog.Background(), ext.Parallelizer(), ext,
		newJobRequest(), jobResult, assumed, snap, func(pod *corev1.Pod) error {
			return c.ForgetPod(klog.Background(), pod)
		}, status, "test")
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
}

func TestCleanupAssumedPodsForgetError(t *testing.T) {
	ext := &fakeExtender{}
	e := NewEngine(newFakeCache())
	assumed := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	jobResult := &framework.JobResult{}
	snap := &sync.Map{}
	status := fwktype.NewStatus(fwktype.Unschedulable, "cleanup")
	e.CleanupAssumedPods(context.Background(), klog.Background(), ext.Parallelizer(), ext,
		newJobRequest(), jobResult, assumed, snap, func(pod *corev1.Pod) error {
			return errors.New("forget failed")
		}, status, "test")
	assert.Equal(t, int32(1), ext.unreserveCalled.Load())
}

// ---- ValidateAndGroupByRequest / MakeJobResultWithStatus / GetPluginSamplePercent ----

func TestValidateAndGroupByRequestErrors(t *testing.T) {
	// no pods
	_, err := ValidateAndGroupByRequest(&framework.JobRequest{PodsByNode: map[string]map[string]framework.PodRequest{}})
	assert.Error(t, err)

	// insufficient pods, not requeued
	jr := &framework.JobRequest{
		MinMember: 5,
		PodsByNode: map[string]map[string]framework.PodRequest{
			"node-a": {"ns/p1": nodePodRequest("node-a", "ns", "p1")},
		},
	}
	_, err = ValidateAndGroupByRequest(jr)
	assert.Error(t, err)

	// requeued bypasses min member check
	jr.Requeued = true
	reqs, err := ValidateAndGroupByRequest(jr)
	assert.NoError(t, err)
	assert.Len(t, reqs, 1)
}

func TestMakeJobResultWithStatus(t *testing.T) {
	reqs := [][]framework.PodRequest{{nodePodRequest("node-a", "ns", "p1"), nodePodRequest("node-a", "ns", "p2")}}
	status := fwktype.NewStatus(fwktype.Unschedulable, "boom")
	jr := MakeJobResultWithStatus(reqs, status)
	assert.Equal(t, status, jr.Status)
	assert.Equal(t, status, jr.PodStatus("ns/p1"))
	assert.Equal(t, status, jr.PodStatus("ns/p2"))
}

func TestGetPluginSamplePercent(t *testing.T) {
	assert.Equal(t, 10, GetPluginSamplePercent())

	t.Setenv("pluginMetricsSamplePercent", "50")
	assert.Equal(t, 50, GetPluginSamplePercent())

	// On parse error strconv.Atoi returns 0, and GetPluginSamplePercent returns that value.
	t.Setenv("pluginMetricsSamplePercent", "not-a-number")
	assert.Equal(t, 0, GetPluginSamplePercent())
}

func eventuallyEqual(t *testing.T, want int32, get func() int32) {
	t.Helper()
	assert.Eventually(t, func() bool { return get() == want }, 2*time.Second, 5*time.Millisecond)
}

// ---- Engine.BindPods ----

func bindPods(ext *fakeExtender, toBind []*framework.AssumeContext) (*framework.JobResult, []*framework.AssumeContext) {
	e := NewEngine(newFakeCache())
	jobResult := &framework.JobResult{}
	var assumed []*framework.AssumeContext
	lock := &sync.Mutex{}
	snap := &sync.Map{}
	e.BindPods(context.Background(), klog.Background(), ext.Parallelizer(), ext,
		newJobRequest(), jobResult, toBind, &assumed, lock, snap, OperationBindJobByPodAsync)
	return jobResult, assumed
}

func TestBindPodsSuccess(t *testing.T) {
	ext := &fakeExtender{}
	toBind := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	jobResult, failed := bindPods(ext, toBind)
	assert.True(t, jobResult.Status.IsSuccess())
	assert.Len(t, failed, 0)
	assert.Equal(t, int32(1), ext.postBindCalled.Load())
}

func TestBindPodsPreBindFail(t *testing.T) {
	ext := &fakeExtender{preBindStatus: fwktype.NewStatus(fwktype.Error, "prebind")}
	toBind := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	jobResult, failed := bindPods(ext, toBind)
	assert.False(t, jobResult.Status.IsSuccess())
	assert.Len(t, failed, 1)
}

func TestBindPodsBindFail(t *testing.T) {
	ext := &fakeExtender{bindStatus: fwktype.NewStatus(fwktype.Error, "bind")}
	toBind := []*framework.AssumeContext{assumeCtx("node-a", "ns", "p1")}
	jobResult, failed := bindPods(ext, toBind)
	assert.False(t, jobResult.Status.IsSuccess())
	assert.Len(t, failed, 1)
}

func TestBindPodsPartial(t *testing.T) {
	ext := &fakeExtender{
		bindStatusPerPod: map[string]*fwktype.Status{
			"ns/p2": fwktype.NewStatus(fwktype.Error, "bind"),
		},
	}
	toBind := []*framework.AssumeContext{
		assumeCtx("node-a", "ns", "p1"),
		assumeCtx("node-a", "ns", "p2"),
	}
	jobResult, failed := bindPods(ext, toBind)
	assert.False(t, jobResult.Status.IsSuccess())
	assert.Len(t, failed, 1)
}

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	"k8s.io/component-base/metrics/testutil"
	"k8s.io/klog/v2/ktesting"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	plfeature "k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeports"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

type countingPreFilterPlugin struct {
	calls atomic.Int32
}

func (p *countingPreFilterPlugin) Name() string {
	return "SandboxCountingPreFilter"
}

func (p *countingPreFilterPlugin) PreFilter(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	p.calls.Add(1)
	return nil, nil
}

func (p *countingPreFilterPlugin) PreFilterExtensions() fwktype.PreFilterExtensions {
	return nil
}

type namedFramework struct {
	framework.Framework
	name string
}

func (f *namedFramework) ProfileName() string {
	return f.name
}

func makeNode(name string, cpu, memory string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
		},
	}
}

func makeSandboxPod(name, hash string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      name,
			UID:       types.UID("uid-" + name),
			Labels: map[string]string{
				apiext.LabelSandbox:             "true",
				apiext.LabelSandboxTemplateHash: hash,
			},
		},
		Spec: corev1.PodSpec{SchedulerName: "koord-scheduler"},
	}
}

// newSandboxTestScheduling builds an equivalence scheduling path with a real scheduler cache holding the given nodes
// and a real framework wired with NodeResourcesFit for PreFilter/Filter/Score.
func newSandboxTestScheduling(t *testing.T, ctx context.Context, nodes ...*corev1.Node) *equivalenceScheduling {
	return newSandboxTestSchedulingWithPlugins(t, ctx, nil, nodes...)
}

func newSandboxTestSchedulingWithPlugins(t *testing.T, ctx context.Context, extraPlugins []schedulertesting.RegisterPluginFunc, nodes ...*corev1.Node) *equivalenceScheduling {
	t.Helper()
	logger, _ := ktesting.NewTestContext(t)
	metrics.Register()

	c := cache.New(ctx, 30*time.Second, nil)
	for _, node := range nodes {
		c.AddNode(logger, node)
	}
	snapshot := cache.NewEmptySnapshot()

	fitFactory := frameworkruntime.FactoryAdapter(plfeature.Features{}, noderesources.NewFit)
	nodePortsFactory := frameworkruntime.FactoryAdapter(plfeature.Features{}, nodeports.New)
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterPluginAsExtensions(noderesources.Name, fitFactory, "PreFilter", "Filter", "Score"),
		schedulertesting.RegisterPluginAsExtensions(nodeports.Name, nodePortsFactory, "PreFilter", "Filter"),
	}
	registeredPlugins = append(registeredPlugins, extraPlugins...)
	fwk, err := schedulertesting.NewFramework(ctx, registeredPlugins, "koord-scheduler",
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
		frameworkruntime.WithPodNominator(internalqueue.NewTestQueue(ctx, nil)),
	)
	require.NoError(t, err)

	s := newEquivalenceScheduling(&scheduler.Scheduler{
		Cache: c,
		Profiles: profile.Map{
			"koord-scheduler": fwk,
		},
	}, nil, defaultEquivalenceClassCacheSize)
	s.equivalence = newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
	return s
}

func TestEquivalenceSchedulingHandlesSandboxPodsWithTemplateHash(t *testing.T) {
	s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)

	assert.True(t, s.handles(makeSandboxPod("sandbox", "hash-a")))
	assert.False(t, s.handles(makeSandboxPod("missing-hash", "")))
	assert.False(t, s.handles(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				apiext.LabelSandboxTemplateHash: "hash-a",
			},
		},
	}))
}

func TestEquivalenceSchedulingNodeEventInvalidation(t *testing.T) {
	for _, unschedulable := range []bool{true, false} {
		name := "cordon"
		if !unschedulable {
			name = "uncordon"
		}
		t.Run(name, func(t *testing.T) {
			s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
			s.equivalence.store("class", quotaNodes(10, "node-a"), 1, nil)
			oldNode := makeNode("node-a", "4", "8Gi")
			oldNode.Spec.Unschedulable = !unschedulable
			newNode := oldNode.DeepCopy()
			newNode.Spec.Unschedulable = unschedulable

			s.handleNodeUpdate(oldNode, newNode)

			_, ok, reason := s.equivalence.next("class", 2)
			assert.False(t, ok)
			assert.Equal(t, equivalenceCacheMissNodeEvent, reason)
		})
	}

	t.Run("irrelevant update keeps the cached node", func(t *testing.T) {
		s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil)
		oldNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name:            "node-a",
			ResourceVersion: "1",
		}}
		newNode := oldNode.DeepCopy()
		newNode.ResourceVersion = "2"

		s.handleNodeUpdate(oldNode, newNode)

		node, ok, reason := s.equivalence.next("class", 2)
		require.True(t, ok)
		assert.Empty(t, reason)
		assert.Equal(t, "node-a", node)
	})

	t.Run("allocatable update removes only the affected node", func(t *testing.T) {
		s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
		s.equivalence.store("class", quotaNodes(1, "node-a", "node-b"), 1,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")})
		oldNode := makeNode("node-a", "4", "8Gi")
		newNode := oldNode.DeepCopy()
		newNode.Status.Allocatable[corev1.ResourceCPU] = resource.MustParse("8")

		s.handleNodeUpdate(oldNode, newNode)

		node, ok, reason := s.equivalence.next("class", 2)
		require.True(t, ok)
		assert.Empty(t, reason)
		assert.Equal(t, "node-b", node)
	})

	t.Run("label update removes the affected node", func(t *testing.T) {
		s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil)
		oldNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
		newNode := oldNode.DeepCopy()
		newNode.Labels = map[string]string{"topology.kubernetes.io/zone": "zone-a"}

		s.handleNodeUpdate(oldNode, newNode)

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissNodeEvent, reason)
	})

	t.Run("Koordinator annotation update removes the affected node", func(t *testing.T) {
		s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil)
		oldNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
		newNode := oldNode.DeepCopy()
		newNode.Annotations = map[string]string{
			apiext.AnnotationNodeRawAllocatable: `{"cpu":"4"}`,
		}

		s.handleNodeUpdate(oldNode, newNode)

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissNodeEvent, reason)
	})

	t.Run("delete tombstone flushes conservatively", func(t *testing.T) {
		s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil)

		s.handleNodeDelete(toolscache.DeletedFinalStateUnknown{Key: "node-a", Obj: &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		}})

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissNodeEvent, reason)
	})

	t.Run("unknown update flushes conservatively", func(t *testing.T) {
		s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil)

		s.handleNodeUpdate(&corev1.Pod{}, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissNodeEvent, reason)
	})
}

func TestEquivalenceSchedulingAllocatableInvalidation(t *testing.T) {
	classes := map[string]corev1.ResourceList{
		"cpu": {
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("100Mi"),
		},
		"mid": {
			apiext.MidCPU:    resource.MustParse("100"),
			apiext.MidMemory: resource.MustParse("100Mi"),
		},
		"batch": {
			apiext.BatchCPU:    resource.MustParse("100"),
			apiext.BatchMemory: resource.MustParse("100Mi"),
		},
		"gpu":       {"example.com/gpu": resource.MustParse("1")},
		"ephemeral": {corev1.ResourceEphemeralStorage: resource.MustParse("1Gi")},
		"zero-mid": {
			corev1.ResourceCPU: resource.MustParse("100m"),
			apiext.MidCPU:      resource.MustParse("0"),
		},
		"best-effort": nil,
	}
	for _, tt := range []struct {
		name        string
		resource    corev1.ResourceName
		alsoChanged corev1.ResourceName
		old, new    string
		invalidated []string
	}{
		{name: "cpu", resource: corev1.ResourceCPU, old: "4", new: "2", invalidated: []string{"cpu", "zero-mid"}},
		{name: "memory", resource: corev1.ResourceMemory, old: "8Gi", new: "4Gi", invalidated: []string{"cpu"}},
		{name: "mid cpu", resource: apiext.MidCPU, old: "2000", new: "1000", invalidated: []string{"mid"}},
		{name: "mid memory", resource: apiext.MidMemory, old: "8Gi", new: "4Gi", invalidated: []string{"mid"}},
		{name: "batch cpu", resource: apiext.BatchCPU, old: "2000", new: "1000", invalidated: []string{"batch"}},
		{name: "batch memory", resource: apiext.BatchMemory, old: "8Gi", new: "4Gi", invalidated: []string{"batch"}},
		{name: "gpu", resource: "example.com/gpu", old: "8", new: "4", invalidated: []string{"gpu"}},
		{name: "ephemeral storage", resource: corev1.ResourceEphemeralStorage, old: "20Gi", new: "10Gi", invalidated: []string{"ephemeral"}},
		{name: "pod slots", resource: corev1.ResourcePods, old: "110", new: "100", invalidated: []string{"cpu", "mid", "batch", "gpu", "ephemeral", "zero-mid", "best-effort"}},
		{name: "resource added", resource: apiext.MidCPU, new: "1000", invalidated: []string{"mid"}},
		{name: "resource removed", resource: apiext.BatchCPU, old: "1000", invalidated: []string{"batch"}},
		{name: "zero resource added", resource: "example.com/gpu", new: "0", invalidated: []string{"gpu"}},
		{name: "zero resource removed", resource: "example.com/gpu", old: "0", invalidated: []string{"gpu"}},
		{name: "equal quantities", resource: corev1.ResourceCPU, old: "4", new: "4000m"},
		{name: "unrequested resource", resource: "example.com/other", old: "8", new: "4"},
		{name: "mid and batch", resource: apiext.MidCPU, alsoChanged: apiext.BatchCPU, old: "2000", new: "1000", invalidated: []string{"mid", "batch"}},
		{name: "mid and cpu", resource: apiext.MidCPU, alsoChanged: corev1.ResourceCPU, old: "2000", new: "1000", invalidated: []string{"mid", "cpu", "zero-mid"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			koordmetrics.Register()
			s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
			t.Cleanup(s.equivalence.flush)
			for key, requests := range classes {
				s.equivalence.store(key, quotaNodes(10, "node-a"), 1, requests)
			}
			s.equivalence.store("other-node", quotaNodes(10, "node-b"), 1, classes["mid"])
			oldNode := makeNode("node-a", "4", "8Gi")
			if tt.old != "" {
				oldNode.Status.Allocatable[tt.resource] = resource.MustParse(tt.old)
			}
			newNode := oldNode.DeepCopy()
			if tt.new == "" {
				delete(newNode.Status.Allocatable, tt.resource)
			} else {
				newNode.Status.Allocatable[tt.resource] = resource.MustParse(tt.new)
			}
			if tt.alsoChanged != "" {
				newNode.Status.Allocatable[tt.alsoChanged] = resource.MustParse("1")
			}
			before, err := testutil.GetGaugeMetricValue(koordmetrics.SandboxEquivalenceClassCacheEntries)
			require.NoError(t, err)
			flushCounter := koordmetrics.SandboxEquivalenceClassFlushes.WithLabelValues(equivalenceCacheMissNodeEvent.String())
			flushBefore, err := testutil.GetCounterMetricValue(flushCounter)
			require.NoError(t, err)

			s.handleNodeUpdate(oldNode, newNode)

			after, err := testutil.GetGaugeMetricValue(koordmetrics.SandboxEquivalenceClassCacheEntries)
			require.NoError(t, err)
			assert.Equal(t, before-float64(len(tt.invalidated)), after)
			flushAfter, err := testutil.GetCounterMetricValue(flushCounter)
			require.NoError(t, err)
			assert.Equal(t, flushBefore, flushAfter)
			invalidated := sets.New(tt.invalidated...)
			for key := range classes {
				node, ok, reason := s.equivalence.next(key, 2)
				if invalidated.Has(key) {
					assert.False(t, ok, key)
					assert.Equal(t, equivalenceCacheMissNodeEvent, reason, key)
					_, _, reason = s.equivalence.next(key, 2)
					assert.NotEqual(t, equivalenceCacheMissNodeEvent, reason, key)
				} else {
					assert.True(t, ok, key)
					assert.Equal(t, "node-a", node, key)
					assert.Empty(t, reason, key)
				}
			}
			node, ok, reason := s.equivalence.next("other-node", 2)
			assert.True(t, ok)
			assert.Equal(t, "node-b", node)
			assert.Empty(t, reason)
			assert.Empty(t, s.equivalence.nodeEventInvalidated)
		})
	}
}

func TestEquivalenceSchedulingMixedNodeUpdates(t *testing.T) {
	for name, change := range map[string]func(*corev1.Node){
		"label": func(node *corev1.Node) {
			node.Labels = map[string]string{"topology.kubernetes.io/zone": "zone-a"}
		},
		"annotation": func(node *corev1.Node) {
			node.Annotations = map[string]string{apiext.AnnotationNodeRawAllocatable: `{"cpu":"4"}`}
		},
		"taint": func(node *corev1.Node) {
			node.Spec.Taints = []corev1.Taint{{Key: "dedicated", Effect: corev1.TaintEffectNoSchedule}}
		},
		"condition": func(node *corev1.Node) {
			node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
		},
		"cordon": func(node *corev1.Node) { node.Spec.Unschedulable = true },
	} {
		t.Run(name, func(t *testing.T) {
			s := newEquivalenceScheduling(nil, nil, defaultEquivalenceClassCacheSize)
			t.Cleanup(s.equivalence.flush)
			s.equivalence.store("cpu", quotaNodes(10, "node-a"), 1,
				corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")})
			s.equivalence.store("best-effort", quotaNodes(10, "node-a"), 1, nil)
			oldNode := makeNode("node-a", "4", "8Gi")
			newNode := oldNode.DeepCopy()
			newNode.Status.Allocatable[apiext.MidCPU] = resource.MustParse("1000")
			change(newNode)

			s.handleNodeUpdate(oldNode, newNode)

			for _, key := range []string{"cpu", "best-effort"} {
				_, ok, reason := s.equivalence.next(key, 2)
				assert.False(t, ok, key)
				assert.Equal(t, equivalenceCacheMissNodeEvent, reason, key)
			}
		})
	}
}

func TestSandboxUnrequestedAllocatableUpdateKeepsBackfill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	oldNode := makeNode("node-a", "4", "8Gi")
	oldNode.Status.Allocatable[apiext.MidCPU] = resource.MustParse("2000")
	s := newSandboxTestScheduling(t, ctx, oldNode)
	t.Cleanup(s.equivalence.flush)
	fwk := s.sched.Profiles["koord-scheduler"]
	pod := makeSandboxPod("p1", "hash")
	pod.Spec.Containers = makeQuotaPod("100m", "100Mi").Spec.Containers
	_, err := s.decide(ctx, framework.NewCycleState(), fwk, pod)
	require.NoError(t, err)
	key := equivalenceCacheKey(fwk.ProfileName(), "hash")
	require.Contains(t, s.equivalence.entries, key)
	entry := s.equivalence.entries[key]
	assert.Equal(t, sets.New(corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods), entry.resourceNames)

	newNode := oldNode.DeepCopy()
	newNode.Status.Allocatable[apiext.MidCPU] = resource.MustParse("1000")
	s.handleNodeUpdate(oldNode, newNode)

	require.Contains(t, s.equivalence.entries, key)
	assert.Same(t, entry, s.equivalence.entries[key])
	node, ok, reason := s.equivalence.next(key, s.sched.CurrentCycle())
	assert.True(t, ok)
	assert.Empty(t, reason)
	assert.Equal(t, "node-a", node)
}

func TestDecideSandboxFullPathBackfillsClass(t *testing.T) {
	ctx := context.Background()
	s := newSandboxTestScheduling(t, ctx, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "8", "16Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]
	state := framework.NewCycleState()

	pod := makeSandboxPod("p1", "hash-a")
	result, err := s.decide(ctx, state, fwk, pod)
	assert.NoError(t, err)
	assert.Contains(t, []string{"node-1", "node-2"}, result.SuggestedHost)
	assert.Equal(t, 2, result.FeasibleNodes)

	// The full path must backfill the class for the next pod of the same template.
	_, ok, _ := s.equivalence.next(equivalenceCacheKey(fwk.ProfileName(), "hash-a"), s.sched.CurrentCycle())
	assert.True(t, ok, "class should be backfilled after the full path")
}

func TestDecideSandboxFastPath(t *testing.T) {
	ctx := context.Background()
	s := newSandboxTestScheduling(t, ctx, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "8", "16Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]

	// The first pod of the class pays the full path, which refreshes the shared snapshot and
	// backfills the class; only then can following pods take the fast path.
	pod := makeSandboxPod("p1", "hash-a")
	_, err := s.decide(ctx, framework.NewCycleState(), fwk, pod)
	assert.NoError(t, err)

	pod = makeSandboxPod("p2", "hash-a")
	result, err := s.decide(ctx, framework.NewCycleState(), fwk, pod)
	assert.NoError(t, err)
	assert.Contains(t, []string{"node-1", "node-2"}, result.SuggestedHost)
	assert.Equal(t, 1, result.EvaluatedNodes, "fast path should evaluate a single node")
	assert.Equal(t, 1, result.FeasibleNodes)

	// The second fast-path pod consumes the next cached node.
	pod = makeSandboxPod("p3", "hash-a")
	result, err = s.decide(ctx, framework.NewCycleState(), fwk, pod)
	assert.NoError(t, err)
	assert.Contains(t, []string{"node-1", "node-2"}, result.SuggestedHost)
}

func TestDecideSandboxCacheIsProfileScoped(t *testing.T) {
	ctx := context.Background()
	s := newSandboxTestScheduling(t, ctx, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "8", "16Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]
	otherProfile := &namedFramework{Framework: fwk, name: "other-scheduler"}

	_, err := s.decide(ctx, framework.NewCycleState(), fwk, makeSandboxPod("p1", "hash-a"))
	require.NoError(t, err)

	result, err := s.decide(ctx, framework.NewCycleState(), otherProfile, makeSandboxPod("p2", "hash-a"))
	require.NoError(t, err)
	assert.Equal(t, 2, result.EvaluatedNodes, "a cache entry from another profile must not take the fast path")
	assert.Len(t, s.equivalence.entries, 2)
}

func TestDecideSandboxFastPathRefreshesSnapshot(t *testing.T) {
	ctx := context.Background()
	logger, _ := ktesting.NewTestContext(t)
	s := newSandboxTestScheduling(t, ctx, makeNode("node-1", "4", "8Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]

	firstPod := makeSandboxPod("p1", "hash-a")
	firstPod.Spec.Containers = []corev1.Container{{
		Name: "main",
		Ports: []corev1.ContainerPort{{
			ContainerPort: 8080,
			HostPort:      8080,
		}},
	}}
	result, err := s.decide(ctx, framework.NewCycleState(), fwk, firstPod)
	require.NoError(t, err)

	firstPod.Spec.NodeName = result.SuggestedHost
	require.NoError(t, s.sched.Cache.AddPod(logger, firstPod))

	secondPod := firstPod.DeepCopy()
	secondPod.Name = "p2"
	secondPod.UID = "uid-p2"
	secondPod.Spec.NodeName = ""
	_, err = s.decide(ctx, framework.NewCycleState(), fwk, secondPod)

	var fitErr *framework.FitError
	require.ErrorAs(t, err, &fitErr)
	assert.Contains(t, fitErr.Diagnosis.UnschedulablePlugins, nodeports.Name)
}

func TestDecideSandboxFastPathMissFallsBackToFullPath(t *testing.T) {
	ctx := context.Background()
	s := newSandboxTestScheduling(t, ctx, makeNode("node-1", "4", "8Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]

	snapshot, ok := fwk.SnapshotSharedLister().(*cache.Snapshot)
	require.True(t, ok)
	state := framework.NewCycleState()
	preFilter := s.runSandboxPreFilter(ctx, state, fwk, makeSandboxPod("p1", "unknown"))
	_, ok, _ = s.scheduleFromEquivalenceClass(ctx, state, fwk, makeSandboxPod("p1", "unknown"), equivalenceCacheKey(fwk.ProfileName(), "unknown"), snapshot, preFilter)
	assert.False(t, ok, "unknown class should miss")

	// A miss falls back to the full path inside decide and backfills the class.
	result, err := s.decide(ctx, framework.NewCycleState(), fwk, makeSandboxPod("p1", "hash-b"))
	assert.NoError(t, err)
	assert.Equal(t, "node-1", result.SuggestedHost)
	_, ok, _ = s.equivalence.next(equivalenceCacheKey(fwk.ProfileName(), "hash-b"), s.sched.CurrentCycle())
	assert.True(t, ok, "class should be backfilled after fallback")
}

func TestDecideSandboxRunsPreFilterOnceOnFullPath(t *testing.T) {
	ctx := context.Background()
	var preFilter *countingPreFilterPlugin
	s := newSandboxTestSchedulingWithPlugins(t, ctx, []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterPreFilterPlugin("SandboxCountingPreFilter", func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			preFilter = &countingPreFilterPlugin{}
			return preFilter, nil
		}),
	}, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "8", "16Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]

	_, err := s.decide(ctx, framework.NewCycleState(), fwk, makeSandboxPod("p1", "hash-a"))
	require.NoError(t, err)
	assert.Equal(t, int32(1), preFilter.calls.Load(), "a full-path miss must not run PreFilter twice")
}

func TestDecideSandboxNoNodes(t *testing.T) {
	ctx := context.Background()
	s := newSandboxTestScheduling(t, ctx)
	fwk := s.sched.Profiles["koord-scheduler"]

	_, err := s.decide(ctx, framework.NewCycleState(), fwk, makeSandboxPod("p1", "hash-a"))
	assert.ErrorIs(t, err, scheduler.ErrNoNodesAvailable)
}

func TestNumFeasibleNodesToFind(t *testing.T) {
	adaptive := int32(0)
	fivePercent := int32(5)
	allNodes := int32(100)
	globalTenPercent := int32(10)
	s := &equivalenceScheduling{percentageOfNodesToScore: globalTenPercent}

	assert.Equal(t, int32(200), s.numFeasibleNodesToFind(nil, 2000))
	assert.Equal(t, int32(680), (&equivalenceScheduling{}).numFeasibleNodesToFind(nil, 2000))
	assert.Equal(t, int32(680), s.numFeasibleNodesToFind(&adaptive, 2000))
	assert.Equal(t, int32(100), s.numFeasibleNodesToFind(&fivePercent, 2000))
	assert.Equal(t, int32(2000), s.numFeasibleNodesToFind(&allNodes, 2000))
}

func TestAdvanceNodeIndexConcurrent(t *testing.T) {
	var index atomic.Int64
	const workers = 8
	const iterations = 1000
	const nodeCount = int64(17)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				advanceNodeIndex(&index, 1, nodeCount)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(workers*iterations)%nodeCount, index.Load())
}

func TestFindNodesThatPassFiltersWithNoCandidates(t *testing.T) {
	ctx := context.Background()
	s := newSandboxTestScheduling(t, ctx, makeNode("node-1", "4", "8Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]
	diagnosis := framework.Diagnosis{NodeToStatus: framework.NewDefaultNodeToStatus()}

	nodes, err := s.findNodesThatPassFilters(ctx, fwk, framework.NewCycleState(), makeSandboxPod("p1", "hash"), &diagnosis, nil)
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

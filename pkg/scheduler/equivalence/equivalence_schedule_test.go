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

package equivalence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	"k8s.io/component-base/metrics/testutil"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
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
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// updateSnapshot refreshes the profile's snapshot as the upstream scheduling cycle does.
func (s *EquivalenceScheduling) updateSnapshot(logger klog.Logger, schedFramework framework.Framework) (*cache.Snapshot, error) {
	snapshot, ok := schedFramework.SnapshotSharedLister().(*cache.Snapshot)
	if !ok {
		return nil, fmt.Errorf("unexpected snapshot shared lister type %T", schedFramework.SnapshotSharedLister())
	}
	if err := s.sched.Cache.UpdateSnapshot(logger, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// hitCount distinguishes reuse from full scheduling when both return a single feasible node.
func hitCount(t *testing.T, profile string) float64 {
	t.Helper()
	koordmetrics.Register()
	hits, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassHits.WithLabelValues(profile))
	require.NoError(t, err)
	return hits
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

// fakeClass is the EquivalenceClass under test. Real workload classes declare membership their own
// way; this one groups the pods carrying the fake equivalence class labels.
var fakeClass = frameworkext.NewFakeEquivalenceClass()

// makeClassPod builds a pod of the fake class. An empty key yields a marked pod the class does not
// handle.
func makeClassPod(name, key string) *corev1.Pod {
	return fakeClass.MakePod(name, key)
}

func newTestSchedulingWithPlugins(t *testing.T, ctx context.Context, extraPlugins []schedulertesting.RegisterPluginFunc, nodes ...*corev1.Node) *EquivalenceScheduling {
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

	s := NewEquivalenceScheduling(&scheduler.Scheduler{
		Cache: c,
		Profiles: profile.Map{
			"koord-scheduler": fwk,
		},
	}, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
	s.equivalence = newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
	return s
}

func TestEquivalenceSchedulingHandlesClassPods(t *testing.T) {
	s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)

	assert.True(t, s.Handles(makeClassPod("member", "key-a")))
	assert.False(t, s.Handles(makeClassPod("member-no-key", "")))
	// The class owns membership: a key without the membership marker is not a member.
	assert.False(t, s.Handles(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{frameworkext.FakeEquivalenceClassKeyLabel: "key-a"},
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
			s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
			s.equivalence.store("class", quotaNodes(10, "node-a"), 1, nil, PlacementSpread)
			oldNode := makeNode("node-a", "4", "8Gi")
			oldNode.Spec.Unschedulable = !unschedulable
			newNode := oldNode.DeepCopy()
			newNode.Spec.Unschedulable = unschedulable

			s.handleNodeUpdate(oldNode, newNode)

			_, ok, reason := s.equivalence.next("class", 2)
			assert.False(t, ok)
			assert.Equal(t, equivalenceCacheMissEmpty, reason)
		})
	}

	t.Run("irrelevant update keeps the cached node", func(t *testing.T) {
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
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
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class", quotaNodes(1, "node-a", "node-b"), 1,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}, PlacementSpread)
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
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		oldNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
		newNode := oldNode.DeepCopy()
		newNode.Labels = map[string]string{"topology.kubernetes.io/zone": "zone-a"}

		s.handleNodeUpdate(oldNode, newNode)

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})

	t.Run("Koordinator annotation update removes the affected node", func(t *testing.T) {
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		oldNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
		newNode := oldNode.DeepCopy()
		newNode.Annotations = map[string]string{
			apiext.AnnotationNodeRawAllocatable: `{"cpu":"4"}`,
		}

		s.handleNodeUpdate(oldNode, newNode)

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})

	t.Run("delete tombstone flushes conservatively", func(t *testing.T) {
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)

		s.handleNodeDelete(toolscache.DeletedFinalStateUnknown{Key: "node-a", Obj: &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		}})

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})

	t.Run("add flushes every class", func(t *testing.T) {
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class-a", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		s.equivalence.store("class-b", quotaNodes(1, "node-b"), 1, nil, PlacementSpread)

		s.handleNodeAdd(makeNode("node-c", "4", "8Gi"))

		assert.Equal(t, 0, s.equivalence.len())
		for _, key := range []string{"class-a", "class-b"} {
			_, ok, reason := s.equivalence.next(key, 2)
			require.False(t, ok)
			assert.Equal(t, equivalenceCacheMissEmpty, reason, key)
		}
	})

	t.Run("add on empty cache records no invalidation", func(t *testing.T) {
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)

		s.handleNodeAdd(makeNode("node-a", "4", "8Gi"))

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})

	t.Run("add of unknown object flushes conservatively", func(t *testing.T) {
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)

		s.handleNodeAdd(&corev1.Pod{})

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})

	t.Run("unknown update flushes conservatively", func(t *testing.T) {
		s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
		s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)

		s.handleNodeUpdate(&corev1.Pod{}, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})

		_, ok, reason := s.equivalence.next("class", 2)
		require.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})
}

func TestRegisterNodeEventHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	nodeInformer := informerFactory.Core().V1().Nodes().Informer()

	s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
	require.NoError(t, s.RegisterNodeEventHandler(nodeInformer))
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	s.equivalence.store("class", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
	_, err := client.CoreV1().Nodes().Create(ctx, makeNode("node-b", "4", "8Gi"), metav1.CreateOptions{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return s.equivalence.len() == 0
	}, 5*time.Second, 10*time.Millisecond)
	_, ok, reason := s.equivalence.next("class", 2)
	require.False(t, ok)
	assert.Equal(t, equivalenceCacheMissEmpty, reason)
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
			s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
			t.Cleanup(s.equivalence.flush)
			for key, requests := range classes {
				s.equivalence.store(key, quotaNodes(10, "node-a"), 1, requests, PlacementSpread)
			}
			s.equivalence.store("other-node", quotaNodes(10, "node-b"), 1, classes["mid"], PlacementSpread)
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
			before, err := testutil.GetGaugeMetricValue(koordmetrics.EquivalenceClassCacheEntries)
			require.NoError(t, err)
			flushBefore, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassFlushes)
			require.NoError(t, err)

			s.handleNodeUpdate(oldNode, newNode)

			after, err := testutil.GetGaugeMetricValue(koordmetrics.EquivalenceClassCacheEntries)
			require.NoError(t, err)
			assert.Equal(t, before-float64(len(tt.invalidated)), after)
			flushAfter, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassFlushes)
			require.NoError(t, err)
			assert.Equal(t, flushBefore, flushAfter)
			invalidated := sets.New(tt.invalidated...)
			for key := range classes {
				node, ok, reason := s.equivalence.next(key, 2)
				if invalidated.Has(key) {
					assert.False(t, ok, key)
					// other-node stays cached, so the dropped class reads as unknown rather than empty.
					assert.Equal(t, equivalenceCacheMissUnknownClass, reason, key)
					_, _, reason = s.equivalence.next(key, 2)
					assert.Equal(t, equivalenceCacheMissUnknownClass, reason, key)
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
			s := NewEquivalenceScheduling(nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
			t.Cleanup(s.equivalence.flush)
			s.equivalence.store("cpu", quotaNodes(10, "node-a"), 1,
				corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}, PlacementSpread)
			s.equivalence.store("best-effort", quotaNodes(10, "node-a"), 1, nil, PlacementSpread)
			oldNode := makeNode("node-a", "4", "8Gi")
			newNode := oldNode.DeepCopy()
			newNode.Status.Allocatable[apiext.MidCPU] = resource.MustParse("1000")
			change(newNode)

			s.handleNodeUpdate(oldNode, newNode)

			for _, key := range []string{"cpu", "best-effort"} {
				_, ok, reason := s.equivalence.next(key, 2)
				assert.False(t, ok, key)
				assert.Equal(t, equivalenceCacheMissEmpty, reason, key)
			}
		})
	}
}

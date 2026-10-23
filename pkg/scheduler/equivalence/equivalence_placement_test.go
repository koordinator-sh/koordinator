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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	plfeature "k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
)

type countingScorePlugin struct {
	preScoreCalls atomic.Int32
	scoreCalls    atomic.Int32
}

func (p *countingScorePlugin) Name() string {
	return "CountingScore"
}

func (p *countingScorePlugin) PreScore(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) *fwktype.Status {
	p.preScoreCalls.Add(1)
	return nil
}

func (p *countingScorePlugin) Score(context.Context, fwktype.CycleState, *corev1.Pod, fwktype.NodeInfo) (int64, *fwktype.Status) {
	p.scoreCalls.Add(1)
	return 0, nil
}

func (p *countingScorePlugin) ScoreExtensions() fwktype.ScoreExtensions {
	return nil
}

// placementSuit schedules class pods with NodeResourcesFit and assumes each placed pod in the
// scheduler cache, so each decision sees the pods placed before it.
type placementSuit struct {
	t      *testing.T
	ctx    context.Context
	logger klog.Logger
	s      *EquivalenceScheduling
	fwk    framework.Framework
	score  *countingScorePlugin
}

func newPlacementSuit(t *testing.T, policy PlacementPolicy, nodes ...*corev1.Node) *placementSuit {
	t.Helper()
	logger, ctx := ktesting.NewTestContext(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	metrics.Register()

	schedulerCache := cache.New(ctx, 30*time.Second, nil)
	for _, node := range nodes {
		schedulerCache.AddNode(logger, node)
	}
	fitArgs := &config.NodeResourcesFitArgs{ScoringStrategy: &config.ScoringStrategy{
		Type:      config.LeastAllocated,
		Resources: []config.ResourceSpec{{Name: string(corev1.ResourceCPU), Weight: 1}, {Name: string(corev1.ResourceMemory), Weight: 1}},
	}}
	fitFactory := func(ctx context.Context, _ runtime.Object, h fwktype.Handle) (fwktype.Plugin, error) {
		return noderesources.NewFit(ctx, fitArgs, h, plfeature.Features{})
	}
	score := &countingScorePlugin{}
	fwk, err := schedulertesting.NewFramework(ctx, []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterPluginAsExtensions(noderesources.Name, fitFactory, "PreFilter", "Filter", "PreScore", "Score"),
		schedulertesting.RegisterPluginAsExtensions(score.Name(), func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return score, nil
		}, "PreScore", "Score"),
	}, "koord-scheduler",
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
		frameworkruntime.WithSnapshotSharedLister(cache.NewEmptySnapshot()),
		frameworkruntime.WithPodNominator(internalqueue.NewTestQueue(ctx, nil)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fwk.Close()) })

	s := NewEquivalenceScheduling(&scheduler.Scheduler{
		Cache:    schedulerCache,
		Profiles: profile.Map{"koord-scheduler": fwk},
	}, nil, DefaultEquivalenceClassCacheSize, policy, fakeClass)
	s.equivalence = newEquivalenceClassCache(time.Hour, DefaultEquivalenceClassCacheSize)
	return &placementSuit{t: t, ctx: ctx, logger: logger, s: s, fwk: fwk, score: score}
}

// place schedules count class pods requesting one CPU and 1Gi each, and returns the node of each.
func (p *placementSuit) place(count int) []string {
	p.t.Helper()
	placed := make([]string, 0, count)
	for i := 0; i < count; i++ {
		pod := makeClassPod(fmt.Sprintf("%s-%d", p.t.Name(), i), "class")
		pod.Spec.Containers = makeQuotaPod("1", "1Gi").Spec.Containers
		result, err := p.s.decide(p.ctx, framework.NewCycleState(), p.fwk, pod)
		require.NoError(p.t, err)
		pod.Spec.NodeName = result.SuggestedHost
		require.NoError(p.t, p.s.sched.Cache.AssumePod(p.logger, pod))
		placed = append(placed, result.SuggestedHost)
	}
	return placed
}

func placementNodes() []*corev1.Node {
	return []*corev1.Node{
		makeNode("node-0", "4", "8Gi"), makeNode("node-1", "4", "8Gi"),
		makeNode("node-2", "4", "8Gi"), makeNode("node-3", "4", "8Gi"),
	}
}

func distinct(nodes []string) int {
	set := map[string]struct{}{}
	for _, node := range nodes {
		set[node] = struct{}{}
	}
	return len(set)
}

func countPerNode(nodes []string) map[string]int {
	counts := map[string]int{}
	for _, node := range nodes {
		counts[node]++
	}
	return counts
}

// Each node of placementNodes holds four pods. The eight pods of these tests are one full-path pod
// plus seven fast-path pods; the eighth placement reaches the drift threshold of four cached nodes.

func TestFastPathPack(t *testing.T) {
	p := newPlacementSuit(t, PlacementPack, placementNodes()...)
	hits := hitCount(t, p.fwk.ProfileName())

	placed := p.place(8)

	assert.Equal(t, hits+7, hitCount(t, p.fwk.ProfileName()), "every pod after the first uses the cached decision")
	assert.Equal(t, 1, distinct(placed[:4]), "pods pack onto one node until it is full: %v", placed)
	assert.Equal(t, 1, distinct(placed[4:]), "then onto the next one: %v", placed)
	assert.NotEqual(t, placed[0], placed[4])
}

func TestFastPathSpread(t *testing.T) {
	p := newPlacementSuit(t, PlacementSpread, placementNodes()...)
	hits := hitCount(t, p.fwk.ProfileName())

	placed := p.place(8)

	assert.Equal(t, hits+7, hitCount(t, p.fwk.ProfileName()))
	assert.Equal(t, 4, distinct(placed[:4]), "pods spread over the nodes: %v", placed)
	assert.Equal(t, placed[:4], placed[4:], "the walk wraps around: %v", placed)
	for node, count := range countPerNode(placed) {
		assert.Equal(t, 2, count, node)
	}
}

func TestFastPathSkipsScoring(t *testing.T) {
	for _, policy := range []PlacementPolicy{PlacementSpread, PlacementPack} {
		t.Run(string(policy), func(t *testing.T) {
			p := newPlacementSuit(t, policy, placementNodes()...)
			hits := hitCount(t, p.fwk.ProfileName())

			p.place(8)

			assert.Equal(t, hits+7, hitCount(t, p.fwk.ProfileName()))
			assert.Equal(t, int32(1), p.score.preScoreCalls.Load(), "only the full path runs PreScore")
			assert.Equal(t, int32(4), p.score.scoreCalls.Load(), "only the full path scores the four nodes")
		})
	}
}

func TestFastPathTriesEachCandidateOnce(t *testing.T) {
	for _, policy := range []PlacementPolicy{PlacementSpread, PlacementPack} {
		t.Run(string(policy), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var nodes []*corev1.Node
			var names []string
			filtered := map[string]fwktype.Code{}
			for i := 0; i < 6; i++ {
				name := fmt.Sprintf("node-%d", i)
				nodes = append(nodes, makeNode(name, "4", "8Gi"))
				names = append(names, name)
				filtered[name] = fwktype.Unschedulable
			}
			s := newTestSchedulingWithPlugins(t, ctx, []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterFilterPlugin("FakeFilter", schedulertesting.NewFakeFilterPlugin(filtered)),
			}, nodes...)
			fwk := s.sched.Profiles["koord-scheduler"]
			key := equivalenceCacheKey(fwk.ProfileName(), "hash")
			s.equivalence.store(key, quotaNodes(100, names...), s.sched.CurrentCycle(), nil, policy)
			s.equivalence.consume(key, names[0], s.sched.CurrentCycle())
			snapshot, err := s.updateSnapshot(klog.FromContext(ctx), fwk)
			require.NoError(t, err)
			pod := makeClassPod("pod", "hash")
			state := framework.NewCycleState()

			result, reason := s.scheduleFromEquivalenceClass(ctx, state, fwk, pod, key, snapshot, s.runPreFilter(ctx, state, fwk, pod))

			assert.Empty(t, result.SuggestedHost)
			assert.Equal(t, equivalenceCacheMissFilterRejected, reason)
			assert.Equal(t, len(names), result.EvaluatedNodes, "each candidate is checked once")
		})
	}
}

func TestFastPathPackRejectedAnchor(t *testing.T) {
	for _, stage := range []string{"PreFilter", "Filter", "extender"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var plugins []schedulertesting.RegisterPluginFunc
			switch stage {
			case "PreFilter":
				plugins = append(plugins, schedulertesting.RegisterPreFilterPlugin("Restrict",
					schedulertesting.NewFakePreFilterPlugin("Restrict", &fwktype.PreFilterResult{NodeNames: sets.New("node-2")}, nil)))
			case "Filter":
				plugins = append(plugins, schedulertesting.RegisterFilterPlugin("Reject",
					schedulertesting.NewFakeFilterPlugin(map[string]fwktype.Code{"node-1": fwktype.Unschedulable})))
			}
			s := newTestSchedulingWithPlugins(t, ctx, plugins, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi"))
			if stage == "extender" {
				s.sched.Extenders = []fwktype.Extender{&schedulertesting.FakeExtender{
					Predicates: []schedulertesting.FitPredicate{func(_ *corev1.Pod, node fwktype.NodeInfo) *fwktype.Status {
						if node.Node().Name == "node-1" {
							return fwktype.NewStatus(fwktype.Unschedulable)
						}
						return nil
					}},
				}}
			}
			fwk := s.sched.Profiles["koord-scheduler"]
			key := equivalenceCacheKey(fwk.ProfileName(), "hash")
			cycle := s.sched.CurrentCycle()
			s.equivalence.store(key, quotaNodes(100, "node-1", "node-2"), cycle, nil, PlacementPack)
			s.equivalence.consume(key, "node-1", cycle)
			snapshot, err := s.updateSnapshot(klog.FromContext(ctx), fwk)
			require.NoError(t, err)
			pod := makeClassPod("pod", "hash")
			state := framework.NewCycleState()

			result, reason := s.scheduleFromEquivalenceClass(ctx, state, fwk, pod, key, snapshot, s.runPreFilter(ctx, state, fwk, pod))

			assert.Empty(t, reason)
			assert.Equal(t, "node-2", result.SuggestedHost)
			assert.Equal(t, 1, result.FeasibleNodes)
			entry := s.equivalence.entries[key]
			require.NotNil(t, entry)
			quota, evaluated := int64(0), 2
			if stage == "PreFilter" {
				quota, evaluated = 99, 1
			}
			assert.Equal(t, evaluated, result.EvaluatedNodes)
			assert.Equal(t, quota, entry.nodes[0].quota)
			assert.Equal(t, int64(99), entry.nodes[1].quota)
			assert.Equal(t, 1, entry.last)
		})
	}
}

func TestFullPathBackfillsConfiguredPlacement(t *testing.T) {
	for _, policy := range []PlacementPolicy{PlacementSpread, PlacementPack} {
		t.Run(string(policy), func(t *testing.T) {
			p := newPlacementSuit(t, policy, placementNodes()...)
			p.place(1)
			entry := p.s.equivalence.entries[equivalenceCacheKey(p.fwk.ProfileName(), "class")]
			require.NotNil(t, entry)
			assert.Equal(t, policy, entry.placement)
		})
	}
}

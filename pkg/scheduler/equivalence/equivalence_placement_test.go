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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
)

// placementSuit assumes each scheduled pod so subsequent real cycles see its resource usage.
type placementSuit struct {
	t *testing.T
	*schedulingSuit
}

func newPlacementSuit(t *testing.T, policy PlacementPolicy, nodes ...*corev1.Node) *placementSuit {
	t.Helper()
	h := newSchedulingSuit(t, nodes...)
	h.engine.placement = policy
	return &placementSuit{t: t, schedulingSuit: h}
}

func (p *placementSuit) place(count int) []string {
	p.t.Helper()
	placed := make([]string, 0, count)
	for i := 0; i < count; i++ {
		pod := p.pod(fmt.Sprintf("pod-%d", i), "class", "1")
		result, err := p.schedule(p.t, pod)
		require.NoError(p.t, err)
		require.NotEmpty(p.t, result.SuggestedHost)
		pod.Spec.NodeName = result.SuggestedHost
		require.NoError(p.t, p.sched.Cache.AssumePod(p.logger, pod))
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
			assert.Equal(t, int32(1), p.hooks.preScoreCalls.Load(), "only the full path runs PreScore")
			assert.Equal(t, int32(4), p.hooks.scoreCalls.Load(), "only the full path scores the four nodes")
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
			_, err := s.updateSnapshot(klog.FromContext(ctx), fwk)
			require.NoError(t, err)
			pod := makeClassPod("pod", "hash")

			// PreFilter runs first: the hook sits at its end, and Filter plugins read the state it
			// writes. Called on a fresh state, NodeResourcesFit's Filter errors out and the narrowing
			// would drop the class instead of reporting a rejection.
			state := framework.NewCycleState()
			_, status, _ := fwk.RunPreFilterPlugins(ctx, state, pod)
			require.True(t, status.IsSuccess(), "PreFilter: %v", status.Message())

			node := s.Reuse(ctx, state, fwk, pod)

			// Every candidate is rejected by this pod's Filter, so the class has no usable node left
			// and the entry is dropped: later pods pay the full path rather than retrying the same
			// rejected nodes.
			assert.Empty(t, node, "no candidate survives this pod's Filter")
			assert.NotContains(t, s.equivalence.entries, key, "a class with no usable candidate is dropped")
		})
	}
}

func TestFastPathPackRejectedAnchor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestSchedulingWithPlugins(t, ctx, []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterFilterPlugin("Reject",
			schedulertesting.NewFakeFilterPlugin(map[string]fwktype.Code{"node-1": fwktype.Unschedulable})),
	}, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi"))
	fwk := s.sched.Profiles["koord-scheduler"]
	key := equivalenceCacheKey(fwk.ProfileName(), "hash")
	cycle := s.sched.CurrentCycle()
	s.equivalence.store(key, quotaNodes(100, "node-1", "node-2"), cycle, nil, PlacementPack)
	s.equivalence.consume(key, "node-1", cycle)
	_, err := s.updateSnapshot(klog.FromContext(ctx), fwk)
	require.NoError(t, err)
	pod := makeClassPod("pod", "hash")
	state := framework.NewCycleState()
	_, status, _ := fwk.RunPreFilterPlugins(ctx, state, pod)
	require.True(t, status.IsSuccess(), "PreFilter: %v", status.Message())

	node := s.Reuse(ctx, state, fwk, pod)

	// The pack anchor was node-1, which this pod's Filter rejects. The anchor is dropped permanently
	// for this entry and the pod moves on to the next candidate.
	assert.Equal(t, "node-2", node)
	s.RecordResult(ctx, state, fwk, pod, node, nil, 0)
	entry := s.equivalence.entries[key]
	require.NotNil(t, entry)
	assert.Equal(t, int64(0), entry.nodes[0].quota, "the rejected anchor stays rejected")
	assert.Equal(t, int64(99), entry.nodes[1].quota)
	assert.Equal(t, 1, entry.last)
}

func TestFullPathBackfillsConfiguredPlacement(t *testing.T) {
	for _, policy := range []PlacementPolicy{PlacementSpread, PlacementPack} {
		t.Run(string(policy), func(t *testing.T) {
			p := newPlacementSuit(t, policy, placementNodes()...)
			p.place(1)
			entry := p.engine.equivalence.entries[equivalenceCacheKey(p.fwk.ProfileName(), "class")]
			require.NotNil(t, entry)
			assert.Equal(t, policy, entry.placement)
		})
	}
}

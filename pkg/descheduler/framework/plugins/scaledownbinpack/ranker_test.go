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

package scaledownbinpack

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeNode(name string, cpu, mem string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			},
		},
	}
}

func makePod(namespace, name, nodeName, cpu, mem string, creationTime time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         namespace,
			Name:              name,
			CreationTimestamp: metav1.NewTime(creationTime),
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpu),
							corev1.ResourceMemory: resource.MustParse(mem),
						},
					},
				},
			},
		},
	}
}

func TestRankPods(t *testing.T) {
	now := time.Now()

	node1 := makeNode("node1", "4", "8Gi") // perfect node, score 0
	node2 := makeNode("node2", "4", "8Gi") // multi-tenant, score > 0
	node3 := makeNode("node3", "4", "8Gi") // zero targets
	node4 := makeNode("node4", "4", "8Gi") // tie-break count with node1 (score 0), 2 target pods
	node5 := makeNode("node5", "4", "8Gi") // tie-break name with node4 (score 0, 2 target pods)
	node6 := makeNode("node6", "4", "8Gi") // pod tie-breaks (score 0), 6 target pods

	nodes := []*corev1.Node{node1, node2, node3, node4, node5, node6}

	// For node1: Perfect node. 1 target pod
	p1 := makePod("default", "p1", "node1", "1", "1Gi", now)

	// For node2: Multi-tenant. 1 target, 1 non-target
	p2 := makePod("default", "p2", "node2", "1", "1Gi", now)
	nt2 := makePod("default", "nt2", "node2", "1", "1Gi", now)

	// For node3: Zero targets. 1 non-target
	nt3 := makePod("default", "nt3", "node3", "1", "1Gi", now)

	// For node4: Tie-break count. 2 target pods
	p4a := makePod("default", "p4a", "node4", "1", "1Gi", now)
	p4b := makePod("default", "p4b", "node4", "1", "1Gi", now)

	// For node5: Tie-break name. 2 target pods. Should be after node4 because node5 > node4
	p5a := makePod("default", "p5a", "node5", "1", "1Gi", now)
	p5b := makePod("default", "p5b", "node5", "1", "1Gi", now)

	// For node6: Pod tie-breaks. Perfect node. Score 0. Target count 6. Should be last among perfect nodes.
	p6_large := makePod("default", "p6-large", "node6", "2", "2Gi", now)
	p6_newest := makePod("default", "p6-newest", "node6", "1", "1Gi", now.Add(time.Hour))
	p6_same1 := makePod("a-ns", "p6-same1", "node6", "1", "1Gi", now)
	p6_same2 := makePod("b-ns", "p6-same2", "node6", "1", "1Gi", now)
	p6_same3 := makePod("b-ns", "p6-same3", "node6", "1", "1Gi", now)
	p6_oldest := makePod("default", "p6-oldest", "node6", "1", "1Gi", now.Add(-time.Hour))

	targetPods := []*corev1.Pod{
		p1, p2, p4a, p4b, p5a, p5b,
		p6_large, p6_newest, p6_same1, p6_same2, p6_same3, p6_oldest,
	}
	nonTargetPods := []*corev1.Pod{nt2, nt3}

	weights := map[corev1.ResourceName]float64{
		corev1.ResourceCPU:    1.0,
		corev1.ResourceMemory: 1.0,
	}

	ranked := RankPods(nodes, targetPods, nil, nonTargetPods, weights)

	// We expect 12 pods (nt2 and nt3 are not target pods, and node3 has no target pods so it's skipped anyway)
	assert.Equal(t, 12, len(ranked), "should rank exactly 12 target pods")

	expectedOrder := []string{
		"p1",         // node1
		"p4a", "p4b", // node4
		"p5a", "p5b", // node5
		"p6-large", "p6-newest", "p6-same1", "p6-same2", "p6-same3", "p6-oldest", // node6
		"p2", // node2
	}

	for i, expectedName := range expectedOrder {
		assert.Equal(t, expectedName, ranked[i].Pod.Name, "rank %d mismatch", i)
		assert.Equal(t, i, ranked[i].Rank, "rank %d has incorrect rank value", i)
	}

	// Verifications
	assert.Equal(t, 0.0, ranked[0].EvacuationScore, "perfect node score is 0")
	assert.Greater(t, ranked[11].EvacuationScore, 0.0, "multi-tenant node ranks later")

	for _, rp := range ranked {
		assert.NotEqual(t, "node3", rp.NodeName, "zero-target nodes are skipped")
	}
}

func TestRankPods_EdgeCases(t *testing.T) {
	now := time.Now()

	// 1. Node with <= 0 capacity (e.g. 0 CPU).
	nodeZeroCap := makeNode("node-zero", "0", "0")
	pZero := makePod("default", "p-zero", "node-zero", "1", "1Gi", now)

	// 2. Normal node to ensure sorting works with default weights.
	nodeNormal := makeNode("node-normal", "4", "8Gi")
	pNormal := makePod("default", "p-normal", "node-normal", "1", "1Gi", now)

	nodes := []*corev1.Node{nodeZeroCap, nodeNormal}
	targetPods := []*corev1.Pod{pZero, pNormal}

	// Pass nil resourceWeights to trigger default weights injection.
	ranked := RankPods(nodes, targetPods, nil, nil, nil)

	// Should rank 2 pods successfully.
	assert.Equal(t, 2, len(ranked))

	// nodeNormal < node-zero in ascending string order.
	assert.Equal(t, "p-normal", ranked[0].Pod.Name)
	assert.Equal(t, "p-zero", ranked[1].Pod.Name)

	// 3. Test skippedTargetPods contribution to EvacuationScore
	nodeSkipped := makeNode("node-skipped", "4", "8Gi")
	pEligible := makePod("default", "p-eligible", "node-skipped", "1", "1Gi", now)
	pSkipped := makePod("default", "p-skipped", "node-skipped", "1", "1Gi", now)

	nodesWithSkipped := []*corev1.Node{nodeNormal, nodeSkipped}
	eligibleWithSkipped := []*corev1.Pod{pNormal, pEligible}
	skippedTargetPods := []*corev1.Pod{pSkipped}

	rankedWithSkipped := RankPods(nodesWithSkipped, eligibleWithSkipped, skippedTargetPods, nil, nil)

	// node-normal has score 0 (no skipped pods)
	// node-skipped has score > 0 (1 skipped pod acts as non-target tax)
	// So node-normal should rank first.
	assert.Equal(t, 2, len(rankedWithSkipped))
	assert.Equal(t, "p-normal", rankedWithSkipped[0].Pod.Name)
	assert.Equal(t, "p-eligible", rankedWithSkipped[1].Pod.Name)
	assert.Equal(t, 0.0, rankedWithSkipped[0].EvacuationScore)
	assert.Greater(t, rankedWithSkipped[1].EvacuationScore, 0.0)
}

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

package nodenumaresource

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
)

// BenchmarkPreFilter measures the cost of the PreFilter phase for a CPU-bind pod
// (QoS=LSR, FullPCPUs bind policy) across a 256-node cluster.
func BenchmarkPreFilter(b *testing.B) {
	const numNodes = 256
	nodes := makeNUMANodes(numNodes)
	suit := newPluginTestSuit(b, nil, nodes)
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(b, err)
	pl := p.(*Plugin)

	for i := 0; i < numNodes; i++ {
		topo := buildCPUTopologyForTest(2, 1, 4, 2)
		pl.topologyOptionsManager.UpdateTopologyOptions(fmt.Sprintf("node-%d", i), func(opts *TopologyOptions) {
			opts.CPUTopology = topo
			for node := 0; node < topo.NumNodes; node++ {
				opts.NUMANodeResources = append(opts.NUMANodeResources, NUMANodeResource{
					Node: node,
					Resources: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("32Gi"),
					},
				})
			}
		})
	}

	suit.start(b)

	pod := makeCPUBindPod(4)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		topologymanager.InitStore(cycleState)
		_, status := pl.PreFilter(context.TODO(), cycleState, pod, nil)
		if !status.IsSuccess() && !status.IsSkip() {
			b.Fatalf("PreFilter failed: %v", status)
		}
	}
}

// BenchmarkFilter_CPUBind measures the cost of the Filter phase for a CPU-bind pod
// on a node with a populated CPUTopology.
func BenchmarkFilter_CPUBind(b *testing.B) {
	const numNodes = 256
	nodes := makeNUMANodes(numNodes)
	suit := newPluginTestSuit(b, nil, nodes)
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(b, err)
	pl := p.(*Plugin)

	topo := buildCPUTopologyForTest(2, 1, 4, 2)
	for i := 0; i < numNodes; i++ {
		nodeName := fmt.Sprintf("node-%d", i)
		alloc := NewNodeAllocation(nodeName)
		pl.topologyOptionsManager.UpdateTopologyOptions(nodeName, func(opts *TopologyOptions) {
			opts.CPUTopology = topo
			for node := 0; node < topo.NumNodes; node++ {
				opts.NUMANodeResources = append(opts.NUMANodeResources, NUMANodeResource{
					Node: node,
					Resources: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("32Gi"),
					},
				})
			}
		})
		manager := pl.resourceManager.(*resourceManager)
		manager.nodeAllocations[nodeName] = alloc
	}

	suit.start(b)

	nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("node-0")
	assert.NoError(b, err)

	pod := makeCPUBindPod(4)
	state := &preFilterState{
		requestCPUBind:         true,
		preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
		numCPUsNeeded:          4,
		requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		cycleState.Write(stateKey, state.Clone().(*preFilterState))
		topologymanager.InitStore(cycleState)
		status := pl.Filter(context.TODO(), cycleState, pod, nodeInfo)
		if !status.IsSuccess() {
			b.Fatalf("Filter failed: %v", status)
		}
	}
}

// BenchmarkScore_CPUBind measures the cost of the Score phase for a CPU-bind pod.
func BenchmarkScore_CPUBind(b *testing.B) {
	const numNodes = 256
	nodes := makeNUMANodes(numNodes)

	suit := newPluginTestSuit(b, nil, nodes)
	suit.nodeNUMAResourceArgs.ScoringStrategy = &schedulingconfig.ScoringStrategy{
		Type: schedulingconfig.LeastAllocated,
		Resources: []config.ResourceSpec{
			{Name: string(corev1.ResourceCPU), Weight: 1},
			{Name: string(corev1.ResourceMemory), Weight: 1},
		},
	}
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(b, err)
	pl := p.(*Plugin)

	topo := buildCPUTopologyForTest(2, 1, 4, 2)
	for i := 0; i < numNodes; i++ {
		nodeName := fmt.Sprintf("node-%d", i)
		alloc := NewNodeAllocation(nodeName)
		pl.topologyOptionsManager.UpdateTopologyOptions(nodeName, func(opts *TopologyOptions) {
			opts.CPUTopology = topo
		})
		manager := pl.resourceManager.(*resourceManager)
		manager.nodeAllocations[nodeName] = alloc
	}

	suit.start(b)

	nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("node-0")
	assert.NoError(b, err)

	pod := makeCPUBindPod(4)
	state := &preFilterState{
		requestCPUBind:         true,
		preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
		numCPUsNeeded:          4,
		requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		cycleState.Write(stateKey, state.Clone().(*preFilterState))
		topologymanager.InitStore(cycleState)
		_, status := pl.Score(context.TODO(), cycleState, pod, nodeInfo)
		if !status.IsSuccess() {
			b.Fatalf("Score failed: %v", status)
		}
	}
}

// BenchmarkPreFilter_LargeCluster measures PreFilter at scale: 1024 nodes.
func BenchmarkPreFilter_LargeCluster(b *testing.B) {
	const numNodes = 1024
	nodes := makeNUMANodes(numNodes)
	suit := newPluginTestSuit(b, nil, nodes)
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(b, err)
	pl := p.(*Plugin)

	topo := buildCPUTopologyForTest(2, 2, 8, 2)
	for i := 0; i < numNodes; i++ {
		nodeName := fmt.Sprintf("node-%d", i)
		pl.topologyOptionsManager.UpdateTopologyOptions(nodeName, func(opts *TopologyOptions) {
			opts.CPUTopology = topo
			for node := 0; node < topo.NumNodes; node++ {
				opts.NUMANodeResources = append(opts.NUMANodeResources, NUMANodeResource{
					Node: node,
					Resources: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("32"),
						corev1.ResourceMemory: resource.MustParse("64Gi"),
					},
				})
			}
		})
	}

	suit.start(b)

	pod := makeCPUBindPod(8)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		topologymanager.InitStore(cycleState)
		_, status := pl.PreFilter(context.TODO(), cycleState, pod, nil)
		if !status.IsSuccess() && !status.IsSkip() {
			b.Fatalf("PreFilter failed: %v", status)
		}
	}
}

func makeNUMANodes(n int) []*corev1.Node {
	nodes := make([]*corev1.Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
				Labels: map[string]string{
					extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicySingleNUMANode),
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("96"),
					corev1.ResourceMemory: resource.MustParse("512Gi"),
				},
			},
		}
	}
	return nodes
}

func makeCPUBindPod(cpus int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bench-pod",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "bench-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(int64(cpus), resource.DecimalSI),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
	}
}

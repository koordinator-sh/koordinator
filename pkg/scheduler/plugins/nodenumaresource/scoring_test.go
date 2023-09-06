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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	apiresource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	st "k8s.io/kubernetes/pkg/scheduler/testing"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestScore(t *testing.T) {
	tests := []struct {
		name           string
		nodes          []*corev1.Node
		numaNodeCounts map[string]int
		requestedPod   *corev1.Pod
		existingPods   []*corev1.Pod
		expectedScores framework.NodeScoreList
		strategy       *schedulerconfig.ScoringStrategy
	}{
		{
			name: "single numa nodes score",
			nodes: []*corev1.Node{
				st.MakeNode().Name("test-node-1").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
					Obj(),
				st.MakeNode().Name("test-node-2").
					Capacity(map[corev1.ResourceName]string{"cpu": "64", "memory": "128Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
					Obj(),
			},
			numaNodeCounts: map[string]int{
				"test-node-1": 2,
				"test-node-2": 1,
			},
			requestedPod: st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "21", "memory": "40Gi"}).Obj(),
			strategy: &schedulerconfig.ScoringStrategy{
				Type: schedulerconfig.MostAllocated,
				Resources: []config.ResourceSpec{
					{
						Name:   string(corev1.ResourceCPU),
						Weight: 1,
					},
					{
						Name:   string(corev1.ResourceMemory),
						Weight: 1,
					},
				},
			},
			expectedScores: []framework.NodeScore{
				{
					Name:  "test-node-1",
					Score: 35,
				},
				{
					Name:  "test-node-2",
					Score: 0,
				},
			},
		},
		{
			name: "restricted numa nodes score",
			nodes: []*corev1.Node{
				st.MakeNode().Name("test-node-1").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
				st.MakeNode().Name("test-node-2").
					Capacity(map[corev1.ResourceName]string{"cpu": "64", "memory": "128Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
			},
			numaNodeCounts: map[string]int{
				"test-node-1": 2,
				"test-node-2": 1,
			},
			requestedPod: st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "54", "memory": "40Gi"}).Obj(),
			strategy: &schedulerconfig.ScoringStrategy{
				Type: schedulerconfig.MostAllocated,
				Resources: []config.ResourceSpec{
					{
						Name:   string(corev1.ResourceCPU),
						Weight: 1,
					},
					{
						Name:   string(corev1.ResourceMemory),
						Weight: 1,
					},
				},
			},
			expectedScores: []framework.NodeScore{
				{
					Name:  "test-node-1",
					Score: 33,
				},
				{
					Name:  "test-node-2",
					Score: 57,
				},
			},
		},
		{
			name: "restricted numa nodes score with same capacity",
			nodes: []*corev1.Node{
				st.MakeNode().Name("test-node-1").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
				st.MakeNode().Name("test-node-2").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
			},
			numaNodeCounts: map[string]int{
				"test-node-1": 2,
				"test-node-2": 2,
			},
			requestedPod: st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "54", "memory": "40Gi"}).Obj(),
			strategy: &schedulerconfig.ScoringStrategy{
				Type: schedulerconfig.MostAllocated,
				Resources: []config.ResourceSpec{
					{
						Name:   string(corev1.ResourceCPU),
						Weight: 1,
					},
					{
						Name:   string(corev1.ResourceMemory),
						Weight: 1,
					},
				},
			},
			expectedScores: []framework.NodeScore{
				{
					Name:  "test-node-1",
					Score: 33,
				},
				{
					Name:  "test-node-2",
					Score: 33,
				},
			},
		},
		{
			name: "single numa nodes score with same capacity but different requested",
			nodes: []*corev1.Node{
				st.MakeNode().Name("test-node-1").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
				st.MakeNode().Name("test-node-2").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
				st.MakeNode().Name("test-node-3").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
			},
			numaNodeCounts: map[string]int{
				"test-node-1": 2,
				"test-node-2": 2,
				"test-node-3": 2,
			},
			requestedPod: st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "40Gi"}).Obj(),
			existingPods: []*corev1.Pod{
				st.MakePod().Node("test-node-1").Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				st.MakePod().Node("test-node-2").Req(map[corev1.ResourceName]string{"cpu": "8", "memory": "32Gi"}).Obj(),
				st.MakePod().Node("test-node-3").Req(map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}).Obj(),
			},
			strategy: &schedulerconfig.ScoringStrategy{
				Type: schedulerconfig.MostAllocated,
				Resources: []config.ResourceSpec{
					{
						Name:   string(corev1.ResourceCPU),
						Weight: 1,
					},
					{
						Name:   string(corev1.ResourceMemory),
						Weight: 1,
					},
				},
			},
			expectedScores: []framework.NodeScore{
				{
					Name:  "test-node-1",
					Score: 26,
				},
				{
					Name:  "test-node-2",
					Score: 39,
				},
				{
					Name:  "test-node-3",
					Score: 65,
				},
			},
		},
		{
			name: "single numa nodes score with same capacity but different requested and LSR",
			nodes: []*corev1.Node{
				st.MakeNode().Name("test-node-1").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
				st.MakeNode().Name("test-node-2").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
				st.MakeNode().Name("test-node-3").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicyRestricted)).
					Obj(),
			},
			numaNodeCounts: map[string]int{
				"test-node-1": 2,
				"test-node-2": 2,
				"test-node-3": 2,
			},
			requestedPod: st.MakePod().
				Label(apiext.LabelPodQoS, string(apiext.QoSLSR)).
				Priority(apiext.PriorityProdValueMax).
				Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "40Gi"}).
				Obj(),
			existingPods: []*corev1.Pod{
				st.MakePod().Node("test-node-1").Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				st.MakePod().Node("test-node-1").UID("123").Label(apiext.LabelPodQoS, string(apiext.QoSLSR)).
					Priority(apiext.PriorityProdValueMax).Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				st.MakePod().Node("test-node-2").Req(map[corev1.ResourceName]string{"cpu": "8", "memory": "32Gi"}).Obj(),
				st.MakePod().Node("test-node-2").UID("123").Label(apiext.LabelPodQoS, string(apiext.QoSLSR)).
					Priority(apiext.PriorityProdValueMax).Req(map[corev1.ResourceName]string{"cpu": "8", "memory": "32Gi"}).Obj(),
				st.MakePod().Node("test-node-3").Req(map[corev1.ResourceName]string{"cpu": "16", "memory": "40Gi"}).Obj(),
				st.MakePod().Node("test-node-3").UID("123").Label(apiext.LabelPodQoS, string(apiext.QoSLSR)).
					Priority(apiext.PriorityProdValueMax).Req(map[corev1.ResourceName]string{"cpu": "16", "memory": "40Gi"}).Obj(),
			},
			strategy: &schedulerconfig.ScoringStrategy{
				Type: schedulerconfig.MostAllocated,
				Resources: []config.ResourceSpec{
					{
						Name:   string(corev1.ResourceCPU),
						Weight: 1,
					},
					{
						Name:   string(corev1.ResourceMemory),
						Weight: 1,
					},
				},
			},
			expectedScores: []framework.NodeScore{
				{
					Name:  "test-node-1",
					Score: 29,
				},
				{
					Name:  "test-node-2",
					Score: 52,
				},
				{
					Name:  "test-node-3",
					Score: 65,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, tt.nodes)
			if tt.strategy != nil {
				suit.nodeNUMAResourceArgs.ScoringStrategy = tt.strategy
			}
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NoError(t, err)
			pl := p.(*Plugin)

			for _, node := range tt.nodes {
				count := tt.numaNodeCounts[node.Name]
				if count > 0 {
					cpusPerNUMANode := node.Status.Allocatable.Cpu().MilliValue() / int64(count)
					memoryPerNUMANode := node.Status.Allocatable.Memory().Value() / int64(count)
					pl.topologyOptionsManager.UpdateTopologyOptions(node.Name, func(options *TopologyOptions) {
						cores := node.Status.Allocatable.Cpu().MilliValue() / 1000 / 2 / int64(count)
						options.CPUTopology = buildCPUTopologyForTest(count, 1, int(cores), 2)
						for i := 0; i < count; i++ {
							options.NUMANodeResources = append(options.NUMANodeResources, NUMANodeResource{
								Node: i,
								Resources: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(cpusPerNUMANode, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(memoryPerNUMANode, resource.BinarySI),
								}})
						}
					})
				}
			}
			for _, v := range tt.existingPods {
				builder := cpuset.NewCPUSetBuilder()
				if AllowUseCPUSet(v) {
					requests, _ := apiresource.PodRequestsAndLimits(v)
					cpuCount := int(requests.Cpu().MilliValue() / 1000)
					for i := 0; i < cpuCount; i++ {
						builder.Add(i)
					}
				}
				pl.resourceManager.Update(v.Spec.NodeName, &PodAllocation{
					UID:       v.UID,
					Namespace: v.Namespace,
					Name:      v.Name,
					CPUSet:    builder.Result(),
					NUMANodeResources: []NUMANodeResource{
						{
							Node:      0,
							Resources: v.Spec.Containers[0].Resources.Requests,
						},
					},
				})
			}

			cycleState := framework.NewCycleState()
			_, status := pl.PreFilter(context.TODO(), cycleState, tt.requestedPod)
			assert.True(t, status.IsSuccess())

			var gotScores framework.NodeScoreList
			for _, n := range tt.nodes {
				nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get(n.Name)
				assert.NoError(t, err)
				status = pl.Filter(context.TODO(), cycleState, tt.requestedPod, nodeInfo)
				assert.True(t, status.IsSuccess())
				score, status := p.(framework.ScorePlugin).Score(context.TODO(), cycleState, tt.requestedPod, n.Name)
				assert.True(t, status.IsSuccess())
				gotScores = append(gotScores, framework.NodeScore{Name: n.Name, Score: score})
			}
			assert.Equal(t, tt.expectedScores, gotScores)
		})
	}
}

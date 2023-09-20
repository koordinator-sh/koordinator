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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiresource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	st "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestNUMANodeScore(t *testing.T) {
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
					Score: 31,
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

func TestPlugin_Score(t *testing.T) {
	tests := []struct {
		name        string
		nodeLabels  map[string]string
		state       *preFilterState
		pod         *corev1.Pod
		cpuTopology *CPUTopology
		want        *framework.Status
		wantScore   int64
	}{
		{
			name: "error with missing preFilterState",
			pod:  &corev1.Pod{},
			want: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name: "error with missing allocationState",
			state: &preFilterState{
				requestCPUBind: true,
			},
			pod:       &corev1.Pod{},
			want:      nil,
			wantScore: 0,
		},
		{
			name: "error with invalid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology: &CPUTopology{},
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   0,
		},
		{
			name: "succeed with skip",
			state: &preFilterState{
				requestCPUBind: false,
			},
			pod:       &corev1.Pod{},
			want:      nil,
			wantScore: 0,
		},
		{
			name: "score with full empty node FullPCPUs",
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   25,
		},
		{
			name: "score with satisfied node FullPCPUs",
			state: &preFilterState{
				requestCPUBind: true,

				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          8,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   50,
		},
		{
			name: "score with full empty node SpreadByPCPUs",
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   25,
		},
		{
			name: "score with exceed socket FullPCPUs",
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          16,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   100,
		},
		{
			name: "score with satisfied socket FullPCPUs",
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          16,
			},
			cpuTopology: buildCPUTopologyForTest(2, 2, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   50,
		},
		{
			name: "score with full empty socket SpreadByPCPUs",
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   25,
		},
		{
			name: "score with Node NUMA Allocate Strategy",
			nodeLabels: map[string]string{
				extension.LabelNodeNUMAAllocateStrategy: string(extension.NodeNUMAAllocateStrategyLeastAllocated),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          2,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   12,
		},
		{
			name: "score with Node CPU Bind Policy",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          8,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   50,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			totalCPUs := 96
			if tt.cpuTopology != nil {
				totalCPUs = tt.cpuTopology.CPUDetails.CPUs().Size()
			}
			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node-1",
						Labels: map[string]string{},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(totalCPUs*1000), resource.DecimalSI),
							corev1.ResourceMemory: resource.MustParse("512Gi"),
						},
					},
				},
			}
			for k, v := range tt.nodeLabels {
				nodes[0].Labels[k] = v
			}

			suit := newPluginTestSuit(t, nodes)
			suit.nodeNUMAResourceArgs.ScoringStrategy = &schedulerconfig.ScoringStrategy{
				Type: schedulingconfig.MostAllocated,
				Resources: []config.ResourceSpec{
					{
						Name:   "cpu",
						Weight: 1,
					},
				},
			}
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			plg := p.(*Plugin)
			allocateState := NewNodeAllocation("test-node-1")
			if tt.cpuTopology != nil {
				plg.topologyOptionsManager.UpdateTopologyOptions(allocateState.nodeName, func(options *TopologyOptions) {
					options.CPUTopology = tt.cpuTopology
				})
			}

			cpuManager := plg.resourceManager.(*resourceManager)
			cpuManager.nodeAllocations[allocateState.nodeName] = allocateState

			suit.start()

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				if tt.state.numCPUsNeeded > 0 {
					tt.state.requests = corev1.ResourceList{
						corev1.ResourceCPU: *resource.NewMilliQuantity(int64(tt.state.numCPUsNeeded)*1000, resource.DecimalSI),
					}
				}
				cycleState.Write(stateKey, tt.state)
			}

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("test-node-1")
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			gotScore, gotStatus := plg.Score(context.TODO(), cycleState, tt.pod, "test-node-1")
			if !reflect.DeepEqual(gotStatus, tt.want) {
				t.Errorf("Score() = %v, want %v", gotStatus, tt.want)
			}
			if !tt.want.IsSuccess() {
				return
			}
			assert.Equal(t, tt.wantScore, gotScore)
		})
	}
}

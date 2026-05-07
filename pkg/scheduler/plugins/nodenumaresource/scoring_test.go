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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	apiresource "k8s.io/component-helpers/resource"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	st "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

var (
	defaultResources = []config.ResourceSpec{
		{Name: string(corev1.ResourceCPU), Weight: 1},
		{Name: string(corev1.ResourceMemory), Weight: 1},
	}
)

func TestNUMANodeScore(t *testing.T) {
	tests := []struct {
		name           string
		nodes          []*corev1.Node
		numaNodeCounts map[string]int
		requestedPod   *corev1.Pod
		existingPods   []*corev1.Pod
		expectedScores fwktype.NodeScoreList
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
			expectedScores: []fwktype.NodeScore{
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
			requestedPod: st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "50", "memory": "40Gi"}).Obj(),
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
			expectedScores: []fwktype.NodeScore{
				{
					Name:  "test-node-1",
					Score: 63,
				},
				{
					Name:  "test-node-2",
					Score: 54,
				},
			},
		},
		{
			name: "single numa nodes score with same capacity but different requested",
			nodes: []*corev1.Node{
				st.MakeNode().Name("test-node-1").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
					Obj(),
				st.MakeNode().Name("test-node-2").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
					Obj(),
				st.MakeNode().Name("test-node-3").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
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
			expectedScores: []fwktype.NodeScore{
				{
					Name:  "test-node-1",
					Score: 19,
				},
				{
					Name:  "test-node-2",
					Score: 19,
				},
				{
					Name:  "test-node-3",
					Score: 19,
				},
			},
		},
		{
			name: "single numa nodes score with same capacity but different requested and LSR",
			nodes: []*corev1.Node{
				st.MakeNode().Name("test-node-1").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
					Obj(),
				st.MakeNode().Name("test-node-2").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
					Obj(),
				st.MakeNode().Name("test-node-3").
					Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
					Label(apiext.LabelNUMATopologyPolicy, string(apiext.NUMATopologyPolicySingleNUMANode)).
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
			expectedScores: []fwktype.NodeScore{
				{
					Name:  "test-node-1",
					Score: 23,
				},
				{
					Name:  "test-node-2",
					Score: 27,
				},
				{
					Name:  "test-node-3",
					Score: 34,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, tt.nodes)
			if tt.strategy != nil {
				suit.nodeNUMAResourceArgs.ScoringStrategy = tt.strategy
			}
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
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
					requests := apiresource.PodRequests(v, apiresource.PodResourcesOptions{})
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
			_, status := pl.PreFilter(context.TODO(), cycleState, tt.requestedPod, nil)
			assert.True(t, status.IsSuccess())

			var gotScores fwktype.NodeScoreList
			for _, n := range tt.nodes {
				nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get(n.Name)
				assert.NoError(t, err)
				status = pl.Filter(context.TODO(), cycleState, tt.requestedPod, nodeInfo)
				assert.True(t, status.IsSuccess())
				score, status := p.(fwktype.ScorePlugin).Score(context.TODO(), cycleState, tt.requestedPod, nodeInfo)
				assert.True(t, status.IsSuccess())
				gotScores = append(gotScores, fwktype.NodeScore{Name: n.Name, Score: score})
			}
			assert.Equal(t, tt.expectedScores, gotScores)
		})
	}
}

func TestPlugin_Score(t *testing.T) {
	node0, _ := bitmask.NewBitMask(0)
	tests := []struct {
		name                      string
		nodeLabels                map[string]string
		state                     *preFilterState
		pod                       *corev1.Pod
		cpuTopology               *CPUTopology
		allocatedCPUs             []int
		matched                   map[types.UID]reusableAlloc
		reservationAllocatePolicy schedulingv1alpha1.ReservationAllocatePolicy
		numaAffinity              bitmask.BitMask
		skipNominate              bool
		mergedMatchedAllocated    map[int]corev1.ResourceList
		extraNUMAAllocations      []NUMANodeResource
		want                      *fwktype.Status
		wantScore                 int64
	}{
		{
			name: "error with missing preFilterState",
			pod:  &corev1.Pod{},
			want: fwktype.AsStatus(fwktype.ErrNotFound),
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
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicyFullPCPUs,
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

				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicyFullPCPUs,
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
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicySpreadByPCPUs,
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
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicyFullPCPUs,
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
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicyFullPCPUs,
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
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicySpreadByPCPUs,
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
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicySpreadByPCPUs,
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
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          8,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   50,
		},
		// ---- Score degradation: allocateWithNominated fails → (0, nil) ----
		{
			// Pod has a nominated Restricted R on NUMA1 but BestHint=NUMA0, no reservation-affinity.
			// tryAllocateFromReusable returns (nil, nil); allocateWithNominated returns Unschedulable.
			// Score degrades to (0, nil) instead of propagating the error (which would cause
			// the framework to upgrade it to SchedulerError on the Pod condition).
			name: "Score degrades to (0,nil) when allocateWithNominated fails",
			state: &preFilterState{
				numCPUsNeeded:          4,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						1: {corev1.ResourceCPU: resource.MustParse("8")},
					},
					remained: map[int]corev1.ResourceList{
						1: {corev1.ResourceCPU: resource.MustParse("8")},
					},
					remainedCPUs: cpuset.NewCPUSet(8, 9, 10, 11, 12, 13, 14, 15),
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      nil,
			wantScore:                 0,
		},
		// ---- Score degradation: tryAllocateFromNode fails → (0, nil) ----
		{
			// No nominated R; tryAllocateFromNode returns Insufficient NUMA cpu because
			// all 8 cpu on hinted NUMA0 are fully allocated. Score degrades to (0, nil).
			name: "Score degrades to (0,nil) when tryAllocateFromNode fails",
			state: &preFilterState{
				numCPUsNeeded:          4,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicyFullPCPUs,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			extraNUMAAllocations: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}},
			},
			pod:          &corev1.Pod{},
			numaAffinity: node0,
			want:         nil,
			wantScore:    0,
		},
		// ---- Score: tryAllocateFromNode mergedMatchedAllocated fix ----
		{
			// Pod has no reservation-affinity, not nominated, falls back to tryAllocateFromNode.
			// Node NUMA0 has 8 cpu total; matched R allocatable=7, owner allocated=4, double-count
			// makes the NUMA account 11 cpu. With mergedMatchedAllocated=4, available=
			// max(0,8-max(0,11-4))=1 cpu, so a 1-cpu pod can be scored.
			// Before the tryAllocateFromNode fix, reusableResources omitted mergedMatchedAllocated
			// so available=0 and Score returned Unschedulable (now degraded to 0,nil).
			name: "Score succeeds with mergedMatchedAllocated offsetting double-count",
			state: &preFilterState{
				numCPUsNeeded:          1,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulerconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("7")},
					},
					remained: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("3")},
					},
				},
			},
			skipNominate: true,
			extraNUMAAllocations: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("11")}},
			},
			mergedMatchedAllocated: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("4")},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      nil,
			wantScore:                 100, // MostAllocated: requested(14)=allocated(18)-reusable(4) on NUMA0 / allocatable(8) → clamped to 100
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

			suit := newPluginTestSuit(t, nil, nodes)
			suit.nodeNUMAResourceArgs.ScoringStrategy = &schedulerconfig.ScoringStrategy{
				Type: schedulerconfig.MostAllocated,
				Resources: []config.ResourceSpec{
					{
						Name:   "cpu",
						Weight: 1,
					},
				},
			}
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			plg := p.(*Plugin)
			allocateState := NewNodeAllocation("test-node-1")
			if tt.cpuTopology != nil {
				plg.topologyOptionsManager.UpdateTopologyOptions(allocateState.nodeName, func(options *TopologyOptions) {
					options.CPUTopology = tt.cpuTopology
					for i := 0; i < tt.cpuTopology.NumNodes; i++ {
						options.NUMANodeResources = append(options.NUMANodeResources, NUMANodeResource{
							Node: i,
							Resources: corev1.ResourceList{
								corev1.ResourceCPU: *resource.NewQuantity(int64(tt.cpuTopology.CPUsPerNode()), resource.DecimalSI),
							},
						})
					}
				})
				if len(tt.allocatedCPUs) > 0 {
					allocateState.addCPUs(tt.cpuTopology, uuid.NewUUID(), cpuset.NewCPUSet(tt.allocatedCPUs...), schedulerconfig.CPUExclusivePolicyNone)
				}
				if len(tt.matched) > 0 {
					for reservationUID, alloc := range tt.matched {
						allocateState.addCPUs(tt.cpuTopology, reservationUID, alloc.remainedCPUs, schedulerconfig.CPUExclusivePolicyNone)
						var numaResources []NUMANodeResource
						for i, allocatable := range alloc.allocatable {
							numaResources = append(numaResources, NUMANodeResource{
								Node:      i,
								Resources: allocatable,
							})
						}
						allocateState.addPodAllocation(&PodAllocation{
							UID:                reservationUID,
							CPUSet:             alloc.remainedCPUs,
							CPUExclusivePolicy: schedulerconfig.CPUExclusivePolicyNone,
							NUMANodeResources:  numaResources,
						}, tt.cpuTopology)
					}
				}
				if len(tt.extraNUMAAllocations) > 0 {
					allocateState.addPodAllocation(&PodAllocation{
						UID:               uuid.NewUUID(),
						NUMANodeResources: tt.extraNUMAAllocations,
					}, tt.cpuTopology)
				}
			}

			cpuManager := plg.resourceManager.(*resourceManager)
			cpuManager.nodeAllocations[allocateState.nodeName] = allocateState

			suit.start(t)

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				if tt.state.numCPUsNeeded > 0 {
					tt.state.requests = corev1.ResourceList{
						corev1.ResourceCPU: *resource.NewMilliQuantity(int64(tt.state.numCPUsNeeded)*1000, resource.DecimalSI),
					}
				}
				cycleState.Write(stateKey, tt.state)
				if len(tt.matched) > 0 {
					for reservationUID, alloc := range tt.matched {
						reservation := &schedulingv1alpha1.Reservation{
							ObjectMeta: metav1.ObjectMeta{
								UID:  reservationUID,
								Name: "test-reservation",
							},
							Spec: schedulingv1alpha1.ReservationSpec{
								AllocatePolicy: tt.reservationAllocatePolicy,
							},
						}
						alloc.rInfo = frameworkext.NewReservationInfo(reservation)
						tt.matched[reservationUID] = alloc
						if !tt.skipNominate {
							rInfo := frameworkext.NewReservationInfo(reservation)
							plg.handle.GetReservationNominator().AddNominatedReservation(tt.pod, "test-node-1", rInfo)
						}
					}
					cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
						nodeToState: frameworkext.NodeReservationRestoreStates{
							"test-node-1": &nodeReservationRestoreStateData{
								matched:                tt.matched,
								mergedMatchedAllocated: tt.mergedMatchedAllocated,
							},
						},
					})
				}
			}

			if tt.numaAffinity != nil {
				topologymanager.InitStore(cycleState)
				store := topologymanager.GetStore(cycleState)
				store.SetAffinity("test-node-1", topologymanager.NUMATopologyHint{NUMANodeAffinity: tt.numaAffinity})
			}

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("test-node-1")
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			gotScore, gotStatus := plg.Score(context.TODO(), cycleState, tt.pod, nodeInfo)
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

func TestScoreWithAmplifiedCPUs(t *testing.T) {
	tests := []struct {
		name          string
		args          schedulerconfig.NodeNUMAResourceArgs
		requestedPod  *corev1.Pod
		nodes         []*corev1.Node
		existingPods  []*corev1.Pod
		cpuTopologies map[string]*CPUTopology
		nodeHasNRT    []string
		nodeRatios    map[string]extension.Ratio
		wantScoreList fwktype.NodeScoreList
	}{
		{
			name:         "ScoringStrategy MostAllocated, non-cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
				makeNode("node3", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 2.0),
			},
			nodeRatios:    map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0, "node3": 2.0},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 16}, {Name: "node3", Score: 26}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy MostAllocated, cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
				makeNode("node3", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0, "node3": 2.0},
			nodeHasNRT: []string{"node1", "node2", "node3"},
			cpuTopologies: map[string]*CPUTopology{
				"node1": buildCPUTopologyForTest(2, 1, 8, 2),
				"node2": buildCPUTopologyForTest(2, 1, 8, 2),
				"node3": buildCPUTopologyForTest(2, 1, 8, 2),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 19}, {Name: "node3", Score: 32}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy MostAllocated, non-cpuset pods, and existing cpuset pod on node",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0},
			nodeHasNRT: []string{"node1", "node2"},
			cpuTopologies: map[string]*CPUTopology{
				"node1": buildCPUTopologyForTest(2, 1, 8, 2),
				"node2": buildCPUTopologyForTest(2, 1, 8, 2),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node2", true),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 68}, {Name: "node2", Score: 35}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy MostAllocated, scheduling cpuset pod with existing non-cpuset pods",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0},
			nodeHasNRT: []string{"node1", "node2"},
			cpuTopologies: map[string]*CPUTopology{
				"node1": buildCPUTopologyForTest(2, 1, 8, 2),
				"node2": buildCPUTopologyForTest(2, 1, 8, 2),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node1", false),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node2", false),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 68}, {Name: "node2", Score: 30}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy MostAllocated, cpuset pods on node, scheduling cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0},
			nodeHasNRT: []string{"node1", "node2"},
			cpuTopologies: map[string]*CPUTopology{
				"node1": buildCPUTopologyForTest(2, 1, 8, 2),
				"node2": buildCPUTopologyForTest(2, 1, 8, 2),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node2", true),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 68}, {Name: "node2", Score: 38}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, no cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node1", false),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node2", false),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 31}, {Name: "node2", Score: 72}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, non-cpuset pod with existing cpuset pods",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0},
			nodeHasNRT: []string{"node1", "node2"},
			cpuTopologies: map[string]*CPUTopology{
				"node1": buildCPUTopologyForTest(2, 1, 8, 2),
				"node2": buildCPUTopologyForTest(2, 1, 8, 2),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node2", true),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 31}, {Name: "node2", Score: 64}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, scheduling cpuset pod with existing non-cpuset pods",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0},
			nodeHasNRT: []string{"node1", "node2"},
			cpuTopologies: map[string]*CPUTopology{
				"node1": buildCPUTopologyForTest(2, 1, 8, 2),
				"node2": buildCPUTopologyForTest(2, 1, 8, 2),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node1", false),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node2", false),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 31}, {Name: "node2", Score: 68}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, cpuset pods on node,scheduling cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "8", "memory": "16Gi"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "32", "memory": "40Gi"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "64", "memory": "60Gi"}, 2.0),
			},
			nodeRatios: map[string]apiext.Ratio{"node1": 1.0, "node2": 2.0},
			nodeHasNRT: []string{"node1", "node2"},
			cpuTopologies: map[string]*CPUTopology{
				"node1": buildCPUTopologyForTest(2, 1, 8, 2),
				"node2": buildCPUTopologyForTest(2, 1, 8, 2),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "20", "memory": "4Gi"}, "node2", true),
			},
			wantScoreList: []fwktype.NodeScore{{Name: "node1", Score: 31}, {Name: "node2", Score: 61}},
			args: schedulerconfig.NodeNUMAResourceArgs{
				ScoringStrategy: &schedulerconfig.ScoringStrategy{
					Type:      schedulerconfig.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, tt.existingPods, tt.nodes)
			suit.nodeNUMAResourceArgs.ScoringStrategy = tt.args.ScoringStrategy
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NoError(t, err)
			suit.start(t)

			pl := p.(*Plugin)

			for _, nodeName := range tt.nodeHasNRT {
				cpuTopology := tt.cpuTopologies[nodeName]
				if cpuTopology == nil {
					continue
				}
				ratio := tt.nodeRatios[nodeName]
				if ratio == 0 {
					ratio = 1
				}
				topologyOptions := TopologyOptions{
					CPUTopology: cpuTopology,
				}
				for i := 0; i < cpuTopology.NumNodes; i++ {
					topologyOptions.NUMANodeResources = append(topologyOptions.NUMANodeResources, NUMANodeResource{
						Node: i,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", extension.Amplify(int64(cpuTopology.CPUsPerNode()), ratio))),
							corev1.ResourceMemory: resource.MustParse("20Gi"),
						}})
				}
				pl.topologyOptionsManager.UpdateTopologyOptions(nodeName, func(options *TopologyOptions) {
					*options = topologyOptions
				})
			}

			handler := &podEventHandler{resourceManager: pl.resourceManager}
			for _, v := range tt.existingPods {
				handler.OnAdd(v, true)
			}

			state := framework.NewCycleState()
			_, status := pl.PreFilter(context.TODO(), state, tt.requestedPod, nil)
			assert.True(t, status.IsSuccess())

			var gotScoreList fwktype.NodeScoreList
			for _, n := range tt.nodes {
				nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get(n.Name)
				assert.NoError(t, err)
				score, status := p.(fwktype.ScorePlugin).Score(context.TODO(), state, tt.requestedPod, nodeInfo)
				if !status.IsSuccess() {
					t.Errorf("unexpected error: %v", status)
				}
				gotScoreList = append(gotScoreList, fwktype.NodeScore{Name: n.Name, Score: score})
			}
			assert.Equal(t, tt.wantScoreList, gotScoreList)
		})
	}
}

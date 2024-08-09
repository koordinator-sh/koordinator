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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestPlugin_AddPod(t *testing.T) {
	skipState := framework.NewCycleState()
	skipState.Write(stateKey, &preFilterState{
		skip: true,
	})
	testState := framework.NewCycleState()
	testState.Write(stateKey, &preFilterState{
		skip: false,
	})
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
			UID:  uuid.NewUUID(),
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
			UID: uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Priority: pointer.Int32(extension.PriorityProdValueMax),
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}
	testPodInfo, err := framework.NewPodInfo(testPod)
	assert.NoError(t, err)
	testNodeInfo := framework.NewNodeInfo(testPod)
	testNodeInfo.SetNode(testNode)
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	topologyOptionsManager := NewTopologyOptionsManager()
	topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
		options.CPUTopology = cpuTopology
	})
	testRM := &resourceManager{
		topologyOptionsManager: topologyOptionsManager,
		nodeAllocations:        map[string]*NodeAllocation{},
	}
	testRM.Update(testNode.Name, &PodAllocation{
		UID:                testPod.UID,
		CPUSet:             cpuset.NewCPUSet(0, 1, 8, 9),
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyPCPULevel,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("2"),
				},
			},
			{
				Node: 1,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("2"),
				},
			},
		},
	})
	type fields struct {
		resourceManager ResourceManager
	}
	type args struct {
		cycleState   *framework.CycleState
		podInfoToAdd *framework.PodInfo
		nodeInfo     *framework.NodeInfo
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *framework.Status
		wantFn func(t *testing.T, state *framework.CycleState)
	}{
		{
			name: "failed to get state",
			args: args{
				cycleState: framework.NewCycleState(),
				nodeInfo:   testNodeInfo,
			},
			want: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name: "state skips",
			args: args{
				cycleState: skipState,
				nodeInfo:   testNodeInfo,
			},
			want: nil,
		},
		{
			name: "nil pod to add",
			args: args{
				cycleState:   testState,
				podInfoToAdd: nil,
				nodeInfo:     testNodeInfo,
			},
			want: nil,
		},
		{
			name: "add pod for node resources",
			fields: fields{
				resourceManager: testRM,
			},
			args: args{
				cycleState:   testState,
				podInfoToAdd: testPodInfo,
				nodeInfo:     testNodeInfo,
			},
			want: nil,
			wantFn: func(t *testing.T, state *framework.CycleState) {
				stateData, status := getPreFilterState(state)
				assert.True(t, status.IsSuccess())
				assert.False(t, stateData.skip)
				assert.NotNil(t, stateData.preemptibleState)
				assert.NotNil(t, stateData.preemptibleState[testNode.Name])
				assert.NotNil(t, stateData.preemptibleState[testNode.Name].nodeAlloc)
				assert.Equal(t, cpuset.NewCPUSet(), stateData.preemptibleState[testNode.Name].nodeAlloc.cpusToAdd)
				assert.Equal(t, cpuset.NewCPUSet(0, 1, 8, 9), stateData.preemptibleState[testNode.Name].nodeAlloc.cpusToRemove)
				assert.Equal(t, map[int]corev1.ResourceList{
					0: {
						corev1.ResourceCPU: *resource.NewQuantity(-2, resource.DecimalSI),
					},
					1: {
						corev1.ResourceCPU: *resource.NewQuantity(-2, resource.DecimalSI),
					},
				}, stateData.preemptibleState[testNode.Name].nodeAlloc.numaResources)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, []*corev1.Node{tt.args.nodeInfo.Node()})
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			pl, ok := p.(*Plugin)
			assert.True(t, ok)
			pl.resourceManager = tt.fields.resourceManager
			got := pl.AddPod(context.TODO(), tt.args.cycleState, &corev1.Pod{}, tt.args.podInfoToAdd, tt.args.nodeInfo)
			assert.Equal(t, tt.want, got)
			if tt.wantFn != nil {
				tt.wantFn(t, tt.args.cycleState)
			}
		})
	}
}

func TestPlugin_RemovePod(t *testing.T) {
	skipState := framework.NewCycleState()
	skipState.Write(stateKey, &preFilterState{
		skip: true,
	})
	testState := framework.NewCycleState()
	testState.Write(stateKey, &preFilterState{
		skip: false,
	})
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
			UID:  uuid.NewUUID(),
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
			UID: uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Priority: pointer.Int32(extension.PriorityProdValueMax),
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}
	testPodInfo, err := framework.NewPodInfo(testPod)
	assert.NoError(t, err)
	testNodeInfo := framework.NewNodeInfo(testPod)
	testNodeInfo.SetNode(testNode)
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	topologyOptionsManager := NewTopologyOptionsManager()
	topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
		options.CPUTopology = cpuTopology
	})
	testRM := &resourceManager{
		topologyOptionsManager: topologyOptionsManager,
		nodeAllocations:        map[string]*NodeAllocation{},
	}
	testRM.Update(testNode.Name, &PodAllocation{
		UID:                testPod.UID,
		CPUSet:             cpuset.NewCPUSet(0, 1, 8, 9),
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyPCPULevel,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("2"),
				},
			},
			{
				Node: 1,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("2"),
				},
			},
		},
	})
	type fields struct {
		resourceManager ResourceManager
	}
	type args struct {
		cycleState      *framework.CycleState
		podInfoToRemove *framework.PodInfo
		nodeInfo        *framework.NodeInfo
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *framework.Status
		wantFn func(t *testing.T, state *framework.CycleState)
	}{
		{
			name: "failed to get state",
			args: args{
				cycleState: framework.NewCycleState(),
				nodeInfo:   testNodeInfo,
			},
			want: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name: "state skips",
			args: args{
				cycleState: skipState,
				nodeInfo:   testNodeInfo,
			},
			want: nil,
		},
		{
			name: "nil pod to add",
			args: args{
				cycleState:      testState,
				podInfoToRemove: nil,
				nodeInfo:        testNodeInfo,
			},
			want: nil,
		},
		{
			name: "add pod for node resources",
			fields: fields{
				resourceManager: testRM,
			},
			args: args{
				cycleState:      testState,
				podInfoToRemove: testPodInfo,
				nodeInfo:        testNodeInfo,
			},
			want: nil,
			wantFn: func(t *testing.T, state *framework.CycleState) {
				stateData, status := getPreFilterState(state)
				assert.True(t, status.IsSuccess())
				assert.False(t, stateData.skip)
				assert.NotNil(t, stateData.preemptibleState)
				assert.NotNil(t, stateData.preemptibleState[testNode.Name])
				assert.NotNil(t, stateData.preemptibleState[testNode.Name].nodeAlloc)
				assert.Equal(t, cpuset.NewCPUSet(0, 1, 8, 9), stateData.preemptibleState[testNode.Name].nodeAlloc.cpusToAdd)
				assert.Equal(t, cpuset.NewCPUSet(), stateData.preemptibleState[testNode.Name].nodeAlloc.cpusToRemove)
				assert.Equal(t, map[int]corev1.ResourceList{
					0: {
						corev1.ResourceCPU: resource.MustParse("2"),
					},
					1: {
						corev1.ResourceCPU: resource.MustParse("2"),
					},
				}, stateData.preemptibleState[testNode.Name].nodeAlloc.numaResources)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, []*corev1.Node{tt.args.nodeInfo.Node()})
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			pl, ok := p.(*Plugin)
			assert.True(t, ok)
			pl.resourceManager = tt.fields.resourceManager
			got := pl.RemovePod(context.TODO(), tt.args.cycleState, &corev1.Pod{}, tt.args.podInfoToRemove, tt.args.nodeInfo)
			assert.Equal(t, tt.want, got)
			if tt.wantFn != nil {
				tt.wantFn(t, tt.args.cycleState)
			}
		})
	}
}

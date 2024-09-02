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
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func TestReserveByNUMANode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				apiext.LabelNUMATopologyPolicy: string(apiext.NUMATopologyPolicySingleNUMANode),
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("104"),
				corev1.ResourceMemory: resource.MustParse("256Gi"),
			},
		},
	}
	suit := newPluginTestSuit(t, nil, []*corev1.Node{node})

	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	pl.topologyOptionsManager.UpdateTopologyOptions(node.Name, func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(2, 1, 26, 2)
		options.NUMANodeResources = []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("52"),
					corev1.ResourceMemory: resource.MustParse("128Gi"),
				},
			},
			{
				Node: 1,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("52"),
					corev1.ResourceMemory: resource.MustParse("128Gi"),
				},
			},
		}
	})

	cycleState := framework.NewCycleState()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "123456",
			Namespace: "default",
			Name:      "test",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
		},
	}
	_, status := pl.PreFilter(context.TODO(), cycleState, pod)
	assert.True(t, status.IsSuccess())

	nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(node.Name)
	assert.NoError(t, err)
	status = pl.Filter(context.TODO(), cycleState, pod, nodeInfo)
	assert.True(t, status.IsSuccess())
	pod.Spec.NodeName = node.Name
	status = pl.Reserve(context.TODO(), cycleState, pod, node.Name)
	assert.True(t, status.IsSuccess())
	state, status := getPreFilterState(cycleState)
	assert.True(t, status.IsSuccess())
	expectPodAllocation := &PodAllocation{
		UID:       pod.UID,
		Namespace: pod.Namespace,
		Name:      pod.Name,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
	}
	for i := range expectPodAllocation.NUMANodeResources {
		assert.Equal(t, expectPodAllocation.NUMANodeResources[i].Node, state.allocation.NUMANodeResources[i].Node)
		assert.True(t, quotav1.Equals(expectPodAllocation.NUMANodeResources[i].Resources, state.allocation.NUMANodeResources[i].Resources))
	}
	expectPodAllocation.NUMANodeResources = nil
	state.allocation.NUMANodeResources = nil
	assert.Equal(t, expectPodAllocation, state.allocation)

}

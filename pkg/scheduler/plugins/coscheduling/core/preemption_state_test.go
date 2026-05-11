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

package core

import (
	"context"
	"testing"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func Test_preemptionEvaluatorImpl_placeToSchedulePods_StatePersistence(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:  resource.MustParse("20"),
				corev1.ResourcePods: resource.MustParse("110"),
			},
		},
	}
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default", UID: "pod1"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10")},
				},
			}},
		},
	}
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default", UID: "pod2"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10")},
				},
			}},
		},
	}
	// pod3 should not fit because pod1 and pod2 already took all 20 CPU
	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod3", Namespace: "default", UID: "pod3"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10")},
				},
			}},
		},
	}

	fh := NewFakeExtendedFramework(t, []*corev1.Node{node}, nil, nil, nil, nil)
	ev := &preemptionEvaluatorImpl{
		handle: fh.(frameworkext.ExtendedHandle),
	}

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	toSchedulePods := []*corev1.Pod{pod1, pod2, pod3}
	cycleStates := map[string]fwktype.CycleState{
		"node1": framework.NewCycleState(),
	}
	potentialNodes := []fwktype.NodeInfo{nodeInfo}
	preemptionCosts := map[string]int{"node1": 0}

	// This addPod mock simulates what happens in real code: it updates nodeInfo
	// We capture the state here to verify that assumedCycleStates is populated
	var capturedState fwktype.CycleState
	addPod := func(state fwktype.CycleState, pod *corev1.Pod, podInfo fwktype.PodInfo, nodeInfo fwktype.NodeInfo) error {
		nodeInfo.AddPodInfo(podInfo)
		capturedState = state
		return nil
	}

	statusMap := map[string]*fwktype.Status{}
	ctx := contextWithJobPreemptionState(context.Background(), &JobPreemptionState{})

	podToNominatedNode, _, unschedulablePods := ev.placeToSchedulePods(ctx, toSchedulePods, cycleStates, potentialNodes, preemptionCosts, addPod, statusMap)

	// Verify that cycle state was captured (meaning it was cloned/assigned)
	assert.NotNil(t, capturedState)

	// Verify that only pod1 and pod2 were assigned to node1
	assert.Equal(t, "node1", podToNominatedNode["default/pod1"])
	assert.Equal(t, "node1", podToNominatedNode["default/pod2"])
	_, pod3Assigned := podToNominatedNode["default/pod3"]
	assert.False(t, pod3Assigned)

	// Verify that pod3 is unschedulable
	assert.Equal(t, 1, len(unschedulablePods))
	assert.Equal(t, "pod3", unschedulablePods[0].Name)
}

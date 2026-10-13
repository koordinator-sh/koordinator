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

package reservation

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	apiresource "k8s.io/component-helpers/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestNominateReservation(t *testing.T) {
	reservation4C8G := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation4C8G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
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
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}
	reservation2C4G := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation2C4G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}
	tests := []struct {
		name            string
		pod             *corev1.Pod
		reservations    []*schedulingv1alpha1.Reservation
		allocated       map[types.UID]corev1.ResourceList
		wantReservation *schedulingv1alpha1.Reservation
		wantStatus      bool
	}{
		{
			name: "reserve pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						reservationutil.AnnotationReservePod: "true",
					},
				},
			},
			wantStatus: true,
		},
		{
			name:       "node without reservations",
			pod:        &corev1.Pod{},
			wantStatus: true,
		},
		{
			name: "preferred reservation",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			reservations: []*schedulingv1alpha1.Reservation{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "preferred-reservation",
						Labels: map[string]string{
							apiext.LabelReservationOrder: "100",
						},
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Template: &corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("2"),
												corev1.ResourceMemory: resource.MustParse("4Gi"),
											},
										},
									},
								},
							},
						},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						NodeName: "test-node",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "normal-reservation",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Template: &corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("2"),
												corev1.ResourceMemory: resource.MustParse("4Gi"),
											},
										},
									},
								},
							},
						},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						NodeName: "test-node",
					},
				},
			},
			wantReservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "preferred-reservation",
					Labels: map[string]string{
						apiext.LabelReservationOrder: "100",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
						},
					},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
				},
			},
			wantStatus: true,
		},
		{
			name: "allocated reservation",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			reservations: []*schedulingv1alpha1.Reservation{
				reservation4C8G,
				reservation2C4G,
			},
			allocated: map[types.UID]corev1.ResourceList{
				reservation2C4G.UID: {
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			wantStatus:      true,
			wantReservation: reservation4C8G,
		},
		{
			name: "matched reservations",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			reservations: []*schedulingv1alpha1.Reservation{
				reservation4C8G,
				reservation2C4G,
			},
			wantStatus:      true,
			wantReservation: reservation2C4G,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{},
			}

			suit := newPluginTestSuitWith(t, nil, []*corev1.Node{node})
			plugin, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := plugin.(*Plugin)
			cycleState := framework.NewCycleState()
			requests := apiresource.PodRequests(tt.pod, apiresource.PodResourcesOptions{})
			state := &stateData{
				schedulingStateData: schedulingStateData{
					nodeReservationStates: map[string]*nodeReservationState{},
					podRequests:           requests,
					podRequestsResources:  framework.NewResource(requests),
					podResourceNames:      quotav1.ResourceNames(requests),
				},
			}
			for _, reservation := range tt.reservations {
				rInfo := frameworkext.NewReservationInfo(reservation)
				if allocated := tt.allocated[reservation.UID]; len(allocated) > 0 {
					rInfo.Allocated = allocated
				}
				nodeRState := state.nodeReservationStates[reservation.Status.NodeName]
				if nodeRState == nil {
					nodeRState = &nodeReservationState{}
				}
				nodeRState.nodeName = reservation.Status.NodeName
				nodeRState.matchedOrIgnored = append(nodeRState.matchedOrIgnored, rInfo)
				state.nodeReservationStates[reservation.Status.NodeName] = nodeRState
				pl.reservationCache.updateReservation(reservation)
			}
			cycleState.Write(stateKey, state)
			nominateRInfo, status := pl.NominateReservation(context.TODO(), cycleState, tt.pod, node.Name)
			if tt.wantReservation == nil {
				assert.Nil(t, nominateRInfo)
			} else {
				assert.NotNil(t, nominateRInfo)
				assert.Equal(t, tt.wantReservation, nominateRInfo.Reservation)
			}
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
		})
	}
}

func TestNominatePreAllocation(t *testing.T) {
	testPreAllocationReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			AllocateOnce:   ptr.To(false),
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			PreAllocation:  true,
		},
	}
	tests := []struct {
		name           string
		rInfo          *frameworkext.ReservationInfo
		preAllocatable []*corev1.Pod
		wantNominated  *corev1.Pod
		wantStatus     bool
	}{
		{
			name:       "node without pre-allocatable",
			rInfo:      &frameworkext.ReservationInfo{},
			wantStatus: true,
		},
		{
			name:  "preferred pre-allocatable",
			rInfo: frameworkext.NewReservationInfo(testPreAllocationReservation),
			preAllocatable: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "preferred-pod",
						Labels: map[string]string{},
						UID:    "preferred-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("2"),
										corev1.ResourceMemory: resource.MustParse("4Gi"),
									},
								},
							},
						},
						NodeName: "test-node",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "normal-pod",
						UID:  "normal-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1"),
										corev1.ResourceMemory: resource.MustParse("1Gi"),
									},
								},
							},
						},
						NodeName: "test-node",
					},
				},
			},
			wantNominated: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "preferred-pod",
					Labels: map[string]string{},
					UID:    "preferred-pod",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					NodeName: "test-node",
				},
			},
			wantStatus: true,
		},
		{
			name:  "preferred pre-allocatable 1",
			rInfo: frameworkext.NewReservationInfo(testPreAllocationReservation),
			preAllocatable: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "normal-pod",
						UID:  "normal-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1"),
										corev1.ResourceMemory: resource.MustParse("1Gi"),
									},
								},
							},
						},
						NodeName: "test-node",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "preferred-pod",
						Labels: map[string]string{},
						UID:    "preferred-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("2"),
										corev1.ResourceMemory: resource.MustParse("4Gi"),
									},
								},
							},
						},
						NodeName: "test-node",
					},
				},
			},
			wantNominated: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "preferred-pod",
					Labels: map[string]string{},
					UID:    "preferred-pod",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					NodeName: "test-node",
				},
			},
			wantStatus: true,
		},
		{
			name:  "no pre-allocatable to nominate",
			rInfo: frameworkext.NewReservationInfo(testPreAllocationReservation),
			preAllocatable: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-pod",
						Labels: map[string]string{},
						UID:    "test-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("6"),
										corev1.ResourceMemory: resource.MustParse("6Gi"),
									},
								},
							},
						},
						NodeName: "test-node",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "normal-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceMemory: resource.MustParse("8Gi"),
									},
								},
							},
						},
						NodeName: "test-node",
					},
				},
			},
			wantNominated: nil,
			wantStatus:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					},
				},
			}

			suit := newPluginTestSuitWith(t, nil, []*corev1.Node{node})
			plugin, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := plugin.(*Plugin)
			cycleState := framework.NewCycleState()
			var requests corev1.ResourceList
			if reservePod := tt.rInfo.GetReservePod(); reservePod != nil {
				requests = apiresource.PodRequests(tt.rInfo.GetReservePod(), apiresource.PodResourcesOptions{})
			}
			state := &stateData{
				schedulingStateData: schedulingStateData{
					nodeReservationStates: map[string]*nodeReservationState{},
					podRequests:           requests,
					podRequestsResources:  framework.NewResource(requests),
					podResourceNames:      quotav1.ResourceNames(requests),
				},
			}
			for _, pod := range tt.preAllocatable {
				nodeRState := state.nodeReservationStates[pod.Spec.NodeName]
				if nodeRState == nil {
					nodeRState = &nodeReservationState{}
				}
				nodeRState.nodeName = pod.Spec.NodeName
				nodeRState.selectedPreAllocatablePods = append(nodeRState.selectedPreAllocatablePods, pod)
				state.nodeReservationStates[pod.Spec.NodeName] = nodeRState
			}
			cycleState.Write(stateKey, state)
			nominatedPod, status := pl.NominatePreAllocation(context.TODO(), cycleState, tt.rInfo, node.Name)
			if tt.wantNominated == nil {
				assert.Nil(t, nominatedPod)
			} else {
				assert.NotNil(t, nominatedPod)
				assert.Equal(t, tt.wantNominated, nominatedPod)
			}
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
		})
	}
}

func newTestReservation(t *testing.T, name string, labels, ownerLabels map[string]string, nodeName string, allocatable corev1.ResourceList) *schedulingv1alpha1.Reservation {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:    uuid.NewUUID(),
			Name:   name,
			Labels: labels,
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: allocatable.DeepCopy(),
							},
						},
					},
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: ownerLabels,
					},
				},
			},
			AllocateOnce:   ptr.To[bool](false),
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
		},
	}
	assert.NoError(t, reservationutil.SetReservationAvailable(reservation, nodeName))
	return reservation
}

func TestMultiReservationsOnSameNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("96"),
				corev1.ResourceMemory: resource.MustParse("1886495404Ki"),
			},
		},
	}

	resourceList := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("16"),
		corev1.ResourceMemory: resource.MustParse("32Gi"),
	}
	labels := map[string]string{
		"foo": "bar",
	}
	suit := newPluginTestSuitWith(t, nil, []*corev1.Node{node})
	var reservations []*schedulingv1alpha1.Reservation
	for i := 0; i < 3; i++ {
		r := newTestReservation(t, fmt.Sprintf("test-r-%d", i), labels, labels, node.Name, resourceList)
		reservations = append(reservations, r)
		_, err := suit.extenderFactory.KoordinatorClientSet().SchedulingV1alpha1().Reservations().Create(context.TODO(), r, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	nodeInfo, err := suit.fw.SnapshotSharedLister().NodeInfos().Get(node.Name)
	assert.NoError(t, err)
	recoverNodeInfoFn := func() {
		for _, v := range reservations {
			nodeInfo.(*framework.NodeInfo).AddPod(reservationutil.NewReservePod(v))
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels:    labels,
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: resourceList,
					},
				},
			},
		},
	}
	affinity := &apiext.ReservationAffinity{
		ReservationSelector: labels,
	}
	assert.NoError(t, apiext.SetReservationAffinity(pod, affinity))
	_, err = suit.fw.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)

	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	suit.start(t)
	pl := p.(*Plugin)

	nominatedReservationCount := map[types.UID]int{}
	for range reservations {
		recoverNodeInfoFn()
		cycleState := framework.NewCycleState()
		pl.BeforePreFilter(context.TODO(), cycleState, pod)
		pl.PreFilter(context.TODO(), cycleState, pod, nil)
		pl.Filter(context.TODO(), cycleState, pod, nodeInfo)
		nm := pl.handle.(frameworkext.FrameworkExtender).GetReservationNominator()
		rInfo, status := nm.NominateReservation(context.TODO(), cycleState, pod, node.Name)
		assert.True(t, status.IsSuccess())
		nm.AddNominatedReservation(pod, node.Name, rInfo)
		rInfo = pl.handle.GetReservationNominator().GetNominatedReservation(pod, node.Name)
		assert.NotNil(t, rInfo, rInfo)
		pl.Reserve(context.TODO(), cycleState, pod, node.Name)
		nominatedReservationCount[rInfo.UID()]++
		nm.DeleteNominatedReservePodOrReservation(pod)
	}

	assert.Len(t, nominatedReservationCount, len(reservations))
	for _, v := range nominatedReservationCount {
		assert.Equal(t, 1, v)
	}
}

func TestReservationsNominator(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("96"),
				corev1.ResourceMemory: resource.MustParse("1886495404Ki"),
			},
		},
	}

	resourceList := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("16"),
		corev1.ResourceMemory: resource.MustParse("32Gi"),
	}
	labels := map[string]string{
		"foo": "bar",
	}
	suit := newPluginTestSuitWith(t, nil, []*corev1.Node{node})
	var pods []*corev1.Pod
	for i := 0; i < 3; i++ {
		r := newTestReservation(t, fmt.Sprintf("test-r-%d", i), labels, labels, node.Name, resourceList)
		r.Status.Phase = "" // set to inactive
		pods = append(pods, reservationutil.NewReservePod(r))
		_, err := suit.extenderFactory.KoordinatorClientSet().SchedulingV1alpha1().Reservations().Create(context.TODO(), r, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	nodeInfo, err := suit.fw.SnapshotSharedLister().NodeInfos().Get(node.Name)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(nodeInfo.GetPods()))

	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	suit.start(t)
	pl := p.(*Plugin)

	nominatorImpl := pl.handle.(frameworkext.FrameworkExtender).GetReservationNominator()

	nominatorImpl.AddNominatedReservePod(pods[0], "node-1")
	ctx := context.TODO()
	state := framework.NewCycleState()
	pod, nodeInfoOut, update, status := pl.BeforeFilter(ctx, state, pods[2], nodeInfo)
	assert.Equal(t, pod, pods[2])
	assert.True(t, update)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, 1, len(nodeInfoOut.GetPods()))

	nominatorImpl.AddNominatedReservePod(pods[1], "node-1")
	pod, nodeInfoOut, update, status = pl.BeforeFilter(ctx, state, pods[2], nodeInfo)
	assert.Equal(t, pod, pods[2])
	assert.True(t, update)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, 2, len(nodeInfoOut.GetPods()))

	// Test gang scenario: same-job nominated reserve pods should be excluded from BeforeFilter.
	t.Run("gang same-job exclusion", func(t *testing.T) {
		gangPod0 := pods[0] // nominated on node-1
		gangPod1 := pods[1] // nominated on node-1

		// Mark gangPod0 as a same-job pod (gang-mate of gangPod1).
		gangState := framework.NewCycleState()
		frameworkext.MakeNominatedPodsOfTheSameJob(gangState, sets.New[string](string(gangPod0.UID)))

		// Scheduling gangPod1: gangPod0 (same-job) should be excluded;
		// gangPod1 itself is excluded by UID check (rInfo.Pod.UID != pod.UID).
		// Result: 0 pods added.
		_, nodeInfoOut, update, status := pl.BeforeFilter(ctx, gangState, gangPod1, nodeInfo)
		assert.True(t, update)
		assert.True(t, status.IsSuccess())
		assert.Equal(t, 0, len(nodeInfoOut.GetPods()),
			"gang-mate nominated pod should be excluded from BeforeFilter")

		// Without same-job marking, gangPod0 should be included.
		noGangState := framework.NewCycleState()
		_, nodeInfoOut2, update2, status2 := pl.BeforeFilter(ctx, noGangState, gangPod1, nodeInfo)
		assert.True(t, update2)
		assert.True(t, status2.IsSuccess())
		assert.Equal(t, 1, len(nodeInfoOut2.GetPods()),
			"without same-job marking, nominated pod should be included")

		// Mixed nomination: verify only same-job pods are excluded, others are included.
		// pods[2] is scheduling; pods[0] is same-job; pods[1] is not.
		// Result: pods[1] included, pods[0] excluded.
		mixedState := framework.NewCycleState()
		frameworkext.MakeNominatedPodsOfTheSameJob(mixedState, sets.New[string](string(gangPod0.UID)))
		_, nodeInfoOut3, update3, status3 := pl.BeforeFilter(ctx, mixedState, pods[2], nodeInfo)
		assert.True(t, update3)
		assert.True(t, status3.IsSuccess())
		assert.Equal(t, 1, len(nodeInfoOut3.GetPods()),
			"mixed: non-same-job nominated pod should still be included")
		// Verify the included pod is pods[1] (non-same-job), not pods[0] (same-job).
		podUIDs := make([]string, len(nodeInfoOut3.GetPods()))
		for i, p := range nodeInfoOut3.GetPods() {
			podUIDs[i] = string(p.GetPod().UID)
		}
		assert.Contains(t, podUIDs, string(gangPod1.UID),
			"mixed: non-same-job nominated pod should be included")
		assert.NotContains(t, podUIDs, string(gangPod0.UID),
			"mixed: same-job nominated pod should be excluded")

		// Clean up nominated pods.
		nominatorImpl.DeleteNominatedReservePod(gangPod0)
		nominatorImpl.DeleteNominatedReservePod(gangPod1)
	})
}

// buildReservePodInfo builds a reserve PodInfo with a distinct UID for the nominator counter tests.
func buildReservePodInfo(t *testing.T, name, nodeName string) *framework.PodInfo {
	labels := map[string]string{"foo": "bar"}
	resourceList := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	r := newTestReservation(t, name, labels, labels, nodeName, resourceList)
	pi, err := framework.NewPodInfo(reservationutil.NewReservePod(r))
	assert.NoError(t, err)
	return pi
}

// totalNominatedReservePodEntries sums the entries actually stored across all nodes.
// Callers must hold at least a read lock on nm.lock.
func totalNominatedReservePodEntries(nm *nominator) int {
	total := 0
	for _, podInfos := range nm.nominatedReservePod {
		total += len(podInfos)
	}
	return total
}

// assertNominatedReservePodCountConsistent asserts the lock-free counter never drifts from the
// number of entries actually stored, guarding every add/remove path.
func assertNominatedReservePodCountConsistent(t *testing.T, nm *nominator) {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	assert.Equal(t, int64(totalNominatedReservePodEntries(nm)), nm.nominatedReservePodCount.Load(),
		"nominatedReservePodCount drifted from the actual stored entries")
}

func TestNominatorReservePodCountConsistency(t *testing.T) {
	nm := newNominator(nil, nil)

	// Empty nominator: the counter is 0 and the lock-free fast path returns a non-nil empty slice.
	assert.Equal(t, int64(0), nm.nominatedReservePodCount.Load())
	got := nm.NominatedReservePodForNode("node-1")
	assert.NotNil(t, got, "fast path must return a non-nil empty slice")
	assert.Equal(t, []*framework.PodInfo{}, got)
	assertNominatedReservePodCountConsistent(t, nm)

	pi1 := buildReservePodInfo(t, "count-r-1", "node-1")
	pi2 := buildReservePodInfo(t, "count-r-2", "node-1")
	pi3 := buildReservePodInfo(t, "count-r-3", "node-2")

	// Adds across multiple nodes: the counter tracks the total entries.
	nm.AddNominatedReservePod(pi1, "node-1")
	assert.Equal(t, int64(1), nm.nominatedReservePodCount.Load())
	nm.AddNominatedReservePod(pi2, "node-1")
	assert.Equal(t, int64(2), nm.nominatedReservePodCount.Load())
	nm.AddNominatedReservePod(pi3, "node-2")
	assert.Equal(t, int64(3), nm.nominatedReservePodCount.Load())
	assertNominatedReservePodCountConsistent(t, nm)

	// count>0 slow path returns the correct per-node entries, and a non-nil empty slice for a node
	// without any nominated pod.
	assert.Equal(t, 2, len(nm.NominatedReservePodForNode("node-1")))
	assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-2")))
	assert.Equal(t, []*framework.PodInfo{}, nm.NominatedReservePodForNode("node-none"))

	// Re-nominating the same pod to the same node keeps the total stable (delete-then-add path).
	nm.AddNominatedReservePod(pi1, "node-1")
	assert.Equal(t, int64(3), nm.nominatedReservePodCount.Load())
	assertNominatedReservePodCountConsistent(t, nm)

	// Moving a pod to another node keeps the total stable and relocates the entry.
	nm.AddNominatedReservePod(pi1, "node-2")
	assert.Equal(t, int64(3), nm.nominatedReservePodCount.Load())
	assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-1")))
	assert.Equal(t, 2, len(nm.NominatedReservePodForNode("node-2")))
	assertNominatedReservePodCountConsistent(t, nm)

	// Deleting all entries returns the counter to 0 and re-engages the fast path.
	nm.DeleteReservePod(pi1.Pod)
	nm.DeleteReservePod(pi2.Pod)
	nm.DeleteReservePod(pi3.Pod)
	assert.Equal(t, int64(0), nm.nominatedReservePodCount.Load())
	assert.Equal(t, []*framework.PodInfo{}, nm.NominatedReservePodForNode("node-1"))
	assertNominatedReservePodCountConsistent(t, nm)

	// Deleting an unknown pod is a no-op and must not drive the counter negative.
	nm.DeleteReservePod(pi1.Pod)
	assert.Equal(t, int64(0), nm.nominatedReservePodCount.Load(), "counter must never go negative")
	assertNominatedReservePodCountConsistent(t, nm)
}

func TestNominatorReservePodCountConcurrentChurn(t *testing.T) {
	nm := newNominator(nil, nil)
	const (
		rounds    = 50
		numR      = 64
		numNode   = 8
		numReader = 4
	)
	pis := make([]*framework.PodInfo, numR)
	for i := range pis {
		pis[i] = buildReservePodInfo(t, fmt.Sprintf("churn-r-%d", i), fmt.Sprintf("node-%d", i%numNode))
	}
	nodeNameOf := func(i int) string { return fmt.Sprintf("node-%d", i%numNode) }

	var readers sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < numReader; r++ {
		readers.Add(1)
		go func(r int) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					// Hammer the per-node hot path concurrently with the writer.
					_ = nm.NominatedReservePodForNode(nodeNameOf(r))
				}
			}
		}(r)
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for round := 0; round < rounds; round++ {
			for i := 0; i < numR; i++ {
				nm.AddNominatedReservePod(pis[i], nodeNameOf(i))
			}
			assert.Equal(t, int64(numR), nm.nominatedReservePodCount.Load())
			assertNominatedReservePodCountConsistent(t, nm)
			for i := 0; i < numR; i++ {
				nm.DeleteReservePod(pis[i].Pod)
			}
			assert.Equal(t, int64(0), nm.nominatedReservePodCount.Load())
			assertNominatedReservePodCountConsistent(t, nm)
		}
	}()

	<-writerDone
	close(stop)
	readers.Wait()

	// At quiescence the counter must be zero and consistent, and the fast path engaged again.
	assert.Equal(t, int64(0), nm.nominatedReservePodCount.Load())
	assertNominatedReservePodCountConsistent(t, nm)
	assert.Equal(t, []*framework.PodInfo{}, nm.NominatedReservePodForNode("node-0"))
}

func TestBeforeFilterWithNilNode(t *testing.T) {
	// The nil-node guard now runs before dereferencing nodeInfo.Node().Name, so a nil node returns
	// early without touching the nominator and without transforming the pod/nodeInfo.
	pl := &Plugin{nominator: newNominator(nil, nil)}
	nodeInfo := framework.NewNodeInfo()
	assert.Nil(t, nodeInfo.Node())

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       types.UID("test-pod-uid"),
		},
	}
	outPod, _, updated, status := pl.BeforeFilter(context.TODO(), framework.NewCycleState(), pod, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.False(t, updated)
	assert.Equal(t, pod, outPod)
}

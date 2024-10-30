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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	apiresource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

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
				assert.Equal(t, tt.wantReservation, nominateRInfo.Reservation)
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
			AllocateOnce:   pointer.Bool(false),
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
			nodeInfo.AddPod(reservationutil.NewReservePod(v))
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
	pl := p.(*Plugin)

	nominatedReservationCount := map[types.UID]int{}
	for range reservations {
		recoverNodeInfoFn()
		cycleState := framework.NewCycleState()
		pl.BeforePreFilter(context.TODO(), cycleState, pod)
		pl.PreFilter(context.TODO(), cycleState, pod)
		pl.Filter(context.TODO(), cycleState, pod, nodeInfo)
		nm := pl.handle.(frameworkext.FrameworkExtender).GetReservationNominator()
		rInfo, status := nm.NominateReservation(context.TODO(), cycleState, pod, node.Name)
		assert.True(t, status.IsSuccess())
		nm.AddNominatedReservation(pod, node.Name, rInfo)
		rInfo = pl.handle.GetReservationNominator().GetNominatedReservation(pod, node.Name)
		assert.NotNil(t, rInfo, rInfo)
		pl.Reserve(context.TODO(), cycleState, pod, node.Name)
		nominatedReservationCount[rInfo.UID()]++
		nm.RemoveNominatedReservations(pod)
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
	assert.Equal(t, 0, len(nodeInfo.Pods))

	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	pl := p.(*Plugin)

	nominatorImpl := pl.handle.(frameworkext.FrameworkExtender).GetReservationNominator()

	nominatorImpl.AddNominatedReservePod(pods[0], "node-1")
	ctx := context.TODO()
	state := framework.NewCycleState()
	pod, nodeInfoOut, update, status := pl.BeforeFilter(ctx, state, pods[2], nodeInfo)
	assert.Equal(t, pod, pods[2])
	assert.True(t, update)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, 1, len(nodeInfoOut.Pods))

	nominatorImpl.AddNominatedReservePod(pods[1], "node-1")
	pod, nodeInfoOut, update, status = pl.BeforeFilter(ctx, state, pods[2], nodeInfo)
	assert.Equal(t, pod, pods[2])
	assert.True(t, update)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, 2, len(nodeInfoOut.Pods))
}

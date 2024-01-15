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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	apiresource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestScore(t *testing.T) {
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
		name         string
		pod          *corev1.Pod
		reservations []*schedulingv1alpha1.Reservation
		allocated    map[types.UID]corev1.ResourceList
		wantScore    int64
	}{
		{
			name: "skip for reserve pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						reservationutil.AnnotationReservePod: "true",
					},
				},
			},
			wantScore: framework.MinNodeScore,
		},
		{
			name:      "no reservation matched on the node",
			pod:       &corev1.Pod{},
			wantScore: framework.MinNodeScore,
		},
		{
			// TODO: should optimize the case
			name: "reservation matched but zero-request pod",
			pod:  &corev1.Pod{},
			reservations: []*schedulingv1alpha1.Reservation{
				reservation2C4G,
			},
			wantScore: framework.MinNodeScore,
		},
		{
			name: "reservation matched and pod has part empty resource requests",
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
			},
			wantScore: 50,
		},
		{
			name: "allocated reservation matched and pod has part empty resource requests",
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
				reservation2C4G,
			},
			allocated: map[types.UID]corev1.ResourceList{
				reservation2C4G.UID: {
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				},
			},
			wantScore: framework.MinNodeScore,
		},
		{
			name: "multi reservations matched and pod has part empty resource requests",
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
			wantScore: framework.MaxNodeScore,
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
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			assert.NotNil(t, p)
			pl := p.(*Plugin)

			cycleState := framework.NewCycleState()
			state := &stateData{
				nodeReservationStates: map[string]nodeReservationState{},
			}
			state.podRequests, _ = apiresource.PodRequestsAndLimits(tt.pod)
			state.podRequestsResources = framework.NewResource(state.podRequests)
			for _, reservation := range tt.reservations {
				rInfo := frameworkext.NewReservationInfo(reservation)
				if allocated := tt.allocated[reservation.UID]; len(allocated) > 0 {
					rInfo.Allocated = allocated
				}
				nodeRState := state.nodeReservationStates[reservation.Status.NodeName]
				nodeRState.nodeName = reservation.Status.NodeName
				nodeRState.matched = append(nodeRState.matched, rInfo)
				state.nodeReservationStates[reservation.Status.NodeName] = nodeRState
				pl.reservationCache.updateReservation(reservation)
			}
			cycleState.Write(stateKey, state)

			status := pl.PreScore(context.TODO(), cycleState, tt.pod, []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
			})
			assert.True(t, status.IsSuccess())

			score, status := pl.Score(context.TODO(), cycleState, tt.pod, node.Name)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.wantScore, score)
		})
	}
}

func TestScoreWithOrder(t *testing.T) {
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
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

	reservationTemplateFn := func(i int) *schedulingv1alpha1.Reservation {
		return &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				UID:  uuid.NewUUID(),
				Name: fmt.Sprintf("test-reservation-%d", i),
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "main",
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
				NodeName: fmt.Sprintf("test-node-%d", i),
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		}
	}

	var nodes []*corev1.Node
	for i := 0; i < 4; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("test-node-%d", i+1),
			},
		})
	}
	suit := newPluginTestSuitWith(t, nil, nodes)
	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	assert.NotNil(t, p)
	pl := p.(*Plugin)

	state := &stateData{
		nodeReservationStates: map[string]nodeReservationState{},
	}
	state.podRequests, _ = apiresource.PodRequestsAndLimits(normalPod)
	state.podRequestsResources = framework.NewResource(state.podRequests)

	// add three Reservations to three node
	for i := 0; i < 3; i++ {
		reservation := reservationTemplateFn(i + 1)
		pl.reservationCache.updateReservation(reservation)
		rInfo := pl.reservationCache.getReservationInfoByUID(reservation.UID)
		nodeRState := state.nodeReservationStates[reservation.Status.NodeName]
		nodeRState.nodeName = reservation.Status.NodeName
		nodeRState.matched = append(nodeRState.matched, rInfo)
		state.nodeReservationStates[reservation.Status.NodeName] = nodeRState
	}

	// add Reservation with LabelReservationOrder
	reservationWithOrder := reservationTemplateFn(4)
	reservationWithOrder.Labels = map[string]string{
		apiext.LabelReservationOrder: "123456",
	}
	pl.reservationCache.updateReservation(reservationWithOrder)
	rInfo := pl.reservationCache.getReservationInfoByUID(reservationWithOrder.UID)
	nodeRState := state.nodeReservationStates[reservationWithOrder.Status.NodeName]
	nodeRState.nodeName = reservationWithOrder.Status.NodeName
	nodeRState.matched = append(nodeRState.matched, rInfo)
	state.nodeReservationStates[reservationWithOrder.Status.NodeName] = nodeRState

	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)

	status := pl.PreScore(context.TODO(), cycleState, normalPod, nodes)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, "test-node-4", state.preferredNode)

	var scoreList framework.NodeScoreList
	for _, v := range nodes {
		score, status := pl.Score(context.TODO(), cycleState, normalPod, v.Name)
		assert.True(t, status.IsSuccess())
		scoreList = append(scoreList, framework.NodeScore{
			Name:  v.Name,
			Score: score,
		})
	}

	expectedNodeScoreList := framework.NodeScoreList{
		{Name: "test-node-1", Score: framework.MaxNodeScore},
		{Name: "test-node-2", Score: framework.MaxNodeScore},
		{Name: "test-node-3", Score: framework.MaxNodeScore},
		{Name: "test-node-4", Score: mostPreferredScore},
	}
	sort.Slice(scoreList, func(i, j int) bool {
		return scoreList[i].Name < scoreList[j].Name
	})
	assert.Equal(t, expectedNodeScoreList, scoreList)

	status = pl.ScoreExtensions().NormalizeScore(context.TODO(), cycleState, normalPod, scoreList)
	assert.True(t, status.IsSuccess())

	expectedNodeScoreList = framework.NodeScoreList{
		{Name: "test-node-1", Score: 10},
		{Name: "test-node-2", Score: 10},
		{Name: "test-node-3", Score: 10},
		{Name: "test-node-4", Score: framework.MaxNodeScore},
	}
	assert.Equal(t, expectedNodeScoreList, scoreList)
}

func TestPreScoreWithNominateReservation(t *testing.T) {
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
	otherNodeReservation4C8G := reservation4C8G.DeepCopy()
	otherNodeReservation4C8G.UID = uuid.NewUUID()
	otherNodeReservation4C8G.Name = "otherNodeReservation4C8G"
	otherNodeReservation4C8G.Status.NodeName = "test-node-1"
	otherNodeReservation2C4G := reservation2C4G.DeepCopy()
	otherNodeReservation2C4G.UID = uuid.NewUUID()
	otherNodeReservation2C4G.Name = "otherNodeReservation2C4G"
	otherNodeReservation2C4G.Status.NodeName = "test-node-1"

	tests := []struct {
		name            string
		pod             *corev1.Pod
		reservations    []*schedulingv1alpha1.Reservation
		allocated       map[types.UID]corev1.ResourceList
		wantReservation map[string]*frameworkext.ReservationInfo
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
						UID: "123456",
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
						UID:  "654321",
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
			wantReservation: map[string]*frameworkext.ReservationInfo{
				"test-node": frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "preferred-reservation",
						Labels: map[string]string{
							apiext.LabelReservationOrder: "100",
						},
						UID: "123456",
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
				}),
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
			wantStatus: true,
			wantReservation: map[string]*frameworkext.ReservationInfo{
				reservation4C8G.Status.NodeName: frameworkext.NewReservationInfo(reservation4C8G),
			},
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
			wantStatus: true,
			wantReservation: map[string]*frameworkext.ReservationInfo{
				reservation2C4G.Status.NodeName: frameworkext.NewReservationInfo(reservation2C4G),
			},
		},
		{
			name: "multiple nodes have reservations",
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
				otherNodeReservation4C8G,
				otherNodeReservation2C4G,
			},
			wantStatus: true,
			wantReservation: map[string]*frameworkext.ReservationInfo{
				reservation2C4G.Status.NodeName:          frameworkext.NewReservationInfo(reservation2C4G),
				otherNodeReservation2C4G.Status.NodeName: frameworkext.NewReservationInfo(otherNodeReservation2C4G),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-1",
					},
				},
			}

			suit := newPluginTestSuitWith(t, nil, nodes)
			plugin, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := plugin.(*Plugin)
			cycleState := framework.NewCycleState()
			state := &stateData{
				nodeReservationStates: map[string]nodeReservationState{},
			}
			state.podRequests, _ = apiresource.PodRequestsAndLimits(tt.pod)
			state.podRequestsResources = framework.NewResource(state.podRequests)
			for _, reservation := range tt.reservations {
				rInfo := frameworkext.NewReservationInfo(reservation)
				if allocated := tt.allocated[reservation.UID]; len(allocated) > 0 {
					rInfo.Allocated = allocated
				}
				nodeRState := state.nodeReservationStates[reservation.Status.NodeName]
				nodeRState.nodeName = reservation.Status.NodeName
				nodeRState.matched = append(nodeRState.matched, rInfo)
				state.nodeReservationStates[reservation.Status.NodeName] = nodeRState
				pl.reservationCache.updateReservation(reservation)
			}
			cycleState.Write(stateKey, state)

			status := pl.PreScore(context.TODO(), cycleState, tt.pod, nodes)
			assert.Equal(t, tt.wantStatus, status.IsSuccess())

			for nodeName, wantReservationInfo := range tt.wantReservation {
				sort.Slice(wantReservationInfo.ResourceNames, func(i, j int) bool {
					return wantReservationInfo.ResourceNames[i] < wantReservationInfo.ResourceNames[j]
				})
				rInfo := pl.handle.GetReservationNominator().GetNominatedReservation(tt.pod, nodeName)
				if rInfo != nil {
					sort.Slice(rInfo.ResourceNames, func(i, j int) bool {
						return rInfo.ResourceNames[i] < rInfo.ResourceNames[j]
					})
				}
				assert.Equal(t, wantReservationInfo, rInfo)
			}
		})
	}
}

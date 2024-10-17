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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestEventHandlerOnAdd(t *testing.T) {
	activeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4000m"),
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
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	pendingReservation := activeReservation.DeepCopy()
	pendingReservation.Status.Phase = schedulingv1alpha1.ReservationPending
	pendingReservation.Status.NodeName = ""

	failedReservation := activeReservation.DeepCopy()
	failedReservation.Status.Phase = schedulingv1alpha1.ReservationFailed

	succeededReservation := activeReservation.DeepCopy()
	succeededReservation.Status.Phase = schedulingv1alpha1.ReservationSucceeded

	tests := []struct {
		name            string
		reservation     *schedulingv1alpha1.Reservation
		wantReservation *schedulingv1alpha1.Reservation
	}{
		{
			name:            "active reservation",
			reservation:     activeReservation,
			wantReservation: activeReservation,
		},
		{
			name:            "pending reservation",
			reservation:     pendingReservation,
			wantReservation: nil,
		},
		{
			name:            "failed reservation",
			reservation:     failedReservation,
			wantReservation: nil,
		},
		{
			name:            "succeeded reservation",
			reservation:     succeededReservation,
			wantReservation: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newReservationCache(nil)
			eh := &reservationEventHandler{cache: cache}
			eh.OnAdd(tt.reservation, true)
			if tt.wantReservation == nil {
				rInfo := cache.getReservationInfoByUID(tt.reservation.UID)
				assert.Nil(t, rInfo)
			} else {
				rInfo := cache.getReservationInfoByUID(tt.wantReservation.UID)
				assert.Equal(t, tt.wantReservation, rInfo.Reservation)
			}
		})
	}
}

func TestEventHandlerUpdate(t *testing.T) {
	activeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4000m"),
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
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	pendingReservation := activeReservation.DeepCopy()
	pendingReservation.Status.Phase = schedulingv1alpha1.ReservationPending
	pendingReservation.Status.NodeName = ""

	failedReservation := activeReservation.DeepCopy()
	failedReservation.Status.Phase = schedulingv1alpha1.ReservationFailed

	succeededReservation := activeReservation.DeepCopy()
	succeededReservation.Status.Phase = schedulingv1alpha1.ReservationSucceeded

	tests := []struct {
		name            string
		oldReservation  *schedulingv1alpha1.Reservation
		newReservation  *schedulingv1alpha1.Reservation
		wantReservation *schedulingv1alpha1.Reservation
	}{
		{
			name:            "pending to active",
			oldReservation:  pendingReservation,
			newReservation:  activeReservation,
			wantReservation: activeReservation,
		},
		{
			name:            "active to failed",
			oldReservation:  activeReservation,
			newReservation:  failedReservation,
			wantReservation: failedReservation,
		},
		{
			name:            "active to succeeded",
			oldReservation:  activeReservation,
			newReservation:  succeededReservation,
			wantReservation: succeededReservation,
		},
		{
			name:            "pending to failed",
			oldReservation:  pendingReservation,
			newReservation:  failedReservation,
			wantReservation: failedReservation,
		},
		{
			name:            "pending to succeeded",
			oldReservation:  pendingReservation,
			newReservation:  succeededReservation,
			wantReservation: succeededReservation,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newReservationCache(nil)
			eh := &reservationEventHandler{cache: cache, rrNominator: newNominator(nil, nil)}
			eh.OnUpdate(tt.oldReservation, tt.newReservation)
			if tt.wantReservation == nil {
				rInfo := cache.getReservationInfoByUID(tt.newReservation.UID)
				assert.Nil(t, rInfo)
			} else {
				rInfo := cache.getReservationInfoByUID(tt.wantReservation.UID)
				assert.Equal(t, tt.wantReservation, rInfo.Reservation)
			}
		})
	}
}

func TestEventHandlerDelete(t *testing.T) {
	activeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4000m"),
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
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}
	cache := newReservationCache(nil)
	eh := &reservationEventHandler{cache: cache, rrNominator: newNominator(nil, nil)}
	eh.OnAdd(activeReservation, true)
	rInfo := cache.getReservationInfoByUID(activeReservation.UID)
	assert.NotNil(t, rInfo)
	reservePodInfo, _ := framework.NewPodInfo(reservation.NewReservePod(activeReservation))
	eh.rrNominator.AddNominatedReservePod(reservePodInfo, "test-node")
	reservePodInfo, _ = framework.NewPodInfo(reservation.NewReservePod(activeReservation))
	assert.Equal(t, []*framework.PodInfo{reservePodInfo}, eh.rrNominator.NominatedReservePodForNode("test-node"))
	eh.OnDelete(activeReservation)
	rInfo = cache.getReservationInfoByUID(activeReservation.UID)
	assert.NotNil(t, rInfo)
	assert.False(t, rInfo.IsAvailable())
	assert.Equal(t, []*framework.PodInfo{}, eh.rrNominator.NominatedReservePodForNode("test-node"))
}

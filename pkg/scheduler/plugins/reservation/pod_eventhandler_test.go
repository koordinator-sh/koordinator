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

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func TestPodEventHandler(t *testing.T) {
	handler := &podEventHandler{
		cache:     newReservationCache(nil),
		nominator: newNominator(),
	}
	reservationUID := uuid.NewUUID()
	reservationName := "test-reservation"
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: reservationName,
			UID:  reservationUID,
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"),
			},
		},
	}
	handler.cache.updateReservation(reservation)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}

	handler.OnAdd(pod)
	rInfo := handler.cache.getReservationInfoByUID(reservationUID)
	assert.Empty(t, rInfo.AssignedPods)

	newPod := pod.DeepCopy()
	apiext.SetReservationAllocated(newPod, reservation)
	handler.OnUpdate(pod, newPod)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Len(t, rInfo.AssignedPods, 0)

	newPod.Spec.NodeName = reservation.Status.NodeName
	handler.OnUpdate(pod, newPod)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Len(t, rInfo.AssignedPods, 1)

	expectPodRequirement := &frameworkext.PodRequirement{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		UID:       pod.UID,
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"),
		},
	}
	assert.Equal(t, expectPodRequirement, rInfo.AssignedPods[pod.UID])

	handler.OnDelete(newPod)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Empty(t, rInfo.AssignedPods)
}

func TestPodEventHandlerWithOperatingPod(t *testing.T) {
	handler := &podEventHandler{
		cache:     newReservationCache(nil),
		nominator: newNominator(),
	}
	reservationUID := uuid.NewUUID()
	reservationName := "test-reservation"
	operatingReservationPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: reservationName,
			UID:  reservationUID,
			Labels: map[string]string{
				apiext.LabelPodOperatingMode: string(apiext.ReservationPodOperatingMode),
			},
			Annotations: map[string]string{},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}
	handler.OnAdd(operatingReservationPod)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}
	handler.OnAdd(pod)
	rInfo := handler.cache.getReservationInfoByUID(operatingReservationPod.UID)
	assert.NotNil(t, rInfo)

	p := operatingReservationPod.DeepCopy()
	err := apiext.SetReservationCurrentOwner(p.Annotations, &corev1.ObjectReference{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		UID:       pod.UID,
	})
	assert.NoError(t, err)
	handler.OnUpdate(operatingReservationPod, p)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Len(t, rInfo.AssignedPods, 1)
	expectPodRequirement := &frameworkext.PodRequirement{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		UID:       pod.UID,
		Requests:  corev1.ResourceList{},
	}
	assert.Equal(t, expectPodRequirement, rInfo.AssignedPods[pod.UID])

	handler.OnDelete(operatingReservationPod)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Nil(t, rInfo)
}

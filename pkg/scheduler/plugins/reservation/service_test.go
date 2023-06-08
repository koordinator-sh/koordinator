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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func TestQueryNodeReservations(t *testing.T) {
	pl := &Plugin{
		reservationCache: newReservationCache(nil),
	}

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			AllocateOnce:   pointer.Bool(false),
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "test-pod",
						},
					},
				},
			},
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
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: "test-node",
		},
	}
	pl.reservationCache.updateReservation(reservation)
	assignedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "default",
			UID:       uuid.NewUUID(),
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
	pl.reservationCache.addPod(reservation.UID, assignedPod)

	owners := []schedulingv1alpha1.ReservationOwner{
		{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test-xxx",
				},
			},
		},
	}
	data, err := json.Marshal(owners)
	assert.NoError(t, err)
	operatingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-operating-pod-1",
			Namespace: "default",
			UID:       uuid.NewUUID(),
			Labels: map[string]string{
				extension.LabelPodOperatingMode: string(extension.ReservationPodOperatingMode),
			},
			Annotations: map[string]string{
				extension.AnnotationReservationOwners: string(data),
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
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
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	pl.reservationCache.updateReservationOperatingPod(operatingPod, &corev1.ObjectReference{
		Name:      "assigned-pod-1",
		Namespace: "default",
		UID:       "123456",
	})

	engine := gin.Default()
	pl.RegisterEndpoints(engine.Group("/"))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/nodeReservations/test-node", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	reservations := &NodeReservations{}
	err = json.NewDecoder(w.Result().Body).Decode(reservations)
	assert.NoError(t, err)

	expectedReservations := &NodeReservations{
		Items: []ReservationItem{
			{
				Name:         reservation.Name,
				Namespace:    "",
				UID:          reservation.UID,
				Kind:         "Reservation",
				Available:    true,
				AllocateOnce: false,
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				Allocated: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				Owners: []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "test-pod",
							},
						},
					},
				},
				AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
				AssignedPods: []*frameworkext.PodRequirement{
					{
						Name:      assignedPod.Name,
						Namespace: assignedPod.Namespace,
						UID:       assignedPod.UID,
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
			{
				Name:         operatingPod.Name,
				Namespace:    operatingPod.Namespace,
				UID:          operatingPod.UID,
				Kind:         "Pod",
				Available:    true,
				AllocateOnce: true,
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				Owners: []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "test-xxx",
							},
						},
					},
				},
				AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
				AssignedPods: []*frameworkext.PodRequirement{
					{
						Name:      "assigned-pod-1",
						Namespace: "default",
						UID:       "123456",
						Requests:  corev1.ResourceList{},
					},
				},
			},
		},
	}
	sort.Slice(expectedReservations.Items, func(i, j int) bool {
		return expectedReservations.Items[i].UID < expectedReservations.Items[j].UID
	})
	sort.Slice(reservations.Items, func(i, j int) bool {
		return reservations.Items[i].UID < reservations.Items[j].UID
	})
	assert.Equal(t, expectedReservations, reservations)
}

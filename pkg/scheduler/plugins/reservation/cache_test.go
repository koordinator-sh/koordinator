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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestCacheUpdateReservation(t *testing.T) {
	cache := newReservationCache(nil)
	reservation := &schedulingv1alpha1.Reservation{
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
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Phase:    schedulingv1alpha1.ReservationAvailable,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}
	cache.updateReservation(reservation)
	reservationInfos := cache.ListAvailableReservationInfosOnNode(reservation.Status.NodeName, true)
	assert.Len(t, reservationInfos, 1)
	rInfo := reservationInfos[0]
	rInfo.RefreshPreCalculated()
	expectReservationInfo := &frameworkext.ReservationInfo{
		Reservation: reservation,
		Pod:         reservationutil.NewReservePod(reservation),
		ResourceNames: []corev1.ResourceName{
			corev1.ResourceCPU,
			corev1.ResourceMemory,
		},
		Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
		Allocated:    nil,
		AssignedPods: map[types.UID]*frameworkext.PodRequirement{},
	}
	expectReservationInfo.RefreshPreCalculated()
	sort.Slice(rInfo.ResourceNames, func(i, j int) bool {
		return rInfo.ResourceNames[i] < rInfo.ResourceNames[j]
	})
	assert.Equal(t, expectReservationInfo, rInfo)

	cache.updateReservation(reservation)
	reservationInfos = cache.ListAvailableReservationInfosOnNode(reservation.Status.NodeName, true)
	assert.Len(t, reservationInfos, 1)
	rInfo = reservationInfos[0]
	sort.Slice(rInfo.ResourceNames, func(i, j int) bool {
		return rInfo.ResourceNames[i] < rInfo.ResourceNames[j]
	})
	assert.Equal(t, expectReservationInfo, rInfo)
}

func TestCacheDeleteReservation(t *testing.T) {
	cache := newReservationCache(nil)
	reservation := &schedulingv1alpha1.Reservation{
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
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}
	cache.updateReservation(reservation)

	rInfo := cache.getReservationInfoByUID(reservation.UID)
	assert.NotNil(t, rInfo)

	expectReservationInfo := &frameworkext.ReservationInfo{
		Reservation: reservation,
		Pod:         reservationutil.NewReservePod(reservation),
		ResourceNames: []corev1.ResourceName{
			corev1.ResourceCPU,
			corev1.ResourceMemory,
		},
		Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
		Allocated:    nil,
		AssignedPods: map[types.UID]*frameworkext.PodRequirement{},
	}
	sort.Slice(rInfo.ResourceNames, func(i, j int) bool {
		return rInfo.ResourceNames[i] < rInfo.ResourceNames[j]
	})
	assert.Equal(t, expectReservationInfo, rInfo)

	cache.DeleteReservation(reservation)
	rInfo = cache.getReservationInfoByUID(reservation.UID)
	assert.Nil(t, rInfo)
}

func TestCacheUpdateReservationIfExists(t *testing.T) {
	cache := newReservationCache(nil)
	reservation := &schedulingv1alpha1.Reservation{
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
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Phase:    schedulingv1alpha1.ReservationAvailable,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	// Update non-existent reservation should not create it
	cache.updateReservationIfExists(reservation)
	rInfo := cache.getReservationInfoByUID(reservation.UID)
	assert.Nil(t, rInfo, "should not create reservation if it doesn't exist")

	// First add the reservation
	cache.updateReservation(reservation)
	rInfo = cache.getReservationInfoByUID(reservation.UID)
	assert.NotNil(t, rInfo)
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, rInfo.Reservation.Status.Phase)

	// Update existing reservation with new phase
	updatedReservation := reservation.DeepCopy()
	updatedReservation.Status.Phase = schedulingv1alpha1.ReservationSucceeded
	updatedReservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("2"),
		corev1.ResourceMemory: resource.MustParse("2Gi"),
	}

	cache.updateReservationIfExists(updatedReservation)
	rInfo = cache.getReservationInfoByUID(reservation.UID)
	assert.NotNil(t, rInfo)
	assert.Equal(t, schedulingv1alpha1.ReservationSucceeded, rInfo.Reservation.Status.Phase)

	// Verify matchable and allocated caches are updated correctly
	// When phase becomes Succeeded, it should be removed from matchable
	assert.NotContains(t, cache.matchableOnNode["test-node-1"], reservation.UID)

	// Test transition from matchable to non-matchable
	cache2 := newReservationCache(nil)
	reservation2 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-2",
			Phase:    schedulingv1alpha1.ReservationAvailable,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
		},
	}

	cache2.updateReservation(reservation2)
	assert.Contains(t, cache2.matchableOnNode["test-node-2"], reservation2.UID)

	// Update to non-matchable state
	reservation2.Status.Phase = schedulingv1alpha1.ReservationFailed
	cache2.updateReservationIfExists(reservation2)
	assert.NotContains(t, cache2.matchableOnNode["test-node-2"], reservation2.UID)
}

func TestCacheAddOrUpdateOrDeletePod(t *testing.T) {
	cache := newReservationCache(nil)
	reservation := &schedulingv1alpha1.Reservation{
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
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2000m"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}
	cache.updateReservation(reservation)

	rInfo := cache.getReservationInfoByUID(reservation.UID)
	assert.NotNil(t, rInfo)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Namespace: "default",
			Name:      "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
	}

	cache.addPod(reservation.UID, pod)

	rInfo = cache.getReservationInfoByUID(reservation.UID)
	sort.Slice(rInfo.ResourceNames, func(i, j int) bool {
		return rInfo.ResourceNames[i] < rInfo.ResourceNames[j]
	})
	expectReservationInfo := &frameworkext.ReservationInfo{
		Reservation: reservation,
		Pod:         reservationutil.NewReservePod(reservation),
		ResourceNames: []corev1.ResourceName{
			corev1.ResourceCPU,
			corev1.ResourceMemory,
		},
		Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4000m"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
		Allocated: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2000m"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
		AssignedPods: map[types.UID]*frameworkext.PodRequirement{
			pod.UID: {
				Namespace: pod.Namespace,
				Name:      pod.Name,
				UID:       pod.UID,
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2000m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	expectReservationInfo.RefreshPreCalculated()
	assert.Equal(t, expectReservationInfo, rInfo)

	cache.updatePod(reservation.UID, reservation.UID, pod, pod)
	rInfo = cache.getReservationInfoByUID(reservation.UID)
	sort.Slice(rInfo.ResourceNames, func(i, j int) bool {
		return rInfo.ResourceNames[i] < rInfo.ResourceNames[j]
	})
	expectReservationInfo.Allocated = quotav1.SubtractWithNonNegativeResult(expectReservationInfo.Allocated, corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("0"),
		corev1.ResourceMemory: resource.MustParse("0"),
	})
	assert.Equal(t, expectReservationInfo, rInfo)

	cache.deletePod(reservation.UID, pod)
	rInfo = cache.getReservationInfoByUID(reservation.UID)
	sort.Slice(rInfo.ResourceNames, func(i, j int) bool {
		return rInfo.ResourceNames[i] < rInfo.ResourceNames[j]
	})
	expectReservationInfo.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("0"),
		corev1.ResourceMemory: resource.MustParse("0"),
	}
	expectReservationInfo.AssignedPods = map[types.UID]*frameworkext.PodRequirement{}
	expectReservationInfo.RefreshPreCalculated()
	assert.Equal(t, expectReservationInfo, rInfo)
}

func TestCacheUpdatePodAcrossReservations(t *testing.T) {
	cache := newReservationCache(nil)
	reservation1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-1",
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
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}
	reservation2 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2000m"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2000m"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}
	cache.updateReservation(reservation1)
	cache.updateReservation(reservation2)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Namespace: "default",
			Name:      "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
	}

	// add pod to reservation1
	cache.addPod(reservation1.UID, pod)
	rInfo1 := cache.getReservationInfoByUID(reservation1.UID)
	assert.Len(t, rInfo1.AssignedPods, 1)
	assert.Contains(t, rInfo1.AssignedPods, pod.UID)

	// move pod from reservation1 to reservation2
	cache.updatePod(reservation1.UID, reservation2.UID, pod, pod)
	rInfo1 = cache.getReservationInfoByUID(reservation1.UID)
	assert.Len(t, rInfo1.AssignedPods, 0)
	rInfo2 := cache.getReservationInfoByUID(reservation2.UID)
	assert.Len(t, rInfo2.AssignedPods, 1)
	assert.Contains(t, rInfo2.AssignedPods, pod.UID)

	// update pod within same reservation
	cache.updatePod(reservation2.UID, reservation2.UID, pod, pod)
	rInfo2 = cache.getReservationInfoByUID(reservation2.UID)
	assert.Len(t, rInfo2.AssignedPods, 1)
	assert.Contains(t, rInfo2.AssignedPods, pod.UID)
}

func TestCacheListAllNodes(t *testing.T) {
	cache := newReservationCache(nil)
	reservation1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-1",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocateOnce: ptr.To[bool](false),
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Phase:    schedulingv1alpha1.ReservationAvailable,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	reservation2 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocateOnce: ptr.To[bool](false),
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-2",
			Phase:    schedulingv1alpha1.ReservationAvailable,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}

	// add both reservations
	cache.updateReservation(reservation1)
	cache.updateReservation(reservation2)

	// test matchable = true
	nodes := cache.ListAllNodes(true)
	assert.Len(t, nodes, 2)
	assert.Contains(t, nodes, "test-node-1")
	assert.Contains(t, nodes, "test-node-2")

	// test matchable = false (only allocated reservations)
	nodes = cache.ListAllNodes(false)
	assert.Len(t, nodes, 0) // no allocated pods yet

	// add a pod to reservation1
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Namespace: "default",
			Name:      "test-pod",
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
		},
	}
	cache.addPod(reservation1.UID, pod)

	// now test-node-1 should be in allocated nodes
	nodes = cache.ListAllNodes(false)
	assert.Len(t, nodes, 1)
	assert.Contains(t, nodes, "test-node-1")

	// matchable should still return both
	nodes = cache.ListAllNodes(true)
	assert.Len(t, nodes, 2)
}

func TestCacheForEachMatchableReservationOnNode(t *testing.T) {
	cache := newReservationCache(nil)
	reservation1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-1",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Phase:    schedulingv1alpha1.ReservationAvailable,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	reservation2 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Phase:    schedulingv1alpha1.ReservationFailed, // not matchable
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}

	cache.updateReservation(reservation1)
	cache.updateReservation(reservation2)

	// iterate over matchable reservations on test-node-1
	count := 0
	status := cache.ForEachMatchableReservationOnNode("test-node-1", func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
		count++
		assert.Equal(t, reservation1.UID, rInfo.UID())
		return true, nil
	})
	assert.Nil(t, status)
	assert.Equal(t, 1, count) // only reservation1 should be visited

	// test early termination
	count = 0
	status = cache.ForEachMatchableReservationOnNode("test-node-1", func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
		count++
		return false, nil // stop iteration
	})
	assert.Nil(t, status)
	assert.Equal(t, 1, count)
}

func TestCacheListAvailableReservationInfosOnNode(t *testing.T) {
	cache := newReservationCache(nil)
	reservation := &schedulingv1alpha1.Reservation{
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
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Phase:    schedulingv1alpha1.ReservationAvailable,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	failedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-failed",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
			Phase:    schedulingv1alpha1.ReservationFailed,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}

	cache.updateReservation(reservation)
	cache.updateReservation(failedReservation)

	// test listAll = false (only matchable)
	rInfos := cache.ListAvailableReservationInfosOnNode("test-node-1", false)
	assert.Len(t, rInfos, 1)
	assert.Equal(t, reservation.UID, rInfos[0].UID())

	// test listAll = true (all reservations)
	rInfos = cache.ListAvailableReservationInfosOnNode("test-node-1", true)
	assert.Len(t, rInfos, 2)
}

// TestReservationCache_PreAllocatableOperations tests all pre-allocatable pod cache operations
func TestReservationCache_PreAllocatableOperations(t *testing.T) {

	t.Run("btree item ordering", func(t *testing.T) {
		// Test priority-based ordering (higher priority first)
		pod1 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "aaa"}}
		pod2 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "bbb"}}
		pod3 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "ccc"}}

		item1 := &preAllocatablePodItem{priority: 100, pod: pod1}
		item2 := &preAllocatablePodItem{priority: 50, pod: pod2}
		item3 := &preAllocatablePodItem{priority: 100, pod: pod3} // Same priority as item1

		// Higher priority should come first
		assert.True(t, item1.Less(item2), "priority 100 should be less than priority 50 (higher priority)")
		assert.False(t, item2.Less(item1), "priority 50 should not be less than priority 100")

		// Same priority, order by UID
		assert.True(t, item1.Less(item3), "with same priority, 'aaa' < 'ccc'")
		assert.False(t, item3.Less(item1), "with same priority, 'ccc' not < 'aaa'")
	})

	t.Run("add and retrieve candidates", func(t *testing.T) {
		// Add pods to multiple nodes with different priorities
		node1Pod1 := createTestPreAllocatablePod("node1-pod1", "node1", "1", "1Gi", "100")
		node1Pod2 := createTestPreAllocatablePod("node1-pod2", "node1", "1", "1Gi", "50")
		node1Pod3 := createTestPreAllocatablePod("node1-pod3", "node1", "1", "1Gi", "200")
		node2Pod1 := createTestPreAllocatablePod("node2-pod1", "node2", "1", "1Gi", "150")

		cache := newReservationCache(nil)
		cache.addPreAllocatableCandidateOnNode(node1Pod1)
		cache.addPreAllocatableCandidateOnNode(node1Pod2)
		cache.addPreAllocatableCandidateOnNode(node1Pod3)
		cache.addPreAllocatableCandidateOnNode(node2Pod1)

		// Verify getAllPreAllocatableCandidates
		result := cache.getAllPreAllocatableCandidates()

		// Verify node1 pods are sorted by priority (descending: 200, 100, 50)
		assert.Len(t, result["node1"], 3)
		assert.Equal(t, "node1-pod3", string(result["node1"][0].UID), "pod with priority 200 should be first")
		assert.Equal(t, "node1-pod1", string(result["node1"][1].UID), "pod with priority 100 should be second")
		assert.Equal(t, "node1-pod2", string(result["node1"][2].UID), "pod with priority 50 should be third")

		// Verify node2
		assert.Len(t, result["node2"], 1)
		assert.Equal(t, "node2-pod1", string(result["node2"][0].UID))

		// Verify node isolation
		assert.Len(t, result, 2, "should have exactly 2 nodes")
	})

	t.Run("update priority and re-sort", func(t *testing.T) {
		cache := newReservationCache(nil)
		// Add a pod with low priority
		updatePod := createTestPreAllocatablePod("update-pod", "node1", "1", "1Gi", "30")
		cache.addPreAllocatableCandidateOnNode(updatePod)

		// Verify it's the only pod
		result := cache.getAllPreAllocatableCandidates()
		assert.Len(t, result["node1"], 1, "should have 1 pod initially")
		assert.Equal(t, "update-pod", string(result["node1"][0].UID), "pod with priority 30 should be first")

		// Update priority to highest
		updatePod.Annotations[apiext.AnnotationPodPreAllocatablePriority] = "300"
		cache.updatePreAllocatableCandidatePriority(updatePod)

		// Verify it's still the only pod but with updated priority
		result = cache.getAllPreAllocatableCandidates()
		assert.Equal(t, "update-pod", string(result["node1"][0].UID), "pod with priority 300 should be first")
		assert.Len(t, result["node1"], 1, "should still have 1 pod")

		// Verify btree and index are in sync
		podCache := cache.preAllocatablePodsOnNode["node1"]
		assert.Equal(t, podCache.tree.Len(), len(podCache.index), "btree and index must be in sync")
	})

	t.Run("delete and cleanup", func(t *testing.T) {
		cache := newReservationCache(nil)
		// Add pods to node1 first
		node1Pod1 := createTestPreAllocatablePod("node1-pod1", "node1", "1", "1Gi", "100")
		node1Pod2 := createTestPreAllocatablePod("node1-pod2", "node1", "1", "1Gi", "50")
		node2Pod1 := createTestPreAllocatablePod("node2-pod1", "node2", "1", "1Gi", "150")
		cache.addPreAllocatableCandidateOnNode(node1Pod1)
		cache.addPreAllocatableCandidateOnNode(node1Pod2)
		cache.addPreAllocatableCandidateOnNode(node2Pod1)

		// Delete a pod from node1
		cache.deletePreAllocatableCandidateOnNode("node1", "node1-pod2")

		podCache := cache.preAllocatablePodsOnNode["node1"]
		assert.NotContains(t, podCache.index, types.UID("node1-pod2"), "deleted pod should not be in index")
		assert.Equal(t, 1, podCache.tree.Len(), "should have 1 pod after deletion")

		// Delete all pods from node2
		cache.deletePreAllocatableCandidateOnNode("node2", "node2-pod1")

		// Verify node2 cache is completely removed (auto cleanup)
		assert.NotContains(t, cache.preAllocatablePodsOnNode, "node2", "empty node cache should be cleaned up")

		// Verify getAllPreAllocatableCandidates doesn't include empty nodes
		result := cache.getAllPreAllocatableCandidates()
		assert.NotContains(t, result, "node2", "result should not contain empty nodes")
	})

	t.Run("edge cases", func(t *testing.T) {
		cache := newReservationCache(nil)
		// nil pod
		cache.addPreAllocatableCandidateOnNode(nil)
		// Should not panic

		// Pod without NodeName
		emptyNodePod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{UID: "empty"},
			Spec:       corev1.PodSpec{NodeName: ""},
		}
		cache.addPreAllocatableCandidateOnNode(emptyNodePod)
		// Should not add to cache

		// Delete from non-existent node
		cache.deletePreAllocatableCandidateOnNode("non-exist-node", "fake-uid")
		// Should not panic

		// Delete with empty nodeName
		cache.deletePreAllocatableCandidateOnNode("", "fake-uid")
		// Should not panic

		// Update priority for non-existent pod
		nonExistPod := createTestPreAllocatablePod("non-exist", "node1", "1", "1Gi", "100")
		cache.updatePreAllocatableCandidatePriority(nonExistPod)
		// Should not panic or add the pod
	})
}

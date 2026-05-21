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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
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
	status := cache.ForEachMatchableReservationOnNode("test-node-1", func(rInfo *frameworkext.ReservationInfo) (bool, *fwktype.Status) {
		count++
		assert.Equal(t, reservation1.UID, rInfo.UID())
		return true, nil
	})
	assert.Nil(t, status)
	assert.Equal(t, 1, count) // only reservation1 should be visited

	// test early termination
	count = 0
	status = cache.ForEachMatchableReservationOnNode("test-node-1", func(rInfo *frameworkext.ReservationInfo) (bool, *fwktype.Status) {
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

const (
	idxPrefixQuota  = "koordinator.sh/quota-node-binder-name-"
	idxPrefixTenant = "tenant.dlc.alibaba-inc.com/"
)

func newIndexedCache() *reservationCache {
	c := newReservationCache(nil)
	c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
		Enabled: true,
		KeyPrefixes: []string{
			idxPrefixQuota,
			idxPrefixQuota, // duplicate, should be deduplicated
			idxPrefixTenant,
			"  ", // should be ignored
			"",
		},
	})
	return c
}

// idxExactTeam / idxExactProject simulate the "plain key" pattern (no
// namespace separator), which is exactly what KeyPrefixes cannot safely
// handle: a prefix="team" would also match label keys like "team-foo". The
// exact-key white-list is the right tool for these.
const (
	idxExactTeam    = "team"
	idxExactProject = "project"
)

func newIndexedCacheWithKeys() *reservationCache {
	c := newReservationCache(nil)
	c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
		Enabled: true,
		KeyPrefixes: []string{
			idxPrefixQuota,
			idxPrefixTenant,
		},
		Keys: []string{
			idxExactTeam,
			idxExactTeam, // duplicate, should be deduplicated
			idxExactProject,
			"  ", // should be ignored
			"",
		},
	})
	return c
}

func newIndexTestReservation(uid, name, node string, labels map[string]string) *schedulingv1alpha1.Reservation {
	return &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:    types.UID(uid),
			Name:   name,
			Labels: labels,
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: node,
			Phase:    schedulingv1alpha1.ReservationAvailable,
		},
	}
}

func TestSetReservationSelectorIndexConfig(t *testing.T) {
	t.Run("nil args", func(t *testing.T) {
		c := newReservationCache(nil)
		c.setReservationSelectorIndexConfig(nil)
		assert.False(t, c.indexEnabled)
	})
	t.Run("disabled", func(t *testing.T) {
		c := newReservationCache(nil)
		c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
			Enabled:     false,
			KeyPrefixes: []string{idxPrefixQuota},
		})
		assert.False(t, c.indexEnabled)
	})
	t.Run("only blanks", func(t *testing.T) {
		c := newReservationCache(nil)
		c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
			Enabled:     true,
			KeyPrefixes: []string{"  ", ""},
		})
		assert.False(t, c.indexEnabled)
	})
	t.Run("dedup prefixes", func(t *testing.T) {
		c := newIndexedCache()
		assert.True(t, c.indexEnabled)
		assert.Equal(t, []string{idxPrefixQuota, idxPrefixTenant}, c.indexedPrefixes)
	})
	t.Run("matchedPrefix", func(t *testing.T) {
		c := newIndexedCache()
		assert.Equal(t, idxPrefixQuota, c.matchedPrefix(idxPrefixQuota+"q1"))
		assert.Equal(t, idxPrefixTenant, c.matchedPrefix(idxPrefixTenant+"t1"))
		assert.Equal(t, "", c.matchedPrefix("unrelated/label"))
	})
	t.Run("only exact keys", func(t *testing.T) {
		c := newReservationCache(nil)
		c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
			Enabled: true,
			Keys:    []string{idxExactTeam, idxExactProject, "  ", ""},
		})
		assert.True(t, c.indexEnabled)
		assert.Empty(t, c.indexedPrefixes)
		assert.True(t, c.indexedKeys.Has(idxExactTeam))
		assert.True(t, c.indexedKeys.Has(idxExactProject))
		assert.Equal(t, 2, c.indexedKeys.Len())
	})
	t.Run("both prefixes and keys", func(t *testing.T) {
		c := newIndexedCacheWithKeys()
		assert.True(t, c.indexEnabled)
		assert.Equal(t, []string{idxPrefixQuota, idxPrefixTenant}, c.indexedPrefixes)
		assert.Equal(t, 2, c.indexedKeys.Len())
	})
	t.Run("matchedKey exact only", func(t *testing.T) {
		c := newIndexedCacheWithKeys()
		assert.True(t, c.matchedKey(idxExactTeam))
		assert.True(t, c.matchedKey(idxExactProject))
		// exact key must NOT be matched as a prefix even if a label key
		// happens to start with it (the prefix white-list does not contain
		// these short tokens).
		assert.False(t, c.matchedKey("team-foo"))
		assert.Equal(t, "", c.matchedPrefix("team-foo"))
	})
}

func TestReservationCacheIndexAddRemoveAndUpdate(t *testing.T) {
	c := newIndexedCache()
	r1 := newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{
		idxPrefixQuota + "qA":  "qA",
		idxPrefixTenant + "t1": "t1",
		"unrelated":            "ignored",
	})
	r2 := newIndexTestReservation("uid-2", "r2", "node-b", map[string]string{
		idxPrefixQuota + "qB": "qB",
	})
	r3 := newIndexTestReservation("uid-3", "r3", "node-c", map[string]string{
		idxPrefixTenant + "t2": "t2",
	})
	c.updateReservation(r1)
	c.updateReservation(r2)
	c.updateReservation(r3)

	// per-uid index entry tracks both the matched prefixes and the bound node.
	assert.ElementsMatch(t, []string{idxPrefixQuota, idxPrefixTenant}, c.indexEntryByUID["uid-1"].prefixes.UnsortedList())
	assert.Equal(t, "node-a", c.indexEntryByUID["uid-1"].node)
	assert.ElementsMatch(t, []string{idxPrefixQuota}, c.indexEntryByUID["uid-2"].prefixes.UnsortedList())
	assert.Equal(t, "node-b", c.indexEntryByUID["uid-2"].node)
	assert.ElementsMatch(t, []string{idxPrefixTenant}, c.indexEntryByUID["uid-3"].prefixes.UnsortedList())
	assert.Equal(t, "node-c", c.indexEntryByUID["uid-3"].node)

	// nodesByPrefix snapshot: per-(prefix,node) UID set.
	assert.ElementsMatch(t, []types.UID{"uid-1"}, c.nodesByPrefix[idxPrefixQuota]["node-a"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-2"}, c.nodesByPrefix[idxPrefixQuota]["node-b"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-1"}, c.nodesByPrefix[idxPrefixTenant]["node-a"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-3"}, c.nodesByPrefix[idxPrefixTenant]["node-c"].UnsortedList())

	// any selector key under prefix `quota` => candidate nodes are r1's + r2's.
	nodes, hit := c.FilterByReservationSelector(map[string]string{idxPrefixQuota + "anything": "ignored-value"})
	assert.True(t, hit)
	sort.Strings(nodes)
	assert.Equal(t, []string{"node-a", "node-b"}, nodes)

	// AND across prefixes: a node must hold a reservation contributing to BOTH prefixes => only node-a.
	nodes, hit = c.FilterByReservationSelector(map[string]string{
		idxPrefixQuota + "x":  "x",
		idxPrefixTenant + "y": "y",
	})
	assert.True(t, hit)
	assert.Equal(t, []string{"node-a"}, nodes)

	// no configured prefix => indexHit=false (caller falls back).
	nodes, hit = c.FilterByReservationSelector(map[string]string{"foo": "bar"})
	assert.False(t, hit)
	assert.Nil(t, nodes)

	// update r1 to drop the tenant-prefixed label.
	updated := newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{
		idxPrefixQuota + "qA": "qA",
	})
	c.updateReservation(updated)
	assert.ElementsMatch(t, []string{idxPrefixQuota}, c.indexEntryByUID["uid-1"].prefixes.UnsortedList())
	// node-a no longer appears under tenant prefix.
	_, exists := c.nodesByPrefix[idxPrefixTenant]["node-a"]
	assert.False(t, exists)

	// after update, only r3/node-c remains under tenant prefix.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxPrefixTenant + "x": "x"})
	assert.True(t, hit)
	assert.Equal(t, []string{"node-c"}, nodes)

	// delete r3 => prefix `tenant` becomes completely empty (top-level entry removed).
	c.DeleteReservation(r3)
	_, hasUID := c.indexEntryByUID["uid-3"]
	assert.False(t, hasUID)
	_, hasPrefix := c.nodesByPrefix[idxPrefixTenant]
	assert.False(t, hasPrefix)
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxPrefixTenant + "x": "x"})
	assert.True(t, hit)
	assert.Empty(t, nodes)
}

func TestReservationCacheIndexDisabledNoOp(t *testing.T) {
	c := newReservationCache(nil)
	r := newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{idxPrefixQuota + "q": "q"})
	c.updateReservation(r)
	nodes, hit := c.FilterByReservationSelector(map[string]string{idxPrefixQuota + "q": "q"})
	assert.False(t, hit)
	assert.Nil(t, nodes)
}

func TestReservationCacheIndexUnboundReservationSkipped(t *testing.T) {
	// A reservation without status.NodeName must NOT contribute a candidate
	// node to the index (it cannot be a real candidate yet anyway).
	c := newIndexedCache()
	unbound := newIndexTestReservation("uid-x", "rx", "", map[string]string{idxPrefixQuota + "q": "q"})
	c.updateReservation(unbound)
	_, hasEntry := c.indexEntryByUID["uid-x"]
	assert.False(t, hasEntry, "reservation without nodeName should not be indexed")
	nodes, hit := c.FilterByReservationSelector(map[string]string{idxPrefixQuota + "q": "q"})
	assert.True(t, hit)
	assert.Empty(t, nodes)
}

func TestReservationCacheIndexConcurrent(t *testing.T) {
	c := newIndexedCache()
	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			r := newIndexTestReservation("u"+itoa(i), "r"+itoa(i), "node-"+itoa(i%10), map[string]string{
				idxPrefixQuota + itoa(i): itoa(i),
			})
			c.updateReservation(r)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			c.FilterByReservationSelector(map[string]string{idxPrefixQuota + "any": "v"})
		}
	}()
	wg.Wait()
	nodes, hit := c.FilterByReservationSelector(map[string]string{idxPrefixQuota + "any": "v"})
	assert.True(t, hit)
	assert.NotEmpty(t, nodes)
}

func TestReservationCacheIndexStartupBackfill(t *testing.T) {
	// Populate reservationInfos BEFORE the index is enabled (simulates a
	// future wiring order where setReservationSelectorIndexConfig runs after
	// some reservations have already been admitted into the cache).
	c := newReservationCache(nil)
	c.updateReservation(newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{idxPrefixQuota + "q": "q"}))
	c.updateReservation(newIndexTestReservation("uid-2", "r2", "node-b", map[string]string{idxPrefixTenant + "t": "t"}))
	c.updateReservation(newIndexTestReservation("uid-3", "r3", "", map[string]string{idxPrefixQuota + "q": "q"})) // unbound: skipped
	assert.False(t, c.indexEnabled)
	assert.Empty(t, c.indexEntryByUID, "index must be empty while disabled")

	// Enabling the index must backfill the existing reservationInfos.
	c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
		Enabled:     true,
		KeyPrefixes: []string{idxPrefixQuota, idxPrefixTenant},
	})
	assert.True(t, c.indexEnabled)
	assert.Len(t, c.indexEntryByUID, 2, "only bound reservations should be backfilled")
	assert.Equal(t, "node-a", c.indexEntryByUID["uid-1"].node)
	assert.Equal(t, "node-b", c.indexEntryByUID["uid-2"].node)
	assert.ElementsMatch(t, []types.UID{"uid-1"}, c.nodesByPrefix[idxPrefixQuota]["node-a"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-2"}, c.nodesByPrefix[idxPrefixTenant]["node-b"].UnsortedList())
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())
}

func TestReservationCacheIndexReconfigure(t *testing.T) {
	c := newIndexedCache()
	c.updateReservation(newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{
		idxPrefixQuota + "q":  "q",
		idxPrefixTenant + "t": "t",
	}))
	c.updateReservation(newIndexTestReservation("uid-2", "r2", "node-b", map[string]string{idxPrefixQuota + "q": "q"}))
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())

	// Narrow the index to only the tenant prefix: every previously-indexed
	// quota-only entry must be evicted, and the surviving entries must be
	// rebuilt from reservationInfos with the new prefix set.
	c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
		Enabled:     true,
		KeyPrefixes: []string{idxPrefixTenant},
	})
	assert.Equal(t, []string{idxPrefixTenant}, c.indexedPrefixes)
	assert.NotContains(t, c.nodesByPrefix, idxPrefixQuota, "stale prefix bucket must be dropped")
	assert.ElementsMatch(t, []types.UID{"uid-1"}, c.nodesByPrefix[idxPrefixTenant]["node-a"].UnsortedList())
	_, hasUID2 := c.indexEntryByUID["uid-2"] // r2 only has quota label => no longer indexed
	assert.False(t, hasUID2)
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())

	// Disable the index entirely: every backing structure must be released.
	c.setReservationSelectorIndexConfig(nil)
	assert.False(t, c.indexEnabled)
	assert.Nil(t, c.nodesByPrefix)
	assert.Nil(t, c.indexEntryByUID)
	assert.Nil(t, c.indexedPrefixes)
}

// TestReservationCacheIndexExactKey covers the "plain key" indexing path
// (label pattern "xxx: yyy"), which is bucketed by (key, value) tuple so a
// selector `{xxx: yyy}` lands directly on the precise candidate set without
// over-matching reservations that only share the same key.
func TestReservationCacheIndexExactKey(t *testing.T) {
	c := newIndexedCacheWithKeys()

	// r1/r2/r3 share the same indexed key "team" but with DIFFERENT values:
	// r1=alpha on node-a, r2=beta on node-b, r3=alpha on node-c. The (k,v)
	// bucketing must keep alpha and beta in separate value-level sub-buckets
	// so a selector {team:alpha} returns only node-a + node-c (NOT node-b).
	// r4 hits exact key "project" with value=x on node-a.
	// r5 has key "team-foo" which MUST NOT be over-matched as exact "team"
	// nor matched by any indexed prefix.
	c.updateReservation(newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{idxExactTeam: "alpha"}))
	c.updateReservation(newIndexTestReservation("uid-2", "r2", "node-b", map[string]string{idxExactTeam: "beta"}))
	c.updateReservation(newIndexTestReservation("uid-3", "r3", "node-c", map[string]string{idxExactTeam: "alpha"}))
	c.updateReservation(newIndexTestReservation("uid-4", "r4", "node-a", map[string]string{idxExactProject: "x"}))
	c.updateReservation(newIndexTestReservation("uid-5", "r5", "node-d", map[string]string{idxExactTeam + "-foo": "y"}))

	// Bucket layout: nodesByExactKV["team"]["alpha"]={node-a:{uid-1}, node-c:{uid-3}};
	// nodesByExactKV["team"]["beta"]={node-b:{uid-2}}; nodesByExactKV["project"]["x"]={node-a:{uid-4}};
	// r5 is NOT indexed at all.
	assert.ElementsMatch(t, []types.UID{"uid-1"}, c.nodesByExactKV[idxExactTeam]["alpha"]["node-a"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-3"}, c.nodesByExactKV[idxExactTeam]["alpha"]["node-c"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-2"}, c.nodesByExactKV[idxExactTeam]["beta"]["node-b"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-4"}, c.nodesByExactKV[idxExactProject]["x"]["node-a"].UnsortedList())
	_, hasUID5 := c.indexEntryByUID["uid-5"]
	assert.False(t, hasUID5, "label key 'team-foo' must NOT be over-matched as exact 'team'")
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())

	// Selector value precision: {team:alpha} hits only node-a + node-c; the
	// node-b reservation (team=beta) MUST NOT leak into the candidate set.
	nodes, hit := c.FilterByReservationSelector(map[string]string{idxExactTeam: "alpha"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-a", "node-c"}, nodes)

	// {team:beta} hits only node-b.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam: "beta"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-b"}, nodes)

	// {team:nonexistent}: indexed key but no reservation has that value =>
	// hit=true with an empty candidate set so the caller can short-circuit.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam: "nonexistent"})
	assert.True(t, hit)
	assert.Empty(t, nodes)

	// {project:x} hits only node-a.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactProject: "x"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-a"}, nodes)

	// AND-join across two exact-(k,v) buckets: only node-a hosts BOTH
	// {team:alpha} and {project:x}.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam: "alpha", idxExactProject: "x"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-a"}, nodes)

	// AND-join with a value mismatch on one axis short-circuits to empty:
	// node-a has team=alpha (not beta), so {team:beta, project:x} => [].
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam: "beta", idxExactProject: "x"})
	assert.True(t, hit)
	assert.Empty(t, nodes)

	// Selector key that is neither in KeyPrefixes nor in Keys MUST fall through
	// (hit=false) so the caller falls back to the legacy O(N) scan.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam + "-foo": "y"})
	assert.False(t, hit)
	assert.Nil(t, nodes)

	// Update r1's value alpha->gamma: the (team,alpha)[node-a] cell must drop
	// uid-1 (and since uid-1 was the only one there, the cell is reclaimed),
	// while a brand-new (team,gamma)[node-a] cell appears.
	c.updateReservation(newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{idxExactTeam: "gamma"}))
	assert.Nil(t, c.nodesByExactKV[idxExactTeam]["alpha"]["node-a"], "team[alpha][node-a] must be released after r1's value changed")
	assert.ElementsMatch(t, []types.UID{"uid-3"}, c.nodesByExactKV[idxExactTeam]["alpha"]["node-c"].UnsortedList())
	assert.ElementsMatch(t, []types.UID{"uid-1"}, c.nodesByExactKV[idxExactTeam]["gamma"]["node-a"].UnsortedList())
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())

	// {team:alpha} now drops node-a, returns only node-c (uid-3).
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam: "alpha"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-c"}, nodes)

	// Drain remaining reservations and verify every backing bucket is reclaimed.
	for _, uid := range []string{"uid-1", "uid-2", "uid-3", "uid-4", "uid-5"} {
		c.DeleteReservation(&schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{UID: types.UID(uid)},
			Status:     schedulingv1alpha1.ReservationStatus{NodeName: "node-a"},
		})
	}
	assert.Empty(t, c.indexEntryByUID)
	assert.Empty(t, c.nodesByExactKV)
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())
}

// TestReservationCacheIndexMixedPrefixAndExactKey covers a workload that has
// BOTH label patterns at the same time (the production scenario), and asserts
// that the AND-join across a prefix bucket and an exact-(k,v) bucket only
// returns nodes that satisfy every selector key+value simultaneously.
func TestReservationCacheIndexMixedPrefixAndExactKey(t *testing.T) {
	c := newIndexedCacheWithKeys()

	// r1 hits both buckets: prefix "quota" key + exact "team=alpha" on node-a.
	// r2 hits only the prefix bucket on node-b.
	// r3 hits only the exact bucket with team=alpha on node-c.
	// r4 hits only the exact bucket with team=beta on node-d (different value).
	c.updateReservation(newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{
		idxPrefixQuota + "q": "q",
		idxExactTeam:         "alpha",
	}))
	c.updateReservation(newIndexTestReservation("uid-2", "r2", "node-b", map[string]string{idxPrefixQuota + "q": "q"}))
	c.updateReservation(newIndexTestReservation("uid-3", "r3", "node-c", map[string]string{idxExactTeam: "alpha"}))
	c.updateReservation(newIndexTestReservation("uid-4", "r4", "node-d", map[string]string{idxExactTeam: "beta"}))

	// r1's entry must record BOTH a tracked prefix and the (key, value) tuple.
	entry := c.indexEntryByUID["uid-1"]
	assert.NotNil(t, entry)
	assert.True(t, entry.prefixes.Has(idxPrefixQuota))
	assert.Equal(t, "alpha", entry.kvs[idxExactTeam])
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())

	// Prefix-only selector: union of node-a (from r1) and node-b (from r2).
	nodes, hit := c.FilterByReservationSelector(map[string]string{idxPrefixQuota + "x": "v"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-a", "node-b"}, nodes)

	// Exact (team=alpha) selector: only node-a (r1) and node-c (r3); node-d
	// (team=beta) MUST NOT leak in.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam: "alpha"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-a", "node-c"}, nodes)

	// Exact (team=beta) selector: only node-d.
	nodes, hit = c.FilterByReservationSelector(map[string]string{idxExactTeam: "beta"})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-d"}, nodes)

	// Mixed selector: AND-join => only node-a hosts a reservation that hits
	// BOTH the prefix bucket AND the exact (team=alpha) bucket. node-b lacks
	// the team label; node-c lacks the quota-prefixed key.
	nodes, hit = c.FilterByReservationSelector(map[string]string{
		idxPrefixQuota + "x": "v",
		idxExactTeam:         "alpha",
	})
	assert.True(t, hit)
	assert.ElementsMatch(t, []string{"node-a"}, nodes)

	// Mixed selector with a non-matching exact value: node-a has team=alpha
	// (not gamma), so the AND-join collapses to empty even though the prefix
	// path alone would yield {node-a, node-b}.
	nodes, hit = c.FilterByReservationSelector(map[string]string{
		idxPrefixQuota + "x": "v",
		idxExactTeam:         "gamma",
	})
	assert.True(t, hit)
	assert.Empty(t, nodes)

	// Dump must expose ByKey (aggregated across values) and ByKeyValue
	// (per-(k,v) granularity) alongside the prefix bucket.
	snap := c.DumpReservationSelectorIndex(true)
	assert.True(t, snap.Enabled)
	assert.ElementsMatch(t, []string{idxExactTeam, idxExactProject}, snap.Keys)

	// ByKey aggregates: team key has 3 reservations across 3 nodes (uid-1,3,4).
	assert.NotNil(t, snap.ByKey[idxExactTeam])
	assert.Equal(t, 3, snap.ByKey[idxExactTeam].Reservations)
	assert.Equal(t, 3, snap.ByKey[idxExactTeam].Nodes)

	// ByKeyValue exposes per-value sub-buckets:
	//   team=alpha => 2 reservations on 2 nodes (r1@node-a, r3@node-c)
	//   team=beta  => 1 reservation on 1 node (r4@node-d)
	assert.NotNil(t, snap.ByKeyValue[idxExactTeam])
	alphaStat := snap.ByKeyValue[idxExactTeam]["alpha"]
	assert.NotNil(t, alphaStat)
	assert.Equal(t, 2, alphaStat.Reservations)
	assert.Equal(t, 2, alphaStat.Nodes)
	betaStat := snap.ByKeyValue[idxExactTeam]["beta"]
	assert.NotNil(t, betaStat)
	assert.Equal(t, 1, betaStat.Reservations)
	assert.Equal(t, 1, betaStat.Nodes)

	// Prefix bucket: r1+r2 on node-a/node-b.
	assert.NotNil(t, snap.ByPrefix[idxPrefixQuota])
	assert.Equal(t, 2, snap.ByPrefix[idxPrefixQuota].Reservations)
}

func TestReservationCacheIndexConsistencyHighFrequency(t *testing.T) {
	// Stress the index with concurrent add/update/delete + status flips and
	// assert the internal auditor reports no inconsistency at the end.
	c := newIndexedCache()
	const (
		nodes  = 8
		rsvCnt = 64
		rounds = 50
	)
	nodeOf := func(i int) string { return "node-" + itoa(i%nodes) }
	uidOf := func(i int) types.UID { return types.UID("u" + itoa(i)) }
	labelsOf := func(i int) map[string]string {
		// Mix the two indexed prefixes to exercise both buckets.
		if i%3 == 0 {
			return map[string]string{idxPrefixQuota + itoa(i): itoa(i), idxPrefixTenant + itoa(i): itoa(i)}
		}
		if i%3 == 1 {
			return map[string]string{idxPrefixQuota + itoa(i): itoa(i)}
		}
		return map[string]string{idxPrefixTenant + itoa(i): itoa(i)}
	}

	var wg sync.WaitGroup
	wg.Add(4)
	// Writer A: rapid add/update churn (re-add same UIDs with possibly changed
	// labels to simulate label/binding updates).
	go func() {
		defer wg.Done()
		for r := 0; r < rounds; r++ {
			for i := 0; i < rsvCnt; i++ {
				labels := labelsOf(i + r)
				c.updateReservation(&schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{UID: uidOf(i), Name: "r" + itoa(i), Labels: labels},
					Spec:       schedulingv1alpha1.ReservationSpec{Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{}}},
					Status:     schedulingv1alpha1.ReservationStatus{NodeName: nodeOf(i), Phase: schedulingv1alpha1.ReservationAvailable},
				})
			}
		}
	}()
	// Writer B: high-frequency delete-then-readd to simulate fast create/delete.
	go func() {
		defer wg.Done()
		for r := 0; r < rounds; r++ {
			for i := 0; i < rsvCnt; i += 2 {
				c.DeleteReservation(&schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{UID: uidOf(i), Name: "r" + itoa(i)},
					Status:     schedulingv1alpha1.ReservationStatus{NodeName: nodeOf(i)},
				})
			}
		}
	}()
	// Writer C: status flip via updateReservationIfExists (Failed/Succeeded path).
	go func() {
		defer wg.Done()
		for r := 0; r < rounds; r++ {
			for i := 1; i < rsvCnt; i += 2 {
				c.updateReservationIfExists(&schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{UID: uidOf(i), Name: "r" + itoa(i), Labels: labelsOf(i + r)},
					Spec:       schedulingv1alpha1.ReservationSpec{Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{}}},
					Status:     schedulingv1alpha1.ReservationStatus{NodeName: nodeOf(i), Phase: schedulingv1alpha1.ReservationFailed},
				})
			}
		}
	}()
	// Reader: concurrent filter calls to race against the writers.
	go func() {
		defer wg.Done()
		for r := 0; r < rounds*4; r++ {
			c.FilterByReservationSelector(map[string]string{idxPrefixQuota + "any": "v"})
			c.FilterByReservationSelector(map[string]string{idxPrefixTenant + "any": "v"})
		}
	}()
	wg.Wait()

	// Snapshot reservationInfos under lock, then drain everything to confirm
	// the index is fully reversible (no ledger leak left behind).
	c.lock.Lock()
	remaining := make([]types.UID, 0, len(c.reservationInfos))
	nodeByUID := make(map[types.UID]string, len(c.reservationInfos))
	for uid, rInfo := range c.reservationInfos {
		remaining = append(remaining, uid)
		nodeByUID[uid] = rInfo.GetNodeName()
	}
	c.lock.Unlock()

	// Auditor must already be clean BEFORE the drain (steady-state invariant).
	assert.Empty(t, c.checkReservationSelectorIndexConsistency(), "index must be consistent under high-frequency concurrent churn")

	for _, uid := range remaining {
		c.DeleteReservation(&schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{UID: uid},
			Status:     schedulingv1alpha1.ReservationStatus{NodeName: nodeByUID[uid]},
		})
	}
	assert.Empty(t, c.indexEntryByUID, "every indexEntry must be released after full drain")
	assert.Empty(t, c.nodesByPrefix, "every nodesByPrefix bucket must be released after full drain")
	assert.Empty(t, c.checkReservationSelectorIndexConsistency())
}

func TestDumpReservationSelectorIndex(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		c := newReservationCache(nil)
		snap := c.DumpReservationSelectorIndex(true)
		assert.NotNil(t, snap)
		assert.False(t, snap.Enabled)
		assert.Empty(t, snap.ByPrefix)
	})
	t.Run("enabled_detail", func(t *testing.T) {
		c := newIndexedCache()
		c.updateReservation(newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{idxPrefixQuota + "q": "q"}))
		c.updateReservation(newIndexTestReservation("uid-2", "r2", "node-a", map[string]string{idxPrefixQuota + "q2": "q2"}))
		c.updateReservation(newIndexTestReservation("uid-3", "r3", "node-b", map[string]string{idxPrefixTenant + "t": "t"}))
		snap := c.DumpReservationSelectorIndex(true)
		assert.True(t, snap.Enabled)
		assert.ElementsMatch(t, []string{idxPrefixQuota, idxPrefixTenant}, snap.Prefixes)
		assert.Equal(t, 3, snap.IndexedReservations)
		quota := snap.ByPrefix[idxPrefixQuota]
		assert.NotNil(t, quota)
		assert.Equal(t, 1, quota.Nodes) // both r1 and r2 are on node-a
		assert.Equal(t, 2, quota.Reservations)
		assert.ElementsMatch(t, []types.UID{"uid-1", "uid-2"}, quota.ByNode["node-a"])
		tenant := snap.ByPrefix[idxPrefixTenant]
		assert.NotNil(t, tenant)
		assert.Equal(t, []types.UID{"uid-3"}, tenant.ByNode["node-b"])
	})
	t.Run("enabled_summary_only", func(t *testing.T) {
		c := newIndexedCache()
		c.updateReservation(newIndexTestReservation("uid-1", "r1", "node-a", map[string]string{idxPrefixQuota + "q": "q"}))
		c.updateReservation(newIndexTestReservation("uid-2", "r2", "node-a", map[string]string{idxPrefixQuota + "q2": "q2"}))
		c.updateReservation(newIndexTestReservation("uid-3", "r3", "node-b", map[string]string{idxPrefixTenant + "t": "t"}))
		snap := c.DumpReservationSelectorIndex(false)
		assert.True(t, snap.Enabled)
		assert.Equal(t, 3, snap.IndexedReservations)
		quota := snap.ByPrefix[idxPrefixQuota]
		assert.NotNil(t, quota)
		assert.Equal(t, 1, quota.Nodes)
		assert.Equal(t, 2, quota.Reservations)
		// summary mode must NOT expose per-node UID lists.
		assert.Nil(t, quota.ByNode)
		tenant := snap.ByPrefix[idxPrefixTenant]
		assert.NotNil(t, tenant)
		assert.Nil(t, tenant.ByNode)
	})
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	negative := false
	if i < 0 {
		negative = true
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

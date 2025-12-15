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

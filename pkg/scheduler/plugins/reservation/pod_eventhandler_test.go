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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestPodEventHandler(t *testing.T) {
	handler := &podEventHandler{
		cache:     newReservationCache(nil),
		nominator: newNominator(nil, nil),
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
			Annotations: map[string]string{
				reservationutil.AnnotationReservePod:      "true",
				reservationutil.AnnotationReservationName: "test-pod",
			},
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

	podInfo, _ := framework.NewPodInfo(pod)
	handler.nominator.AddNominatedReservePod(podInfo, "test-node-1")
	assert.Equal(t, "test-node-1", handler.nominator.nominatedReservePodToNode[pod.UID])
	assert.Equal(t, []*framework.PodInfo{podInfo}, handler.nominator.nominatedReservePod["test-node-1"])
	handler.OnAdd(pod, true)
	rInfo := handler.cache.getReservationInfoByUID(reservationUID)
	assert.Empty(t, rInfo.AssignedPods)
	// pod not assigned, no need to delete reservation nominated node
	assert.Equal(t, "test-node-1", handler.nominator.nominatedReservePodToNode[pod.UID])
	assert.Equal(t, []*framework.PodInfo{podInfo}, handler.nominator.nominatedReservePod["test-node-1"])

	newPod := pod.DeepCopy()
	apiext.SetReservationAllocated(newPod, reservation)
	handler.OnUpdate(pod, newPod)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Len(t, rInfo.AssignedPods, 0)
	// pod not assigned, no need to delete reservation nominated node
	assert.Equal(t, "test-node-1", handler.nominator.nominatedReservePodToNode[pod.UID])
	assert.Equal(t, []*framework.PodInfo{podInfo}, handler.nominator.nominatedReservePod["test-node-1"])

	newPod.Spec.NodeName = reservation.Status.NodeName
	handler.OnUpdate(pod, newPod)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Len(t, rInfo.AssignedPods, 1)
	// pod assigned, delete reservation nominated node
	assert.Equal(t, "", handler.nominator.nominatedReservePodToNode[pod.UID])
	assert.Equal(t, []*framework.PodInfo(nil), handler.nominator.nominatedReservePod["test-node-1"])

	expectPodRequirement := &frameworkext.PodRequirement{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		UID:       pod.UID,
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"),
		},
	}
	assert.Equal(t, expectPodRequirement, rInfo.AssignedPods[pod.UID])

	podInfo, _ = framework.NewPodInfo(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-1",
			UID:  "test-1",
		},
	})
	handler.nominator.nominatedReservePod["test-node-1"] = []*framework.PodInfo{podInfo}
	podInfo, _ = framework.NewPodInfo(newPod)
	handler.nominator.AddNominatedReservePod(podInfo, "test-node-1")
	assert.Equal(t, "test-node-1", handler.nominator.nominatedReservePodToNode[newPod.UID])
	podInfo1, _ := framework.NewPodInfo(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-1",
			UID:  "test-1",
		},
	})
	podInfo2, _ := framework.NewPodInfo(newPod)
	assert.Equal(t, []*framework.PodInfo{podInfo1, podInfo2}, handler.nominator.nominatedReservePod["test-node-1"])

	handler.OnDelete(newPod)
	rInfo = handler.cache.getReservationInfoByUID(reservationUID)
	assert.Empty(t, rInfo.AssignedPods)
	assert.Equal(t, "", handler.nominator.nominatedReservePodToNode[newPod.UID])
	podInfo, _ = framework.NewPodInfo(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-1",
			UID:  "test-1",
		},
	})
	assert.Equal(t, []*framework.PodInfo{podInfo}, handler.nominator.nominatedReservePod["test-node-1"])
}

func TestPodEventHandlerWithOperatingPod(t *testing.T) {
	handler := &podEventHandler{
		cache:     newReservationCache(nil),
		nominator: newNominator(nil, nil),
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
	handler.OnAdd(operatingReservationPod, true)

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
	handler.OnAdd(pod, true)
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

func TestPodEventHandlerUpdatePodAcrossReservations(t *testing.T) {
	handler := &podEventHandler{
		cache:     newReservationCache(nil),
		nominator: newNominator(nil, nil),
	}
	reservation1UID := uuid.NewUUID()
	reservation1Name := "test-reservation-1"
	reservation1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: reservation1Name,
			UID:  reservation1UID,
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
	reservation2UID := uuid.NewUUID()
	reservation2Name := "test-reservation-2"
	reservation2 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: reservation2Name,
			UID:  reservation2UID,
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
	handler.cache.updateReservation(reservation1)
	handler.cache.updateReservation(reservation2)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
		},
	}

	// assign pod to reservation1
	oldPod := pod.DeepCopy()
	apiext.SetReservationAllocated(oldPod, reservation1)
	handler.updatePod(nil, oldPod)
	rInfo1 := handler.cache.getReservationInfoByUID(reservation1UID)
	assert.Len(t, rInfo1.AssignedPods, 1)
	assert.Contains(t, rInfo1.AssignedPods, oldPod.UID)
	rInfo2 := handler.cache.getReservationInfoByUID(reservation2UID)
	assert.Len(t, rInfo2.AssignedPods, 0)

	// move pod from reservation1 to reservation2
	newPod := pod.DeepCopy()
	apiext.SetReservationAllocated(newPod, reservation2)
	handler.updatePod(oldPod, newPod)
	rInfo1 = handler.cache.getReservationInfoByUID(reservation1UID)
	assert.Len(t, rInfo1.AssignedPods, 0)
	rInfo2 = handler.cache.getReservationInfoByUID(reservation2UID)
	assert.Len(t, rInfo2.AssignedPods, 1)
	assert.Contains(t, rInfo2.AssignedPods, newPod.UID)

	// remove reservation allocated
	oldPod = newPod.DeepCopy()
	newPod = newPod.DeepCopy()
	delete(newPod.Annotations, apiext.AnnotationReservationAllocated)
	handler.updatePod(oldPod, newPod)
	rInfo1 = handler.cache.getReservationInfoByUID(reservation1UID)
	assert.Len(t, rInfo1.AssignedPods, 0)
	rInfo2 = handler.cache.getReservationInfoByUID(reservation2UID)
	assert.Len(t, rInfo2.AssignedPods, 0)
}

// TestPodEventHandler_PreAllocatableCacheSync tests event handler cache synchronization
func TestPodEventHandler_PreAllocatableCacheSync(t *testing.T) {
	handler := &podEventHandler{
		cache: newReservationCache(nil),
	}

	tests := []struct {
		name       string
		oldPod     *corev1.Pod
		newPod     *corev1.Pod
		verifyFunc func(t *testing.T, cache *reservationCache)
	}{
		{
			name:   "add label makes pod candidate",
			oldPod: createPodWithLabels("pod1", "node1", nil),
			newPod: createPodWithLabels("pod1", "node1", map[string]string{
				apiext.LabelPodPreAllocatable: "true",
			}),
			verifyFunc: func(t *testing.T, cache *reservationCache) {
				podCache := cache.preAllocatablePodsOnNode["node1"]
				assert.NotNil(t, podCache, "node1 cache should exist")
				assert.Contains(t, podCache.index, types.UID("pod1"), "pod1 should be in cache")
			},
		},
		{
			name: "remove label removes from cache",
			oldPod: createPodWithLabels("pod2", "node1", map[string]string{
				apiext.LabelPodPreAllocatable: "true",
			}),
			newPod: createPodWithLabels("pod2", "node1", nil),
			verifyFunc: func(t *testing.T, cache *reservationCache) {
				podCache := cache.preAllocatablePodsOnNode["node1"]
				if podCache != nil {
					assert.NotContains(t, podCache.index, types.UID("pod2"), "pod2 should be removed from cache")
				}
			},
		},
		{
			name:   "node change moves pod between caches",
			oldPod: createCandidatePod("pod3", "node1", "50"),
			newPod: createCandidatePod("pod3", "node2", "50"),
			verifyFunc: func(t *testing.T, cache *reservationCache) {
				// Should be in node2
				node2Cache := cache.preAllocatablePodsOnNode["node2"]
				assert.NotNil(t, node2Cache, "node2 cache should exist")
				assert.Contains(t, node2Cache.index, types.UID("pod3"), "pod3 should be in node2")

				// Should NOT be in node1
				node1Cache := cache.preAllocatablePodsOnNode["node1"]
				if node1Cache != nil {
					assert.NotContains(t, node1Cache.index, types.UID("pod3"), "pod3 should not be in node1")
				}
			},
		},
		{
			name:   "score change triggers re-sort",
			oldPod: createCandidatePod("pod4", "node1", "40"),
			newPod: createCandidatePod("pod4", "node1", "400"),
			verifyFunc: func(t *testing.T, cache *reservationCache) {
				podCache := cache.preAllocatablePodsOnNode["node1"]
				assert.NotNil(t, podCache)
				item := podCache.index[types.UID("pod4")]
				assert.NotNil(t, item, "pod4 should be in cache")
				assert.Equal(t, int64(400), item.score, "score should be updated to 400")
			},
		},
		{
			name:   "non-candidate pod ignored",
			oldPod: nil,
			newPod: createPodWithLabels("pod5", "node1", map[string]string{
				"other-label": "value",
			}),
			verifyFunc: func(t *testing.T, cache *reservationCache) {
				podCache := cache.preAllocatablePodsOnNode["node1"]
				if podCache != nil {
					assert.NotContains(t, podCache.index, types.UID("pod5"), "non-candidate pod should not be in cache")
				}
			},
		},
		{
			name:   "score unchanged no update",
			oldPod: createCandidatePod("pod6", "node1", "60"),
			newPod: createCandidatePod("pod6", "node1", "60"),
			verifyFunc: func(t *testing.T, cache *reservationCache) {
				podCache := cache.preAllocatablePodsOnNode["node1"]
				assert.NotNil(t, podCache)
				item := podCache.index[types.UID("pod6")]
				assert.NotNil(t, item)
				assert.Equal(t, int64(60), item.score)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup: add oldPod to cache if it's a candidate
			if tt.oldPod != nil && isPreAllocatablePod(tt.oldPod) && tt.oldPod.Spec.NodeName != "" {
				handler.cache.addPreAllocatableCandidateOnNode(tt.oldPod)
			}

			// Execute: update cache
			handler.updatePreAllocatableCandidatesCache(tt.oldPod, tt.newPod)

			// Verify
			tt.verifyFunc(t, handler.cache)
		})
	}
}

// Helper functions for test pod creation

func createPodWithLabels(uid, nodeName string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:    types.UID(uid),
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
	}
}

func createCandidatePod(uid, nodeName, score string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID(uid),
			Labels: map[string]string{
				apiext.LabelPodPreAllocatable: "true",
			},
			Annotations: map[string]string{
				apiext.AnnotationPodPreAllocatableScore: score,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
	}
}

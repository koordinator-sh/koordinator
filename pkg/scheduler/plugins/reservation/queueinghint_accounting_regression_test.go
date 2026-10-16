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
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// TestRestoreUnmatchedReservationSubtractsGrowingAllocation pins the premise
// the Available accounting wake-up rests on.
//
// A reservation and the pods consuming it are both counted in
// NodeInfo.Requested, so restoreUnmatchedReservations subtracts what the
// reservation has allocated to undo the double counting. Associating an
// already-bound pod raises Allocated without changing that pod's requests, so
// more is subtracted and the total fitsNode compares against drops. Allocation
// growth is therefore not something the queueing hint can skip.
func TestRestoreUnmatchedReservationSubtractsGrowingAllocation(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("10"),
		}},
	}
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-unmatched", UID: "r-unmatched"},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("6"),
					}},
				}},
			}},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: node.Name,
			// NewReservationInfo takes an Available reservation's capacity from
			// status, and AllocatedPods are masked to those resource names.
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("6")},
		},
	}
	boundPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "bound", Namespace: "default", UID: "bound"},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("3"),
				}},
			}},
		},
	}
	// The reserve pods and the bound pod together account for the whole node.
	newNodeInfo := func() *framework.NodeInfo {
		ni := framework.NewNodeInfo()
		ni.SetNode(node)
		ni.Requested = &framework.Resource{MilliCPU: 10000}
		return ni
	}

	unassociated := newNodeInfo()
	require.NoError(t, restoreUnmatchedReservations(unassociated, frameworkext.NewReservationInfo(r)))
	assert.Equal(t, int64(10000), unassociated.Requested.MilliCPU,
		"with nothing allocated there is no double counting to undo")

	rInfo := frameworkext.NewReservationInfo(r)
	rInfo.AddAssignedPod(boundPod)
	associated := newNodeInfo()
	require.NoError(t, restoreUnmatchedReservations(associated, rInfo))
	assert.Equal(t, int64(7000), associated.Requested.MilliCPU,
		"associating the bound pod removes its double counting from the node total")

	assert.Less(t, associated.Requested.MilliCPU, unassociated.Requested.MilliCPU,
		"allocation growth lowers what fitsNode compares against, so it can admit a waiter "+
			"that has no relationship to this reservation")
}

// TestReservationInfoPodSlotIsIndependentOfAllocatedQuantities pins the second
// premise: the reservation's pod-count limit is checked against
// len(AssignedPods), not against the allocated resource quantities, so
// releasing an owner can free a slot while Allocated stays equal.
func TestReservationInfoPodSlotIsIndependentOfAllocatedQuantities(t *testing.T) {
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-slots", UID: "r-slots"},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourcePods: resource.MustParse("2"),
					}},
				}},
			}},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:       schedulingv1alpha1.ReservationAvailable,
			NodeName:    "node-1",
			Allocatable: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("2")},
		},
	}
	zeroRequestPod := func(name string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name)},
			Spec:       corev1.PodSpec{NodeName: "node-1", Containers: []corev1.Container{{}}},
		}
	}

	full := frameworkext.NewReservationInfo(r)
	full.AddAssignedPod(zeroRequestPod("owner-a"))
	full.AddAssignedPod(zeroRequestPod("owner-b"))

	freed := frameworkext.NewReservationInfo(r)
	freed.AddAssignedPod(zeroRequestPod("owner-a"))

	assert.Equal(t, 2, full.GetAllocatedPods())
	assert.Equal(t, 1, freed.GetAllocatedPods())
	assert.True(t, quotav1.Equals(full.Allocated, freed.Allocated),
		"the owners carry no requests, so releasing one moves no allocated quantity")

	maxPods := full.Allocatable[corev1.ResourcePods]
	assert.Greater(t, int64(full.GetAllocatedPods())+1, maxPods.Value(),
		"the waiter is rejected by fitsReservation's pod-count check while both owners hold a slot")
	assert.LessOrEqual(t, int64(freed.GetAllocatedPods())+1, maxPods.Value(),
		"releasing one owner frees the slot without changing Allocated")
}

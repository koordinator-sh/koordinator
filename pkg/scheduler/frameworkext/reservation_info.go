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

package frameworkext

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/api/v1/resource"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type ReservationInfo struct {
	Reservation   *schedulingv1alpha1.Reservation
	ResourceNames []corev1.ResourceName
	Allocatable   corev1.ResourceList
	Allocated     corev1.ResourceList
	Pods          map[types.UID]*PodRequirement
}

type PodRequirement struct {
	Namespace string
	Name      string
	UID       types.UID
	Requests  corev1.ResourceList
}

func NewReservationInfo(r *schedulingv1alpha1.Reservation) *ReservationInfo {
	allocatable := reservationutil.ReservationRequests(r)
	resourceNames := quotav1.ResourceNames(allocatable)

	return &ReservationInfo{
		Reservation:   r.DeepCopy(),
		ResourceNames: resourceNames,
		Allocatable:   allocatable,
		Pods:          map[types.UID]*PodRequirement{},
	}
}

func (ri *ReservationInfo) Clone() *ReservationInfo {
	resourceNames := make([]corev1.ResourceName, 0, len(ri.ResourceNames))
	for _, v := range ri.ResourceNames {
		resourceNames = append(resourceNames, v)
	}

	pods := map[types.UID]*PodRequirement{}
	for k, v := range ri.Pods {
		pods[k] = &PodRequirement{
			Namespace: v.Namespace,
			Name:      v.Name,
			UID:       v.UID,
			Requests:  v.Requests.DeepCopy(),
		}
	}

	return &ReservationInfo{
		Reservation:   ri.Reservation.DeepCopy(),
		ResourceNames: resourceNames,
		Allocatable:   ri.Allocatable.DeepCopy(),
		Allocated:     ri.Allocated.DeepCopy(),
		Pods:          pods,
	}
}

func (ri *ReservationInfo) UpdateReservation(r *schedulingv1alpha1.Reservation) {
	ri.Reservation = r.DeepCopy()
	ri.Allocatable = reservationutil.ReservationRequests(r)
	ri.ResourceNames = quotav1.ResourceNames(ri.Allocatable)
	ri.Allocated = quotav1.Mask(ri.Allocated, ri.ResourceNames)
}

func (ri *ReservationInfo) AddPod(pod *corev1.Pod) {
	requests, _ := resource.PodRequestsAndLimits(pod)
	ri.Allocated = quotav1.Add(ri.Allocated, quotav1.Mask(requests, ri.ResourceNames))
	ri.Pods[pod.UID] = &PodRequirement{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       pod.UID,
		Requests:  requests,
	}
}

func (ri *ReservationInfo) RemovePod(pod *corev1.Pod) {
	if requirement, ok := ri.Pods[pod.UID]; ok {
		if len(requirement.Requests) > 0 {
			ri.Allocated = quotav1.SubtractWithNonNegativeResult(ri.Allocated, quotav1.Mask(requirement.Requests, ri.ResourceNames))
		}
		delete(ri.Pods, pod.UID)
	}
}

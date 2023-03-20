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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func setReservationAvailable(r *schedulingv1alpha1.Reservation, nodeName string) {
	r.Status.NodeName = nodeName
	r.Status.Phase = schedulingv1alpha1.ReservationAvailable
	r.Status.CurrentOwners = make([]corev1.ObjectReference, 0)

	requests := getReservationRequests(r)
	r.Status.Allocatable = requests
	r.Status.Allocated = nil

	// initialize the conditions
	r.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{
			Type:               schedulingv1alpha1.ReservationConditionScheduled,
			Status:             schedulingv1alpha1.ConditionStatusTrue,
			Reason:             schedulingv1alpha1.ReasonReservationScheduled,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusTrue,
			Reason:             schedulingv1alpha1.ReasonReservationAvailable,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		},
	}
}

func getReservationRequests(r *schedulingv1alpha1.Reservation) corev1.ResourceList {
	requests, _ := resourceapi.PodRequestsAndLimits(&corev1.Pod{
		Spec: r.Spec.Template.Spec,
	})
	return requests
}

func getReservePorts(r *schedulingv1alpha1.Reservation) framework.HostPortInfo {
	portInfo := framework.HostPortInfo{}
	for _, container := range r.Spec.Template.Spec.Containers {
		for _, podPort := range container.Ports {
			portInfo.Add(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
		}
	}
	return portInfo
}

// matchReservationOwners checks if the scheduling pod matches the reservation's owner spec.
// `reservation.spec.owners` defines the DNF (disjunctive normal form) of ObjectReference, ControllerReference
// (extended), LabelSelector, which means multiple selectors are firstly ANDed and secondly ORed.
func matchReservationOwners(pod *corev1.Pod, r *schedulingv1alpha1.Reservation) bool {
	// assert pod != nil && r != nil
	// Owners == nil matches nothing, while Owners = [{}] matches everything
	for _, owner := range r.Spec.Owners {
		if matchObjectRef(pod, owner.Object) &&
			matchReservationControllerReference(pod, owner.Controller) &&
			matchLabelSelector(pod, owner.LabelSelector) {
			return true
		}
	}
	return false
}

func matchObjectRef(pod *corev1.Pod, objRef *corev1.ObjectReference) bool {
	// `ResourceVersion`, `FieldPath` are ignored.
	// since only pod type are compared, `Kind` field is also ignored.
	return objRef == nil ||
		(len(objRef.UID) == 0 || pod.UID == objRef.UID) &&
			(len(objRef.Name) == 0 || pod.Name == objRef.Name) &&
			(len(objRef.Namespace) == 0 || pod.Namespace == objRef.Namespace) &&
			(len(objRef.APIVersion) == 0 || pod.APIVersion == objRef.APIVersion)
}

func matchReservationControllerReference(pod *corev1.Pod, controllerRef *schedulingv1alpha1.ReservationControllerReference) bool {
	// controllerRef matched if any of pod owner references matches the controllerRef;
	// typically a pod has only one controllerRef
	if controllerRef == nil {
		return true
	}
	if len(controllerRef.Namespace) > 0 && controllerRef.Namespace != pod.Namespace { // namespace field is extended
		return false
	}
	// currently `BlockOwnerDeletion` is ignored
	for _, podOwner := range pod.OwnerReferences {
		if (controllerRef.Controller == nil || podOwner.Controller != nil && *controllerRef.Controller == *podOwner.Controller) &&
			(len(controllerRef.UID) == 0 || controllerRef.UID == podOwner.UID) &&
			(len(controllerRef.Name) == 0 || controllerRef.Name == podOwner.Name) &&
			(len(controllerRef.Kind) == 0 || controllerRef.Kind == podOwner.Kind) &&
			(len(controllerRef.APIVersion) == 0 || controllerRef.APIVersion == podOwner.APIVersion) {
			return true
		}
	}
	return false
}

func matchLabelSelector(pod *corev1.Pod, labelSelector *metav1.LabelSelector) bool {
	if labelSelector == nil {
		return true
	}
	selector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return false
	}
	return selector.Matches(labels.Set(pod.Labels))
}

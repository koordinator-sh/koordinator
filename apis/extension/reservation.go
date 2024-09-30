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

package extension

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	// LabelReservationIgnored indicates whether the pod should schedule ignoring resource reservations on the nodes.
	// If a scheduling pod set this label, the pod can allocate the node unreserved resources unallocated and the
	// reserved resources unallocated of all reservations on the node. If a pod scheduled with this label on a node,
	// the reservations of the node will not consider the pod as their owners. To avoid the pods setting with this label
	// to block the other pods allocated reserved resources, it should be used with the reservation preemption.
	LabelReservationIgnored = SchedulingDomainPrefix + "/reservation-ignored"

	// LabelReservationOrder controls the preference logic for Reservation.
	// Reservation with lower order is preferred to be selected before Reservation with higher order.
	// But if it is 0, Reservation will be selected according to the capacity score.
	LabelReservationOrder = SchedulingDomainPrefix + "/reservation-order"

	// AnnotationReservationAllocated represents the reservation allocated by the pod.
	AnnotationReservationAllocated = SchedulingDomainPrefix + "/reservation-allocated"

	// AnnotationReservationAffinity represents the constraints of Pod selection Reservation
	AnnotationReservationAffinity = SchedulingDomainPrefix + "/reservation-affinity"

	// AnnotationReservationRestrictedOptions represent the Reservation Restricted options
	AnnotationReservationRestrictedOptions = SchedulingDomainPrefix + "/reservation-restricted-options"
)

type ReservationAllocated struct {
	Name string    `json:"name,omitempty"`
	UID  types.UID `json:"uid,omitempty"`
}

// ReservationAffinity represents the constraints of Pod selection Reservation
type ReservationAffinity struct {
	// Specifies the reservation name directly, other reservation affinity fields will be ignored.
	Name string `json:"name,omitempty"`
	// If the affinity requirements specified by this field are not met at
	// scheduling time, the pod will not be scheduled onto the node.
	// If the affinity requirements specified by this field cease to be met
	// at some point during pod execution (e.g. due to an update), the system
	// may or may not try to eventually evict the pod from its node.
	RequiredDuringSchedulingIgnoredDuringExecution *ReservationAffinitySelector `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
	// ReservationSelector is a selector which must be true for the pod to fit on a reservation.
	// Selector which must match a reservation's labels for the pod to be scheduled on that node.
	ReservationSelector map[string]string `json:"reservationSelector,omitempty"`
	// Specifies the pod's reservation tolerations. This can tolerate taints of Reservation.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// ReservationAffinitySelector represents the union of the results of one or more label queries
// over a set of reservations; that is, it represents the OR of the selectors represented
// by the reservation selector terms.
type ReservationAffinitySelector struct {
	// Required. A list of reservation selector terms. The terms are ORed.
	// Reuse corev1.NodeSelectorTerm to avoid defining too many repeated definitions.
	ReservationSelectorTerms []corev1.NodeSelectorTerm `json:"reservationSelectorTerms,omitempty"`
}

type ReservationRestrictedOptions struct {
	// Resources means that when the Pod intersects with these resources,
	// it can only allocate the reserved amount at most.
	// If the Reservation's AllocatePolicy is Restricted, and no resources configured,
	// by default the resources equal all reserved resources by the Reservation.
	Resources []corev1.ResourceName `json:"resources,omitempty"`
}

func IsReservationIgnored(pod *corev1.Pod) bool {
	return pod != nil && pod.Labels != nil && pod.Labels[LabelReservationIgnored] == "true"
}

func GetReservationAllocated(pod *corev1.Pod) (*ReservationAllocated, error) {
	if pod.Annotations == nil {
		return nil, nil
	}
	data, ok := pod.Annotations[AnnotationReservationAllocated]
	if !ok {
		return nil, nil
	}
	reservationAllocated := &ReservationAllocated{}
	err := json.Unmarshal([]byte(data), reservationAllocated)
	if err != nil {
		return nil, err
	}
	return reservationAllocated, nil
}

func SetReservationAllocated(pod *corev1.Pod, r metav1.Object) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	reservationAllocated := &ReservationAllocated{
		Name: r.GetName(),
		UID:  r.GetUID(),
	}
	data, _ := json.Marshal(reservationAllocated) // assert no error
	pod.Annotations[AnnotationReservationAllocated] = string(data)
}

func IsReservationAllocateOnce(r *schedulingv1alpha1.Reservation) bool {
	return pointer.BoolDeref(r.Spec.AllocateOnce, true)
}

func GetReservationAffinity(annotations map[string]string) (*ReservationAffinity, error) {
	s, ok := annotations[AnnotationReservationAffinity]
	if !ok {
		return nil, nil
	}
	var affinity ReservationAffinity
	if s != "" {
		if err := json.Unmarshal([]byte(s), &affinity); err != nil {
			return nil, err
		}
	}
	return &affinity, nil
}

func SetReservationAffinity(obj metav1.Object, affinity *ReservationAffinity) error {
	data, err := json.Marshal(affinity)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationReservationAffinity] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetReservationRestrictedOptions(annotations map[string]string) (*ReservationRestrictedOptions, error) {
	var options ReservationRestrictedOptions
	if s, ok := annotations[AnnotationReservationRestrictedOptions]; ok && s != "" {
		if err := json.Unmarshal([]byte(s), &options); err != nil {
			return nil, err
		}
	}
	return &options, nil
}

func SetReservationRestrictedOptions(obj metav1.Object, options *ReservationRestrictedOptions) error {
	data, err := json.Marshal(options)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationReservationRestrictedOptions] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

const (
	AnnotationExactMatchReservationSpec = SchedulingDomainPrefix + "/exact-match-reservation"
)

type ExactMatchReservationSpec struct {
	ResourceNames []corev1.ResourceName `json:"resourceNames,omitempty"`
}

func SetExactMatchReservationSpec(obj metav1.Object, spec *ExactMatchReservationSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationExactMatchReservationSpec] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetExactMatchReservationSpec(annotations map[string]string) (*ExactMatchReservationSpec, error) {
	if s := annotations[AnnotationExactMatchReservationSpec]; s != "" {
		var exactMatchReservationSpec ExactMatchReservationSpec
		if err := json.Unmarshal([]byte(s), &exactMatchReservationSpec); err != nil {
			return nil, err
		}
		return &exactMatchReservationSpec, nil
	}
	return nil, nil
}

func ExactMatchReservation(podRequests, reservationAllocatable corev1.ResourceList, spec *ExactMatchReservationSpec) bool {
	if spec == nil || len(spec.ResourceNames) == 0 {
		return true
	}
	for _, resourceName := range spec.ResourceNames {
		allocatable, existsInReservation := reservationAllocatable[resourceName]
		request, existsInPod := podRequests[resourceName]
		if !existsInReservation || !existsInPod {
			if !existsInReservation && !existsInPod {
				return true
			}
			return false
		}

		if allocatable.Cmp(request) != 0 {
			return false
		}
	}
	return true
}

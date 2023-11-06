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

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	// LabelPodOperatingMode describes the mode of operation for Pod.
	LabelPodOperatingMode = SchedulingDomainPrefix + "/operating-mode"

	// AnnotationReservationOwners indicates the owner specification which can allocate reserved resources
	AnnotationReservationOwners = SchedulingDomainPrefix + "/reservation-owners"

	// AnnotationReservationCurrentOwner indicates current resource owners which allocated the reservation resources.
	AnnotationReservationCurrentOwner = SchedulingDomainPrefix + "/reservation-current-owner"
)

// The concept of PodOperatingMode refers to the design document https://docs.google.com/document/d/1sbFUA_9qWtorJkcukNULr12FKX6lMvISiINxAURHNFo/edit#heading=h.xgjl2srtytjt

type PodOperatingMode string

const (
	// RunnablePodOperatingMode represents the original pod behavior, it is the default mode where the
	// pod’s containers are executed by Kubelet when the pod is assigned a node.
	RunnablePodOperatingMode PodOperatingMode = "Runnable"

	// ReservationPodOperatingMode means the pod represents a scheduling and resource reservation unit
	ReservationPodOperatingMode PodOperatingMode = "Reservation"
)

func IsReservationOperatingMode(pod *corev1.Pod) bool {
	return pod.Labels[LabelPodOperatingMode] == string(ReservationPodOperatingMode)
}

func SetReservationOwners(obj metav1.Object, owners []schedulingv1alpha1.ReservationOwner) error {
	data, err := json.Marshal(owners)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationReservationOwners] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetReservationOwners(annotations map[string]string) ([]schedulingv1alpha1.ReservationOwner, error) {
	var owners []schedulingv1alpha1.ReservationOwner
	if s := annotations[AnnotationReservationOwners]; s != "" {
		err := json.Unmarshal([]byte(s), &owners)
		if err != nil {
			return nil, err
		}
	}
	return owners, nil
}

func GetReservationCurrentOwner(annotations map[string]string) (*corev1.ObjectReference, error) {
	var owner corev1.ObjectReference
	s := annotations[AnnotationReservationCurrentOwner]
	if s == "" {
		return nil, nil
	}
	err := json.Unmarshal([]byte(s), &owner)
	if err != nil {
		return nil, err
	}
	return &owner, nil
}

func SetReservationCurrentOwner(annotations map[string]string, owner *corev1.ObjectReference) error {
	if owner == nil {
		return nil
	}
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	annotations[AnnotationReservationCurrentOwner] = string(data)
	return nil
}

func RemoveReservationCurrentOwner(annotations map[string]string) {
	delete(annotations, AnnotationReservationCurrentOwner)
}

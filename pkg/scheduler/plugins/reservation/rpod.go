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

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var (
	// AnnotationReservePod indicates whether the pod is a reserved pod.
	AnnotationReservePod = apiext.SchedulingDomainPrefix + "/reserve-pod"
	// AnnotationReservationName indicates the name of the reservation.
	AnnotationReservationName = apiext.SchedulingDomainPrefix + "/reservation-name"
	// AnnotationReservationNode indicates the node name if the reservation specifies a node.
	AnnotationReservationNode = apiext.SchedulingDomainPrefix + "/reservation-node"
)

// NewReservePod returns a fake pod set as the reservation's specifications.
// The reserve pod is only visible for the scheduler and does not make actual creation on nodes.
func NewReservePod(r *schedulingv1alpha1.Reservation) *corev1.Pod {
	reservePod := &corev1.Pod{
		ObjectMeta: r.Spec.Template.ObjectMeta,
		Spec:       r.Spec.Template.Spec,
	}
	// name, uid: reservation uid
	reservePod.Name = GetReservationKey(r)
	reservePod.UID = r.UID
	if len(reservePod.Namespace) <= 0 {
		reservePod.Namespace = corev1.NamespaceDefault
	}

	// annotate the reservePod
	if reservePod.Annotations == nil {
		reservePod.Annotations = map[string]string{}
	}
	reservePod.Annotations[AnnotationReservePod] = "true"
	reservePod.Annotations[AnnotationReservationName] = r.Name // for search inversely

	// annotate node name specified
	if len(reservePod.Spec.NodeName) > 0 {
		// if the reservation specifies a nodeName, annotate it and cleanup spec.nodeName for other plugins not
		// processing the nodeName before binding
		reservePod.Annotations[AnnotationReservationNode] = reservePod.Spec.NodeName
		reservePod.Spec.NodeName = ""
	}
	// use reservation status.nodeName as the real scheduled result
	if nodeName := GetReservationNodeName(r); len(nodeName) > 0 {
		reservePod.Spec.NodeName = nodeName
	}

	return reservePod
}

func IsReservePod(pod *corev1.Pod) bool {
	return pod != nil && pod.Annotations != nil && pod.Annotations[AnnotationReservePod] == "true"
}

func GetReservationKey(r *schedulingv1alpha1.Reservation) string {
	return string(r.UID)
}

func GetReservePodKey(pod *corev1.Pod) string {
	return string(pod.UID)
}

func GetReservePodNodeName(pod *corev1.Pod) string {
	return pod.Annotations[AnnotationReservationNode]
}

func GetReservationNameFromReservePod(pod *corev1.Pod) string {
	return pod.Annotations[AnnotationReservationName]
}

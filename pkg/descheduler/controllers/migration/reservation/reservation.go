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
	"sigs.k8s.io/controller-runtime/pkg/client"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var _ Object = &Reservation{}

type Reservation struct {
	*sev1alpha1.Reservation
}

func NewReservation(reservation *sev1alpha1.Reservation) Object {
	return &Reservation{Reservation: reservation}
}

func (r *Reservation) String() string {
	return r.Reservation.Name
}

func (r *Reservation) OriginObject() client.Object {
	return r.Reservation
}

func (r *Reservation) GetReservationCondition(conditionType sev1alpha1.ReservationConditionType) *sev1alpha1.ReservationCondition {
	for _, v := range r.Status.Conditions {
		if v.Type == conditionType {
			return &sev1alpha1.ReservationCondition{
				LastProbeTime:      v.LastProbeTime,
				LastTransitionTime: v.LastTransitionTime,
				Reason:             v.Reason,
				Message:            v.Message,
			}
		}
	}
	return nil
}

func (r *Reservation) GetUnschedulableCondition() *sev1alpha1.ReservationCondition {
	cond := r.GetReservationCondition(sev1alpha1.ReservationConditionScheduled)
	if cond == nil || cond.Reason != sev1alpha1.ReasonReservationUnschedulable {
		return nil
	}
	return cond
}

func (r *Reservation) QueryPreemptedPodsRefs() []corev1.ObjectReference {
	return nil
}

func (r *Reservation) GetBoundPod() *corev1.ObjectReference {
	if len(r.Status.CurrentOwners) == 0 {
		return nil
	}
	return &r.Status.CurrentOwners[0]
}

func (r *Reservation) GetReservationOwners() []sev1alpha1.ReservationOwner {
	return r.Spec.Owners
}

func (r *Reservation) GetScheduledNodeName() string {
	return r.Status.NodeName
}

func (r *Reservation) IsPending() bool {
	return r.Status.Phase == "" || r.Status.Phase == sev1alpha1.ReservationPending
}

func (r *Reservation) IsScheduled() bool {
	return r.Status.NodeName != ""
}

func (r *Reservation) NeedPreemption() bool {
	return false
}

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

package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func isReservationNeedExpiration(r *schedulingv1alpha1.Reservation) bool {
	// 1. failed or succeeded reservations does not need to expire
	if r.Status.Phase == schedulingv1alpha1.ReservationFailed || r.Status.Phase == schedulingv1alpha1.ReservationSucceeded {
		return false
	}
	// 2. disable expiration if TTL is set as 0
	if r.Spec.TTL != nil && r.Spec.TTL.Duration == 0 {
		return false
	}
	// 3. if both TTL and Expires are set, firstly check Expires
	return r.Spec.Expires != nil && time.Now().After(r.Spec.Expires.Time) ||
		r.Spec.TTL != nil && time.Since(r.CreationTimestamp.Time) > r.Spec.TTL.Duration
}

func isReservationNeedCleanup(r *schedulingv1alpha1.Reservation) bool {
	if r == nil {
		return true
	}
	if reservationutil.IsReservationExpired(r) {
		for _, condition := range r.Status.Conditions {
			if condition.Reason == schedulingv1alpha1.ReasonReservationExpired {
				return time.Since(condition.LastTransitionTime.Time) > defaultGCDuration
			}
		}
	} else if reservationutil.IsReservationSucceeded(r) {
		for _, condition := range r.Status.Conditions {
			if condition.Reason == schedulingv1alpha1.ReasonReservationSucceeded {
				return time.Since(condition.LastProbeTime.Time) > defaultGCDuration
			}
		}
	}
	return false
}

func setReservationExpired(r *schedulingv1alpha1.Reservation) {
	r.Status.Phase = schedulingv1alpha1.ReservationFailed
	// not duplicate expired info
	idx := -1
	isReady := false
	for i, condition := range r.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ReservationConditionReady {
			idx = i
			isReady = condition.Status == schedulingv1alpha1.ConditionStatusTrue
		}
	}
	if idx < 0 { // if not set condition
		condition := schedulingv1alpha1.ReservationCondition{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusFalse,
			Reason:             schedulingv1alpha1.ReasonReservationExpired,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		}
		r.Status.Conditions = append(r.Status.Conditions, condition)
	} else if isReady { // if was ready
		condition := schedulingv1alpha1.ReservationCondition{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusFalse,
			Reason:             schedulingv1alpha1.ReasonReservationExpired,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		}
		r.Status.Conditions[idx] = condition
	} else { // if already not ready
		r.Status.Conditions[idx].Reason = schedulingv1alpha1.ReasonReservationExpired
		r.Status.Conditions[idx].LastProbeTime = metav1.Now()
	}
}

func setReservationSucceeded(r *schedulingv1alpha1.Reservation) {
	r.Status.Phase = schedulingv1alpha1.ReservationSucceeded
	idx := -1
	for i, condition := range r.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ReservationConditionReady {
			idx = i
		}
	}
	if idx < 0 { // if not set condition
		condition := schedulingv1alpha1.ReservationCondition{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusFalse,
			Reason:             schedulingv1alpha1.ReasonReservationSucceeded,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		}
		r.Status.Conditions = append(r.Status.Conditions, condition)
	} else {
		r.Status.Conditions[idx].Status = schedulingv1alpha1.ConditionStatusFalse
		r.Status.Conditions[idx].Reason = schedulingv1alpha1.ReasonReservationSucceeded
		r.Status.Conditions[idx].LastProbeTime = metav1.Now()
	}
}

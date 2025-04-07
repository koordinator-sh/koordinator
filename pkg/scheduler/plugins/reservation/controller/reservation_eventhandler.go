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
	"k8s.io/client-go/tools/cache"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func (c *Controller) onReservationAdd(obj interface{}) {
	reservation, _ := obj.(*schedulingv1alpha1.Reservation)
	if reservation != nil {
		c.queue.Add(reservation.Name)
	}
}

func (c *Controller) onReservationUpdate(oldObj, newObj interface{}) {
	oldReservation, _ := oldObj.(*schedulingv1alpha1.Reservation)
	newReservation, _ := newObj.(*schedulingv1alpha1.Reservation)
	if oldReservation != nil && newReservation != nil {
		if oldReservation.Generation != newReservation.Generation ||
			oldReservation.Status.Phase != newReservation.Status.Phase ||
			oldReservation.Status.NodeName != newReservation.Status.NodeName {
			c.queue.Add(newReservation.Name)
		}
	}
}

func (c *Controller) onReservationDelete(obj interface{}) {
	var r *schedulingv1alpha1.Reservation
	switch t := obj.(type) {
	case *schedulingv1alpha1.Reservation:
		r = t
	case cache.DeletedFinalStateUnknown:
		r, _ = t.Obj.(*schedulingv1alpha1.Reservation)
	}
	if r == nil {
		return
	}
	c.queue.Add(r.Name)
}

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

package eventhandlers

import (
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
)

var assignedPodDelete = framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Delete, Label: "AssignedPodDelete"}

// AddScheduleEventHandler adds reservation event handlers for the scheduler just like pods'.
// One special case is that reservations have expiration, which the scheduler should cleanup expired ones from the
// cache and queue.
func AddScheduleEventHandler(sched *scheduler.Scheduler, extendedHandle frameworkext.ExtendedHandle) {
	reservationInformer := extendedHandle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations().Informer()
	// scheduled reservations for pod cache
	reservationInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *schedulingv1alpha1.Reservation:
				return reservation.IsReservationScheduled(t)
			case cache.DeletedFinalStateUnknown:
				if _, ok := t.Obj.(*schedulingv1alpha1.Reservation); ok {
					// DeletedFinalStateUnknown object can be stale, so just try to cleanup without check.
					return true
				}
				klog.Errorf("unable to convert object %T to *schedulingv1alpha1.Reservation in %T", t.Obj, sched)
				return false
			default:
				klog.Errorf("unable to handle object in %T: %T", obj, sched)
				return false
			}
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				addReservationToCache(sched, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				updateReservationInCache(sched, oldObj, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				deleteReservationFromCache(sched, obj)
			},
		},
	})
	// unscheduled & non-expired reservations for scheduling queue
	reservationInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *schedulingv1alpha1.Reservation:
				// scheduler is always responsible for schedulingv1alpha1.reservation object
				return !reservation.IsReservationScheduled(t) && !reservation.IsReservationExpired(t)
			case cache.DeletedFinalStateUnknown:
				if _, ok := t.Obj.(*schedulingv1alpha1.Reservation); ok {
					// DeletedFinalStateUnknown object can be stale, so just try to cleanup without check.
					return true
				}
				klog.Errorf("unable to convert object %T to *schedulingv1alpha1.Reservation in %T", t.Obj, sched)
				return false
			default:
				klog.Errorf("unable to handle object in %T: %T", obj, sched)
				return false
			}
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				addReservationToSchedulingQueue(sched, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				updateReservationInSchedulingQueue(sched, oldObj, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				deleteReservationFromSchedulingQueue(sched, obj)
			},
		},
	})
	// expired reservations
	reservationInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *schedulingv1alpha1.Reservation:
				// scheduler is always responsible for schedulingv1alpha1.reservation object
				return reservation.IsReservationExpired(t)
			default: // else should be processed by other handlers
				klog.Errorf("unable to handle object in %T: %T", obj, sched)
				return false
			}
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				handleExpiredReservation(sched, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				handleExpiredReservation(sched, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				handleExpiredReservation(sched, obj)
			},
		},
	})
}

func addReservationToCache(sched *scheduler.Scheduler, obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.Errorf("addReservationToCache failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	err := reservation.ValidateReservation(r)
	if err != nil {
		klog.Errorf("addReservationToCache failed, invalid reservation, err: %v", err)
		return
	}
	klog.V(3).InfoS("Add event for scheduled reservation", "reservation", klog.KObj(r))

	// update pod cache and trigger pod assigned event for scheduling queue
	reservePod := reservation.NewReservePod(r)
	if err := sched.SchedulerCache.AddPod(reservePod); err != nil {
		klog.Errorf("scheduler cache AddPod failed for reservation, reservation %s, err: %v", klog.KObj(reservePod), err)
	}
	sched.SchedulingQueue.AssignedPodAdded(reservePod)
}

func updateReservationInCache(sched *scheduler.Scheduler, oldObj, newObj interface{}) {
	oldR, oldOK := oldObj.(*schedulingv1alpha1.Reservation)
	newR, newOK := newObj.(*schedulingv1alpha1.Reservation)
	if !oldOK || !newOK {
		klog.Errorf("updateReservationInCache failed, cannot convert object to *schedulingv1alpha1.Reservation, old %T, new %T", oldObj, newObj)
		return
	}

	// A delete event followed by an immediate add event may be merged into a update event.
	// In this case, we should invalidate the old object, and then add the new object.
	if oldR.UID != newR.UID {
		deleteReservationFromCache(sched, oldObj)
		addReservationToCache(sched, newObj)
		return
	}

	// nodeName update of the same reservations is not allowed and may corrupt the cache
	if oldR.Status.NodeName != newR.Status.NodeName {
		klog.Errorf("updateReservationInCache failed, update on status.nodeName is forbidden, old %s, new %s",
			oldR.Status.NodeName, newR.Status.NodeName)
		return
	}

	// update pod cache and trigger pod assigned event for scheduling queue
	oldReservePod := reservation.NewReservePod(oldR)
	newReservePod := reservation.NewReservePod(newR)
	if err := sched.SchedulerCache.UpdatePod(oldReservePod, newReservePod); err != nil {
		klog.Errorf("scheduler cache UpdatePod failed for reservation, old %s, new %s, err: %v", klog.KObj(oldR), klog.KObj(newR), err)
	}
	sched.SchedulingQueue.AssignedPodUpdated(newReservePod)
}

func deleteReservationFromCache(sched *scheduler.Scheduler, obj interface{}) {
	var r *schedulingv1alpha1.Reservation
	switch t := obj.(type) {
	case *schedulingv1alpha1.Reservation:
		r = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		r, ok = t.Obj.(*schedulingv1alpha1.Reservation)
		if !ok {
			klog.Errorf("deleteReservationFromCache failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", t.Obj)
			return
		}
	default:
		klog.Errorf("deleteReservationFromCache failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	klog.V(3).InfoS("Delete event for scheduled reservation", "reservation", klog.KObj(r))

	// delete pod cache and trigger pod deleted event for scheduling queue
	reservePod := reservation.NewReservePod(r)
	if err := sched.SchedulerCache.RemovePod(reservePod); err != nil {
		klog.Errorf("scheduler cache RemovePod failed for reservation, reservation %s, err: %v", klog.KObj(r), err)
	}
	sched.SchedulingQueue.MoveAllToActiveOrBackoffQueue(assignedPodDelete, nil)
}

func addReservationToSchedulingQueue(sched *scheduler.Scheduler, obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.Errorf("addReservationToSchedulingQueue failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	err := reservation.ValidateReservation(r)
	if err != nil {
		klog.Errorf("addReservationToSchedulingQueue failed, invalid reservation, err: %v", err)
		return
	}
	klog.V(3).InfoS("Add event for unscheduled reservation", "reservation", klog.KObj(r))

	reservePod := reservation.NewReservePod(r)
	if err = sched.SchedulingQueue.Add(reservePod); err != nil {
		klog.Errorf("failed to add reserve pod into scheduling queue, reservation %v, err: %v", klog.KObj(reservePod), err)
	}
}

func updateReservationInSchedulingQueue(sched *scheduler.Scheduler, oldObj, newObj interface{}) {
	oldR, oldOK := oldObj.(*schedulingv1alpha1.Reservation)
	newR, newOK := newObj.(*schedulingv1alpha1.Reservation)
	if !oldOK || !newOK {
		klog.Errorf("updateReservationInSchedulingQueue failed, cannot convert object to *schedulingv1alpha1.Reservation, old %T, new %T", oldObj, newObj)
		return
	}
	// Bypass update event that carries identical objects to avoid duplicate scheduling.
	// https://github.com/kubernetes/kubernetes/pull/96071
	if oldR.ResourceVersion == newR.ResourceVersion {
		return
	}

	newReservePod := reservation.NewReservePod(newR)
	isAssumed, err := sched.SchedulerCache.IsAssumedPod(newReservePod)
	if err != nil {
		klog.Errorf("failed to check whether reserve pod %s is assumed, err: %v", klog.KObj(newReservePod), err)
	}
	if isAssumed {
		return
	}

	oldReservePod := reservation.NewReservePod(oldR)
	if err = sched.SchedulingQueue.Update(oldReservePod, newReservePod); err != nil {
		klog.Errorf("failed to update reserve pod in scheduling queue, old %s, new %s, err: %v", klog.KObj(oldReservePod), klog.KObj(newReservePod), err)
	}
}

func deleteReservationFromSchedulingQueue(sched *scheduler.Scheduler, obj interface{}) {
	var r *schedulingv1alpha1.Reservation
	switch t := obj.(type) {
	case *schedulingv1alpha1.Reservation:
		r = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		r, ok = t.Obj.(*schedulingv1alpha1.Reservation)
		if !ok {
			klog.Errorf("deleteReservationFromSchedulingQueue failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", t.Obj)
			return
		}
	default:
		klog.Errorf("deleteReservationFromSchedulingQueue failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	klog.V(3).InfoS("Delete event for unscheduled reservation", "reservation", klog.KObj(r))

	reservePod := reservation.NewReservePod(r)
	if err := sched.SchedulingQueue.Delete(reservePod); err != nil {
		klog.Errorf("failed to delete reserve pod in scheduling queue, reservation %s, err: %v", klog.KObj(r), err)
	}
	// Currently, reservations do not support waiting
	// fwk.RejectWaitingPod(reservePod.UID)
}

func handleExpiredReservation(sched *scheduler.Scheduler, obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.Errorf("handleExpiredReservation failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}

	// if the reservation has been scheduled, remove the reserve pod from the pod cache
	reservePod := reservation.NewReservePod(r)
	if len(r.Status.NodeName) > 0 {
		err := sched.SchedulerCache.RemovePod(reservePod)
		if err != nil {
			klog.Errorf("failed to remove reserve pod in scheduler cache, reservation %v, err: %v", klog.KObj(r), err)
		}
		sched.SchedulingQueue.MoveAllToActiveOrBackoffQueue(assignedPodDelete, nil)
	} else { // otherwise, try dequeue the reserve pod from the scheduling queu
		// in case the pod just finished scheduling cycle and deleted, also check if pod is cached
		_, err := sched.SchedulerCache.GetPod(reservePod)
		if err == nil {
			klog.V(5).InfoS("reserve pod is just scheduled and deleted, remove it in cache", "reservation", klog.KObj(r))
			err = sched.SchedulerCache.RemovePod(reservePod)
			if err != nil {
				klog.Errorf("failed to remove reserve pod in scheduler cache, reservation %v, err: %v", klog.KObj(r), err)
			}
			sched.SchedulingQueue.MoveAllToActiveOrBackoffQueue(assignedPodDelete, nil)
		}

		err = sched.SchedulingQueue.Delete(reservePod)
		if err != nil {
			klog.Errorf("failed to delete reserve pod in scheduling queue, reservation %v, err: %v", klog.KObj(r), err)
		}
	}
	klog.V(4).InfoS("handle expired reservation", "reservation", klog.KObj(r), "phase", r.Status.Phase)
}

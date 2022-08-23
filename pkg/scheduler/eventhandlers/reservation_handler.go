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
	"k8s.io/kubernetes/pkg/scheduler/profile"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
)

func AddReservationErrorHandler(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, extendedHandle frameworkext.ExtendedHandle) {
	reservationLister := extendedHandle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations().Lister()
	defaultErrorFn := sched.Error
	sched.Error = func(podInfo *framework.QueuedPodInfo, err error) {
		pod := podInfo.Pod
		// if the pod is not a reserve pod, use the default error handler
		if !reservation.IsReservePod(pod) {
			defaultErrorFn(podInfo, err)
			return
		}

		// NOTE: If the pod is a reserve pod, we simply check the corresponding reservation status if the reserve pod
		// need requeue for the next scheduling cycle.
		if err == scheduler.ErrNoNodesAvailable {
			klog.V(2).InfoS("Unable to schedule reserve pod; no nodes are registered to the cluster; waiting", "pod", klog.KObj(pod))
		} else if fitError, ok := err.(*framework.FitError); ok {
			// Inject UnschedulablePlugins to PodInfo, which will be used later for moving Pods between queues efficiently.
			podInfo.UnschedulablePlugins = fitError.Diagnosis.UnschedulablePlugins
			klog.V(2).InfoS("Unable to schedule reserve pod; no fit; waiting", "pod", klog.KObj(pod), "err", err)
		} else {
			klog.ErrorS(err, "Error scheduling reserve pod; retrying", "pod", klog.KObj(pod))
		}

		// Check if the corresponding reservation exists in informer cache.
		rName := reservation.GetReservationNameFromReservePod(pod)
		cachedR, err := reservationLister.Get(rName)
		if err != nil {
			klog.InfoS("Reservation doesn't exist in informer cache",
				"pod", klog.KObj(pod), "reservation", rName, "err", err)
			return
		}
		// In the case of extender, the pod may have been bound successfully, but timed out returning its response to the scheduler.
		// It could result in the live version to carry .spec.nodeName, and that's inconsistent with the internal-queued version.
		if nodeName := reservation.GetReservationNodeName(cachedR); len(nodeName) != 0 {
			klog.InfoS("Reservation has been assigned to node. Abort adding it back to queue.",
				"pod", klog.KObj(pod), "reservation", rName, "node", nodeName)
			return
		}
		podInfo.PodInfo = framework.NewPodInfo(reservation.NewReservePod(cachedR))
		if err = internalHandler.GetQueue().AddUnschedulableIfNotPresent(podInfo, internalHandler.GetQueue().SchedulingCycle()); err != nil {
			klog.ErrorS(err, "Error occurred")
		}
	}
}

// AddScheduleEventHandler adds reservation event handlers for the scheduler just like pods'.
// One special case is that reservations have expiration, which the scheduler should cleanup expired ones from the
// cache and queue.
func AddScheduleEventHandler(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, extendedHandle frameworkext.ExtendedHandle) {
	reservationInformer := extendedHandle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations().Informer()
	// scheduled reservations for pod cache
	reservationInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *schedulingv1alpha1.Reservation:
				return reservation.IsReservationAvailable(t)
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
				addReservationToCache(sched, internalHandler, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				updateReservationInCache(sched, internalHandler, oldObj, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				deleteReservationFromCache(sched, internalHandler, obj)
			},
		},
	})
	// unscheduled & non-failed reservations for scheduling queue
	reservationInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *schedulingv1alpha1.Reservation:
				return isResponsibleForReservation(sched.Profiles, t) && !reservation.IsReservationAvailable(t) &&
					!reservation.IsReservationFailed(t) && !reservation.IsReservationSucceeded(t)
			case cache.DeletedFinalStateUnknown:
				if r, ok := t.Obj.(*schedulingv1alpha1.Reservation); ok {
					// DeletedFinalStateUnknown object can be stale, so just try to cleanup without check.
					return isResponsibleForReservation(sched.Profiles, r)
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
				addReservationToSchedulingQueue(sched, internalHandler, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				updateReservationInSchedulingQueue(sched, internalHandler, oldObj, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				deleteReservationFromSchedulingQueue(sched, internalHandler, obj)
			},
		},
	})
	// failed reservations
	reservationInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *schedulingv1alpha1.Reservation:
				// scheduler is always responsible for schedulingv1alpha1.reservation object
				return reservation.IsReservationFailed(t)
			default: // else should be processed by other handlers
				klog.Errorf("unable to handle object in %T: %T", obj, sched)
				return false
			}
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				handleExpiredReservation(sched, internalHandler, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				handleExpiredReservation(sched, internalHandler, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				handleExpiredReservation(sched, internalHandler, obj)
			},
		},
	})
}

func addReservationToCache(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.Errorf("addReservationToCache failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	// only add valid reservation into cache
	err := reservation.ValidateReservation(r)
	if err != nil {
		klog.Errorf("addReservationToCache failed, invalid reservation, err: %v", err)
		return
	}
	klog.V(3).InfoS("Add event for scheduled reservation", "reservation", klog.KObj(r))

	// update pod cache and trigger pod assigned event for scheduling queue
	reservePod := reservation.NewReservePod(r)
	if err = internalHandler.GetCache().AddPod(reservePod); err != nil {
		klog.Errorf("scheduler cache AddPod failed for reservation, reservation %s, err: %v", klog.KObj(reservePod), err)
	}
	internalHandler.GetQueue().AssignedPodAdded(reservePod)
}

func updateReservationInCache(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, oldObj, newObj interface{}) {
	oldR, oldOK := oldObj.(*schedulingv1alpha1.Reservation)
	newR, newOK := newObj.(*schedulingv1alpha1.Reservation)
	if !oldOK || !newOK {
		klog.Errorf("updateReservationInCache failed, cannot convert object to *schedulingv1alpha1.Reservation, old %T, new %T", oldObj, newObj)
		return
	}

	// A delete event followed by an immediate add event may be merged into a update event.
	// In this case, we should invalidate the old object, and then add the new object.
	if oldR.UID != newR.UID {
		deleteReservationFromCache(sched, internalHandler, oldObj)
		addReservationToCache(sched, internalHandler, newObj)
		return
	}

	// nodeName update of the same reservations is not allowed and may corrupt the cache
	if reservation.GetReservationNodeName(oldR) != reservation.GetReservationNodeName(newR) {
		klog.Errorf("updateReservationInCache failed, update on existing nodeName is forbidden, old %s, new %s",
			reservation.GetReservationNodeName(oldR), reservation.GetReservationNodeName(newR))
		return
	}

	// update pod cache and trigger pod assigned event for scheduling queue
	err := reservation.ValidateReservation(newR)
	if err != nil {
		klog.Errorf("updateReservationInCache failed, invalid reservation, err: %v", err)
		return
	}
	oldReservePod := reservation.NewReservePod(oldR)
	newReservePod := reservation.NewReservePod(newR)
	if err := internalHandler.GetCache().UpdatePod(oldReservePod, newReservePod); err != nil {
		klog.Errorf("scheduler cache UpdatePod failed for reservation, old %s, new %s, err: %v", klog.KObj(oldR), klog.KObj(newR), err)
	}
	internalHandler.GetQueue().AssignedPodAdded(newReservePod)
}

func deleteReservationFromCache(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, obj interface{}) {
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
	err := reservation.ValidateReservation(r)
	if err != nil {
		klog.Errorf("deleteReservationFromCache failed, invalid reservation, err: %v", err)
		return
	}
	reservePod := reservation.NewReservePod(r)
	if err := internalHandler.GetCache().RemovePod(reservePod); err != nil {
		klog.Errorf("scheduler cache RemovePod failed for reservation, reservation %s, err: %v", klog.KObj(r), err)
	}
	internalHandler.MoveAllToActiveOrBackoffQueue(assignedPodDelete)
}

func addReservationToSchedulingQueue(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.Errorf("addReservationToSchedulingQueue failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	klog.V(3).InfoS("Add event for unscheduled reservation", "reservation", klog.KObj(r))

	reservePod := reservation.NewReservePod(r)
	if err := internalHandler.GetQueue().Add(reservePod); err != nil {
		klog.Errorf("failed to add reserve pod into scheduling queue, reservation %v, err: %v", klog.KObj(reservePod), err)
	}
}

func updateReservationInSchedulingQueue(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, oldObj, newObj interface{}) {
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
	isAssumed, err := internalHandler.GetCache().IsAssumedPod(newReservePod)
	if err != nil {
		klog.Errorf("failed to check whether reserve pod %s is assumed, err: %v", klog.KObj(newReservePod), err)
	}
	if isAssumed {
		return
	}

	oldReservePod := reservation.NewReservePod(oldR)
	if err = internalHandler.GetQueue().Update(oldReservePod, newReservePod); err != nil {
		klog.Errorf("failed to update reserve pod in scheduling queue, old %s, new %s, err: %v", klog.KObj(oldReservePod), klog.KObj(newReservePod), err)
	}
}

func deleteReservationFromSchedulingQueue(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, obj interface{}) {
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
	if err := internalHandler.GetQueue().Delete(reservePod); err != nil {
		klog.Errorf("failed to delete reserve pod in scheduling queue, reservation %s, err: %v", klog.KObj(r), err)
	}
	// Currently, reservations do not support waiting
	// fwk.RejectWaitingPod(reservePod.UID)
}

func handleExpiredReservation(sched *scheduler.Scheduler, internalHandler SchedulerInternalHandler, obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.Errorf("handleExpiredReservation failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}

	// if the reservation has been scheduled, remove the reserve pod from the pod cache
	reservePod := reservation.NewReservePod(r)

	// in case the pod has expired before scheduling cache initialized, or the pod just finished scheduling cycle and
	// deleted, both we need to check if pod is cached
	_, err := internalHandler.GetCache().GetPod(reservePod)
	if err == nil {
		err = internalHandler.GetCache().RemovePod(reservePod)
		if err != nil {
			klog.Errorf("failed to remove expired reserve pod in scheduler cache, reservation %v, err: %s",
				klog.KObj(r), err)
		}
		internalHandler.MoveAllToActiveOrBackoffQueue(assignedPodDelete)
	}

	if len(reservation.GetReservationNodeName(r)) <= 0 {
		// pod is unscheduled, try dequeue the reserve pod from the scheduling queue
		err = internalHandler.GetQueue().Delete(reservePod)
		if err != nil {
			klog.Errorf("failed to delete expired reserve pod in scheduling queue, reservation %v, err: %v", klog.KObj(r), err)
		}
	}
	klog.V(4).InfoS("handle expired reservation", "reservation", klog.KObj(r), "phase", r.Status.Phase)
}

func isResponsibleForReservation(profiles profile.Map, r *schedulingv1alpha1.Reservation) bool {
	return profiles.HandlesSchedulerName(reservation.GetReservationSchedulerName(r))
}

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
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/apis/core/validation"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/profile"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulingv1alpha1lister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// Register schedulingv1alpha1 scheme to report event
var _ = schedulingv1alpha1.AddToScheme(scheme.Scheme)

func MakeReservationErrorHandler(
	sched *scheduler.Scheduler,
	schedAdapter frameworkext.Scheduler,
	koordClientSet koordclientset.Interface,
	koordSharedInformerFactory koordinatorinformers.SharedInformerFactory,
) frameworkext.PreErrorHandlerFilter {
	reservationLister := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()
	reservationErrorFn := makeReservationErrorFunc(schedAdapter, reservationLister)
	return func(ctx context.Context, f framework.Framework, podInfo *framework.QueuedPodInfo, status *framework.Status, nominatingInfo *framework.NominatingInfo, start time.Time) bool {
		pod := podInfo.Pod
		fwk, ok := sched.Profiles[pod.Spec.SchedulerName]
		if !ok {
			klog.Errorf("profile not found for scheduler name %q", pod.Spec.SchedulerName)
			return true
		}

		schedulingErr := status.AsError()

		// if the pod is not a reserve pod, use the default error handler
		// If the Pod failed to schedule or no post-filter plugins, should remove exist NominatedReservation of the Pod.
		if _, ok := schedulingErr.(*framework.FitError); !ok || !fwk.HasPostFilterPlugins() {
			if extendedHandle, ok := fwk.(frameworkext.ExtendedHandle); ok {
				if !reservationutil.IsReservePod(pod) {
					extendedHandle.GetReservationNominator().RemoveNominatedReservations(pod)
				} else {
					extendedHandle.GetReservationNominator().DeleteNominatedReservePod(pod)
				}
			}
		}

		if !reservationutil.IsReservePod(pod) {
			return false
		}

		reservationErrorFn(ctx, fwk, podInfo, status, nominatingInfo, start)

		rName := reservationutil.GetReservationNameFromReservePod(pod)
		r, err := reservationLister.Get(rName)
		if err != nil {
			return true
		}

		msg := truncateMessage(schedulingErr.Error())
		fwk.EventRecorder().Eventf(r, nil, corev1.EventTypeWarning, "FailedScheduling", "Scheduling", msg)

		updateReservationStatus(koordClientSet, reservationLister, rName, schedulingErr)
		return true
	}
}

func makeReservationErrorFunc(sched frameworkext.Scheduler, reservationLister schedulingv1alpha1lister.ReservationLister) scheduler.FailureHandlerFn {
	return func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *framework.Status, nominatingInfo *framework.NominatingInfo, start time.Time) {
		pod := podInfo.Pod
		err := status.AsError()
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
		rName := reservationutil.GetReservationNameFromReservePod(pod)
		cachedR, err := reservationLister.Get(rName)
		if err != nil {
			klog.InfoS("Reservation doesn't exist in informer cache",
				"pod", klog.KObj(pod), "reservation", rName, "err", err)
			return
		}
		// In the case of extender, the pod may have been bound successfully, but timed out returning its response to the scheduler.
		// It could result in the live version to carry .spec.nodeName, and that's inconsistent with the internal-queued version.
		if nodeName := reservationutil.GetReservationNodeName(cachedR); len(nodeName) != 0 {
			klog.InfoS("Reservation has been assigned to node. Abort adding it back to queue.",
				"pod", klog.KObj(pod), "reservation", rName, "node", nodeName)
			return
		}
		podInfo.PodInfo, _ = framework.NewPodInfo(reservationutil.NewReservePod(cachedR))
		logger := klog.FromContext(ctx)
		if err = sched.GetSchedulingQueue().AddUnschedulableIfNotPresent(logger, podInfo, sched.GetSchedulingQueue().SchedulingCycle()); err != nil {
			klog.ErrorS(err, "Error occurred")
		}
	}
}

func updateReservationStatus(client koordclientset.Interface, reservationLister schedulingv1alpha1lister.ReservationLister, rName string, schedulingErr error) {
	err := util.RetryOnConflictOrTooManyRequests(func() error {
		r, err := reservationLister.Get(rName)
		if errors.IsNotFound(err) {
			klog.V(4).Infof("skip the UpdateStatus for reservation %q since the object is not found", rName)
			return nil
		} else if err != nil {
			klog.V(3).ErrorS(err, "failed to get reservation", "reservation", rName)
			return err
		}

		curR := r.DeepCopy()
		setReservationUnschedulable(curR, schedulingErr.Error())
		_, err = client.SchedulingV1alpha1().Reservations().UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
		if err != nil {
			klog.V(4).ErrorS(err, "failed to UpdateStatus for unschedulable", "reservation", klog.KObj(curR))
		}
		return err
	})
	if err != nil {
		klog.Warningf("failed to UpdateStatus reservation %s, err: %v", rName, err)
	}
}

func setReservationUnschedulable(r *schedulingv1alpha1.Reservation, msg string) {
	// unschedule reservations can try scheduling in next cycles, so we does not update its phase
	// not duplicate condition info
	idx := -1
	isScheduled := false
	for i, condition := range r.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ReservationConditionScheduled {
			idx = i
			isScheduled = condition.Status == schedulingv1alpha1.ConditionStatusTrue
		}
	}
	if idx < 0 { // if not set condition
		condition := schedulingv1alpha1.ReservationCondition{
			Type:               schedulingv1alpha1.ReservationConditionScheduled,
			Status:             schedulingv1alpha1.ConditionStatusFalse,
			Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
			Message:            msg,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		}
		r.Status.Conditions = append(r.Status.Conditions, condition)
	} else if isScheduled { // if is scheduled, keep the condition status
		r.Status.Conditions[idx].LastProbeTime = metav1.Now()
	} else { // if already unschedulable, update the message
		r.Status.Conditions[idx].Reason = schedulingv1alpha1.ReasonReservationUnschedulable
		r.Status.Conditions[idx].Message = msg
		r.Status.Conditions[idx].LastProbeTime = metav1.Now()
	}
}

func truncateMessage(message string) string {
	max := validation.NoteLengthLimit
	if len(message) <= max {
		return message
	}
	suffix := " ..."
	return message[:max-len(suffix)] + suffix
}

// AddScheduleEventHandler adds reservation event handlers for the scheduler just like pods'.
// One special case is that reservations have expiration, which the scheduler should cleanup expired ones from the
// cache and queue.
func AddScheduleEventHandler(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler, koordSharedInformerFactory koordinatorinformers.SharedInformerFactory) {
	reservationInformer := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Informer()
	// scheduled reservations for pod cache
	reservationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			addReservationToSchedulerCache(schedAdapter, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			updateReservationInSchedulerCache(schedAdapter, oldObj, newObj)
		},
		DeleteFunc: func(obj interface{}) {
			deleteReservationFromSchedulerCache(schedAdapter, obj)
		},
	})
	// unscheduled & non-failed reservations for scheduling queue
	reservationInformer.AddEventHandler(unscheduledReservationEventHandler(sched, schedAdapter))
}

func unscheduledReservationEventHandler(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler) cache.ResourceEventHandler {
	return cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *schedulingv1alpha1.Reservation:
				return isResponsibleForReservation(sched.Profiles, t) && !reservationutil.IsReservationAvailable(t) &&
					!reservationutil.IsReservationFailed(t) && !reservationutil.IsReservationSucceeded(t)
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
				addReservationToSchedulingQueue(schedAdapter, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				updateReservationInSchedulingQueue(schedAdapter, oldObj, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				deleteReservationFromSchedulingQueue(sched, schedAdapter, obj)
			},
		},
	}
}

func toReservation(obj interface{}) *schedulingv1alpha1.Reservation {
	var r *schedulingv1alpha1.Reservation
	switch t := obj.(type) {
	case *schedulingv1alpha1.Reservation:
		r = t
	case cache.DeletedFinalStateUnknown:
		r, _ = t.Obj.(*schedulingv1alpha1.Reservation)
	}
	return r
}

func addReservationToSchedulerCache(sched frameworkext.Scheduler, obj interface{}) {
	r := toReservation(obj)
	if r == nil {
		klog.Errorf("addReservationToSchedulerCache failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	if !reservationutil.IsReservationAvailable(r) {
		return
	}

	klog.V(3).InfoS("Try to add reservation into SchedulerCache",
		"reservation", klog.KObj(r), "reservationUID", r.UID, "node", reservationutil.GetReservationNodeName(r))

	// only add valid reservation into cache
	err := reservationutil.ValidateReservation(r)
	if err != nil {
		klog.ErrorS(err, "Failed to add reservation into SchedulerCache, invalid reservation", "reservation", klog.KObj(r))
		return
	}

	// update pod cache and trigger pod assigned event for scheduling queue
	reservePod := reservationutil.NewReservePod(r)
	if err = sched.GetCache().AddPod(klog.Background(), reservePod); err != nil {
		klog.ErrorS(err, "Failed to add reservation into SchedulerCache", "reservation", klog.KObj(reservePod))
	} else {
		klog.V(4).InfoS("Successfully add reservation into SchedulerCache", "reservation", klog.KObj(r))
	}
	sched.GetSchedulingQueue().AssignedPodAdded(klog.Background(), reservePod)
}

func updateReservationInSchedulerCache(sched frameworkext.Scheduler, oldObj, newObj interface{}) {
	oldR := toReservation(oldObj)
	newR := toReservation(newObj)
	if oldR == nil || newR == nil {
		klog.Errorf("updateReservationInSchedulerCache failed, cannot convert object to *schedulingv1alpha1.Reservation, old %T, new %T", oldObj, newObj)
		return
	}

	if newR.Status.NodeName == "" {
		return
	}

	// A delete event followed by an immediate add event may be merged into a update event.
	// In this case, we should invalidate the old object, and then add the new object.
	if oldR.UID != newR.UID {
		deleteReservationFromSchedulerCache(sched, oldObj)
		addReservationToSchedulerCache(sched, newObj)
		return
	}

	// Pending to Available
	if !reservationutil.IsReservationAvailable(oldR) && reservationutil.IsReservationAvailable(newR) {
		addReservationToSchedulerCache(sched, newR)
		return
	}

	// Available to Succeeded or Failed
	if reservationutil.IsReservationAvailable(oldR) && !reservationutil.IsReservationAvailable(newR) {
		deleteReservationFromSchedulerCache(sched, newR)
		return
	}

	// Just update Pending/Succeeded/Failed Reservation
	if !reservationutil.IsReservationAvailable(newR) {
		return
	}

	klog.V(4).InfoS("Try to update reservation into SchedulerCache",
		"reservation", klog.KObj(newR), "reservationUID", newR.UID, "node", reservationutil.GetReservationNodeName(newR))

	// nodeName update of the same reservations is not allowed and may corrupt the cache
	if reservationutil.GetReservationNodeName(oldR) != reservationutil.GetReservationNodeName(newR) {
		klog.Errorf("It is not allowed to update the Reservation.Status.NodeName of an already allocated reservation, reservation: %s", newR.Name)
		return
	}

	// update pod cache and trigger pod assigned event for scheduling queue
	err := reservationutil.ValidateReservation(newR)
	if err != nil {
		klog.ErrorS(err, "Failed to update reservation into SchedulerCache, invalid reservation", "reservation", klog.KObj(newR))
		return
	}
	oldReservePod := reservationutil.NewReservePod(oldR)
	newReservePod := reservationutil.NewReservePod(newR)
	if err := sched.GetCache().UpdatePod(klog.Background(), oldReservePod, newReservePod); err != nil {
		klog.ErrorS(err, "Failed to update reservation into SchedulerCache", "reservation", klog.KObj(newR))
	} else {
		klog.V(4).InfoS("Successfully update reservation into SchedulerCache", "reservation", klog.KObj(newR))
	}
	sched.GetSchedulingQueue().AssignedPodUpdated(klog.Background(), oldReservePod, newReservePod)
}

func deleteReservationFromSchedulerCache(sched frameworkext.Scheduler, obj interface{}) {
	r := toReservation(obj)
	if r == nil {
		klog.Errorf("deleteReservationFromSchedulerCache failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}

	if r.Status.NodeName == "" {
		return
	}

	klog.V(4).InfoS("Try to delete reservation from SchedulerCache",
		"reservation", klog.KObj(r), "reservationUID", r.UID, "node", reservationutil.GetReservationNodeName(r))

	// delete pod cache and trigger pod deleted event for scheduling queue
	err := reservationutil.ValidateReservation(r)
	if err != nil {
		klog.ErrorS(err, "Failed to delete reservation from SchedulerCache, invalid reservation", "reservation", klog.KObj(r))
		return
	}

	reservationCache := reservation.GetReservationCache()
	rInfo := reservationCache.DeleteReservation(r)
	if rInfo == nil {
		klog.Warningf("The impossible happened. Missing ReservationInfo in ReservationCache, reservation: %v", klog.KObj(r))
		return
	} else {
		klog.V(4).InfoS("Successfully delete reservation from ReservationCache", "reservation", klog.KObj(r))
	}

	reservePod := reservationutil.NewReservePod(r)
	if _, err = sched.GetCache().GetPod(reservePod); err == nil {
		if len(rInfo.AllocatedPorts) > 0 {
			allocatablePorts := util.RequestedHostPorts(reservePod)
			util.RemoveHostPorts(allocatablePorts, rInfo.AllocatedPorts)
			util.ResetHostPorts(reservePod, allocatablePorts)

			// The Pod status in the Cache must be refreshed once to ensure that subsequent deletions are valid.
			if err := sched.GetCache().UpdatePod(klog.Background(), reservePod, reservePod); err != nil {
				klog.ErrorS(err, "Failed update reservation into SchedulerCache in delete stage", "reservation", klog.KObj(r))
			}
		}

		if err := sched.GetCache().RemovePod(klog.Background(), reservePod); err != nil {
			klog.ErrorS(err, "Failed to remove reservation from SchedulerCache", "reservation", klog.KObj(r))
		} else {
			klog.V(4).InfoS("Successfully delete reservation from SchedulerCache", "reservation", klog.KObj(r))
		}

		sched.GetSchedulingQueue().MoveAllToActiveOrBackoffQueue(klog.Background(), frameworkext.AssignedPodDelete, nil, nil, nil)
	}
}

func addReservationToSchedulingQueue(sched frameworkext.Scheduler, obj interface{}) {
	r := toReservation(obj)
	if r == nil {
		klog.Errorf("addReservationToSchedulingQueue failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	klog.V(3).InfoS("Add event for unscheduled reservation", "reservation", klog.KObj(r))

	reservePod := reservationutil.NewReservePod(r)
	if err := sched.GetSchedulingQueue().Add(klog.Background(), reservePod); err != nil {
		klog.Errorf("failed to add reserve pod into scheduling queue, reservation %v, err: %v", klog.KObj(reservePod), err)
	}
}

func updateReservationInSchedulingQueue(sched frameworkext.Scheduler, oldObj, newObj interface{}) {
	oldR := toReservation(oldObj)
	newR := toReservation(newObj)
	if oldR == nil || newR == nil {
		klog.Errorf("updateReservationInSchedulingQueue failed, cannot convert object to *schedulingv1alpha1.Reservation, old %T, new %T", oldObj, newObj)
		return
	}
	// Bypass update event that carries identical objects to avoid duplicate scheduling.
	// https://github.com/kubernetes/kubernetes/pull/96071
	if oldR.ResourceVersion == newR.ResourceVersion {
		return
	}

	newReservePod := reservationutil.NewReservePod(newR)
	isAssumed, err := sched.GetCache().IsAssumedPod(newReservePod)
	if err != nil {
		klog.Errorf("failed to check whether reserve pod %s is assumed, err: %v", klog.KObj(newReservePod), err)
	}
	if isAssumed {
		return
	}

	oldReservePod := reservationutil.NewReservePod(oldR)
	if err = sched.GetSchedulingQueue().Update(klog.Background(), oldReservePod, newReservePod); err != nil {
		klog.Errorf("failed to update reserve pod in scheduling queue, old %s, new %s, err: %v", klog.KObj(oldReservePod), klog.KObj(newReservePod), err)
	}
}

func deleteReservationFromSchedulingQueue(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler, obj interface{}) {
	r := toReservation(obj)
	if r == nil {
		klog.Errorf("deleteReservationFromSchedulingQueue failed, cannot convert to *schedulingv1alpha1.Reservation, obj %T", obj)
		return
	}
	klog.V(3).InfoS("Delete event for unscheduled reservation", "reservation", klog.KObj(r))

	reservePod := reservationutil.NewReservePod(r)
	if err := schedAdapter.GetSchedulingQueue().Delete(reservePod); err != nil {
		klog.Errorf("failed to delete reserve pod in scheduling queue, reservation %s, err: %v", klog.KObj(r), err)
	}
	fwk := sched.Profiles[reservePod.Spec.SchedulerName]
	if fwk != nil {
		fwk.RejectWaitingPod(reservePod.UID)
	}
}

func isResponsibleForReservation(profiles profile.Map, r *schedulingv1alpha1.Reservation) bool {
	return profiles.HandlesSchedulerName(reservationutil.GetReservationSchedulerName(r))
}

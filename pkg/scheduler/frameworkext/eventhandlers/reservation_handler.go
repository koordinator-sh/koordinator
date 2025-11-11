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
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/apis/core/validation"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulingv1alpha1lister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// Register schedulingv1alpha1 scheme to report event
var _ = schedulingv1alpha1.AddToScheme(scheme.Scheme)

var (
	// for fit reservation error item
	fitErrPrefix = regexp.MustCompile("^0/[0-9]+ nodes are available: ")
	// for reservation total item
	reserveTotalRe = regexp.MustCompile("^([0-9]+) Reservation\\(s\\) matched owner total$")
	// for reservation name matched item
	reserveNameTotalRe = regexp.MustCompile("^([0-9]+) Reservation\\(s\\) exactly matches the requested reservation name$")
	// for node related item
	reserveNodeDetailRe = regexp.MustCompile("^([0-9]+ Reservation\\(s\\)) (for node reason that .*)$")
	// for reservation detail item
	reserveDetailRe = regexp.MustCompile("^([0-9]+) Reservation\\(s\\) .*$")
	// for reservation level failure message format
	reserveLevelMsgFmt = "0/%d reservations are available: %s."
)

func MakeReservationErrorHandler(
	sched *scheduler.Scheduler,
	schedAdapter frameworkext.Scheduler,
	koordClientSet koordclientset.Interface,
	koordSharedInformerFactory koordinatorinformers.SharedInformerFactory,
) frameworkext.PreErrorHandlerFilter {
	reservationLister := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()
	failureHandler := handleReservationSchedulingFailure(sched, schedAdapter, reservationLister, koordClientSet)
	return func(ctx context.Context, f framework.Framework, podInfo *framework.QueuedPodInfo, status *framework.Status, nominatingInfo *framework.NominatingInfo, start time.Time) bool {
		pod := podInfo.Pod
		fwk, ok := sched.Profiles[pod.Spec.SchedulerName]
		if !ok {
			klog.Errorf("profile not found for scheduler name %q, pod %s", pod.Spec.SchedulerName, klog.KObj(pod))
			return true
		}

		// if the pod is not a reserve pod, use the default error handler
		// If the Pod failed to schedule or no post-filter plugins, should remove exist NominatedReservation of the Pod.
		if extendedHandle, ok := fwk.(frameworkext.ExtendedHandle); ok {
			if reservationNominator := extendedHandle.GetReservationNominator(); reservationNominator != nil {
				// If the pod preempting successfully, we should keep the nomination of the pod to reservation, and other pods can not allocate
				// the preempted reserved resources in the next cycle.
				if nominatingInfo == nil || nominatingInfo.NominatingMode == framework.ModeOverride && nominatingInfo.NominatedNodeName == "" || nominatingInfo.NominatingMode == framework.ModeNoop && pod.Status.NominatedNodeName == "" {
					reservationNominator.DeleteNominatedReservePodOrReservation(pod)
				} else {
					klog.V(5).Infof("Keep the NominatedReservation of the Pod %s, nominatingInfo %+v, nominatedNodeName %s", klog.KObj(pod), nominatingInfo, pod.Status.NominatedNodeName)
				}
			}
		}

		// export event on reservation level asynchronously if a normal pod specifies the reservation affinity
		if _, reserveAffExist := pod.Annotations[extension.AnnotationReservationAffinity]; reserveAffExist {
			go func() {
				schedulingErr := status.AsError()
				// for pod specified reservation affinity, export new event on reservation level
				reservationLevelMsg, hasReservation := generatePodEventOnReservationLevel(schedulingErr.Error())
				klog.V(7).Infof("origin scheduling error info: %s. hasReservation %v. reservation msg: %s",
					schedulingErr.Error(), hasReservation, reservationLevelMsg)
				if hasReservation {
					msg := truncateMessage(reservationLevelMsg)
					// user reason=FailedScheduling-Reservation to avoid event being auto-merged
					fwk.EventRecorder().Eventf(pod, nil, corev1.EventTypeWarning, "FailedScheduling-Reservation", "Scheduling", msg)
				}
			}()
			return false
		} else if reservationutil.IsReservePod(pod) {
			// handle failure for the reserve pod
			failureHandler(ctx, f, podInfo, status, nominatingInfo, start)
			return true
		}
		// not reservation CR, not pod with reservation affinity
		return false
	}
}

func addNominatedReservation(f framework.Framework, podInfo *framework.QueuedPodInfo, nominatingInfo *framework.NominatingInfo) {
	frameworkExtender, ok := f.(frameworkext.FrameworkExtender)
	if !ok {
		return
	}

	reservationNominator := frameworkExtender.GetReservationNominator()
	if reservationNominator == nil {
		return
	}
	var nodeName string
	if nominatingInfo.Mode() == framework.ModeOverride {
		nodeName = nominatingInfo.NominatedNodeName
	} else if nominatingInfo.Mode() == framework.ModeNoop {
		nodeName = podInfo.Pod.Status.NominatedNodeName
	}
	reservationNominator.AddNominatedReservePod(podInfo.Pod, nodeName)
}

// input:
// "0/1 nodes are available: 3 Reservation(s) didn't match affinity rules, 1 Reservation(s) is unshedulable, 1 Reservation(s) is unavailable,
// 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory, 1 Insufficient cpu, 1 Insufficient memory.
// 8 Reservation(s) matched owner total, Gang "default/demo-job-podgroup" gets rejected due to pod is unschedulable."
// output:
// "0/8 reservations are available: 3 Reservation(s) didn't match affinity rules, 1 Reservation(s) is unschedulable, 1 Reservation(s) is unavailable,
// 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory."
func generatePodEventOnReservationLevel(errorMsg string) (string, bool) {
	trimErrorMsg := strings.TrimSpace(errorMsg)

	// expect: ["", "3 Reservation(s) ..."]
	prefixSplit := fitErrPrefix.Split(trimErrorMsg, -1)
	if len(prefixSplit) != 2 || prefixSplit[0] != "" {
		return "", false
	}

	// "3 Reservations ..., 1 Reservation xxx. 1 Reservation ..."
	detailedMsg := prefixSplit[1]
	// "3 Reservations ..., 1 Reservation xxx, 1 Reservation ..."
	detailedMsg = strings.ReplaceAll(detailedMsg, ". ", ", ")
	// ["3 Reservation(s) ...", " 1 Reservation(s) ...", ..., " 8 Reservation(s) matched owner total.", " Gang rejected..."]
	detailSplit := strings.FieldsFunc(detailedMsg, func(c rune) bool {
		return c == ','
	})

	total := int64(-1)
	resultDetails := make([]string, 0, len(detailSplit))
	nodeRelatedDetails := make([]string, 0, len(detailSplit))
	var reservationNameDetail []string

	for _, item := range detailSplit {
		trimItem := strings.Trim(item, ". ")
		totalStr := reserveTotalRe.FindAllStringSubmatch(trimItem, -1)

		if len(totalStr) > 0 && len(totalStr[0]) == 2 {
			// matched total item "8 Reservation(s) matched owner total"
			var err error
			if total, err = strconv.ParseInt(totalStr[0][1], 10, 64); err != nil {
				return "", false
			}
		} else if reserveNodeDetailRe.MatchString(trimItem) {
			// node related item, e.g. "2 Reservation(s) for node reason that node(s) didn't match pod affinity rules"
			reserveNodeSubMatch := reserveNodeDetailRe.FindStringSubmatch(trimItem)
			if len(reserveNodeSubMatch) <= 1 {
				continue
			}
			// expect: ["2 Reservation(s)", "didn't match pod affinity rules"]
			nodeReasonWords := make([]string, 0, len(reserveNodeSubMatch)-1)
			for _, vv := range reserveNodeSubMatch[1:] {
				if vv == "" {
					continue
				}
				nodeReasonWords = append(nodeReasonWords, vv)
			}
			nodeRelatedDetails = append(nodeRelatedDetails, strings.Join(nodeReasonWords, " "))
		} else if reserveNameTotalRe.MatchString(trimItem) {
			reservationNameDetail = append(reservationNameDetail, trimItem)
		} else if reserveDetailRe.MatchString(trimItem) {
			// reservation itself item, append to details, e.g. " 1 Reservation(s) ..."
			resultDetails = append(resultDetails, trimItem)
		}
	}

	// put the reservation name at the front, and put the node-related details at the end
	if d := len(reservationNameDetail); d > 0 {
		resultDetails = append(resultDetails, reservationNameDetail...)
		copy(resultDetails[d:], resultDetails[:len(resultDetails)-d])
		copy(resultDetails[:d], reservationNameDetail)
	}
	resultDetails = append(resultDetails, nodeRelatedDetails...)

	return fmt.Sprintf(reserveLevelMsgFmt, total, strings.Join(resultDetails, ", ")), total >= 0
}

func handleReservationSchedulingFailure(sched *scheduler.Scheduler,
	schedAdapter frameworkext.Scheduler,
	reservationLister schedulingv1alpha1lister.ReservationLister,
	koordClientSet koordclientset.Interface) scheduler.FailureHandlerFn {
	// Here we follow the procedure of the normal pod handling in the framework, except using the reservation object.
	return func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *framework.Status, nominatingInfo *framework.NominatingInfo, start time.Time) {
		calledDone := false
		defer func() {
			if !calledDone {
				// Basically, AddUnschedulableIfNotPresent calls DonePod internally.
				// But, AddUnschedulableIfNotPresent isn't called in some corner cases.
				// Here, we call DonePod explicitly to avoid leaking the pod.
				schedAdapter.GetSchedulingQueue().Done(podInfo.Pod.UID)
			}
		}()

		logger := klog.FromContext(ctx)
		reason := corev1.PodReasonSchedulerError
		if status.IsUnschedulable() {
			reason = corev1.PodReasonUnschedulable
		}

		switch reason {
		case corev1.PodReasonUnschedulable:
			metrics.PodUnschedulable(fwk.ProfileName(), metrics.SinceInSeconds(start))
		case corev1.PodReasonSchedulerError:
			metrics.PodScheduleError(fwk.ProfileName(), metrics.SinceInSeconds(start))
		}

		pod := podInfo.Pod
		err := status.AsError()
		rName := reservationutil.GetReservationNameFromReservePod(pod)

		// NOTE: If the pod is a reserve pod, we simply check the corresponding reservation status if the reserve pod
		// need requeue for the next scheduling cycle.
		if err == scheduler.ErrNoNodesAvailable {
			klog.V(2).InfoS("Unable to schedule reserve pod; no nodes are registered to the cluster; waiting",
				"pod", klog.KObj(pod), "reservation", rName)
		} else if fitError, ok := err.(*framework.FitError); ok {
			// Inject UnschedulablePlugins to PodInfo, which will be used later for moving Pods between queues efficiently.
			podInfo.UnschedulablePlugins = fitError.Diagnosis.UnschedulablePlugins
			klog.V(2).InfoS("Unable to schedule reserve pod; no fit; waiting",
				"pod", klog.KObj(pod), "reservation", rName, "err", err)
		} else {
			klog.ErrorS(err, "Error scheduling reserve pod; retrying",
				"pod", klog.KObj(pod), "reservation", rName)
		}

		// Check if the corresponding reservation exists in informer cache.
		cachedR, e := reservationLister.Get(rName)
		if e != nil {
			klog.InfoS("Reservation doesn't exist in informer cache",
				"pod", klog.KObj(pod), "reservation", rName, "err", e)
			// We need to call DonePod here because we don't call AddUnschedulableIfNotPresent in this case.
			return
		}

		if k8sfeature.DefaultFeatureGate.Enabled(features.DynamicSchedulerCheck) {
			// The scheduler name of a reservation can change in-flight, so we need to double-check if the scheduler
			// is not matched anymore. If unmatched, we should abort the failure handling to avoid applying a
			// failure state with another scheduler concurrently.
			// TODO: Add same check for pods
			if !isResponsibleForReservation(sched.Profiles, cachedR) {
				klog.InfoS("Reservation doesn't belong to this scheduler, abort the failure handling",
					"pod", klog.KObj(pod), "reservation", rName, "schedulerName", reservationutil.GetReservationSchedulerName(cachedR))
				return
			}
		}

		// In the case of extender, the pod may have been bound successfully, but timed out returning its response to the scheduler.
		// It could result in the live version to carry .spec.nodeName, and that's inconsistent with the internal-queued version.
		if nodeName := reservationutil.GetReservationNodeName(cachedR); len(nodeName) != 0 {
			klog.InfoS("Reservation has been assigned to node. Abort adding it back to queue.",
				"pod", klog.KObj(pod), "reservation", rName, "node", nodeName)
			// We need to call DonePod here because we don't call AddUnschedulableIfNotPresent in this case.
		} else {
			podInfo.PodInfo, _ = framework.NewPodInfo(reservationutil.NewReservePod(cachedR))
			if e = schedAdapter.GetSchedulingQueue().AddUnschedulableIfNotPresent(logger, podInfo, schedAdapter.GetSchedulingQueue().SchedulingCycle()); e != nil {
				klog.ErrorS(e, "Error occurred")
			}
			calledDone = true
		}

		// nominate for the reserve pod if it is
		// FIXME: We expect use the default nominator for a nominated reserve pod, since it makes no benefit to
		//   maintain another nominator. However, the default nominator relies the podLister to fetch the real pod
		//   from the informer cache.
		addNominatedReservation(fwk, podInfo, nominatingInfo)

		errMsg := status.Message()
		msg := truncateMessage(errMsg)
		fwk.EventRecorder().Eventf(cachedR, nil, corev1.EventTypeWarning, "FailedScheduling", "Scheduling", msg)

		updateReservationStatus(koordClientSet, reservationLister, rName, err)
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
		reservationutil.SetReservationUnschedulable(curR, schedulingErr.Error())
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

func truncateMessage(message string) string {
	max := validation.NoteLengthLimit
	if len(message) <= max {
		return message
	}
	suffix := " ..."
	return message[:max-len(suffix)] + suffix
}

func reservationEventHandlers(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler) cache.ResourceEventHandler {
	// TODO: add metrics for handler latency
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			r := toReservation(obj)
			if r == nil {
				klog.Errorf("cannot convert add object to *schedulingv1alpha1.Reservation, obj %T", obj)
				return
			}
			addReservation(sched, schedAdapter, r)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldR := toReservation(oldObj)
			newR := toReservation(newObj)
			if oldR == nil || newR == nil {
				klog.Errorf("cannot convert update objects to *schedulingv1alpha1.Reservation, old %T, new %T", oldObj, newObj)
				return
			}
			updateReservation(sched, schedAdapter, oldR, newR)
		},
		DeleteFunc: func(obj interface{}) {
			r := toReservation(obj)
			if r == nil {
				klog.Errorf("cannot convert delete object to *schedulingv1alpha1.Reservation, obj %T", obj)
				return
			}
			deleteReservation(sched, schedAdapter, r)
		},
	}
}

// addReservation handles an Add event for a Reservation object.
// 1. If adding an unassigned and responsible Reservation, add it to the scheduling queue.
// 2. If adding an available (assigned and not terminated) Reservation, add it to the scheduler cache.
func addReservation(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler, r *schedulingv1alpha1.Reservation) {
	// To avoid scheduler cache corrupted, only add valid reservation into cache.
	err := reservationutil.ValidateReservation(r)
	if err != nil {
		klog.ErrorS(err, "Failed to add reservation, invalid reservation", "reservation", klog.KObj(r))
		return
	}
	klog.V(6).InfoS("Handle to add reservation", "reservation", klog.KObj(r), "uid", r.UID, "version", r.ResourceVersion)
	if isReservationActive(r) && isResponsibleForReservation(sched.Profiles, r) {
		addReservationToSchedulingQueue(schedAdapter, r)
	} else if reservationutil.IsReservationAvailable(r) {
		addReservationToSchedulerCache(schedAdapter, r)
	}
}

// updateReservation handles an Update event for a Reservation object.
// Base states: unassigned (active, nodeName is empty), available (assigned, nodeName not empty), terminated (Failed/Succeeded).
// Supported transition states:
// 0. terminated -> terminated: keep terminal state
// 1. unassigned -> available: reservation gets scheduled
// 2. available -> terminated: reservation expires or completes
// 3. unassigned -> terminated: reservation scheduling fails permanently
// 4. available -> unassigned (extended): assumed binding failure, rollback for retry
// 5. available -> available with different nodeName (extended): node migration in multi-scheduler scenarios
func updateReservation(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler, oldR, newR *schedulingv1alpha1.Reservation) {
	// To avoid scheduler cache corrupted, only update valid reservation into cache.
	err := reservationutil.ValidateReservation(newR)
	if err != nil {
		klog.ErrorS(err, "Failed to update reservation, invalid reservation", "reservation", klog.KObj(newR))
		return
	}

	oldTerminated := reservationutil.IsReservationFailed(oldR) || reservationutil.IsReservationSucceeded(oldR)
	newTerminated := reservationutil.IsReservationFailed(newR) || reservationutil.IsReservationSucceeded(newR)

	// Case 0: Keep terminated.
	if oldTerminated && newTerminated {
		return
	}

	oldNodeName := reservationutil.GetReservationNodeName(oldR)
	newNodeName := reservationutil.GetReservationNodeName(newR)
	oldAvailable := reservationutil.IsReservationAvailable(oldR)
	newAvailable := reservationutil.IsReservationAvailable(newR)
	oldActive := isReservationActive(oldR)
	newActive := isReservationActive(newR)
	oldResponsible := isResponsibleForReservation(sched.Profiles, oldR)
	newResponsible := isResponsibleForReservation(sched.Profiles, newR)

	klog.V(6).InfoS("Handle to update reservation", "reservation", klog.KObj(newR), "uid", newR.UID,
		"oldNodeName", oldNodeName, "newNodeName", newNodeName,
		"oldAvailable", oldAvailable, "newAvailable", newAvailable,
		"oldActive", oldActive, "newActive", newActive,
		"oldTerminated", oldTerminated, "newTerminated", newTerminated)

	// Case 1: Keep available
	if oldAvailable && newAvailable {
		// Update available reservation in cache (handles nodeName change internally)
		updateReservationInSchedulerCache(schedAdapter, oldR, newR)
		return
	}

	// Case 2: From unassigned to available (scheduling succeeded)
	if oldActive && newAvailable {
		addReservationToSchedulerCache(schedAdapter, newR)
		if oldResponsible {
			// Remove from scheduling queue since it's now scheduled
			deleteReservationFromSchedulingQueue(sched, schedAdapter, oldR)
		}
		return
	}

	// Case 3: From available to terminated
	if oldAvailable && newTerminated {
		// Remove from cache, no need to requeue for terminal states
		deleteReservationFromSchedulerCache(schedAdapter, oldR)
		if oldResponsible {
			// Ensure it's removed from scheduling queue
			deleteReservationFromSchedulingQueue(sched, schedAdapter, oldR)
		}
		return
	}

	// Case 4: From available to unassigned (assumed binding failure, rollback)
	// This is an extended case for multi-scheduler scenarios. We cleanup
	// cache before requeue the request to avoid cache leak.
	if oldAvailable && newActive {
		// Remove from cache since it's no longer scheduled
		deleteReservationFromSchedulerCache(schedAdapter, oldR)
		if newResponsible {
			// Requeue for retry
			addReservationToSchedulingQueue(schedAdapter, newR)
		}
		return
	}

	// Case 5: Keep unassigned (active)
	if oldActive && newActive {
		// Handle scheduler name changes
		if oldResponsible && newResponsible {
			updateReservationInSchedulingQueue(schedAdapter, oldR, newR)
		} else if !oldResponsible && newResponsible {
			addReservationToSchedulingQueue(schedAdapter, newR)
		} else if oldResponsible && !newResponsible {
			deleteReservationFromSchedulingQueue(sched, schedAdapter, oldR)
		}
		return
	}

	// Case 6: From unassigned to terminated
	if oldActive && newTerminated {
		if oldResponsible {
			deleteReservationFromSchedulingQueue(sched, schedAdapter, oldR)
		}
		return
	}

	// Unexpected state transitions
	klog.ErrorS(nil, "Unexpected reservation state transition", "reservation", klog.KObj(newR), "uid", newR.UID,
		"oldPhase", oldR.Status.Phase, "newPhase", newR.Status.Phase,
		"oldNodeName", oldNodeName, "newNodeName", newNodeName)
}

// deleteReservation handles a Delete event for a Reservation object.
// 1. Delete the Reservation from the scheduler cache if it exists.
// 2. If deleting an unassigned and responsible Reservation, delete it from the scheduling queue.
func deleteReservation(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler, r *schedulingv1alpha1.Reservation) {
	klog.V(6).InfoS("Handle to delete reservation", "reservation", klog.KObj(r), "uid", r.UID, "version", r.ResourceVersion)
	deleteReservationFromSchedulerCache(schedAdapter, r)
	if isResponsibleForReservation(sched.Profiles, r) {
		deleteReservationFromSchedulingQueue(sched, schedAdapter, r)
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

func addReservationToSchedulerCache(sched frameworkext.Scheduler, r *schedulingv1alpha1.Reservation) {
	klog.V(3).InfoS("Try to add reservation into SchedulerCache", "reservation", klog.KObj(r), "reservationUID", r.UID, "node", reservationutil.GetReservationNodeName(r))
	// update pod cache and trigger pod assigned event for scheduling queue
	reservePod := reservationutil.NewReservePod(r)
	if err := sched.GetCache().AddPod(klog.Background(), reservePod); err != nil {
		klog.ErrorS(err, "Failed to add reservation into SchedulerCache", "reservation", klog.KObj(reservePod))
	} else {
		klog.V(4).InfoS("Successfully add reservation into SchedulerCache", "reservation", klog.KObj(r))
	}
	sched.GetSchedulingQueue().AssignedPodAdded(klog.Background(), reservePod)
}

func updateReservationInSchedulerCache(sched frameworkext.Scheduler, oldR, newR *schedulingv1alpha1.Reservation) {
	// A fast lookup for unscheduled reservation.
	oldNodeName := reservationutil.GetReservationNodeName(oldR)
	newNodeName := reservationutil.GetReservationNodeName(newR)
	if oldNodeName == "" && newNodeName == "" {
		return
	}

	// A delete event followed by an immediate add event may be merged into a update event.
	// In extended multi-scheduler scenarios the nodeName may change as a merged event.
	// In this case, we should invalidate the old object, and then add the new object,
	// while it does not guarantee the resources between the delete and add process.
	if oldR.UID != newR.UID || oldNodeName != newNodeName {
		klog.V(3).InfoS("Reservation assigned status changed, handling as delete-then-add", "reservation", klog.KObj(newR), "oldUID", oldR.UID, "newUID", newR.UID, "oldNode", oldNodeName, "newNode", newNodeName)
		deleteReservationFromSchedulerCache(sched, oldR)
		addReservationToSchedulerCache(sched, newR)
		return
	}

	klog.V(4).InfoS("Try to update reservation into SchedulerCache", "reservation", klog.KObj(newR), "reservationUID", newR.UID, "node", newNodeName)
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

func deleteReservationFromSchedulerCache(sched frameworkext.Scheduler, r *schedulingv1alpha1.Reservation) {
	if r.Status.NodeName == "" {
		return
	}

	klog.V(4).InfoS("Try to delete reservation from SchedulerCache", "reservation", klog.KObj(r), "reservationUID", r.UID, "node", reservationutil.GetReservationNodeName(r))

	reservationCache := frameworkext.GetReservationCache()
	rInfo := reservationCache.DeleteReservation(r)
	if rInfo == nil {
		klog.Warningf("The impossible happened. Missing ReservationInfo in ReservationCache, reservation: %v", klog.KObj(r), "reservationUID", r.UID)
		return
	} else {
		klog.V(4).InfoS("Successfully delete reservation from ReservationCache", "reservation", klog.KObj(r), "reservationUID", r.UID)
	}

	reservePod := reservationutil.NewReservePod(r)
	if _, err := sched.GetCache().GetPod(reservePod); err != nil {
		klog.V(4).InfoS("Reservation not found in SchedulerCache, skipping deletion", "reservation", klog.KObj(r), "reservationUID", r.UID)
		return
	}
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
		klog.ErrorS(err, "Failed to remove reservation from SchedulerCache", "reservation", klog.KObj(r), "reservationUID", r.UID)
	} else {
		klog.V(4).InfoS("Successfully delete reservation from SchedulerCache", "reservation", klog.KObj(r), "reservationUID", r.UID)
	}

	sched.GetSchedulingQueue().MoveAllToActiveOrBackoffQueue(klog.Background(), frameworkext.AssignedPodDelete, nil, nil, nil)
}

func addReservationToSchedulingQueue(sched frameworkext.Scheduler, r *schedulingv1alpha1.Reservation) {
	reservePod := reservationutil.NewReservePod(r)
	klog.V(3).InfoS("Add event for unscheduled reservation", "reservation", klog.KObj(r), "reservationUID", r.UID)
	if err := sched.GetSchedulingQueue().Add(klog.Background(), reservePod); err != nil {
		klog.ErrorS(err, "Failed to add reserve pod into scheduling queue, reservation", "reservation", klog.KObj(r), "pod", klog.KObj(reservePod))
	}
}

func updateReservationInSchedulingQueue(sched frameworkext.Scheduler, oldR, newR *schedulingv1alpha1.Reservation) {
	// Bypass update event that carries identical objects to avoid duplicate scheduling.
	// https://github.com/kubernetes/kubernetes/pull/96071
	if oldR.ResourceVersion == newR.ResourceVersion {
		return
	}

	oldReservePod := reservationutil.NewReservePod(oldR)
	newReservePod := reservationutil.NewReservePod(newR)
	// Normal update flow: check if assumed
	isAssumed, err := sched.GetCache().IsAssumedPod(newReservePod)
	if err != nil {
		klog.ErrorS(err, "Failed to check whether reserve pod is assumed", "reservation", klog.KObj(newR), "pod", klog.KObj(newReservePod))
	}
	if isAssumed {
		// SAFETY: Don't update assumed pods in the queue
		// They are in the binding process and should not be disturbed
		klog.V(6).InfoS("Skipping queue update for assumed reserve pod, likely binding in progress", "reservation", klog.KObj(newR), "reservationUID", newR.UID)
		return
	}

	// Update in scheduling queue for other cases (e.g., spec changes)
	if err = sched.GetSchedulingQueue().Update(klog.Background(), oldReservePod, newReservePod); err != nil {
		klog.ErrorS(err, "Failed to update reserve pod in scheduling queue", "reservation", klog.KObj(newR), "reservationUID", newR.UID, "pod", klog.KObj(newReservePod), "new version", newR.ResourceVersion, "old version", oldR.ResourceVersion)
	}
}

func deleteReservationFromSchedulingQueue(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler, r *schedulingv1alpha1.Reservation) {
	klog.V(3).InfoS("Delete event for unscheduled reservation", "reservation", klog.KObj(r), "reservationUID", r.UID, "phase", r.Status.Phase, "nodeName", reservationutil.GetReservationNodeName(r))

	reservePod := reservationutil.NewReservePod(r)
	// Try to delete from scheduling queue
	// For scenario 2 (scheduling success), the pod may not be in the queue anymore (already assumed)
	if err := schedAdapter.GetSchedulingQueue().Delete(reservePod); err != nil {
		// It's normal for assumed pods to not be in the queue
		klog.V(4).InfoS("Failed to delete reserve pod from scheduling queue (may be already assumed)",
			"reservation", klog.KObj(r), "error", err)
	}

	fwk := sched.Profiles[reservePod.Spec.SchedulerName]
	if fwk != nil {
		fwk.RejectWaitingPod(reservePod.UID)
	}
}

func isResponsibleForReservation(profiles profile.Map, r *schedulingv1alpha1.Reservation) bool {
	return profiles.HandlesSchedulerName(reservationutil.GetReservationSchedulerName(r))
}

// isReservationActive checks if the reservation is unassigned (not scheduled to a node yet) and not terminated.
// A reservation is considered active if:
// 1. It has no nodeName assigned (unscheduled)
// 2. It is not in a terminal state (Failed or Succeeded)
func isReservationActive(r *schedulingv1alpha1.Reservation) bool {
	return reservationutil.GetReservationNodeName(r) == "" && !reservationutil.IsReservationFailed(r) && !reservationutil.IsReservationSucceeded(r)
}

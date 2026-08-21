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

package sandbox

import (
	"context"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"
)

// bindingCycle mirrors scheduler.bindingCycle (schedule_one.go:269): it waits on Permit, then runs
// PreBind, Bind and PostBind for an assumed pod.
func (w *Workflow) bindingCycle(
	ctx context.Context,
	state fwktype.CycleState,
	schedFramework framework.Framework,
	scheduleResult *scheduleResult,
	assumedPodInfo *framework.QueuedPodInfo,
	start time.Time,
	podsToActivate *framework.PodsToActivate) *fwktype.Status {
	logger := klog.FromContext(ctx)

	assumedPod := assumedPodInfo.Pod

	if w.nominatedNodeNameForExpectationEnabled {
		preFlightStatus := schedFramework.RunPreBindPreFlights(ctx, state, assumedPod, scheduleResult.SuggestedHost)
		if preFlightStatus.Code() == fwktype.Error ||
			// Unschedulable status is not supported in PreBindPreFlight and hence we regard it as an error.
			preFlightStatus.IsRejected() {
			return preFlightStatus
		}
		if preFlightStatus.IsSuccess() || schedFramework.WillWaitOnPermit(ctx, assumedPod) {
			// Add NominatedNodeName to tell the external components (e.g., the cluster autoscaler) that the pod is about to be bound to the node.
			// We only do this when any of WaitOnPermit or PreBind will work because otherwise the pod will be soon bound anyway.
			if err := w.updatePod(ctx, schedFramework, assumedPod, nil, &fwktype.NominatingInfo{
				NominatedNodeName: scheduleResult.SuggestedHost,
				NominatingMode:    fwktype.ModeOverride,
			}); err != nil {
				logger.Error(err, "Failed to update the nominated node name in the binding cycle", "pod", klog.KObj(assumedPod), "nominatedNodeName", scheduleResult.SuggestedHost)
				// We continue the processing because it's not critical enough to stop binding cycles here.
			}
		}
	}

	// Run "permit" plugins.
	if status := schedFramework.WaitOnPermit(ctx, assumedPod); !status.IsSuccess() {
		if status.IsRejected() {
			fitErr := &framework.FitError{
				NumAllNodes: 1,
				Pod:         assumedPodInfo.Pod,
				Diagnosis: framework.Diagnosis{
					NodeToStatus: framework.NewDefaultNodeToStatus(),
				},
			}
			fitErr.Diagnosis.NodeToStatus.Set(scheduleResult.SuggestedHost, status)
			fitErr.Diagnosis.AddPluginStatus(status)
			return fwktype.NewStatus(status.Code()).WithError(fitErr)
		}
		return status
	}

	// Any failures after this point cannot lead to the Pod being considered unschedulable.
	// We define the Pod as "unschedulable" only when Pods are rejected at specific extension points, and Permit is the last one in the scheduling/binding cycle.
	// If a Pod fails on PreBind or Bind, it should be moved to BackoffQ for retry.
	//
	// We can call Done() here because
	// we can free the cluster events stored in the scheduling queue sooner, which is worth for busy clusters memory consumption wise.
	w.sched.SchedulingQueue.Done(assumedPod.UID)

	// Run "prebind" plugins.
	if status := schedFramework.RunPreBindPlugins(ctx, state, assumedPod, scheduleResult.SuggestedHost); !status.IsSuccess() {
		return status
	}

	// Run "bind" plugins.
	if status := w.bind(ctx, schedFramework, assumedPod, scheduleResult.SuggestedHost, state); !status.IsSuccess() {
		return status
	}

	// Calculating nodeResourceString can be heavy. Avoid it if klog verbosity is below 2.
	logger.V(2).Info("Successfully bound pod to node", "pod", klog.KObj(assumedPod), "node", scheduleResult.SuggestedHost, "evaluatedNodes", scheduleResult.EvaluatedNodes, "feasibleNodes", scheduleResult.FeasibleNodes)
	metrics.PodScheduled(schedFramework.ProfileName(), metrics.SinceInSeconds(start))
	metrics.PodSchedulingAttempts.Observe(float64(assumedPodInfo.Attempts))
	if assumedPodInfo.InitialAttemptTimestamp != nil {
		metrics.PodSchedulingSLIDuration.WithLabelValues(getAttemptsLabel(assumedPodInfo)).Observe(metrics.SinceInSeconds(*assumedPodInfo.InitialAttemptTimestamp))
	}
	// Run "postbind" plugins.
	schedFramework.RunPostBindPlugins(ctx, state, assumedPod, scheduleResult.SuggestedHost)

	// At the end of a successful binding cycle, move up Pods if needed.
	if len(podsToActivate.Map) != 0 {
		w.sched.SchedulingQueue.Activate(logger, podsToActivate.Map)
		// Unlike the logic in schedulingCycle(), we don't bother deleting the entries
		// as `podsToActivate.Map` is no longer consumed.
	}

	return nil
}

// bind mirrors scheduler.bind (schedule_one.go:978): extenders first, then bind plugins, and always
// finishBinding so the assumed pod can expire from the cache.
func (w *Workflow) bind(ctx context.Context, schedFramework framework.Framework, assumed *corev1.Pod, targetNode string, state fwktype.CycleState) (status *fwktype.Status) {
	logger := klog.FromContext(ctx)
	defer func() {
		w.finishBinding(logger, schedFramework, assumed, targetNode, status)
	}()

	bound, err := w.extendersBinding(logger, assumed, targetNode)
	if bound {
		return fwktype.AsStatus(err)
	}
	return schedFramework.RunBindPlugins(ctx, state, assumed, targetNode)
}

// extendersBinding mirrors scheduler.extendersBinding (schedule_one.go:992).
// TODO(#87159): Move this to a Plugin.
func (w *Workflow) extendersBinding(logger klog.Logger, pod *corev1.Pod, node string) (bool, error) {
	for _, extender := range w.sched.Extenders {
		if !extender.IsBinder() || !extender.IsInterested(pod) {
			continue
		}
		err := extender.Bind(&corev1.Binding{
			ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
			Target:     corev1.ObjectReference{Kind: "Node", Name: node},
		})
		if err != nil && extender.IsIgnorable() {
			logger.Info("Skipping extender in bind as it returned error and has ignorable flag set", "extender", extender, "err", err)
			continue
		}
		return true, err
	}
	return false, nil
}

// finishBinding mirrors scheduler.finishBinding (schedule_one.go:1010): it always signals the
// cache that binding is finished (so the assumed pod can expire) and emits the "Scheduled" event
// on success.
func (w *Workflow) finishBinding(logger klog.Logger, fwk framework.Framework, assumed *corev1.Pod, targetNode string, status *fwktype.Status) {
	if finErr := w.sched.Cache.FinishBinding(logger, assumed); finErr != nil {
		utilruntime.HandleErrorWithLogger(logger, finErr, "Scheduler cache FinishBinding failed")
	}
	if !status.IsSuccess() {
		logger.V(1).Info("Failed to bind pod", "pod", klog.KObj(assumed))
		return
	}

	fwk.EventRecorder().Eventf(assumed, nil, corev1.EventTypeNormal, "Scheduled", "Binding", "Successfully assigned %v/%v to %v", assumed.Namespace, assumed.Name, targetNode)
}

// handleBindingCycleError mirrors scheduler.handleBindingCycleError (schedule_one.go:356): it
// unreserves and forgets the assumed pod, moves other pods that may now be schedulable, and
// requeues the failed pod through the scheduler failure handler, preserving Koordinator's
// error handling, monitoring, and auditing hooks.
func (w *Workflow) handleBindingCycleError(
	ctx context.Context,
	state fwktype.CycleState,
	fwk framework.Framework,
	podInfo *framework.QueuedPodInfo,
	start time.Time,
	scheduleResult *scheduleResult,
	status *fwktype.Status) {
	logger := klog.FromContext(ctx)

	assumedPod := podInfo.Pod
	// trigger un-reserve plugins to clean up state associated with the reserved Pod
	fwk.RunReservePluginsUnreserve(ctx, state, assumedPod, scheduleResult.SuggestedHost)
	if forgetErr := w.sched.Cache.ForgetPod(logger, assumedPod); forgetErr != nil {
		utilruntime.HandleErrorWithContext(ctx, forgetErr, "scheduler cache ForgetPod failed")
	} else {
		// "Forget"ing an assumed Pod in binding cycle should be treated as a PodDelete event,
		// as the assumed Pod had occupied a certain amount of resources in scheduler cache.
		//
		// Avoid moving the assumed Pod itself as it's always Unschedulable.
		// It's intentional to "defer" this operation; otherwise MoveAllToActiveOrBackoffQueue() would
		// add this event to in-flight events and thus move the assumed pod to backoffQ anyways if the plugins don't have appropriate QueueingHint.
		if status.IsRejected() {
			defer w.sched.SchedulingQueue.MoveAllToActiveOrBackoffQueue(logger, framework.EventAssignedPodDelete, assumedPod, nil, func(pod *corev1.Pod) bool {
				return assumedPod.UID != pod.UID
			})
		} else {
			w.sched.SchedulingQueue.MoveAllToActiveOrBackoffQueue(logger, framework.EventAssignedPodDelete, assumedPod, nil, nil)
		}
	}

	w.sched.FailureHandler(ctx, fwk, podInfo, status, clearNominatedNode, start)
}

// getAttemptsLabel mirrors getAttemptsLabel in schedule_one.go: it buckets the scheduling attempt
// count to bound the cardinality of the PodSchedulingDuration metric.
func getAttemptsLabel(p *framework.QueuedPodInfo) string {
	// We breakdown the pod scheduling duration by attempts capped to a limit
	// to avoid ending up with a high cardinality metric.
	if p.Attempts >= 15 {
		return "15+"
	}
	return strconv.Itoa(p.Attempts)
}

// updatePod mirrors updatePod in schedule_one.go: it patches the pod condition and/or the
// nominated node name, preferring the framework's API cacher when available.
func (w *Workflow) updatePod(ctx context.Context, schedFramework framework.Framework, pod *corev1.Pod, condition *corev1.PodCondition, nominatingInfo *fwktype.NominatingInfo) error {
	if apiCacher := schedFramework.APICacher(); apiCacher != nil {
		// When API cacher is available, use it to patch the status.
		_, err := apiCacher.PatchPodStatus(pod, condition, nominatingInfo)
		return err
	}
	logger := klog.FromContext(ctx)
	logValues := []any{"pod", klog.KObj(pod)}
	if condition != nil {
		logValues = append(logValues, "conditionType", condition.Type, "conditionStatus", condition.Status, "conditionReason", condition.Reason)
	}
	if nominatingInfo != nil {
		logValues = append(logValues, "nominatedNodeName", nominatingInfo.NominatedNodeName, "nominatingMode", nominatingInfo.Mode())
	}
	logger.V(3).Info("Updating pod condition and nominated node name", logValues...)

	podStatusCopy := pod.Status.DeepCopy()
	// NominatedNodeName is updated only if we are trying to set it, and the value is
	// different from the existing one.
	nnnNeedsUpdate := nominatingInfo.Mode() == fwktype.ModeOverride && pod.Status.NominatedNodeName != nominatingInfo.NominatedNodeName
	podConditionNeedsUpdate := condition != nil && podutil.UpdatePodCondition(podStatusCopy, condition)
	if !podConditionNeedsUpdate && !nnnNeedsUpdate {
		return nil
	}
	if nnnNeedsUpdate {
		podStatusCopy.NominatedNodeName = nominatingInfo.NominatedNodeName
	}
	return schedutil.PatchPodStatus(ctx, w.kubeClient, pod.Name, pod.Namespace, &pod.Status, podStatusCopy)
}

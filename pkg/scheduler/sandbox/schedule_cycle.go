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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
)

// scheduleResult mirrors scheduler.ScheduleResult (scheduler.go:159) but keeps the nominatingInfo
// accessible: the upstream struct carries it in an unexported field, so the mirror keeps its own
// alongside the embedded public fields.
type scheduleResult struct {
	scheduler.ScheduleResult
	nominatingInfo *fwktype.NominatingInfo
}

// frameworkForPod mirrors scheduler.frameworkForPod (schedule_one.go:390).
func (w *Workflow) frameworkForPod(pod *corev1.Pod) (framework.Framework, error) {
	fwk, ok := w.sched.Profiles[pod.Spec.SchedulerName]
	if !ok {
		return nil, fmt.Errorf("profile not found for scheduler name %q", pod.Spec.SchedulerName)
	}
	return fwk, nil
}

// skipPodSchedule mirrors scheduler.skipPodSchedule (schedule_one.go:399): it returns true if the
// pod could be skipped for the specified cases.
func (w *Workflow) skipPodSchedule(ctx context.Context, fwk framework.Framework, pod *corev1.Pod) bool {
	// Case 1: pod is being deleted.
	if pod.DeletionTimestamp != nil {
		fwk.EventRecorder().Eventf(pod, nil, corev1.EventTypeWarning, "FailedScheduling", "Scheduling", "skip schedule deleting pod: %v/%v", pod.Namespace, pod.Name)
		klog.FromContext(ctx).V(3).Info("Skip schedule deleting pod", "pod", klog.KObj(pod))
		return true
	}

	// Case 2: pod that has been assumed could be skipped.
	// An assumed pod can be added again to the scheduling queue if it got an update event
	// during its previous scheduling cycle but before getting assumed.
	isAssumed, err := w.sched.Cache.IsAssumedPod(pod)
	if err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "Failed to check whether pod is assumed", "pod", klog.KObj(pod))
		return false
	}
	return isAssumed
}

// schedulingCycle mirrors scheduler.schedulingCycle (schedule_one.go:141): it runs the full
// decision path (SchedulePod = PreFilter/Filter/Score), falls back to PostFilter on fit errors,
// then assumes the pod and runs Reserve and Permit.
func (w *Workflow) schedulingCycle(
	ctx context.Context,
	state fwktype.CycleState,
	schedFramework framework.Framework,
	podInfo *framework.QueuedPodInfo,
	start time.Time,
	podsToActivate *framework.PodsToActivate,
) (*scheduleResult, *framework.QueuedPodInfo, *fwktype.Status) {
	logger := klog.FromContext(ctx)
	pod := podInfo.Pod
	result, err := w.sched.SchedulePod(ctx, schedFramework, state, pod)
	if err != nil {
		defer func() {
			metrics.SchedulingAlgorithmLatency.Observe(metrics.SinceInSeconds(start))
		}()
		if err == scheduler.ErrNoNodesAvailable {
			status := fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable).WithError(err)
			return &scheduleResult{nominatingInfo: clearNominatedNode}, podInfo, status
		}

		fitError, ok := err.(*framework.FitError)
		if !ok {
			logger.Error(err, "Error selecting node for pod", "pod", klog.KObj(pod))
			return &scheduleResult{nominatingInfo: clearNominatedNode}, podInfo, fwktype.AsStatus(err)
		}

		// SchedulePod() may have failed because the pod would not fit on any host, so we try to
		// preempt, with the expectation that the next time the pod is tried for scheduling it
		// will fit due to the preemption. It is also possible that a different pod will schedule
		// into the resources that were preempted, but this is harmless.

		if !schedFramework.HasPostFilterPlugins() {
			logger.V(3).Info("No PostFilter plugins are registered, so no preemption will be performed")
			return &scheduleResult{nominatingInfo: clearNominatedNode}, podInfo, fwktype.NewStatus(fwktype.Unschedulable).WithError(err)
		}

		// Run PostFilter plugins to attempt to make the pod schedulable in a future scheduling cycle.
		postFilterResult, status := schedFramework.RunPostFilterPlugins(ctx, state, pod, fitError.Diagnosis.NodeToStatus)
		msg := status.Message()
		fitError.Diagnosis.PostFilterMsg = msg
		if status.Code() == fwktype.Error {
			utilruntime.HandleErrorWithContext(ctx, nil, "Status after running PostFilter plugins for pod", "pod", klog.KObj(pod), "status", msg)
		} else {
			logger.V(5).Info("Status after running PostFilter plugins for pod", "pod", klog.KObj(pod), "status", msg)
		}

		var nominatingInfo *fwktype.NominatingInfo
		if postFilterResult != nil {
			nominatingInfo = postFilterResult.NominatingInfo
		}
		return &scheduleResult{nominatingInfo: nominatingInfo}, podInfo, fwktype.NewStatus(fwktype.Unschedulable).WithError(err)
	}

	metrics.SchedulingAlgorithmLatency.Observe(metrics.SinceInSeconds(start))
	// Tell the cache to assume that a pod now is running on a given node, even though it hasn't been bound yet.
	// This allows us to keep scheduling without waiting on binding to occur.
	assumedPodInfo := podInfo.DeepCopy()
	assumedPod := assumedPodInfo.Pod
	// assume modifies `assumedPod` by setting NodeName=scheduleResult.SuggestedHost
	err = w.assume(logger, assumedPod, result.SuggestedHost)
	if err != nil {
		// This is most probably result of a BUG in retrying logic.
		// We report an error here so that pod scheduling can be retried.
		// This relies on the fact that Error will check if the pod has been bound
		// to a node and if so will not add it back to the unscheduled pods queue
		// (otherwise this would cause an infinite loop).
		return &scheduleResult{nominatingInfo: clearNominatedNode}, assumedPodInfo, fwktype.AsStatus(err)
	}

	// Run the Reserve method of reserve plugins.
	if sts := schedFramework.RunReservePluginsReserve(ctx, state, assumedPod, result.SuggestedHost); !sts.IsSuccess() {
		// trigger un-reserve to clean up state associated with the reserved Pod
		schedFramework.RunReservePluginsUnreserve(ctx, state, assumedPod, result.SuggestedHost)
		if forgetErr := w.sched.Cache.ForgetPod(logger, assumedPod); forgetErr != nil {
			utilruntime.HandleErrorWithContext(ctx, forgetErr, "Scheduler cache ForgetPod failed")
		}

		if sts.IsRejected() {
			fitErr := &framework.FitError{
				NumAllNodes: 1,
				Pod:         pod,
				Diagnosis: framework.Diagnosis{
					NodeToStatus: framework.NewDefaultNodeToStatus(),
				},
			}
			fitErr.Diagnosis.NodeToStatus.Set(result.SuggestedHost, sts)
			fitErr.Diagnosis.AddPluginStatus(sts)
			return &scheduleResult{nominatingInfo: clearNominatedNode}, assumedPodInfo, fwktype.NewStatus(sts.Code()).WithError(fitErr)
		}
		return &scheduleResult{nominatingInfo: clearNominatedNode}, assumedPodInfo, sts
	}

	// Run "permit" plugins.
	runPermitStatus := schedFramework.RunPermitPlugins(ctx, state, assumedPod, result.SuggestedHost)
	if !runPermitStatus.IsWait() && !runPermitStatus.IsSuccess() {
		// trigger un-reserve to clean up state associated with the reserved Pod
		schedFramework.RunReservePluginsUnreserve(ctx, state, assumedPod, result.SuggestedHost)
		if forgetErr := w.sched.Cache.ForgetPod(logger, assumedPod); forgetErr != nil {
			utilruntime.HandleErrorWithContext(ctx, forgetErr, "Scheduler cache ForgetPod failed")
		}

		if runPermitStatus.IsRejected() {
			fitErr := &framework.FitError{
				NumAllNodes: 1,
				Pod:         pod,
				Diagnosis: framework.Diagnosis{
					NodeToStatus: framework.NewDefaultNodeToStatus(),
				},
			}
			fitErr.Diagnosis.NodeToStatus.Set(result.SuggestedHost, runPermitStatus)
			fitErr.Diagnosis.AddPluginStatus(runPermitStatus)
			return &scheduleResult{nominatingInfo: clearNominatedNode}, assumedPodInfo, fwktype.NewStatus(runPermitStatus.Code()).WithError(fitErr)
		}

		return &scheduleResult{nominatingInfo: clearNominatedNode}, assumedPodInfo, runPermitStatus
	}

	// At the end of a successful scheduling cycle, pop and move up Pods if needed.
	if len(podsToActivate.Map) != 0 {
		w.sched.SchedulingQueue.Activate(logger, podsToActivate.Map)
		// Clear the entries after activation.
		podsToActivate.Map = make(map[string]*corev1.Pod)
	}

	return &scheduleResult{ScheduleResult: result}, assumedPodInfo, nil
}

// assume signals to the cache that a pod is already in the cache, so that binding can be asynchronous.
// assume modifies `assumed`.
func (w *Workflow) assume(logger klog.Logger, assumed *corev1.Pod, host string) error {
	// Optimistically assume that the binding will succeed and send it to apiserver
	// in the background.
	// If the binding fails, scheduler will release resources allocated to assumed pod
	// immediately.
	assumed.Spec.NodeName = host

	if err := w.sched.Cache.AssumePod(logger, assumed); err != nil {
		logger.Error(err, "Scheduler cache AssumePod failed")
		return err
	}

	// If "assumed" is a nominated pod, remove it from the internal cache.
	if w.sched.SchedulingQueue != nil {
		w.sched.SchedulingQueue.DeleteNominatedPodIfExists(assumed)
	}

	return nil
}

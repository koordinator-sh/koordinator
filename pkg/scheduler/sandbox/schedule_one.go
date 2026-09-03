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
	"math/rand"
	"time"

	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// pluginMetricsSamplePercent is the percentage of plugin metrics to be sampled,
// mirroring schedule_one.go.
const pluginMetricsSamplePercent = 10

// clearNominatedNode instructs the scheduler to clear the pod's nominated node when requeuing a pod
// whose asynchronous binding cycle failed, mirroring schedule_one.go.
var clearNominatedNode = &fwktype.NominatingInfo{NominatingMode: fwktype.ModeOverride, NominatedNodeName: ""}

// ScheduleOne selects the sandbox or default decision path, runs the scheduling cycle
// synchronously, and launches the asynchronous binding cycle on success.
func (w *SandboxCustomWorkflow) ScheduleOne(ctx context.Context) {
	logger := klog.FromContext(ctx)
	workflow := w.workflow
	sched := workflow.sched
	podInfo, err := sched.NextPod(logger)
	if err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "Error while retrieving next pod from scheduling queue")
		return
	}
	// pod could be nil when schedulerQueue is closed
	if podInfo == nil || podInfo.Pod == nil {
		return
	}

	pod := podInfo.Pod
	logger = klog.LoggerWithValues(logger, "pod", klog.KObj(pod))
	ctx = klog.NewContext(ctx, logger)
	logger.V(4).Info("About to try and schedule pod", "pod", klog.KObj(pod))

	fwk, err := workflow.frameworkForPod(pod)
	if err != nil {
		// This shouldn't happen, because we only accept for scheduling the pods
		// which specify a scheduler name that matches one of the profiles.
		logger.Error(err, "Error occurred")
		sched.SchedulingQueue.Done(pod.UID)
		return
	}
	if workflow.skipPodSchedule(ctx, fwk, pod) {
		// We don't put this Pod back to the queue, but we have to cleanup the in-flight pods/events.
		sched.SchedulingQueue.Done(pod.UID)
		return
	}

	logger.V(3).Info("Attempting to schedule pod", "pod", klog.KObj(pod))

	// Synchronously attempt to find a fit for the pod.
	start := time.Now()
	state := framework.NewCycleState()
	// For the sake of performance, scheduler does not measure and export the scheduler_plugin_execution_duration metric
	// for every plugin execution in each scheduling cycle. Instead it samples a portion of scheduling cycles - percentage
	// determined by pluginMetricsSamplePercent. The line below helps to randomly pick appropriate scheduling cycles.
	state.SetRecordPluginMetrics(rand.Intn(100) < pluginMetricsSamplePercent)

	// Initialize an empty podsToActivate struct, which will be filled up by plugins or stay empty.
	podsToActivate := framework.NewPodsToActivate()
	state.Write(framework.PodsToActivateKey, podsToActivate)

	schedulingCycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	decision := w.decisionForPod(pod)
	if decision.workflowRunsKoordinatorHooks {
		workflow.startKoordinatorSchedule(state, fwk, pod)
	}

	scheduleResult, assumedPodInfo, status := workflow.schedulingCycle(schedulingCycleCtx, state, fwk, podInfo, start, podsToActivate, decision)
	if !status.IsSuccess() {
		sched.FailureHandler(schedulingCycleCtx, fwk, assumedPodInfo, status, scheduleResult.nominatingInfo, start)
		return
	}

	// Acquire before starting the goroutine so backpressure does not accumulate waiting goroutines.
	slotWaitStart := time.Now()
	if err := w.acquireBindingSlot(ctx); err != nil {
		if w.bindingSlots != nil {
			koordmetrics.SandboxBindingSlotWaitDuration.WithLabelValues(fwk.ProfileName()).Observe(time.Since(slotWaitStart).Seconds())
		}
		workflow.handleBindingCycleError(ctx, state, fwk, assumedPodInfo, start, scheduleResult, fwktype.AsStatus(err))
		return
	}
	if w.bindingSlots != nil {
		koordmetrics.SandboxBindingSlotWaitDuration.WithLabelValues(fwk.ProfileName()).Observe(time.Since(slotWaitStart).Seconds())
	}
	bindingSlot := &bindingSlotLease{workflow: w, held: w.bindingSlots != nil}

	// bind the pod to its host asynchronously (we can do this b/c of the assumption step above).
	go func() {
		defer bindingSlot.release()

		bindingCycleCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		metrics.Goroutines.WithLabelValues(metrics.Binding).Inc()
		defer metrics.Goroutines.WithLabelValues(metrics.Binding).Dec()

		bindingStart := time.Now()
		status := workflow.bindingCycle(bindingCycleCtx, state, fwk, scheduleResult, assumedPodInfo, start, podsToActivate, bindingSlot)
		bindingResult := "success"
		if !status.IsSuccess() {
			bindingResult = "error"
		}
		koordmetrics.SandboxBindingDuration.WithLabelValues(fwk.ProfileName(), bindingResult).Observe(time.Since(bindingStart).Seconds())
		if !status.IsSuccess() {
			workflow.handleBindingCycleError(bindingCycleCtx, state, fwk, assumedPodInfo, start, scheduleResult, status)
			return
		}
		workflow.completeBindingCycle(bindingCycleCtx, state, fwk, scheduleResult, assumedPodInfo, start, podsToActivate)
	}()
}

func (w *SandboxCustomWorkflow) decisionForPod(pod *corev1.Pod) schedulingDecision {
	if w.scheduling != nil && w.scheduling.handles(pod) {
		return schedulingDecision{
			decide:                       w.scheduling.decide,
			workflowRunsKoordinatorHooks: true,
		}
	}
	return schedulingDecision{decide: w.workflow.decideDefault}
}

func (w *Workflow) startKoordinatorSchedule(state fwktype.CycleState, fwk framework.Framework, pod *corev1.Pod) {
	extender, ok := fwk.(frameworkext.FrameworkExtender)
	if !ok {
		return
	}
	frameworkext.InitDiagnosis(state, pod)
	if starter, ok := fwk.(interface{ StartMonitoring(*corev1.Pod) }); ok {
		starter.StartMonitoring(pod)
	}
	if auditor := extender.GetWorkloadAuditor(); auditor != nil {
		auditor.RecordAttemptPod(pod)
	}
}

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

package batch

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/batch/framework"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// clearNominatedNode instructs the scheduler to clear the pod's nominated node when requeuing a pod
// whose asynchronous binding cycle failed, mirroring schedule_one.go.
var clearNominatedNode = &fwktype.NominatingInfo{NominatingMode: fwktype.ModeOverride, NominatedNodeName: ""}

var _ frameworkext.BatchScheduler = &BatchScheduler{}

// BatchScheduler runs a scheduling cycle for a whole job described by a
// BatchScheduleResult that was computed by a FindOneNodePlugin. It reuses the shared batch Engine
// for the scheduling/cleanup logic.
//
// The inline path mirrors the upstream per-pod scheduling/binding cycle
// (schedule_one.go): after all pods are reserved and assumed it runs Permit sequentially (pod by
// pod) and then launches one asynchronous binding goroutine per pod (WaitOnPermit/PreBind/Bind/
// PostBind). This lets gang and network-topology plugins finalize their per-job state on the success
// path, and lets a bind failure be handled independently per pod, just like the upstream scheduler.
type BatchScheduler struct {
	engine         *Engine
	cache          cache.Cache
	failureHandler scheduler.FailureHandlerFn
}

// NewBatchScheduler creates a BatchScheduler that operates on the given scheduler cache.
// failureHandler is the scheduler's (koord-wrapped) failure handler, used to requeue a pod whose
// asynchronous binding cycle failed, mirroring schedule_one.go's handleBindingCycleError.
func NewBatchScheduler(c cache.Cache, failureHandler scheduler.FailureHandlerFn) *BatchScheduler {
	return &BatchScheduler{
		engine:         NewEngine(c),
		cache:          c,
		failureHandler: failureHandler,
	}
}

// BatchSchedule schedules and binds all pods described by the plan.
// On any scheduling failure it unreserves and forgets the assumed pods and returns the failure status.
// On success it returns nil.
func (bs *BatchScheduler) BatchSchedule(
	ctx context.Context,
	ext frameworkext.FrameworkExtender,
	cycleState fwktype.CycleState,
	triggerPod *corev1.Pod,
	plan *frameworkext.BatchScheduleResult,
) *fwktype.Status {
	logger := klog.FromContext(ctx)
	// start marks the beginning of this job's scheduling cycle; it is the basis for the per-pod
	// PodScheduled latency, mirroring schedule_one.go where start is the scheduling cycle start.
	start := time.Now()
	// Tag every log line emitted during this scheduling cycle (including the ones from the derived
	// binding-cycle context and helpers that receive this logger) with the cycle start time, so logs
	// from concurrent or successive batch cycles can be told apart.
	logger = logger.WithValues("batchScheduleTime", start)
	// Reuse the scheduler-configured parallelizer (KubeSchedulerConfiguration.Parallelism) so the inline
	// batch path honors the operator's parallelism setting instead of a hardcoded value.
	parallelizer := ext.Parallelizer()

	for _, p := range plan.Pods {
		key := framework.GetPodKey(p)
		if plan.PodToNodeName == nil || plan.PodToNodeName[key] == "" {
			return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("batch schedule plan missing node for pod %s", key))
		}
	}

	jobRequest := buildJobRequest(triggerPod, plan)
	podRequestsByNode, err := ValidateAndGroupByRequest(jobRequest)
	if err != nil {
		logger.V(4).Error(err, "Failed to validate batch schedule plan", "job", jobRequest.String(), "triggerPod", klog.KObj(triggerPod))
		return fwktype.AsStatus(err)
	}

	jobResult := &framework.JobResult{Version: jobRequest.Version}
	var assumedPods []*framework.AssumeContext
	assumedPodLock := &sync.Mutex{}
	nodeSnapshot := &sync.Map{}

	// Scheduling cycle: PreFilter, Filter, Reserve and Assume for all pods of the job.
	scheduleCycleStart := time.Now()
	logger.V(4).Info("Starting batch scheduling cycle", "job", jobRequest.String(), "pods", jobRequest.MinMember)
	bs.engine.RunSchedulingCycle(ctx, logger, parallelizer, ext, jobRequest, jobResult, podRequestsByNode,
		&assumedPods, assumedPodLock, nodeSnapshot, func(pod *corev1.Pod) bool {
			return bs.skipPod(logger, pod)
		})
	logger.V(4).Info("Finished batch scheduling cycle", "job", jobRequest.String(), "assumedPods", len(assumedPods), "elapsed", time.Since(scheduleCycleStart))
	if !jobResult.Status.IsSuccess() {
		logger.V(4).Info("Failed to batch schedule the job, cleaning up assumed pods", "job", jobRequest.String(), "status", jobResult.Message())
		// Build the compact failure message before cleanup: cleanup overwrites every assumed pod's
		// per-pod status with the cleanup status, which would otherwise corrupt the first-failed-pod detection.
		failMsg := jobResult.ExampleMessage(podRequestsByNode, assumedPods)
		bs.cleanup(ctx, logger, parallelizer, ext, jobRequest, jobResult, assumedPods, nodeSnapshot, jobResult.Status, "batch_schedule_failure")
		return fwktype.NewStatus(jobResult.Status.Code(), failMsg)
	}

	// Permit cycle (sequential, pod by pod): mirror schedule_one.go:231-253. Gang/network-topology
	// Permit plugins finalize their per-job state here (the final member's Permit allows the whole
	// gang group and clears the gangSchedulingContext / deletes the network-topology plan). Non-final
	// members return Wait and are released by the final member before we start binding.
	permitStart := time.Now()
	logger.V(4).Info("Starting batch permit cycle", "job", jobRequest.String(), "pods", len(assumedPods))
	permitStatus := bs.runPermit(ctx, ext, assumedPods, nodeSnapshot)
	logger.V(4).Info("Finished batch permit cycle", "job", jobRequest.String(), "elapsed", time.Since(permitStart))
	if !permitStatus.IsSuccess() {
		logger.V(4).Info("Failed to permit the batch scheduled job, cleaning up assumed pods", "job", jobRequest.String(), "status", permitStatus.Message())
		bs.cleanup(ctx, logger, parallelizer, ext, jobRequest, jobResult, assumedPods, nodeSnapshot, permitStatus, "batch_permit_failure")
		return permitStatus
	}

	// Binding cycle (asynchronous, one goroutine per pod): mirror schedule_one.go:121-137. Bind on a
	// context tied to the scheduler's lifetime rather than the trigger pod's scheduling cycle (which
	// is canceled the moment this call returns). StopEverything shares the Scheduler.Run(ctx)
	// lifecycle, so the binding goroutines survive the scheduling cycle but are still canceled on
	// scheduler shutdown, mirroring schedule_one.go where the binding cycle inherits the Run ctx. Each
	// pod's bind failure is handled independently by handleBindingCycleError (Unreserve + ForgetPod +
	// requeue via the scheduler's failure handler).
	baseBindCtx := context.Background()
	if sched := ext.Scheduler(); sched != nil {
		if stopCh := sched.StopEverything(); stopCh != nil {
			baseBindCtx = wait.ContextForChannel(stopCh)
		}
	}
	bindCtx := klog.NewContext(baseBindCtx, logger)
	// The trigger pod is the only job member that was popped from the scheduling queue, so it alone
	// carries the scheduling attempt info (stashed in its managed fields). The whole job is scheduled on
	// this single attempt, so every member pod shares the trigger's attempt count and first-attempt
	// timestamp when emitting the per-pod scheduling metrics (mirroring schedule_one.go:295-300).
	attempts, initialAttemptTimestamp, hasAttemptInfo := frameworkext.PodScheduleAttemptInfo(triggerPod)
	bm := podBindMetrics{
		start:                   start,
		attempts:                attempts,
		initialAttemptTimestamp: initialAttemptTimestamp,
		hasAttemptInfo:          hasAttemptInfo,
	}
	bindDispatchStart := time.Now()
	logger.V(4).Info("Starting batch binding cycle", "job", jobRequest.String(), "pods", len(assumedPods))
	for _, ac := range assumedPods {
		ac := ac
		go bs.bindingCycleOne(bindCtx, ext, jobRequest, ac, nodeSnapshot, bm)
	}

	// Binding runs asynchronously, so this only measures the time to dispatch the per-pod binding
	// goroutines, not their completion.
	logger.V(4).Info("Finished dispatching batch binding cycle, binding asynchronously", "job", jobRequest.String(), "assumedPods", len(assumedPods), "elapsed", time.Since(bindDispatchStart))
	return nil
}

// attemptsLabel mirrors schedule_one.go's getAttemptsLabel: it buckets the scheduling attempt count
// to bound the cardinality of the PodSchedulingDuration metric.
func attemptsLabel(attempts int) string {
	if attempts >= 15 {
		return "15+"
	}
	return strconv.Itoa(attempts)
}

// podBindMetrics carries the trigger pod's scheduling-cycle timing and attempt info so each
// asynchronous binding goroutine can emit the per-pod scheduling metrics (schedule_one.go:295-300).
// Every member pod of the job shares these values: the whole job is scheduled on the trigger pod's
// single scheduling-queue attempt, so its attempt count and first-attempt timestamp apply to all
// members. hasAttemptInfo is false when the trigger pod carries no queue info.
type podBindMetrics struct {
	start                   time.Time
	attempts                int
	initialAttemptTimestamp *time.Time
	hasAttemptInfo          bool
}

// runPermit runs the Permit extension point sequentially for every assumed pod, mirroring
// schedule_one.go:231-253. A pod whose Permit returns Wait is registered as a waiting pod and will
// be released by a later pod's Permit (e.g. the final gang member). It returns the first non-Wait,
// non-Success status, or nil if every pod is permitted and the waiting pods were released.
func (bs *BatchScheduler) runPermit(
	ctx context.Context,
	ext frameworkext.FrameworkExtender,
	assumedPods []*framework.AssumeContext,
	nodeSnapshot *sync.Map,
) *fwktype.Status {
	var lastStatus *fwktype.Status
	for _, ac := range assumedPods {
		// applyNodeSnapshotLister swaps in the per-node snapshot when present (injected by the optional
		// snapshot override); a no-op when EnableBatchScheduleNodeSnapshot is disabled.
		restoreSnapshot := applyNodeSnapshotLister(ext, nodeSnapshot, ac.NodeName)
		status := ext.RunPermitPlugins(ctx, ac.CycleState, ac.Pod, ac.NodeName)
		restoreSnapshot()
		if !status.IsWait() && !status.IsSuccess() {
			return fwktype.NewStatus(status.Code(),
				fmt.Sprintf("permit failed for pod %s@%s: %s", framework.GetPodKey(ac.Pod), ac.NodeName, status.Message()))
		}
		lastStatus = status
	}
	// Every pod returned Wait or Success. A Success is what releases the waiting pods (e.g. the gang
	// member that completes the gang group triggers AllowGangGroup). Because each Permit call assumes
	// one more pod, the completing pod is necessarily the last one permitted, so the final permit must
	// be Success. If it is still Wait, no plugin released the waiting pods (e.g. the gang never reached
	// its min member) and their WaitOnPermit would block until timeout, so roll back the whole job.
	if lastStatus != nil && lastStatus.IsWait() {
		return fwktype.NewStatus(fwktype.Unschedulable,
			fmt.Sprintf("inline batch permit did not complete: %d pods are still waiting after all pods were permitted", len(assumedPods)))
	}
	return nil
}

// bindingCycleOne runs the asynchronous binding cycle for a single assumed pod, mirroring
// schedule_one.go:121-137 (PreBind, Bind, PostBind). On any failure it delegates to
// handleBindingCycleError. It runs in its own goroutine, so it sets its own per-goroutine snapshot
// lister (SetSnapshotLister is keyed by goroutine id).
//
// Unlike schedule_one.go, it does not gate binding on WaitOnPermit: runPermit already ran
// synchronously for the whole job and allowed every member (the final member released the gang group)
// before any binding started, so each member that returned Wait already has a buffered Success in its
// permit channel and there is nothing left to wait on. It still calls WaitOnPermit once, but only for
// cleanup and ignores the returned status: WaitOnPermit is the only path that removes a pod from the
// framework's internal waitingPods map (via its deferred remove), so skipping it entirely would leak
// those entries. Ignoring the status also avoids a failure-amplifying window: a member whose
// PreBind/Bind fails runs Unreserve which, in gang mode, may reject the shared gang (permit) group;
// treating that as this pod's failure would reject a sibling that was otherwise bindable.
//
// Edge-case safety invariants (only the trigger pod was dequeued; sibling pods of the same job are
// scheduled/assumed/bound here but remain in the scheduling queue):
//   - Bind succeeds while siblings are still queued: safe. Assume runs synchronously in the trigger
//     pod's scheduling cycle before binding starts, and the scheduling cycle is serialized, so a
//     sibling cannot be scheduled concurrently. If a sibling is later popped, skipPodSchedule's
//     IsAssumedPod guard skips it (no double schedule/bind), and once Bind writes spec.nodeName the
//     informer removes the now-assigned pod from the queue.
//   - NominatedNode: it is already removed at Assume time via DeleteNominatedPodIfExists (see
//     engine.Assume), so the binding cycle carries no nomination; nothing to clear on success.
func (bs *BatchScheduler) bindingCycleOne(
	ctx context.Context,
	ext frameworkext.FrameworkExtender,
	jobRequest *framework.JobRequest,
	ac *framework.AssumeContext,
	nodeSnapshot *sync.Map,
	bm podBindMetrics,
) {
	logger := klog.FromContext(ctx)
	// Mirror schedule_one.go:124-125: derive a cancellable child of the scheduler-lifetime binding
	// context so it is canceled both on scheduler shutdown (parent) and when this goroutine returns.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	metrics.Goroutines.WithLabelValues(metrics.Binding).Inc()
	defer metrics.Goroutines.WithLabelValues(metrics.Binding).Dec()

	// applyNodeSnapshotLister swaps in the per-node snapshot when present (injected by the optional
	// snapshot override); a no-op when EnableBatchScheduleNodeSnapshot is disabled.
	defer applyNodeSnapshotLister(ext, nodeSnapshot, ac.NodeName)()

	// Drain the permit state for cleanup only: WaitOnPermit is the sole path that removes this pod from
	// the framework's waitingPods map (its deferred remove), so we must call it to avoid leaking the
	// entries added when Permit returned Wait. runPermit already allowed every member before binding
	// started, so this returns the buffered Success immediately without blocking. We ignore a
	// non-success status on purpose: a sibling's concurrent Unreserve may reject the shared gang group,
	// and that must not fail this otherwise bindable pod (see the function doc).
	if status := ext.WaitOnPermit(ctx, ac.Pod); !status.IsSuccess() {
		logger.V(4).Info("Inline batch WaitOnPermit returned non-success; ignoring and proceeding to bind", "job", jobRequest.String(), "pod", klog.KObj(ac.Pod), "node", ac.NodeName, "status", status.Message())
	}

	if status := ext.RunPreBindPlugins(ctx, ac.CycleState, ac.Pod, ac.NodeName); !status.IsSuccess() {
		logger.V(2).Info("Inline batch PreBind failed", "job", jobRequest.String(), "pod", klog.KObj(ac.Pod), "node", ac.NodeName, "status", status.Message())
		bs.handleBindingCycleError(ctx, ext, ac, status, bm.start)
		return
	}
	// Bind mirrors schedule_one.go's sched.bind (929-943) + finishBinding (959-969): run the bind
	// plugins, then always signal the cache that binding is finished (so the assumed pod can expire),
	// and on success emit the "Scheduled" event exactly like the upstream scheduler. FinishBinding is
	// called on both the success and failure paths (on failure handleBindingCycleError then forgets
	// the pod), matching upstream where finishBinding runs via a deferred call inside bind().
	bindStatus := ext.RunBindPlugins(ctx, ac.CycleState, ac.Pod, ac.NodeName)
	if finErr := bs.cache.FinishBinding(logger, ac.Pod); finErr != nil {
		logger.Error(finErr, "Scheduler cache FinishBinding failed", "pod", klog.KObj(ac.Pod))
	}
	if !bindStatus.IsSuccess() {
		logger.V(2).Info("Inline batch Bind failed", "job", jobRequest.String(), "pod", klog.KObj(ac.Pod), "node", ac.NodeName, "status", bindStatus.Message())
		bs.handleBindingCycleError(ctx, ext, ac, bindStatus, bm.start)
		return
	}
	ext.EventRecorder().Eventf(ac.Pod, nil, corev1.EventTypeNormal, "Scheduled", "Binding", "Successfully assigned %v/%v to %v", ac.Pod.Namespace, ac.Pod.Name, ac.NodeName)

	// Success logging and metrics mirror schedule_one.go:295-300. bm.start is the job's scheduling-cycle
	// start (set in BatchSchedule); the attempt count and first-attempt timestamp come from the trigger
	// pod and are shared by every member pod, since the whole job was scheduled on the trigger's single
	// scheduling-queue attempt.
	logger.V(2).Info("Successfully bound pod to node", "job", jobRequest.String(), "pod", klog.KObj(ac.Pod), "node", ac.NodeName)
	metrics.PodScheduled(ext.ProfileName(), metrics.SinceInSeconds(bm.start))
	if bm.hasAttemptInfo {
		metrics.PodSchedulingAttempts.Observe(float64(bm.attempts))
		if bm.initialAttemptTimestamp != nil {
			metrics.PodSchedulingSLIDuration.WithLabelValues(attemptsLabel(bm.attempts)).Observe(metrics.SinceInSeconds(*bm.initialAttemptTimestamp))
		}
	}

	ext.RunPostBindPlugins(ctx, ac.CycleState, ac.Pod, ac.NodeName)

	// The pod won't go back to the scheduling queue, so mark it Done here (mirrors schedule_one.go:136).
	// Only the trigger pod is actually in-flight (it was the one popped from the queue), so Done is
	// required for it because its BatchScheduledReason "failure" is suppressed and nothing else would
	// call Done. For sibling pods (never popped) Done is a safe no-op.
	if sched := ext.Scheduler(); sched != nil {
		if queue := sched.GetSchedulingQueue(); queue != nil {
			queue.Done(ac.Pod.UID)
		}
	}
}

// handleBindingCycleError mirrors schedule_one.go:313-345 for a single pod: it unreserves and
// forgets the assumed pod, moves other pods that may now be schedulable, and requeues the failed pod
// through the scheduler's (koord-wrapped) failure handler.
//
// Edge-case safety invariants:
//   - Requeue is idempotent: the failure status does not contain BatchScheduledReason, so the
//     suppression filter does not fire and the pod is requeued via AddUnschedulableIfNotPresent. If
//     the failed pod is a sibling still sitting in the queue, that call detects it and does not add a
//     duplicate; its deferred done() is a safe no-op for a non-in-flight pod.
//   - NominatedNode: requeue passes clearNominatedNode (ModeOverride, "") so no stale nomination
//     pins the pod, matching schedule_one.go's handleBindingCycleError.
//   - Gang caveat: Permit already ran SucceedGangScheduling, so Unreserve here (coscheduling.Unreserve)
//     may, in strict gang mode, release/reject the whole gang group; the requeued pod re-forms the
//     gang on a later cycle. This is inherent to gang + async binding, not specific to this path.
func (bs *BatchScheduler) handleBindingCycleError(
	ctx context.Context,
	ext frameworkext.FrameworkExtender,
	ac *framework.AssumeContext,
	status *fwktype.Status,
	start time.Time,
) {
	logger := klog.FromContext(ctx)
	assumedPod := ac.Pod
	// trigger un-reserve plugins to clean up state associated with the reserved pod
	ext.RunReservePluginsUnreserve(ctx, ac.CycleState, assumedPod, ac.NodeName)

	var queue frameworkext.SchedulingQueue
	if sched := ext.Scheduler(); sched != nil {
		queue = sched.GetSchedulingQueue()
	}
	// Use the raw scheduler cache ForgetPod (not ext.ForgetPod) here: RunReservePluginsUnreserve above
	// already ran the plugins' cleanup, so ext.ForgetPod would re-fire the registered forget handlers
	// and clean up twice. This mirrors schedule_one.go's handleBindingCycleError, which forgets via the
	// scheduler cache directly.
	if forgetErr := bs.cache.ForgetPod(logger, assumedPod); forgetErr != nil {
		logger.Error(forgetErr, "Scheduler cache ForgetPod failed after inline batch bind failure", "pod", klog.KObj(assumedPod), "node", ac.NodeName)
	} else if queue != nil {
		// "Forget"ing an assumed pod in the binding cycle should be treated as a PodDelete event, as
		// the assumed pod had occupied resources in the scheduler cache. Avoid moving the assumed pod
		// itself as it's always Unschedulable.
		if status.Code() == fwktype.Unschedulable {
			defer queue.MoveAllToActiveOrBackoffQueue(logger, frameworkext.AssignedPodDelete, assumedPod, nil, func(pod *corev1.Pod) bool {
				return assumedPod.UID != pod.UID
			})
		} else {
			queue.MoveAllToActiveOrBackoffQueue(logger, frameworkext.AssignedPodDelete, assumedPod, nil, nil)
		}
	}

	if bs.failureHandler == nil {
		return
	}
	attempts, initialAttemptTimestamp, ok := frameworkext.PodScheduleAttemptInfo(assumedPod)
	queuedPodInfo := &schedulerframework.QueuedPodInfo{
		PodInfo:                 &schedulerframework.PodInfo{Pod: assumedPod},
		InitialAttemptTimestamp: &start,
		Attempts:                0,
	}
	if ok {
		queuedPodInfo.Attempts = attempts
		queuedPodInfo.InitialAttemptTimestamp = initialAttemptTimestamp
	}
	bs.failureHandler(ctx, ext, queuedPodInfo, status, clearNominatedNode, start)

	// A binding-cycle failure means the pod already cleared the scheduling cycle once and the failure
	// (WaitOnPermit/PreBind/Bind) is typically transient. Now that the failure handler has requeued it
	// (to the unschedulable/backoff queue), activate it so it re-enters the activeQ immediately instead
	// of waiting out the backoff, giving the pod a prompt retry. Best-effort: activate is a no-op if the
	// pod is not found in the unschedulable/backoff queues.
	if queue != nil {
		queue.Activate(logger, map[string]*corev1.Pod{assumedPod.Namespace + "/" + assumedPod.Name: assumedPod})
	}
}

// cleanup triggers Unreserve and forgets the given assumed pods from the scheduler cache. It forgets
// via the raw cache (not ext.ForgetPod): RunReservePluginsUnreserve already ran the plugins' cleanup,
// so ext.ForgetPod would re-fire the registered forget handlers and clean up twice. status is the
// failure that triggered the cleanup, supplied by the caller (e.g. the scheduling-cycle jobResult
// status or the Permit failure status); it is recorded as each assumed pod's per-pod status.
func (bs *BatchScheduler) cleanup(
	ctx context.Context,
	logger klog.Logger,
	parallelizer fwktype.Parallelizer,
	ext frameworkext.FrameworkExtender,
	jobRequest *framework.JobRequest,
	jobResult *framework.JobResult,
	assumedPods []*framework.AssumeContext,
	nodeSnapshot *sync.Map,
	status *fwktype.Status,
	reason string,
) {
	if len(assumedPods) == 0 {
		return
	}
	bs.engine.CleanupAssumedPods(ctx, logger, parallelizer, ext, jobRequest, jobResult, assumedPods, nodeSnapshot,
		func(pod *corev1.Pod) error {
			return bs.cache.ForgetPod(logger, pod)
		}, status, reason)
}

// skipPod skips pods that are being deleted or already scheduled/assumed on a node.
func (bs *BatchScheduler) skipPod(logger klog.Logger, pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		logger.V(4).Info("Pod has been deleted, skip batch scheduling", "pod", klog.KObj(pod), "uid", pod.UID)
		return true
	}
	if bs.cache == nil {
		return false
	}
	cachedPod, err := bs.cache.GetPod(pod)
	if err == nil && cachedPod != nil && len(cachedPod.Spec.NodeName) > 0 {
		logger.V(4).Info("Skip already scheduled pod for batch scheduling", "pod", klog.KObj(pod), "uid", pod.UID, "node", cachedPod.Spec.NodeName)
		return true
	}
	return false
}

// buildJobRequest builds a JobRequest by grouping the plan's pods by their planned node.
func buildJobRequest(triggerPod *corev1.Pod, plan *frameworkext.BatchScheduleResult) *framework.JobRequest {
	podsByNode := map[string]map[string]framework.PodRequest{}
	for _, pod := range plan.Pods {
		key := framework.GetPodKey(pod)
		nodeName := plan.PodToNodeName[key]
		if nodeName == "" {
			continue
		}
		if podsByNode[nodeName] == nil {
			podsByNode[nodeName] = map[string]framework.PodRequest{}
		}
		podsByNode[nodeName][key] = framework.PodRequest{NodeName: nodeName, Pod: pod}
	}
	return &framework.JobRequest{
		SchedulerName: triggerPod.Spec.SchedulerName,
		Namespace:     triggerPod.Namespace,
		JobName:       triggerPod.Name,
		MinMember:     len(plan.Pods),
		PodsByNode:    podsByNode,
	}
}

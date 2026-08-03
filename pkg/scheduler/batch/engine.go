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

// Package batch provides a reusable batch scheduling engine that runs a whole-job
// scheduling cycle (PreFilter/Filter/Reserve/Assume) and binding cycle (PreBind/Bind/PostBind)
// for a group of pods described by a JobRequest. It is used by the inline batch scheduler
// triggered from a FindOneNodePlugin.
package batch

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/batch/framework"
	batchmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/batch/metrics"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/deviceshare"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/nodenumaresource"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
)

const (
	ErrPreFilterFailed    = "pre-filter failed for pod %s/%s/%s, %s"
	ErrNodeIsPreFilterOut = "node %s is pre-filter out for pod %s/%s/%s"
	ErrFilterPodFailed    = "filter pod %s/%s/%s on node %s failed, err: %s"
	ErrAssumePodFailed    = "assume pod %s/%s/%s on node %s failed, err: %s"
	ErrInvalidNodeInfo    = "invalidate node info %s failed for pod %s/%s/%s, err: %s"
	ErrReservePodFailed   = "reserve pod %s/%s/%s on node %s failed, err: %s"
	ErrPreBindPodFailed   = "pre-bind pod %s/%s/%s on node %s failed, status: %s"
	ErrBindPodFailed      = "bind pod %s/%s/%s on node %s failed, status: %s"
	ErrSiblingPodFailed   = "sibling pods %s failed"
)

var (
	JobStatusScheduleFailed = fwktype.NewStatus(fwktype.Unschedulable, "job batch schedule failed")
	JobStatusBindFailed     = fwktype.NewStatus(fwktype.Unschedulable, "job bind failed")
)

const (
	OperationScheduleJobByNode = "ScheduleJobByNode"
	OperationBindJobByPodAsync = "BindJobByPodAsync"
	OperationBindJobByPodSync  = "BindJobByPodSync"
	OperationUnreserveJobByPod = "UnreserveJobByPod"
)

// Engine runs the scheduling and binding cycle for a batch of pods on a shared scheduler cache.
type Engine struct {
	cache cache.Cache
}

// NewEngine creates a batch scheduling Engine that operates on the given scheduler cache.
func NewEngine(c cache.Cache) *Engine {
	batchmetrics.Register()
	return &Engine{cache: c}
}

// RunSchedulingCycle runs the scheduling cycle for all pods in the job request.
// It runs PreFilter, Filter, Reserve and Assume plugins for each pod on each node.
// skipPod, when set and returning true for a pod, marks the pod as Skipped (e.g. already scheduled or deleted).
func (e *Engine) RunSchedulingCycle(
	ctx context.Context,
	logger klog.Logger,
	parallelizer fwktype.Parallelizer,
	fwk schedulerframework.Framework,
	jobRequest *framework.JobRequest,
	jobResult *framework.JobResult,
	podRequestsByNode [][]framework.PodRequest,
	assumedPods *[]*framework.AssumeContext,
	assumedPodLock *sync.Mutex,
	nodeSnapshot *sync.Map,
	skipPod func(pod *corev1.Pod) bool,
) {
	skippedPods := &atomic.Int64{} // account for skipped pods
	// Collect the UIDs of every pod FindOneNode placed for this job, once. The inline batch cycle skips
	// re-running the FindOneNode planner per pod (see frameworkExtenderImpl.RunPreFilterPlugins), so we
	// replicate the planner's MakeNominatedPodsOfTheSameJob side effect on each per-pod cycle state
	// below; reservation.BeforeFilter reads it during Filter to ignore nominated reservations that
	// belong to this same job.
	var sameJobPodUIDs = sets.New[string]()
	for _, podRequestsOnNode := range podRequestsByNode {
		for i := range podRequestsOnNode {
			sameJobPodUIDs.Insert(string(podRequestsOnNode[i].Pod.UID))
		}
	}
	parallelizer.Until(ctx, len(podRequestsByNode), func(i int) {
		podRequestsOnNode := podRequestsByNode[i]
		if len(podRequestsOnNode) <= 0 {
			return
		}
		nodeStart := time.Now()
		defer func() {
			batchmetrics.BatchAlgorithmByNodeLatency.Observe(time.Since(nodeStart).Seconds())
		}()
		nodeName := podRequestsOnNode[0].NodeName
		// prepareNodeSnapshot performs the optional internal node-snapshot injection, gated by the
		// EnableBatchScheduleNodeSnapshot feature; it is a no-op when the gate is disabled.
		defer prepareNodeSnapshot(logger, e.cache, fwk, nodeSnapshot, nodeName)()
		nodeInfo, err := fwk.SnapshotSharedLister().NodeInfos().Get(nodeName)
		if err != nil {
			status := fwktype.AsStatus(err)
			for _, request := range podRequestsOnNode {
				jobResult.SetPodStatus(request.Pod, status)
			}
			jobResult.Status = JobStatusScheduleFailed
			return
		}
		schedulingHintForNode := &hinter.SchedulingHintStateData{
			PreFilterNodes: []string{nodeName},
			Extensions: map[string]interface{}{
				nodenumaresource.Name: struct{}{},
				deviceshare.Name:      struct{}{},
			},
		}
		nodeReservationRestored := false

		for j := range podRequestsOnNode {
			podRequest := &podRequestsOnNode[j]
			pod := podRequest.Pod
			podStart := time.Now()
			logger.V(4).Info("Starting to batch schedule pod", "job", jobRequest.String(), "pod", klog.KObj(pod), "uid", pod.UID, "node", nodeName, "hint", schedulingHintForNode)

			// check if the pod should be skipped
			if skipPod != nil && skipPod(pod) {
				jobResult.SetPodStatus(pod, fwktype.NewStatus(fwktype.Skip))
				skippedPods.Add(1)
				logger.V(4).Info("Skip the pod", "job", jobRequest.String(), "version", jobRequest.Version, "pod", klog.KObj(pod), "uid", pod.UID)
				batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
				continue
			}

			// init cycle state for pod
			cycleState := schedulerframework.NewCycleState()
			samplePercent := GetPluginSamplePercent()
			cycleState.SetRecordPluginMetrics(rand.Intn(100) < samplePercent)
			// give plugin a hint to avoid unnecessary check.
			// When the per-node batch snapshot is disabled, the whole scheduling cycle runs on the
			// framework's shared snapshot; skip the reservation node-info restore entirely (even for the
			// first pod) so the reservation PreFilter neither mutates the shared snapshot nor invalidates
			// node info. When enabled, each node uses a fresh isolated snapshot, so the first valid pod
			// restores the reservation node info once (the first pod might be skipped, so we track the first
			// valid pod) and the remaining pods skip it.
			skipRestoreNodeInfo := nodeReservationRestored || !k8sfeature.DefaultFeatureGate.Enabled(features.EnableBatchScheduleNodeSnapshot)
			schedulingHintForNode.Extensions[reservation.Name] = reservation.HintExtensions{
				SkipRestoreNodeInfo: skipRestoreNodeInfo,
			}
			if !skipRestoreNodeInfo {
				nodeReservationRestored = true
			}
			hinter.SetSchedulingHintState(cycleState, schedulingHintForNode)
			// Mark the per-pod cycle state as engine-driven so the nested RunPreFilterPlugins below does
			// not recursively trigger the top-level inline batch schedule.
			hinter.MarkBatchSchedulingCycle(cycleState)
			// Replicate the FindOneNode planner's same-job nomination (see sameJobPodUIDs above): the
			// nested RunPreFilterPlugins skips FindOneNode in the batch cycle, so write it here instead.
			frameworkext.MakeNominatedPodsOfTheSameJob(cycleState, sameJobPodUIDs)

			// PreFilter
			preFilterResult, status, _ := fwk.RunPreFilterPlugins(ctx, cycleState, pod)
			if !status.IsSuccess() {
				errMsg := fmt.Sprintf(ErrPreFilterFailed, pod.Namespace, pod.Name, pod.UID, status.Message())
				for k := j; k < len(podRequestsOnNode); k++ {
					jobResult.SetPodStatus(podRequestsOnNode[k].Pod, fwktype.NewStatus(fwktype.Unschedulable, errMsg))
				}
				jobResult.Status = JobStatusScheduleFailed
				batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
				return
			}
			if !preFilterResult.AllNodes() && !preFilterResult.NodeNames.Has(nodeName) {
				errMsg := fmt.Sprintf(ErrNodeIsPreFilterOut, nodeName, pod.Namespace, pod.Name, pod.UID)
				for k := j; k < len(podRequestsOnNode); k++ {
					jobResult.SetPodStatus(podRequestsOnNode[k].Pod, fwktype.NewStatus(fwktype.Unschedulable, errMsg))
				}
				jobResult.Status = JobStatusScheduleFailed
				batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
				logger.V(4).Info("Failed to schedule pod which is pre-filtered out", "job", jobRequest.String(), "version", jobRequest.Version, "pod", klog.KObj(pod), "uid", pod.UID, "node", nodeName)
				return
			}

			// Filter
			phaseStartTime := time.Now()
			status = fwk.RunFilterPluginsWithNominatedPods(ctx, cycleState, pod, nodeInfo)
			metrics.FrameworkExtensionPointDuration.WithLabelValues(metrics.Filter, status.Code().String(), fwk.ProfileName()).Observe(metrics.SinceInSeconds(phaseStartTime))
			if !status.IsSuccess() {
				errMsg := fmt.Sprintf(ErrFilterPodFailed, pod.Namespace, pod.Name, pod.UID, nodeName, status.Message())
				for k := j; k < len(podRequestsOnNode); k++ {
					jobResult.SetPodStatus(podRequestsOnNode[k].Pod, fwktype.NewStatus(fwktype.Unschedulable, errMsg))
				}
				jobResult.Status = JobStatusScheduleFailed
				batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
				return
			}

			// Assume and Reserve
			// Tell the cache to assume that a pod now is running on a given node, even though it hasn't been bound yet.
			// This allows us to keep scheduling without waiting on binding to occur.
			assumedPod := pod.DeepCopy()
			// In k8s 1.35 NodeInfo is an interface without AddPod; add via a computed PodInfo.
			podInfoToAdd, _ := schedulerframework.NewPodInfo(assumedPod)
			nodeInfo.AddPodInfo(podInfoToAdd)

			// When the per-node batch snapshot is disabled, the scheduling cycle reads and mutates the
			// framework's shared snapshot in place (AddPod above modified the shared snapshot's nodeInfo).
			// Invalidate the node in the cache so the next top-level UpdateSnapshot re-clones it from the
			// cache, resetting the in-place mutation and keeping the shared snapshot consistent. When the
			// per-node snapshot is enabled, each node already uses a fresh isolated snapshot, so this is
			// unnecessary. Upstream k8s cache.Cache has no InvalidNodeInfo, so route through the
			// koordinator scheduler cache wrapper (implemented on top of AddPod/RemovePod).
			if !k8sfeature.DefaultFeatureGate.Enabled(features.EnableBatchScheduleNodeSnapshot) {
				if ext, ok := fwk.(frameworkext.FrameworkExtender); ok && ext.Scheduler() != nil {
					if err := ext.Scheduler().GetCache().InvalidNodeInfo(logger, nodeName); err != nil {
						errMsg := fmt.Sprintf(ErrInvalidNodeInfo, nodeName, pod.Namespace, pod.Name, pod.UID, err.Error())
						for k := j; k < len(podRequestsOnNode); k++ {
							jobResult.SetPodStatus(podRequestsOnNode[k].Pod, fwktype.NewStatus(fwktype.Error, errMsg))
						}
						jobResult.Status = JobStatusScheduleFailed
						batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
						return
					}
				}
			}

			// assume modifies `assumedPod` by setting NodeName=scheduleResult.SuggestedHost
			err = e.Assume(logger, assumedPod, nodeName, fwk)
			if err != nil {
				errMsg := fmt.Sprintf(ErrAssumePodFailed, pod.Namespace, pod.Name, pod.UID, nodeName, err.Error())
				for k := j; k < len(podRequestsOnNode); k++ {
					jobResult.SetPodStatus(podRequestsOnNode[k].Pod, fwktype.NewStatus(fwktype.Error, errMsg))
				}
				jobResult.Status = JobStatusScheduleFailed
				batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
				// AssumePod failed, no need to add to assumedPods for cleanup
				return
			}
			// Record the assumed pod
			assumedPodLock.Lock()
			*assumedPods = append(*assumedPods, &framework.AssumeContext{
				CycleState: cycleState,
				NodeName:   nodeName,
				Pod:        assumedPod,
			})
			assumedPodLock.Unlock()
			// Run the Reserve method of reserve plugins.
			if sts := fwk.RunReservePluginsReserve(ctx, cycleState, assumedPod, nodeName); !sts.IsSuccess() {
				// Unreserve/ForgetPod later in the failure handler
				errMsg := fmt.Sprintf(ErrReservePodFailed, assumedPod.Namespace, assumedPod.Name, assumedPod.UID, nodeName, sts.Message())
				for k := j; k < len(podRequestsOnNode); k++ {
					jobResult.SetPodStatus(podRequestsOnNode[k].Pod, fwktype.NewStatus(fwktype.Error, errMsg))
				}
				jobResult.Status = JobStatusScheduleFailed
				batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
				return
			}

			jobResult.SetPodStatus(pod, fwktype.NewStatus(fwktype.Success))
			batchmetrics.BatchAlgorithmByPodLatency.Observe(time.Since(podStart).Seconds())
			logger.V(4).Info("Assumed pod for job request", "job", jobRequest.String(), "pod", klog.KObj(assumedPod), "uid", assumedPod.UID, "node", nodeName)
		}
	}, OperationScheduleJobByNode)
	if skipped := skippedPods.Load(); skipped > 0 {
		logger.V(4).Info("Skipped some pods for job request", "job", jobRequest.String(), "version", jobRequest.Version, "skippedPods", skipped)
	}
}

// CleanupAssumedPods triggers Unreserve for all assumed pods and forgets them from the cache.
// The provided status is set on each cleaned pod, and forget is used to remove the pod from the cache
// (and optionally to run plugin forget handlers). cleanupReason is used as a metric label.
func (e *Engine) CleanupAssumedPods(
	ctx context.Context,
	logger klog.Logger,
	parallelizer fwktype.Parallelizer,
	fwk schedulerframework.Framework,
	jobRequest *framework.JobRequest,
	jobResult *framework.JobResult,
	assumedPods []*framework.AssumeContext,
	nodeSnapshot *sync.Map,
	forget func(pod *corev1.Pod) error,
	status *fwktype.Status,
	cleanupReason string,
) {
	var cleanupCount atomic.Int32
	parallelizer.Until(ctx, len(assumedPods), func(i int) {
		assumedPod := assumedPods[i]
		defer applyNodeSnapshotLister(fwk, nodeSnapshot, assumedPod.NodeName)()
		// trigger un-reserve to clean up state associated with the reserved Pod
		fwk.RunReservePluginsUnreserve(ctx, assumedPod.CycleState, assumedPod.Pod, assumedPod.NodeName)
		if forgetErr := forget(assumedPod.Pod); forgetErr != nil {
			logger.Error(forgetErr, "Scheduler cache ForgetPod failed", "job", jobRequest.String(), "version", jobRequest.Version, "pod", klog.KObj(assumedPod.Pod), "uid", assumedPod.Pod.UID, "node", assumedPod.NodeName)
		} else {
			cleanupCount.Add(1)
			logger.V(4).Info("Successfully unreserve and forgot pod", "job", jobRequest.String(), "version", jobRequest.Version, "pod", klog.KObj(assumedPod.Pod), "uid", assumedPod.Pod.UID, "node", assumedPod.NodeName)
		}
		jobResult.SetPodStatus(assumedPod.Pod, status)
	}, OperationUnreserveJobByPod)
	// Record cleanup metrics
	batchmetrics.AssumedPodsCleanupTotal.WithLabelValues(cleanupReason).Add(float64(cleanupCount.Load()))
}

// Assume signals to the cache that a pod is already in the cache, so that binding can be asynchronous.
// Assume modifies `assumed`.
func (e *Engine) Assume(logger klog.Logger, assumed *corev1.Pod, host string, fwk schedulerframework.Framework) error {
	// Optimistically assume that the binding will succeed and send it to apiserver
	// in the background.
	// If the binding fails, scheduler will release resources allocated to assumed pod
	// immediately.
	assumed.Spec.NodeName = host

	if err := e.cache.AssumePod(logger, assumed); err != nil {
		logger.Error(err, "Scheduler cache AssumePod failed", "pod", klog.KObj(assumed), "node", host)
		return err
	}
	fwk.DeleteNominatedPodIfExists(assumed)
	return nil
}

// ValidateAndGroupByRequest validates the job request and groups the pod requests by node.
func ValidateAndGroupByRequest(jobRequest *framework.JobRequest) ([][]framework.PodRequest, error) {
	podCount := 0
	var requestsByNode [][]framework.PodRequest
	for _, podRequests := range jobRequest.PodsByNode {
		podCount += len(podRequests)
		pods := make([]framework.PodRequest, 0, len(podRequests))
		for _, pod := range podRequests {
			pods = append(pods, pod)
		}
		sort.Slice(pods, func(i, j int) bool {
			return pods[i].Pod.Name < pods[j].Pod.Name
		})
		requestsByNode = append(requestsByNode, pods)
	}
	if len(requestsByNode) <= 0 || len(requestsByNode[0]) <= 0 {
		return nil, fmt.Errorf("no pods to schedule")
	}
	// A dequeued job request may no longer have enough member pods. e.g. When the job is in activeQ but got some pods deleted.
	// A special case is that the requeued request we do not guarantee the member pods any more, but try best to schedule the remaining.
	if !jobRequest.Requeued && podCount < jobRequest.MinMember {
		return nil, fmt.Errorf("job request is not ready, pod count %d < min member %d", podCount, jobRequest.MinMember)
	}
	return requestsByNode, nil
}

// MakeJobResultWithStatus builds a JobResult where every pod carries the given status.
func MakeJobResultWithStatus(podRequestsOnNode [][]framework.PodRequest, status *fwktype.Status) *framework.JobResult {
	jobResult := &framework.JobResult{}
	for i := range podRequestsOnNode {
		podRequests := podRequestsOnNode[i]
		for _, request := range podRequests {
			jobResult.SetPodStatus(request.Pod, status)
		}
	}
	jobResult.Status = status
	return jobResult
}

const (
	// pluginMetricsSamplePercent is the percentage of plugin metrics to be sampled.
	pluginMetricsSamplePercent = 10
)

// GetPluginSamplePercent returns the percentage of plugin metrics to be sampled.
func GetPluginSamplePercent() int {
	samplePercent := pluginMetricsSamplePercent
	if rawSamplePercent := os.Getenv("pluginMetricsSamplePercent"); rawSamplePercent != "" {
		var err error
		samplePercent, err = strconv.Atoi(rawSamplePercent)
		if err != nil {
			klog.ErrorS(err, "failed to parse pluginMetricsSamplePercent from env", "value", rawSamplePercent)
		}
	}
	klog.V(5).Infof("plugin metrics sample percent: %d", samplePercent)
	return samplePercent
}

// BuildSiblingFailedStatus builds the Unschedulable status used to mark assumed pods that must be
// cleaned up because sibling pods in the same job failed.
func BuildSiblingFailedStatus(failedPodKeys []string) *fwktype.Status {
	failedMsg := fmt.Sprintf(ErrSiblingPodFailed, strings.Join(failedPodKeys, ","))
	return fwktype.NewStatus(fwktype.Unschedulable, failedMsg)
}

// BindPods binds all assumed pods to their nodes.
// It runs PreBind, Bind and PostBind plugins for each assumed pod.
// Pods that fail PreBind/Bind are appended to assumedPods for later cleanup by the caller.
func (e *Engine) BindPods(
	ctx context.Context,
	logger klog.Logger,
	parallelizer fwktype.Parallelizer,
	fwk schedulerframework.Framework,
	jobRequest *framework.JobRequest,
	jobResult *framework.JobResult,
	toBindPods []*framework.AssumeContext,
	assumedPods *[]*framework.AssumeContext,
	assumedPodLock *sync.Mutex,
	nodeSnapshot *sync.Map,
	operation string,
) {
	var successCount, failureCount atomic.Int32
	parallelizer.Until(ctx, len(toBindPods), func(i int) {
		assumedPod := toBindPods[i]
		// applyNodeSnapshotLister swaps in the per-node snapshot when present (injected by the optional
		// snapshot override); a no-op when EnableBatchScheduleNodeSnapshot is disabled.
		defer applyNodeSnapshotLister(fwk, nodeSnapshot, assumedPod.NodeName)()
		logger.V(4).Info("About to async bind pod to node", "request", jobRequest.ID(), "version", jobRequest.Version, "pod", klog.KObj(assumedPod.Pod), "uid", assumedPod.Pod.UID, "node", assumedPod.NodeName)
		metrics.Goroutines.WithLabelValues(metrics.Binding).Inc()
		defer metrics.Goroutines.WithLabelValues(metrics.Binding).Dec()

		if status := fwk.RunPreBindPlugins(ctx, assumedPod.CycleState, assumedPod.Pod, assumedPod.NodeName); !status.IsSuccess() {
			errMsg := fmt.Sprintf(ErrPreBindPodFailed, assumedPod.Pod.Namespace, assumedPod.Pod.Name, assumedPod.Pod.UID, assumedPod.NodeName, status.Message())
			logger.V(2).Info(errMsg)
			jobResult.SetPodStatus(assumedPod.Pod, fwktype.NewStatus(fwktype.Error, errMsg))
			jobResult.Status = JobStatusBindFailed
			assumedPodLock.Lock()
			*assumedPods = append(*assumedPods, assumedPod)
			assumedPodLock.Unlock()
			failureCount.Add(1)
			batchmetrics.BindFailuresByReason.WithLabelValues("pre_bind_error", "PreBind").Inc()
			return
		}
		// NOTE: Need a retry when the Bind failed due to a retriable error like http 429.
		if status := fwk.RunBindPlugins(ctx, assumedPod.CycleState, assumedPod.Pod, assumedPod.NodeName); !status.IsSuccess() {
			errMsg := fmt.Sprintf(ErrBindPodFailed, assumedPod.Pod.Namespace, assumedPod.Pod.Name, assumedPod.Pod.UID, assumedPod.NodeName, status.Message())
			logger.V(2).Info(errMsg)
			jobResult.SetPodStatus(assumedPod.Pod, fwktype.NewStatus(fwktype.Error, errMsg))
			jobResult.Status = JobStatusBindFailed
			assumedPodLock.Lock()
			*assumedPods = append(*assumedPods, assumedPod)
			assumedPodLock.Unlock()
			failureCount.Add(1)
			// Categorize bind failure reason
			batchmetrics.BindFailuresByReason.WithLabelValues("bind_error", "Bind").Inc()
			return
		}
		successCount.Add(1)
		logger.V(4).Info("Successfully bound pod to node", "request", jobRequest.ID(), "pod", klog.KObj(assumedPod.Pod), "uid", assumedPod.Pod.UID, "node", assumedPod.NodeName)
		fwk.RunPostBindPlugins(ctx, assumedPod.CycleState, assumedPod.Pod, assumedPod.NodeName)
	}, operation)

	// Record partial bind metrics
	success := successCount.Load()
	failure := failureCount.Load()
	if success > 0 && failure > 0 {
		// Partial bind success scenario detected
		batchmetrics.PartialBindJobsTotal.Inc()
		batchmetrics.PartialBindPodsTotal.WithLabelValues("success").Add(float64(success))
		batchmetrics.PartialBindPodsTotal.WithLabelValues("failure").Add(float64(failure))
		logger.V(2).Info("Partial bind detected in job", "job", jobRequest.String(), "version", jobRequest.Version,
			"successPods", success, "failedPods", failure, "totalPods", len(toBindPods))
	}
}

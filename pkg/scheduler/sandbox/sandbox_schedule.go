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
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulerframeworkext "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

const (
	// minFeasibleNodesToFind is the minimum number of nodes that would be scored
	// in each scheduling cycle. This is a semi-arbitrary value to ensure that a
	// certain minimum of nodes are checked for feasibility. This in turn helps
	// ensure a minimum level of spreading.
	minFeasibleNodesToFind = 100
	// minFeasibleNodesPercentageToFind is the minimum percentage of nodes that
	// would be scored in each scheduling cycle. This is a semi-arbitrary value
	// to ensure that a certain minimum of nodes are checked for feasibility.
	// This in turn helps ensure a minimum level of spreading.
	minFeasibleNodesPercentageToFind = 5
)

type equivalenceScheduling struct {
	sched                    *scheduler.Scheduler
	equivalence              *equivalenceClassCache
	nextStartNodeIndex       atomic.Int64
	percentageOfNodesToScore int32
}

func newEquivalenceScheduling(sched *scheduler.Scheduler, percentageOfNodesToScore *int32, cacheCapacity int) *equivalenceScheduling {
	s := &equivalenceScheduling{
		sched:       sched,
		equivalence: newEquivalenceClassCache(defaultEquivalenceClassTTL, cacheCapacity),
	}
	if percentageOfNodesToScore != nil {
		s.percentageOfNodesToScore = *percentageOfNodesToScore
	}
	return s
}

func (s *equivalenceScheduling) registerNodeEventHandler(informer toolscache.SharedIndexInformer) error {
	// Any change of the node inventory may invalidate cached equivalence-class decisions;
	// flushing everything is the conservative choice and rebuilding is one full scheduling
	// cycle away.
	_, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { s.flushEquivalenceCache("node_event") },
		UpdateFunc: func(oldObj, newObj interface{}) { s.flushEquivalenceCache("node_event") },
		DeleteFunc: func(obj interface{}) { s.flushEquivalenceCache("node_event") },
	})
	return err
}

func (s *equivalenceScheduling) handles(pod *corev1.Pod) bool {
	return apiext.IsSandboxPod(pod) && apiext.GetSandboxTemplateHash(pod) != ""
}

func (s *equivalenceScheduling) flushEquivalenceCache(reason string) {
	s.equivalence.flush()
	koordmetrics.SandboxEquivalenceClassFlushes.WithLabelValues(reason).Inc()
}

type sandboxPreFilterResult struct {
	result             *fwktype.PreFilterResult
	status             *fwktype.Status
	unscheduledPlugins sets.Set[string]
}

func (s *equivalenceScheduling) runSandboxPreFilter(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod) sandboxPreFilterResult {
	result, status, unscheduledPlugins := schedFramework.RunPreFilterPlugins(ctx, state, pod)
	return sandboxPreFilterResult{
		result:             result,
		status:             status,
		unscheduledPlugins: unscheduledPlugins,
	}
}

// decide is the decision path for sandbox pods carrying a template hash. It first tries
// the equivalence-class fast path; on a miss it runs a full self-orchestrated scheduling cycle
// (the exported scheduler.SchedulePod cannot be used here because ScheduleResult only carries
// the winning host, not the score-ordered feasible list needed for cache backfill) and backfills
// the class.
func (s *equivalenceScheduling) decide(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod) (result scheduler.ScheduleResult, err error) {
	start := time.Now()
	path := "full"
	resultLabel := "error"
	defer func() {
		if err == nil {
			resultLabel = "success"
		} else if _, ok := err.(*framework.FitError); ok {
			resultLabel = "unschedulable"
		}
		koordmetrics.SandboxSchedulingDuration.WithLabelValues(schedFramework.ProfileName(), path, resultLabel).Observe(time.Since(start).Seconds())
		if err == nil && result.SuggestedHost != "" {
			koordmetrics.PodSchedulingEvaluatedNodes.Observe(float64(result.EvaluatedNodes))
			koordmetrics.PodSchedulingFeasibleNodes.Observe(float64(result.FeasibleNodes))
		}
	}()

	hash := apiext.GetSandboxTemplateHash(pod)
	snapshot, err := s.updateSnapshot(klog.FromContext(ctx), schedFramework)
	if err != nil {
		return scheduler.ScheduleResult{}, err
	}
	if snapshot.NumNodes() == 0 {
		return scheduler.ScheduleResult{}, scheduler.ErrNoNodesAvailable
	}

	preFilter := s.runSandboxPreFilter(ctx, state, schedFramework, pod)
	if !preFilter.status.IsSuccess() {
		koordmetrics.SandboxEquivalenceClassMisses.WithLabelValues(schedFramework.ProfileName(), equivalenceCacheMissPreFilter.String()).Inc()
		result, _, err = s.scheduleSandboxPod(ctx, state, schedFramework, pod, snapshot, preFilter)
		return result, err
	}

	if node, ok, reason := s.scheduleFromEquivalenceClass(ctx, state, schedFramework, pod, hash, snapshot, preFilter); ok {
		path = "fast"
		koordmetrics.SandboxEquivalenceClassHits.WithLabelValues(schedFramework.ProfileName()).Inc()
		return scheduler.ScheduleResult{SuggestedHost: node, EvaluatedNodes: 1, FeasibleNodes: 1}, nil
	} else {
		if reason == "" {
			reason = equivalenceCacheMissEmpty
		}
		koordmetrics.SandboxEquivalenceClassMisses.WithLabelValues(schedFramework.ProfileName(), reason.String()).Inc()
	}

	result, orderedNodes, err := s.scheduleSandboxPod(ctx, state, schedFramework, pod, snapshot, preFilter)
	if err == nil {
		// The quota baselines reflect every occupant (running and assumed) at decision time.
		// The pod paying for this full path occupies one slot on the suggested host itself.
		cycle := s.sched.CurrentCycle()
		s.equivalence.store(hash, buildQuotaNodesWithPlugins(ctx, state, pod, orderedNodes, schedFramework.SnapshotSharedLister(), s.equivalenceCapacityPlugins(schedFramework)), cycle)
		s.equivalence.recordConsumption(hash, result.SuggestedHost, cycle)
	}
	return result, err
}

func (s *equivalenceScheduling) equivalenceCapacityPlugins(schedFramework framework.Framework) []schedulerframeworkext.EquivalenceCapacityPlugin {
	if extender, ok := schedFramework.(schedulerframeworkext.FrameworkExtender); ok {
		return extender.EquivalenceCapacityPlugins()
	}
	return nil
}

func (s *equivalenceScheduling) updateSnapshot(logger klog.Logger, schedFramework framework.Framework) (*cache.Snapshot, error) {
	snapshot, ok := schedFramework.SnapshotSharedLister().(*cache.Snapshot)
	if !ok {
		return nil, fmt.Errorf("unexpected snapshot shared lister type %T", schedFramework.SnapshotSharedLister())
	}
	if err := s.sched.Cache.UpdateSnapshot(logger, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// scheduleFromEquivalenceClass tries to reuse the cached decision of the pod's equivalence
// class: it takes the next cached node and validates it with a single-node Filter pass. PreFilter
// still runs per pod because plugins read its state from the cycle state (e.g. NodeResourcesFit's
// Filter reads the PreFilter-computed pod requests). The second return value is false when the
// class cannot serve this pod (miss, expiry, exhaustion, or a plugin error), and the caller
// falls back to the full scheduling path.
func (s *equivalenceScheduling) scheduleFromEquivalenceClass(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod, hash string, snapshot *cache.Snapshot, preFilter sandboxPreFilterResult) (string, bool, equivalenceCacheMissReason) {
	if !preFilter.status.IsSuccess() {
		return "", false, equivalenceCacheMissPreFilter
	}
	var sawFilterRejected, sawSnapshotError bool
	for {
		node, ok, reason := s.equivalence.next(hash, s.sched.CurrentCycle())
		if !ok {
			if sawFilterRejected && reason == equivalenceCacheMissQuotaExhausted {
				return "", false, equivalenceCacheMissFilterRejected
			}
			if sawSnapshotError && reason == equivalenceCacheMissQuotaExhausted {
				return "", false, equivalenceCacheMissSnapshotError
			}
			return "", false, reason
		}
		// Respect this pod's PreFilter node restriction: same-class pods should produce the
		// same PreFilter result, but the restriction is cheap to honor and keeps the reuse safe.
		if !preFilter.result.AllNodes() && !preFilter.result.NodeNames.Has(node) {
			continue
		}
		nodeInfo, err := snapshot.NodeInfos().Get(node)
		if err != nil {
			// The node is gone from the snapshot; drop it and try the next candidate.
			sawSnapshotError = true
			continue
		}
		filterStatus := schedFramework.RunFilterPluginsWithNominatedPods(ctx, state, pod, nodeInfo)
		if filterStatus.IsSuccess() {
			return node, true, ""
		}
		if filterStatus.Code() == fwktype.Error {
			s.flushEquivalenceCache(equivalenceCacheMissFilterError.String())
			return "", false, equivalenceCacheMissFilterError
		}
		// The cached node no longer fits (its resources were consumed since the decision was
		// cached); drop it and try the next candidate.
		sawFilterRejected = true
	}
}

func advanceNodeIndex(index *atomic.Int64, delta, nodeCount int64) {
	if nodeCount <= 0 {
		return
	}
	for {
		old := index.Load()
		next := (old + delta) % nodeCount
		if index.CompareAndSwap(old, next) {
			return
		}
	}
}

// scheduleSandboxPod mirrors scheduler.schedulePod (schedule_one.go:421) for sandbox pods,
// returning the score-ordered feasible node names alongside the result for cache backfill.
// Unlike the upstream it does not consult the opportunistic-batching node hint: the sandbox
// equivalence class replaces that mechanism for the multi-pod-per-node case.
func (s *equivalenceScheduling) scheduleSandboxPod(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod, snapshot *cache.Snapshot, preFilter sandboxPreFilterResult) (scheduler.ScheduleResult, []string, error) {
	var result scheduler.ScheduleResult

	feasibleNodes, diagnosis, err := s.findNodesThatFitPod(ctx, schedFramework, state, pod, snapshot, preFilter)
	if err != nil {
		return result, nil, err
	}
	if len(feasibleNodes) == 0 {
		return result, nil, &framework.FitError{
			Pod:         pod,
			NumAllNodes: snapshot.NumNodes(),
			Diagnosis:   diagnosis,
		}
	}

	// When only one node after predicate, just use it.
	if len(feasibleNodes) == 1 {
		node := feasibleNodes[0].Node().Name
		return scheduler.ScheduleResult{
			SuggestedHost:  node,
			EvaluatedNodes: 1 + diagnosis.NodeToStatus.Len(),
			FeasibleNodes:  1,
		}, []string{node}, nil
	}

	priorityList, err := prioritizeNodes(ctx, s.sched.Extenders, schedFramework, state, pod, feasibleNodes)
	if err != nil {
		return result, nil, err
	}
	sortedPrioritizedNodes := newSortedNodeScores(priorityList)
	orderedNodes := make([]string, 0, sortedPrioritizedNodes.Len())
	for sortedPrioritizedNodes.Len() > 0 {
		orderedNodes = append(orderedNodes, sortedPrioritizedNodes.Pop())
	}

	return scheduler.ScheduleResult{
		SuggestedHost:  orderedNodes[0],
		EvaluatedNodes: len(feasibleNodes) + diagnosis.NodeToStatus.Len(),
		FeasibleNodes:  len(feasibleNodes),
	}, orderedNodes, nil
}

// findNodesThatFitPod mirrors scheduler.findNodesThatFitPod (schedule_one.go:482), without the
// opportunistic-batching node hint (replaced by the sandbox equivalence class).
func (s *equivalenceScheduling) findNodesThatFitPod(ctx context.Context, schedFramework framework.Framework, state fwktype.CycleState, pod *corev1.Pod, snapshot *cache.Snapshot, preFilter sandboxPreFilterResult) ([]fwktype.NodeInfo, framework.Diagnosis, error) {
	logger := klog.FromContext(ctx)
	diagnosis := framework.Diagnosis{
		NodeToStatus: framework.NewDefaultNodeToStatus(),
	}

	allNodes, err := snapshot.NodeInfos().List()
	if err != nil {
		return nil, diagnosis, err
	}
	// PreFilter runs once in decide and its result is reused by both the
	// equivalence-class fast path and the full fallback path.
	preRes := preFilter.result
	status := preFilter.status
	diagnosis.UnschedulablePlugins = preFilter.unscheduledPlugins
	if !status.IsSuccess() {
		if !status.IsRejected() {
			return nil, diagnosis, status.AsError()
		}
		// All nodes in NodeToStatus will have the same status so that they can be handled in the preemption.
		diagnosis.NodeToStatus.SetAbsentNodesStatus(status)

		// Record the messages from PreFilter in Diagnosis.PreFilterMsg.
		msg := status.Message()
		diagnosis.PreFilterMsg = msg
		logger.V(5).Info("Status after running PreFilter plugins for pod", "pod", klog.KObj(pod), "status", msg)
		diagnosis.AddPluginStatus(status)
		return nil, diagnosis, nil
	}

	// "NominatedNodeName" can potentially be set in a previous scheduling cycle as a result of preemption.
	// This node is likely the only candidate that will fit the pod, and hence we try it first before iterating over all nodes.
	if len(pod.Status.NominatedNodeName) > 0 {
		feasibleNodes, err := s.evaluateNominatedNode(ctx, pod, schedFramework, state, "", snapshot, diagnosis)
		if err != nil {
			utilruntime.HandleErrorWithContext(ctx, err, "Evaluation failed on nominated node", "pod", klog.KObj(pod), "node", pod.Status.NominatedNodeName)
		}
		// Nominated node passes all the filters, scheduler is good to assign this node to the pod.
		if len(feasibleNodes) != 0 {
			return feasibleNodes, diagnosis, nil
		}
	}

	nodes := allNodes
	if !preRes.AllNodes() {
		nodes = make([]fwktype.NodeInfo, 0, len(preRes.NodeNames))
		for nodeName := range preRes.NodeNames {
			// PreRes may return nodeName(s) which do not exist; we verify
			// node exists in the Snapshot.
			if nodeInfo, err := snapshot.Get(nodeName); err == nil {
				nodes = append(nodes, nodeInfo)
			}
		}
		diagnosis.NodeToStatus.SetAbsentNodesStatus(fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, fmt.Sprintf("node(s) didn't satisfy plugin(s) %v", sets.List(preFilter.unscheduledPlugins))))
	}
	feasibleNodes, err := s.findNodesThatPassFilters(ctx, schedFramework, state, pod, &diagnosis, nodes)
	// always try to update the nextStartNodeIndex regardless of whether an error has occurred
	// this is helpful to make sure that all the nodes have a chance to be searched
	processedNodes := len(feasibleNodes) + diagnosis.NodeToStatus.Len()
	advanceNodeIndex(&s.nextStartNodeIndex, int64(processedNodes), int64(len(allNodes)))
	if err != nil {
		return nil, diagnosis, err
	}

	feasibleNodesAfterExtender, err := findNodesThatPassExtenders(ctx, s.sched.Extenders, pod, feasibleNodes, diagnosis.NodeToStatus)
	if err != nil {
		return nil, diagnosis, err
	}
	if len(feasibleNodesAfterExtender) != len(feasibleNodes) {
		// Extenders filtered out some nodes.
		//
		// Extender doesn't support any kind of requeueing feature like EnqueueExtensions in the scheduling framework.
		// When Extenders reject some Nodes and the pod ends up being unschedulable,
		// we put fwk.ExtenderName to pInfo.UnschedulablePlugins.
		// This Pod will be requeued from unschedulable pod pool to activeQ/backoffQ
		// by any kind of cluster events.
		// https://github.com/kubernetes/kubernetes/issues/122019
		if diagnosis.UnschedulablePlugins == nil {
			diagnosis.UnschedulablePlugins = sets.New[string]()
		}
		diagnosis.UnschedulablePlugins.Insert(framework.ExtenderName)
	}

	return feasibleNodesAfterExtender, diagnosis, nil
}

func (s *equivalenceScheduling) evaluateNominatedNode(
	ctx context.Context,
	pod *corev1.Pod,
	schedFramework framework.Framework,
	state fwktype.CycleState,
	nodeHint string,
	snapshot *cache.Snapshot,
	diagnosis framework.Diagnosis,
) ([]fwktype.NodeInfo, error) {
	// In the future we could potentially use the hint if the nominated node failed.
	// https://github.com/kubernetes/kubernetes/issues/135163
	nnn := pod.Status.NominatedNodeName
	if len(nnn) == 0 {
		nnn = nodeHint
	}

	nodeInfo, err := snapshot.Get(nnn)
	if err != nil {
		return nil, err
	}
	node := []fwktype.NodeInfo{nodeInfo}
	feasibleNodes, err := s.findNodesThatPassFilters(ctx, schedFramework, state, pod, &diagnosis, node)
	if err != nil {
		return nil, err
	}

	feasibleNodes, err = findNodesThatPassExtenders(ctx, s.sched.Extenders, pod, feasibleNodes, diagnosis.NodeToStatus)
	if err != nil {
		return nil, err
	}

	return feasibleNodes, nil
}

// hasScoring checks if scoring nodes is configured.
func (s *equivalenceScheduling) hasScoring(fwk framework.Framework) bool {
	if fwk.HasScorePlugins() {
		return true
	}
	for _, extender := range s.sched.Extenders {
		if extender.IsPrioritizer() {
			return true
		}
	}
	return false
}

// hasExtenderFilters checks if any extenders filter nodes.
func (s *equivalenceScheduling) hasExtenderFilters() bool {
	for _, extender := range s.sched.Extenders {
		if extender.IsFilter() {
			return true
		}
	}
	return false
}

// findNodesThatPassFilters mirrors scheduler.findNodesThatPassFilters (schedule_one.go:625),
// using the equivalence scheduling path's own nextStartNodeIndex instead of the scheduler's private one.
func (s *equivalenceScheduling) findNodesThatPassFilters(
	ctx context.Context,
	schedFramework framework.Framework,
	state fwktype.CycleState,
	pod *corev1.Pod,
	diagnosis *framework.Diagnosis,
	nodes []fwktype.NodeInfo) ([]fwktype.NodeInfo, error) {
	numAllNodes := len(nodes)
	if numAllNodes == 0 {
		return nil, nil
	}
	numNodesToFind := s.numFeasibleNodesToFind(schedFramework.PercentageOfNodesToScore(), int32(numAllNodes))
	if !s.hasExtenderFilters() && !s.hasScoring(schedFramework) {
		numNodesToFind = 1
	}

	// Create feasible list with enough space to avoid growing it
	// and allow assigning.
	feasibleNodes := make([]fwktype.NodeInfo, numNodesToFind)
	startNodeIndex := int(s.nextStartNodeIndex.Load()) % numAllNodes

	if !schedFramework.HasFilterPlugins() {
		for i := range feasibleNodes {
			feasibleNodes[i] = nodes[(startNodeIndex+i)%numAllNodes]
		}
		return feasibleNodes, nil
	}

	errCh := parallelize.NewErrorChannel()
	var feasibleNodesLen int32
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(errors.New("findNodesThatPassFilters has completed"))

	type nodeStatus struct {
		node   string
		status *fwktype.Status
	}
	result := make([]*nodeStatus, numAllNodes)
	checkNode := func(i int) {
		// We check the nodes starting from where we left off in the previous scheduling cycle,
		// this is to make sure all nodes have the same chance of being examined across pods.
		nodeInfo := nodes[(startNodeIndex+i)%numAllNodes]
		status := schedFramework.RunFilterPluginsWithNominatedPods(ctx, state, pod, nodeInfo)
		if status.Code() == fwktype.Error {
			errCh.SendErrorWithCancel(status.AsError(), func() {
				cancel(errors.New("some other Filter operation failed"))
			})
			return
		}
		if status.IsSuccess() {
			length := atomic.AddInt32(&feasibleNodesLen, 1)
			if length > numNodesToFind {
				cancel(errors.New("findNodesThatPassFilters has found enough nodes"))
				atomic.AddInt32(&feasibleNodesLen, -1)
			} else {
				feasibleNodes[length-1] = nodeInfo
			}
		} else {
			result[i] = &nodeStatus{node: nodeInfo.Node().Name, status: status}
		}
	}

	beginCheckNode := time.Now()
	statusCode := fwktype.Success
	defer func() {
		// We record Filter extension point latency here instead of in framework.go because framework.RunFilterPlugins
		// function is called for each node, whereas we want to have an overall latency for all nodes per scheduling cycle.
		// Note that this latency also includes latency for `addNominatedPods`, which calls framework.RunPreFilterAddPod.
		metrics.FrameworkExtensionPointDuration.WithLabelValues(metrics.Filter, statusCode.String(), schedFramework.ProfileName()).Observe(metrics.SinceInSeconds(beginCheckNode))
	}()

	// Stops searching for more nodes once the configured number of feasible nodes
	// are found.
	schedFramework.Parallelizer().Until(ctx, numAllNodes, checkNode, metrics.Filter)
	feasibleNodes = feasibleNodes[:feasibleNodesLen]
	for _, item := range result {
		if item == nil {
			continue
		}
		diagnosis.NodeToStatus.Set(item.node, item.status)
		diagnosis.AddPluginStatus(item.status)
	}
	if err := errCh.ReceiveError(); err != nil {
		statusCode = fwktype.Error
		return feasibleNodes, err
	}
	return feasibleNodes, nil
}

// numFeasibleNodesToFind returns the number of feasible nodes that once found, the scheduler stops
// its search for more feasible nodes.
func (s *equivalenceScheduling) numFeasibleNodesToFind(percentageOfNodesToScore *int32, numAllNodes int32) (numNodes int32) {
	if numAllNodes < minFeasibleNodesToFind {
		return numAllNodes
	}

	// Use profile percentageOfNodesToScore if it's set. Otherwise, use global percentageOfNodesToScore.
	var percentage int32
	if percentageOfNodesToScore != nil {
		percentage = *percentageOfNodesToScore
	} else {
		percentage = s.percentageOfNodesToScore
	}

	if percentage == 0 {
		percentage = int32(50) - numAllNodes/125
		if percentage < minFeasibleNodesPercentageToFind {
			percentage = minFeasibleNodesPercentageToFind
		}
	}

	numNodes = numAllNodes * percentage / 100
	if numNodes < minFeasibleNodesToFind {
		return minFeasibleNodesToFind
	}

	return numNodes
}

func findNodesThatPassExtenders(ctx context.Context, extenders []fwktype.Extender, pod *corev1.Pod, feasibleNodes []fwktype.NodeInfo, statuses *framework.NodeToStatus) ([]fwktype.NodeInfo, error) {
	logger := klog.FromContext(ctx)

	// Extenders are called sequentially.
	// Nodes in original feasibleNodes can be excluded in one extender, and pass on to the next
	// extender in a decreasing manner.
	for _, extender := range extenders {
		if len(feasibleNodes) == 0 {
			break
		}
		if !extender.IsInterested(pod) {
			continue
		}

		// Status of failed nodes in failedAndUnresolvableMap will be added to <statuses>,
		// so that the scheduler framework can respect the UnschedulableAndUnresolvable status for
		// particular nodes, and this may eventually improve preemption efficiency.
		// Note: users are recommended to configure the extenders that may return UnschedulableAndUnresolvable
		// status ahead of others.
		feasibleList, failedMap, failedAndUnresolvableMap, err := extender.Filter(pod, feasibleNodes)
		if err != nil {
			if extender.IsIgnorable() {
				logger.Info("Skipping extender as it returned error and has ignorable flag set", "extender", extender, "err", err)
				continue
			}
			return nil, err
		}

		for failedNodeName, failedMsg := range failedAndUnresolvableMap {
			statuses.Set(failedNodeName, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, failedMsg))
		}

		for failedNodeName, failedMsg := range failedMap {
			if _, found := failedAndUnresolvableMap[failedNodeName]; found {
				// failedAndUnresolvableMap takes precedence over failedMap
				// note that this only happens if the extender returns the node in both maps
				continue
			}
			statuses.Set(failedNodeName, fwktype.NewStatus(fwktype.Unschedulable, failedMsg))
		}

		feasibleNodes = feasibleList
	}
	return feasibleNodes, nil
}

// prioritizeNodes prioritizes the nodes by running the score plugins,
// which return a score for each node from the call to RunScorePlugins().
// The scores from each plugin are added together to make the score for that node, then
// any extenders are run as well.
// All scores are finally combined (added) to get the total weighted scores of all nodes.
func prioritizeNodes(
	ctx context.Context,
	extenders []fwktype.Extender,
	schedFramework framework.Framework,
	state fwktype.CycleState,
	pod *corev1.Pod,
	nodes []fwktype.NodeInfo,
) ([]fwktype.NodePluginScores, error) {
	logger := klog.FromContext(ctx)
	// If no priority configs are provided, then all nodes will have a score of one.
	// This is required to generate the priority list in the required format
	if len(extenders) == 0 && !schedFramework.HasScorePlugins() {
		result := make([]fwktype.NodePluginScores, 0, len(nodes))
		for i := range nodes {
			result = append(result, fwktype.NodePluginScores{
				Name:       nodes[i].Node().Name,
				TotalScore: 1,
			})
		}
		return result, nil
	}

	// Run PreScore plugins.
	preScoreStatus := schedFramework.RunPreScorePlugins(ctx, state, pod, nodes)
	if !preScoreStatus.IsSuccess() {
		return nil, preScoreStatus.AsError()
	}

	// Run the Score plugins.
	nodesScores, scoreStatus := schedFramework.RunScorePlugins(ctx, state, pod, nodes)
	if !scoreStatus.IsSuccess() {
		return nil, scoreStatus.AsError()
	}

	// Additional details logged at level 10 if enabled.
	loggerVTen := logger.V(10)
	if loggerVTen.Enabled() {
		for _, nodeScore := range nodesScores {
			for _, pluginScore := range nodeScore.Scores {
				loggerVTen.Info("Plugin scored node for pod", "pod", klog.KObj(pod), "plugin", pluginScore.Name, "node", nodeScore.Name, "score", pluginScore.Score)
			}
		}
	}

	if len(extenders) != 0 && nodes != nil {
		// allNodeExtendersScores has all extenders scores for all nodes.
		// It is keyed with node name.
		allNodeExtendersScores := make(map[string]*fwktype.NodePluginScores, len(nodes))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := range extenders {
			if !extenders[i].IsInterested(pod) {
				continue
			}
			wg.Add(1)
			go func(extIndex int) {
				metrics.Goroutines.WithLabelValues(metrics.PrioritizingExtender).Inc()
				defer func() {
					metrics.Goroutines.WithLabelValues(metrics.PrioritizingExtender).Dec()
					wg.Done()
				}()
				prioritizedList, weight, err := extenders[extIndex].Prioritize(pod, nodes)
				if err != nil {
					// Prioritization errors from extender can be ignored, let k8s/other extenders determine the priorities
					logger.V(5).Info("Failed to run extender's priority function. No score given by this extender.", "error", err, "pod", klog.KObj(pod), "extender", extenders[extIndex].Name())
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for i := range *prioritizedList {
					nodename := (*prioritizedList)[i].Host
					score := (*prioritizedList)[i].Score
					if loggerVTen.Enabled() {
						loggerVTen.Info("Extender scored node for pod", "pod", klog.KObj(pod), "extender", extenders[extIndex].Name(), "node", nodename, "score", score)
					}

					// MaxExtenderPriority may diverge from the max priority used in the scheduler and defined by MaxNodeScore,
					// therefore we need to scale the score returned by extenders to the score range used by the scheduler.
					finalscore := score * weight * (fwktype.MaxNodeScore / extenderv1.MaxExtenderPriority)

					if allNodeExtendersScores[nodename] == nil {
						allNodeExtendersScores[nodename] = &fwktype.NodePluginScores{
							Name:   nodename,
							Scores: make([]fwktype.PluginScore, 0, len(extenders)),
						}
					}
					allNodeExtendersScores[nodename].Scores = append(allNodeExtendersScores[nodename].Scores, fwktype.PluginScore{
						Name:  extenders[extIndex].Name(),
						Score: finalscore,
					})
					allNodeExtendersScores[nodename].TotalScore += finalscore
				}
			}(i)
		}
		// wait for all go routines to finish
		wg.Wait()
		for i := range nodesScores {
			if score, ok := allNodeExtendersScores[nodes[i].Node().Name]; ok {
				nodesScores[i].Scores = append(nodesScores[i].Scores, score.Scores...)
				nodesScores[i].TotalScore += score.TotalScore
				nodesScores[i].Randomizer = rand.Int()
			}
		}
	}

	if loggerVTen.Enabled() {
		for i := range nodesScores {
			loggerVTen.Info("Calculated node's final score for pod", "pod", klog.KObj(pod), "node", nodesScores[i].Name, "score", nodesScores[i].TotalScore)
		}
	}
	return nodesScores, nil
}

type sortedNodeScores struct {
	nodes nodeScoreHeap
}

func newSortedNodeScores(nodeScoreList []fwktype.NodePluginScores) *sortedNodeScores {
	var h nodeScoreHeap = nodeScoreList
	heap.Init(&h)
	return &sortedNodeScores{nodes: h}
}

func (s *sortedNodeScores) Pop() string {
	ent := heap.Pop(&s.nodes).(fwktype.NodePluginScores)
	return ent.Name
}

// Used only for unit tests.
func (s *sortedNodeScores) PopScore() fwktype.NodePluginScores {
	ent := heap.Pop(&s.nodes).(fwktype.NodePluginScores)
	return ent
}

func (s *sortedNodeScores) Len() int {
	return s.nodes.Len()
}

// nodeScoreHeap is a heap of fwktype.NodePluginScores.
type nodeScoreHeap []fwktype.NodePluginScores

// nodeScoreHeap implements heap.Interface.
var _ heap.Interface = &nodeScoreHeap{}

func (h nodeScoreHeap) Len() int { return len(h) }
func (h nodeScoreHeap) Less(i, j int) bool {
	return (h[i].TotalScore > h[j].TotalScore ||
		(h[i].TotalScore == h[j].TotalScore && h[i].Randomizer > h[j].Randomizer))
}
func (h nodeScoreHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *nodeScoreHeap) Push(x interface{}) {
	*h = append(*h, x.(fwktype.NodePluginScores))
}

func (h *nodeScoreHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

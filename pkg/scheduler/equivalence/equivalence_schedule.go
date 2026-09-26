/*
Copyright 2022 The Koordinator Authors.
Copyright 2014 The Kubernetes Authors.

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

package equivalence

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	schedulerframeworkext "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// This file holds local mirrors of upstream Kubernetes scheduler node selection, forked from
// k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go. Every mirrored function points at its
// upstream origin and lists each divergence, tagged by how much attention it needs when the k8s
// pin moves:
//
//	DIFF(upstream->local) [mechanical]  upstream statements are preserved; only the receiver, an
//	                                    accessor path, or a parameter's source changed. Verify by
//	                                    inspection after an upgrade.
//	DIFF(upstream->local) [behavioral]  statements were added, dropped, or reordered. Re-review by
//	                                    hand after an upgrade.
//
// Upstream copies that need no change at all live in upstream_primitives.go instead.

// These constants mirror upstream's package-level constants in schedule_one.go.
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

// EquivalenceScheduling reuses one full node-selection decision across the pods of an
// EquivalenceClass. It owns no workload semantics: the caller supplies the class, which decides
// which pods participate and what identifies their group.
type EquivalenceScheduling struct {
	sched              *scheduler.Scheduler
	class              schedulerframeworkext.EquivalenceClass
	equivalence        *equivalenceClassCache
	nextStartNodeIndex atomic.Int64
	// percentageOfNodesToScore mirrors upstream's unexported sched.percentageOfNodesToScore, which
	// numFeasibleNodesToFind reads as the fallback when the profile leaves its own setting nil.
	// Callers MUST pass the same value they gave scheduler.WithPercentageOfNodesToScore (see
	// cmd/koord-scheduler/app/server.go) — passing anything else silently forks this path's node
	// sampling semantics from the upstream path it mirrors. The copy only exists because the
	// upstream field is unexported.
	percentageOfNodesToScore int32
}

// equivalenceCacheKey namespaces the class key by scheduler profile: the same class scheduled under
// two profiles is two independent decisions, because profiles configure different plugins.
func equivalenceCacheKey(profileName, classKey string) string {
	return profileName + "/" + classKey
}

// NewEquivalenceScheduling returns an equivalence-class scheduling engine whose cache holds at most
// cacheCapacity class decisions. percentageOfNodesToScore falls back to the scheduler's adaptive
// heuristic when nil. class must return a non-empty Key for every pod it Handles.
func NewEquivalenceScheduling(sched *scheduler.Scheduler, percentageOfNodesToScore *int32, cacheCapacity int, class schedulerframeworkext.EquivalenceClass) *EquivalenceScheduling {
	s := &EquivalenceScheduling{
		sched:       sched,
		class:       class,
		equivalence: newEquivalenceClassCache(DefaultEquivalenceClassTTL, cacheCapacity),
	}
	if percentageOfNodesToScore != nil {
		s.percentageOfNodesToScore = *percentageOfNodesToScore
	}
	return s
}

func (s *EquivalenceScheduling) RegisterNodeEventHandler(informer toolscache.SharedIndexInformer) error {
	_, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    s.handleNodeAdd,
		UpdateFunc: s.handleNodeUpdate,
		DeleteFunc: s.handleNodeDelete,
	})
	return err
}

func (s *EquivalenceScheduling) handleNodeAdd(obj interface{}) {
	if _, ok := obj.(*corev1.Node); !ok {
		s.flushNodeEventCache()
		return
	}
	// A new node changes the candidate set and can change normalized scores for every class.
	// Skip the flush when the cache is already empty (e.g. the informer's initial sync delivers
	// one Add per existing node): flushing an empty cache is a no-op that would only inflate
	// the flush metric.
	if s.equivalence.len() == 0 {
		return
	}
	s.flushNodeEventCache()
}

func (s *EquivalenceScheduling) handleNodeUpdate(oldObj, newObj interface{}) {
	oldNode, oldOK := objAsNode(oldObj)
	newNode, newOK := objAsNode(newObj)
	if !oldOK || !newOK || oldNode.Name == "" || oldNode.Name != newNode.Name {
		s.flushNodeEventCache()
		return
	}
	// The upstream queue classifier reports uncordon but not cordon.
	if oldNode.Spec.Unschedulable != newNode.Spec.Unschedulable {
		s.removeNodeFromCache(newNode.Name)
		return
	}
	changes := framework.NodeSchedulingPropertiesChange(newNode, oldNode)
	if len(changes) == 0 {
		return
	}
	for _, change := range changes {
		if change.ActionType != fwktype.UpdateNodeAllocatable {
			s.removeNodeFromCache(newNode.Name)
			return
		}
	}

	// Unrequested resources do not affect resource-based quota. Cached score ordering
	// remains bounded by TTL and drift.
	changedResources := sets.New[corev1.ResourceName]()
	for name, oldQuantity := range oldNode.Status.Allocatable {
		newQuantity, exists := newNode.Status.Allocatable[name]
		if !exists || !oldQuantity.Equal(newQuantity) {
			changedResources.Insert(name)
		}
	}
	for name := range newNode.Status.Allocatable {
		if _, exists := oldNode.Status.Allocatable[name]; !exists {
			changedResources.Insert(name)
		}
	}
	s.equivalence.removeNode(newNode.Name, changedResources)
}

func (s *EquivalenceScheduling) handleNodeDelete(obj interface{}) {
	// DeletedFinalStateUnknown is deliberately handled conservatively: the tombstone may not carry
	// the latest node state needed to know which cached decisions are safe to retain.
	node, ok := objAsNode(obj)
	if !ok || node.Name == "" {
		s.flushNodeEventCache()
		return
	}
	s.removeNodeFromCache(node.Name)
}

// classKey returns the group identity of the pod's equivalence class.
func (s *EquivalenceScheduling) classKey(pod *corev1.Pod) string {
	return s.class.Key(pod)
}

func objAsNode(obj interface{}) (*corev1.Node, bool) {
	node, ok := obj.(*corev1.Node)
	return node, ok && node != nil
}

func (s *EquivalenceScheduling) flushNodeEventCache() {
	s.equivalence.flushNodeEvent()
	koordmetrics.RecordEquivalenceClassFlush(equivalenceCacheMissNodeEvent.String())
}

func (s *EquivalenceScheduling) removeNodeFromCache(nodeName string) {
	s.equivalence.removeNode(nodeName, nil)
}

var _ schedulerframeworkext.SchedulingDecisionProvider = &EquivalenceScheduling{}

// Handles implements frameworkext.SchedulingDecisionProvider: it reports whether the
// equivalence-class path owns the pod's node-selection decision.
func (s *EquivalenceScheduling) Handles(pod *corev1.Pod) bool {
	return s.class.Handles(pod)
}

// SchedulePod implements frameworkext.SchedulingDecisionProvider by delegating to the
// equivalence-class decision path. FrameworkExtenderFactory.scheduleOne invokes it in place of the
// upstream schedulePod for pods that Handles reports.
func (s *EquivalenceScheduling) SchedulePod(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod) (scheduler.ScheduleResult, error) {
	return s.decide(ctx, state, schedFramework, pod)
}

func (s *EquivalenceScheduling) flushEquivalenceCache(reason string) {
	s.equivalence.flush()
	koordmetrics.RecordEquivalenceClassFlush(reason)
}

type preFilterResult struct {
	result             *fwktype.PreFilterResult
	status             *fwktype.Status
	unscheduledPlugins sets.Set[string]
}

func (s *EquivalenceScheduling) runPreFilter(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod) preFilterResult {
	result, status, unscheduledPlugins := schedFramework.RunPreFilterPlugins(ctx, state, pod)
	return preFilterResult{
		result:             result,
		status:             status,
		unscheduledPlugins: unscheduledPlugins,
	}
}

// decide tries equivalence reuse before full node selection. The full path retains
// score-ordered candidates for quota backfill, which scheduler.SchedulePod's
// ScheduleResult does not expose.
func (s *EquivalenceScheduling) decide(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod) (result scheduler.ScheduleResult, err error) {
	start := time.Now()
	path := koordmetrics.SchedulingPathFull
	resultLabel := koordmetrics.SchedulingResultError
	defer func() {
		if err == nil {
			resultLabel = koordmetrics.SchedulingResultSuccess
		} else if _, ok := err.(*framework.FitError); ok {
			resultLabel = koordmetrics.SchedulingResultUnschedulable
		}
		koordmetrics.RecordEquivalenceClassSchedulingDuration(schedFramework.ProfileName(), path, resultLabel, time.Since(start))
	}()

	cacheKey := equivalenceCacheKey(schedFramework.ProfileName(), s.classKey(pod))
	snapshot, err := s.updateSnapshot(klog.FromContext(ctx), schedFramework)
	if err != nil {
		return scheduler.ScheduleResult{}, err
	}
	if snapshot.NumNodes() == 0 {
		return scheduler.ScheduleResult{}, scheduler.ErrNoNodesAvailable
	}

	preFilter := s.runPreFilter(ctx, state, schedFramework, pod)
	if !preFilter.status.IsSuccess() {
		koordmetrics.RecordEquivalenceClassMiss(schedFramework.ProfileName(), equivalenceCacheMissPreFilter.String())
		result, _, err = s.schedulePod(ctx, state, schedFramework, pod, snapshot, preFilter)
		return result, err
	}

	// A pod re-queued after preemption carries a NominatedNodeName pointing at the node freed for
	// it. The equivalence fast path does not consult NominatedNodeName, so it could place the pod
	// elsewhere and let a different pod grab the reserved room. Skip the fast path in that case and
	// let the full path try the nominated node first (see findNodesThatFitPod).
	nominated := len(pod.Status.NominatedNodeName) > 0
	var fastResult scheduler.ScheduleResult
	if nominated {
		koordmetrics.RecordEquivalenceClassMiss(schedFramework.ProfileName(), equivalenceCacheMissNominated.String())
	} else {
		var reason equivalenceCacheMissReason
		fastResult, reason = s.scheduleFromEquivalenceClass(ctx, state, schedFramework, pod, cacheKey, snapshot, preFilter)
		if fastResult.SuggestedHost != "" {
			path = koordmetrics.SchedulingPathFast
			koordmetrics.RecordEquivalenceClassHit(schedFramework.ProfileName())
			return fastResult, nil
		}
		koordmetrics.RecordEquivalenceClassMiss(schedFramework.ProfileName(), reason.String())
	}

	result, orderedNodes, err := s.schedulePod(ctx, state, schedFramework, pod, snapshot, preFilter)
	// Count evaluations in both paths, including nodes rechecked during fallback.
	result.EvaluatedNodes += fastResult.EvaluatedNodes
	if err == nil && !nominated {
		// The quota baselines reflect every occupant (running and assumed) at decision time.
		// The pod paying for this full path occupies one slot on the suggested host itself.
		cycle := s.sched.CurrentCycle()
		s.equivalence.store(cacheKey, buildQuotaNodesWithPlugins(ctx, state, pod, orderedNodes, schedFramework.SnapshotSharedLister(), s.equivalenceCapacityPlugins(schedFramework)), cycle, podRequestsForQuota(pod))
		s.equivalence.recordConsumption(cacheKey, result.SuggestedHost, cycle)
	}
	return result, err
}

func (s *EquivalenceScheduling) equivalenceCapacityPlugins(schedFramework framework.Framework) []schedulerframeworkext.EquivalenceCapacityPlugin {
	if extender, ok := schedFramework.(schedulerframeworkext.FrameworkExtender); ok {
		return extender.EquivalenceCapacityPlugins()
	}
	return nil
}

func (s *EquivalenceScheduling) updateSnapshot(logger klog.Logger, schedFramework framework.Framework) (*cache.Snapshot, error) {
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
// class: it takes the next cached node and validates it with a single-node framework Filter and
// extender Filter pass. PreFilter still runs per pod because plugins read its state from the cycle
// state (e.g. NodeResourcesFit's Filter reads the PreFilter-computed pod requests). An empty
// SuggestedHost and a miss reason tell the caller to fall back to full scheduling.
func (s *EquivalenceScheduling) scheduleFromEquivalenceClass(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod, cacheKey string, snapshot *cache.Snapshot, preFilter preFilterResult) (result scheduler.ScheduleResult, missReason equivalenceCacheMissReason) {
	var sawFilterRejected, sawSnapshotError bool
	cycle := s.sched.CurrentCycle()
	plugins := s.equivalenceCapacityPlugins(schedFramework)
	for {
		node, ok, reason := s.equivalence.next(cacheKey, cycle)
		if !ok {
			if sawFilterRejected && reason == equivalenceCacheMissQuotaExhausted {
				return result, equivalenceCacheMissFilterRejected
			}
			if sawSnapshotError && reason == equivalenceCacheMissQuotaExhausted {
				return result, equivalenceCacheMissSnapshotError
			}
			return result, reason
		}
		// Respect this pod's PreFilter node restriction: same-class pods should produce the
		// same PreFilter result, but the restriction is cheap to honor and keeps the reuse safe.
		if !preFilter.result.AllNodes() && !preFilter.result.NodeNames.Has(node) {
			sawFilterRejected = true
			s.equivalence.rejectNode(cacheKey, node, cycle)
			continue
		}
		nodeInfo, err := snapshot.NodeInfos().Get(node)
		if err != nil {
			// The node is gone from the snapshot; drop it and try the next candidate.
			sawSnapshotError = true
			s.equivalence.rejectNode(cacheKey, node, cycle)
			continue
		}
		result.EvaluatedNodes++
		filterStatus := schedFramework.RunFilterPluginsWithNominatedPods(ctx, state, pod, nodeInfo)
		if filterStatus.Code() == fwktype.Error {
			s.flushEquivalenceCache(equivalenceCacheMissFilterError.String())
			return result, equivalenceCacheMissFilterError
		}
		if !filterStatus.IsSuccess() {
			sawFilterRejected = true
			s.equivalence.rejectNode(cacheKey, node, cycle)
			continue
		}
		// Plugins may compute pod-specific capacity during Filter, including lazy restoration.
		quota, reusable := equivalencePluginCapacity(ctx, state, pod, nodeInfo, plugins)
		if !reusable {
			// Drop the entry so the next pod of this class does not waste a fast-path attempt
			// on a decision the plugin has already rejected.
			s.flushEquivalenceCache(equivalenceCacheMissPluginVeto.String())
			return result, equivalenceCacheMissPluginVeto
		}
		if quota <= 0 {
			s.equivalence.rejectNode(cacheKey, node, cycle)
			continue
		}
		extenderNodes, err := findNodesThatPassExtenders(
			ctx,
			s.sched.Extenders,
			pod,
			[]fwktype.NodeInfo{nodeInfo},
			framework.NewDefaultNodeToStatus(),
		)
		if err != nil {
			s.flushEquivalenceCache(equivalenceCacheMissExtenderError.String())
			return result, equivalenceCacheMissExtenderError
		}
		if len(extenderNodes) == 0 {
			sawFilterRejected = true
			s.equivalence.rejectNode(cacheKey, node, cycle)
			continue
		}
		// The cached node passed all framework and extender filters.
		result.SuggestedHost = node
		result.FeasibleNodes = 1
		return result, ""
	}
}

// advanceNodeIndex moves the node scan cursor forward by delta, wrapping at nodeCount.
//
// Upstream: no equivalent — k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go assigns
// sched.nextStartNodeIndex directly. This path makes the cursor atomic and CAS-updates it because
// several pods can advance it concurrently here.
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

// schedulePod runs the full node-selection cycle for one pod: filter every node, score the
// survivors, and return the best host together with the complete score-ordered candidate list
// that quota backfill consumes.
//
// Upstream: k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go:421 (Scheduler.schedulePod)
// DIFF(upstream->local) [behavioral]: drops both fwk.StoreScheduleResults calls and their
// OpportunisticBatching gate — the equivalence-class cache replaces the per-node hint, so the
// upstream hint is never written.
// DIFF(upstream->local) [behavioral]: replaces newSortedNodeScores/Pop with a sort.Slice over the
// whole priority list, because quota backfill needs every candidate, not just the winner.
// DIFF(upstream->local) [behavioral]: drops the utiltrace instrumentation.
// DIFF(upstream->local) [mechanical]: returns 3 values — the extra []string is the ordered
// candidates — and takes the refreshed snapshot and PreFilter result from decide instead of
// computing them here. Upstream Run/ScheduleOne still own the lifecycle.
func (s *EquivalenceScheduling) schedulePod(ctx context.Context, state fwktype.CycleState, schedFramework framework.Framework, pod *corev1.Pod, snapshot *cache.Snapshot, preFilter preFilterResult) (scheduler.ScheduleResult, []string, error) {
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
	// DIFF: backfill needs every candidate, not the upstream heap's lazy Pop interface.
	sort.Slice(priorityList, func(i, j int) bool {
		return priorityList[i].TotalScore > priorityList[j].TotalScore ||
			(priorityList[i].TotalScore == priorityList[j].TotalScore && priorityList[i].Randomizer > priorityList[j].Randomizer)
	})
	orderedNodes := make([]string, len(priorityList))
	for i := range priorityList {
		orderedNodes[i] = priorityList[i].Name
	}

	return scheduler.ScheduleResult{
		SuggestedHost:  orderedNodes[0],
		EvaluatedNodes: len(feasibleNodes) + diagnosis.NodeToStatus.Len(),
		FeasibleNodes:  len(feasibleNodes),
	}, orderedNodes, nil
}

// findNodesThatFitPod returns the nodes that survive the filter plugins and the extender filters.
//
// Upstream: k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go:482 (Scheduler.findNodesThatFitPod)
// DIFF(upstream->local) [behavioral]: equivalence-class reuse replaces the opportunistic-batching
// node hint; nominated nodes still take precedence.
// DIFF(upstream->local) [behavioral]: PreFilter runs once in decide and its result is reused by
// both the equivalence-class fast path and this full fallback, so it arrives as a parameter
// instead of being invoked here.
// DIFF(upstream->local) [mechanical]: returns 3 values instead of 5 — nodeHint and PodSignature
// are gone along with the hint mechanism.
// DIFF(upstream->local) [mechanical]: takes the refreshed snapshot as a parameter instead of
// reading sched.nodeInfoSnapshot.
// DIFF(upstream->local) [mechanical]: reads s.nextStartNodeIndex instead of sched.nextStartNodeIndex.
func (s *EquivalenceScheduling) findNodesThatFitPod(ctx context.Context, schedFramework framework.Framework, state fwktype.CycleState, pod *corev1.Pod, snapshot *cache.Snapshot, preFilter preFilterResult) ([]fwktype.NodeInfo, framework.Diagnosis, error) {
	logger := klog.FromContext(ctx)
	diagnosis := framework.Diagnosis{
		NodeToStatus: framework.NewDefaultNodeToStatus(),
	}

	allNodes, err := snapshot.NodeInfos().List()
	if err != nil {
		return nil, diagnosis, err
	}
	preRes := preFilter.result
	status := preFilter.status
	diagnosis.UnschedulablePlugins = preFilter.unscheduledPlugins
	if !status.IsSuccess() {
		if !status.IsRejected() {
			return nil, diagnosis, status.AsError()
		}
		diagnosis.NodeToStatus.SetAbsentNodesStatus(status)
		msg := status.Message()
		diagnosis.PreFilterMsg = msg
		logger.V(5).Info("Status after running PreFilter plugins for pod", "pod", klog.KObj(pod), "status", msg)
		diagnosis.AddPluginStatus(status)
		return nil, diagnosis, nil
	}

	if len(pod.Status.NominatedNodeName) > 0 {
		feasibleNodes, err := s.evaluateNominatedNode(ctx, pod, schedFramework, state, snapshot, diagnosis)
		if err != nil {
			utilruntime.HandleErrorWithContext(ctx, err, "Evaluation failed on nominated node", "pod", klog.KObj(pod), "node", pod.Status.NominatedNodeName)
		}
		if len(feasibleNodes) != 0 {
			return feasibleNodes, diagnosis, nil
		}
	}

	nodes := allNodes
	if !preRes.AllNodes() {
		nodes = make([]fwktype.NodeInfo, 0, len(preRes.NodeNames))
		for nodeName := range preRes.NodeNames {
			if nodeInfo, err := snapshot.Get(nodeName); err == nil {
				nodes = append(nodes, nodeInfo)
			}
		}
		diagnosis.NodeToStatus.SetAbsentNodesStatus(fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, fmt.Sprintf("node(s) didn't satisfy plugin(s) %v", sets.List(preFilter.unscheduledPlugins))))
	}
	feasibleNodes, err := s.findNodesThatPassFilters(ctx, schedFramework, state, pod, &diagnosis, nodes)
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
		if diagnosis.UnschedulablePlugins == nil {
			diagnosis.UnschedulablePlugins = sets.New[string]()
		}
		diagnosis.UnschedulablePlugins.Insert(framework.ExtenderName)
	}

	return feasibleNodesAfterExtender, diagnosis, nil
}

// evaluateNominatedNode tries the node nominated for this pod before scanning the cluster.
//
// Upstream: k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go:575 (Scheduler.evaluateNominatedNode)
// DIFF(upstream->local) [behavioral]: drops the nodeHint fallback along with the parameter —
// upstream reads `if len(nnn) == 0 { nnn = nodeHint }`, so here only pod.Status.NominatedNodeName
// is consulted. The hint came from the opportunistic-batching mechanism this path replaces.
// DIFF(upstream->local) [mechanical]: takes the snapshot as a parameter instead of reading
// sched.nodeInfoSnapshot.
// DIFF(upstream->local) [mechanical]: calls this type's findNodesThatPassFilters.
func (s *EquivalenceScheduling) evaluateNominatedNode(
	ctx context.Context,
	pod *corev1.Pod,
	schedFramework framework.Framework,
	state fwktype.CycleState,
	snapshot *cache.Snapshot,
	diagnosis framework.Diagnosis,
) ([]fwktype.NodeInfo, error) {
	nnn := pod.Status.NominatedNodeName

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

// findNodesThatPassFilters returns the nodes that pass the filter plugins.
//
// Upstream: k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go:625 (Scheduler.findNodesThatPassFilters)
// DIFF(upstream->local) [behavioral]: adds a numAllNodes == 0 early return. A PreFilter restriction
// can name only nodes that are absent from the snapshot, and upstream's index arithmetic would
// then divide by zero.
// DIFF(upstream->local) [mechanical]: reads the atomic s.nextStartNodeIndex cursor instead of
// sched.nextStartNodeIndex.
func (s *EquivalenceScheduling) findNodesThatPassFilters(
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
	if !hasExtenderFilters(s.sched) && !hasScoring(s.sched, schedFramework) {
		numNodesToFind = 1
	}

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
		metrics.FrameworkExtensionPointDuration.WithLabelValues(metrics.Filter, statusCode.String(), schedFramework.ProfileName()).Observe(metrics.SinceInSeconds(beginCheckNode))
	}()

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

// numFeasibleNodesToFind returns the number of feasible nodes at which the scheduler stops
// searching for more.
//
// Upstream: k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go:712 (Scheduler.numFeasibleNodesToFind)
// DIFF(upstream->local) [mechanical]: statements are preserved, but the nil-fallback line reads
// s.percentageOfNodesToScore instead of upstream's sched.percentageOfNodesToScore. That upstream
// field is unexported, so this function cannot take *scheduler.Scheduler and stay body-identical
// the way hasScoring/hasExtenderFilters in upstream_primitives.go do.
func (s *EquivalenceScheduling) numFeasibleNodesToFind(percentageOfNodesToScore *int32, numAllNodes int32) (numNodes int32) {
	if numAllNodes < minFeasibleNodesToFind {
		return numAllNodes
	}

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

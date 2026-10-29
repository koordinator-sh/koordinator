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

package equivalence

import (
	"context"
	"errors"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulerframeworkext "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// This file implements equivalence-class reuse on top of three extension points the scheduler
// framework already owns, so no upstream node-selection code is forked:
//
//   - Reuse runs at the end of frameworkext's PreFilter and narrows the cycle to one node whose
//     cached decision is still valid. The upstream scheduler then filters that node and, because a
//     single node is feasible, returns it without scoring.
//   - RecordScoreTable runs at the end of frameworkext's RunScorePlugins and captures the weighted
//     scores of a full cycle. Upstream reorders the returned slice in place afterwards, so the
//     recorder copies it.
//   - RecordResult runs at the end of frameworkext's scheduleOne, after reservation nomination and
//     resizing, and backfills the class with the captured table.

// PlacementPolicy selects how pods use the cached score-ordered nodes.
type PlacementPolicy string

const (
	// PlacementSpread rotates through the cached nodes, using the anchor as a fallback.
	PlacementSpread PlacementPolicy = "spread"
	// PlacementPack prefers filling the anchor while the cache is valid. Drift
	// (consumed >= 2*len(nodes)) and TTL can trigger a full-path recomputation before it fills.
	PlacementPack PlacementPolicy = "pack"
)

// reuseStateKey carries the per-pod reuse context across the three extension points above.
const reuseStateKey fwktype.StateKey = "koordinator.sh/equivalence-reuse-state"

// reuseState is what one scheduling cycle tells the reuse machinery: whether the pod may reuse or
// backfill its class decision, the proposed node, and the score table of a full cycle.
type reuseState struct {
	eligible bool
	proposal string
	scores   []fwktype.NodePluginScores
}

func (s *reuseState) Clone() fwktype.StateData {
	ns := *s
	return &ns
}

// readReuseState returns the reuse context of this cycle, or nil when the narrowing never ran for
// the pod, e.g. because PreFilter already restricted the node set or the pod carries a nominated
// node. A pod without a context neither reuses nor backfills, which is what upstream would do.
func readReuseState(state fwktype.CycleState) *reuseState {
	if state == nil {
		return nil
	}
	data, err := state.Read(reuseStateKey)
	if err != nil {
		return nil
	}
	rs, ok := data.(*reuseState)
	if !ok {
		return nil
	}
	return rs
}

// EquivalenceScheduling reuses one full node-selection decision across the pods of an
// EquivalenceClass. It owns no workload semantics: the caller supplies the class, which decides
// which pods participate and what identifies their group.
type EquivalenceScheduling struct {
	sched       *scheduler.Scheduler
	class       schedulerframeworkext.EquivalenceClass
	equivalence *equivalenceClassCache
	placement   PlacementPolicy
}

var (
	_ schedulerframeworkext.ReuseProvider = &EquivalenceScheduling{}
	_ schedulerframeworkext.ReuseRecorder = &EquivalenceScheduling{}
)

// equivalenceCacheKey namespaces the class key by scheduler profile: the same class scheduled under
// two profiles is two independent decisions, because profiles configure different plugins.
func equivalenceCacheKey(profileName, classKey string) string {
	return profileName + "/" + classKey
}

// NewEquivalenceScheduling returns an equivalence-class scheduling engine whose cache holds at most
// cacheCapacity class decisions and whose fast path selects nodes using placement. class must
// return a non-empty Key for every pod it Handles.
func NewEquivalenceScheduling(sched *scheduler.Scheduler, cacheCapacity int, placement PlacementPolicy, class schedulerframeworkext.EquivalenceClass) *EquivalenceScheduling {
	return &EquivalenceScheduling{
		sched:       sched,
		class:       class,
		equivalence: newEquivalenceClassCache(DefaultEquivalenceClassTTL, cacheCapacity),
		placement:   placement,
	}
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
	koordmetrics.RecordEquivalenceClassFlush()
}

func (s *EquivalenceScheduling) removeNodeFromCache(nodeName string) {
	s.equivalence.removeNode(nodeName, nil)
}

// Handles implements both frameworkext.ReuseProvider and frameworkext.ReuseRecorder: it reports
// whether the pod belongs to a class this engine caches decisions for.
func (s *EquivalenceScheduling) Handles(pod *corev1.Pod) bool {
	return s.class.Handles(pod)
}

// Reuse returns a node from the equivalence class cache so the cycle can skip full node selection.
// It runs at the end of PreFilter, once the cycle state is fully populated and the class decision
// can be consulted.
//
// Candidates are tried in the cached score order. The cache only records where the class landed
// last time, not whether this pod still fits there: occupants, taints and reservations change
// between two pods of a class. So every candidate is checked with this pod's own Filter.
//
// That check runs on a copy of the cycle state, because the Filter path mutates the state it is
// given: Reservation's BeforeFilter calls RunPreFilterExtensionAddPod, which subtracts the
// nominated pods' requests. Upstream re-runs Filter on the real state for the winning node, so
// without the copy those requests would be subtracted twice.
func (s *EquivalenceScheduling) Reuse(ctx context.Context, state fwktype.CycleState, fwk framework.Framework, pod *corev1.Pod) string {
	profile := fwk.ProfileName()
	rs := &reuseState{}
	state.Write(reuseStateKey, rs)

	classKey := s.classKey(pod)
	if classKey == "" {
		koordmetrics.RecordEquivalenceClassMiss(profile, equivalenceCacheMissUnknownClass.String())
		return ""
	}
	cacheKey := equivalenceCacheKey(profile, classKey)

	// An ineligible pod neither reuses nor backfills the class decision.
	if eligible, reason := s.checkEligibility(ctx, state, fwk, pod, cacheKey); !eligible {
		koordmetrics.RecordEquivalenceClassMiss(profile, reason.String())
		return ""
	}
	rs.eligible = true

	cycle := s.sched.CurrentCycle()
	lister := fwk.SnapshotSharedLister()
	// Every round tries a node this pod has not tried yet, so the loop ends within len(nodes)
	// rounds whatever happens to the cached quotas.
	tried := sets.New[string]()
	var sawFilterRejected, sawSnapshotError bool
	for {
		node, reason := s.equivalence.candidate(cacheKey, cycle, tried)
		if node == "" {
			switch {
			case sawFilterRejected && reason == equivalenceCacheMissQuotaExhausted:
				reason = equivalenceCacheMissFilterRejected
			case sawSnapshotError && reason == equivalenceCacheMissQuotaExhausted:
				reason = equivalenceCacheMissSnapshotError
			}
			koordmetrics.RecordEquivalenceClassMiss(profile, reason.String())
			return ""
		}
		tried.Insert(node)

		nodeInfo, err := lister.NodeInfos().Get(node)
		if err != nil {
			// The node is gone from the snapshot; drop it and try the next candidate.
			sawSnapshotError = true
			s.equivalence.rejectNode(cacheKey, node, cycle)
			continue
		}
		status := fwk.RunFilterPluginsWithNominatedPods(ctx, state.Clone(), pod, nodeInfo)
		if status.Code() == fwktype.Error {
			s.equivalence.remove(cacheKey)
			koordmetrics.RecordEquivalenceClassMiss(profile, equivalenceCacheMissFilterError.String())
			return ""
		}
		if !status.IsSuccess() {
			sawFilterRejected = true
			s.equivalence.rejectNode(cacheKey, node, cycle)
			continue
		}

		// Upstream may prefer a native node hint, or reject this proposal in its second Filter.
		rs.proposal = node
		return node
	}
}

// checkEligibility asks the eligibility plugins whether the pod may reuse or backfill its class
// decision, following the status semantics of frameworkext.EquivalenceEligibilityPlugin: a veto
// invalidates the cached decision of the pod's class only, while a plugin error keeps just this pod
// off the cache. It returns the miss reason when the pod is not eligible.
func (s *EquivalenceScheduling) checkEligibility(ctx context.Context, state fwktype.CycleState, fwk framework.Framework, pod *corev1.Pod, cacheKey string) (bool, equivalenceCacheMissReason) {
	handle, ok := fwk.(schedulerframeworkext.EquivalenceAwareHandle)
	if !ok {
		return true, ""
	}
	logger := klog.FromContext(ctx)
	for _, plugin := range handle.EquivalenceEligibilityPlugins() {
		status := plugin.EquivalenceEligible(ctx, state, pod)
		switch {
		case status.IsSuccess() || status.IsSkip():
			continue
		case status.IsRejected():
			// Log the transition from reusable to vetoed once; per-pod vetoes are visible in the
			// miss metric and would flood the log at sandbox scheduling rates.
			if s.equivalence.remove(cacheKey) {
				logger.Info("Equivalence class decision invalidated by plugin", "pod", klog.KObj(pod), "class", cacheKey, "plugin", plugin.Name(), "reason", status.Message())
			} else {
				logger.V(4).Info("Pod is not eligible for equivalence reuse", "pod", klog.KObj(pod), "class", cacheKey, "plugin", plugin.Name(), "reason", status.Message())
			}
			return false, equivalenceCacheMissPluginVeto
		default:
			logger.Error(status.AsError(), "Failed to check equivalence eligibility", "pod", klog.KObj(pod), "class", cacheKey, "plugin", plugin.Name())
			return false, equivalenceCacheMissEligibilityError
		}
	}
	return true, ""
}

// RecordScoreTable keeps the weighted scores of this cycle for the backfill that follows it. The
// caller passes a copy because the upstream scheduler reorders the returned slice in place when it
// picks a winner.
func (s *EquivalenceScheduling) RecordScoreTable(state fwktype.CycleState, pod *corev1.Pod, scores []fwktype.NodePluginScores) {
	rs := readReuseState(state)
	if rs == nil || !rs.eligible {
		return
	}
	rs.scores = scores
}

// RecordResult accounts for the terminal node-selection outcome after nomination and resizing.
// A reuse proposal consumes quota only on success, against the host upstream actually selected.
func (s *EquivalenceScheduling) RecordResult(ctx context.Context, state fwktype.CycleState, fwk framework.Framework, pod *corev1.Pod, host string, err error, duration time.Duration) {
	rs := readReuseState(state)
	profile := fwk.ProfileName()
	path := koordmetrics.SchedulingPathFull
	if rs != nil && rs.proposal != "" && (err != nil || rs.proposal == host) {
		path = koordmetrics.SchedulingPathFast
	}
	result := koordmetrics.SchedulingResultSuccess
	if err != nil {
		result = koordmetrics.SchedulingResultError
		var fitError *framework.FitError
		if errors.As(err, &fitError) {
			result = koordmetrics.SchedulingResultUnschedulable
		}
	}
	koordmetrics.RecordEquivalenceClassSchedulingDuration(profile, path, result, duration)
	if err != nil || host == "" || rs == nil || !rs.eligible {
		return
	}
	cacheKey := equivalenceCacheKey(profile, s.classKey(pod))
	cycle := s.sched.CurrentCycle()

	if rs.proposal != "" {
		// Native hints can bypass PreFilter narrowing. Keep the cached table, but charge only
		// the final host; a different host is not an equivalence hit.
		s.equivalence.consume(cacheKey, host, cycle)
		if host == rs.proposal {
			koordmetrics.RecordEquivalenceClassHit(profile)
		}
		return
	}

	// Rank the captured table the way upstream ranks it without extenders: by total score, with ties
	// broken by the randomizer. Ties are resolved arbitrarily either way, and every pod re-validates
	// its node with its own Filter, so this order only decides which candidates are tried first.
	ordered := rs.scores
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].TotalScore != ordered[j].TotalScore {
			return ordered[i].TotalScore > ordered[j].TotalScore
		}
		return ordered[i].Randomizer > ordered[j].Randomizer
	})
	nodes := make([]string, 0, len(ordered)+1)
	for i := range ordered {
		nodes = append(nodes, ordered[i].Name)
	}
	if len(nodes) == 0 {
		// No score table: upstream returns before scoring when a single node is feasible, or the
		// profile configures no score plugins, or a native hint succeeds. Backfill the chosen node.
		nodes = append(nodes, host)
	}

	quotaNodes := buildQuotaNodes(pod, nodes, fwk.SnapshotSharedLister())
	if len(quotaNodes) == 0 {
		// Distinct from a cold cache: the cycle ran but no candidate fit the pod's request.
		koordmetrics.RecordEquivalenceClassMiss(profile, equivalenceCacheMissNotBackfilled.String())
	}
	s.equivalence.store(cacheKey, quotaNodes, cycle, podRequestsForQuota(pod), s.placement)
	// The pod paying for this cycle occupies one slot on the host itself.
	s.equivalence.consume(cacheKey, host, cycle)
}

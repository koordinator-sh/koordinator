package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	apipod "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	schedulerutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	ReasonAlreadyPreempted                 = "preemption already attempted by %s with message %s"
	ReasonNoPendingPods                    = "no pending pods"
	ReasonPreemptionPolicyNever            = "not eligible due to preemptionPolicy=Never."
	ReasonTerminatingVictimOnNominatedNode = "not eligible due to terminating pod on the nominated node."
	ReasonNoPotentialVictims               = "no potential victims"
	ReasonListNode                         = "list nodes from snapshot err"
	ReasonSelectVictimsOnNode              = "select victims on node"
	ReasonNoNodesAvailable                 = "no nodes available"
	ReasonTriggerPodPreemptSuccess         = "preempt success, alreadyWaitingForBound: %d/%d"
)

const (
	OperationRemovePossibleVictims = "RemovePossibleVictims"
	OperationFindFeasibleNodes     = "FindFeasibleNodes"
	OperationSelectVictimsOnNode   = "SelectVictimsOnNode"
	OperationSetNominatedNode      = "SetNominatedNode"
	OperationPreemptPod            = "PreemptPod"
	OperationClearNominatedNode    = "ClearNominatedNode"
)

// IsEligiblePodFunc is a function which may be assigned to the DefaultPreemption plugin.
// This may implement rules/filtering around preemption eligibility, which is in addition to
// the internal requirement that the victim pod have lower priority than the preemptor pod.
// Any customizations should always allow system services to preempt normal pods, to avoid
// problems if system pods are unable to find space.
type IsEligiblePodFunc func(nodeInfo *framework.NodeInfo, victim *framework.PodInfo, preemptor *corev1.Pod) bool

type PreemptionEvaluator interface {
	Preempt(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, m framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status)
}

type preemptionEvaluatorImpl struct {
	// IsEligiblePod returns whether a victim pod is allowed to be preempted by a preemptor pod.
	// This filtering is in addition to the internal requirement that the victim pod have lower
	// priority than the preemptor pod. Any customizations should always allow system services
	// to preempt normal pods, to avoid problems if system pods are unable to find space.
	IsEligiblePod IsEligiblePodFunc

	handle            frameworkext.ExtendedHandle
	gangCache         *GangCache
	gangContextHolder *GangSchedulingContextHolder
}

func NewPreemptionEvaluator(handle framework.Handle, gangCache *GangCache, gangContextHolder *GangSchedulingContextHolder) PreemptionEvaluator {
	if handle == nil {
		return nil
	}
	return &preemptionEvaluatorImpl{
		IsEligiblePod: func(nodeInfo *framework.NodeInfo, victim *framework.PodInfo, preemptor *corev1.Pod) bool {
			return extension.IsPodPreemptible(victim.Pod) && !extension.IsPodNonPreemptible(victim.Pod)
		},
		handle:            handle.(frameworkext.ExtendedHandle),
		gangCache:         gangCache,
		gangContextHolder: gangContextHolder,
	}
}

type JobPreemptionStateContextKey struct {
}

type JobPreemptionState struct {
	triggerPodKey string
	preemptorKey  string

	allPendingPods []*corev1.Pod
	allWaitingPods []*corev1.Pod
	allPods        []*corev1.Pod

	failedMessage                 string
	reason                        string
	terminatingPodOnNominatedNode map[string]string
	durationOfNodeInfoClone       time.Duration
	durationOfCycleStateClone     time.Duration
	possibleVictims               map[string][]*framework.PodInfo
	podToNominatedNode            map[string]string
	statusMap                     framework.NodeToStatusMap
	unschedulablePods             []*corev1.Pod
	selectVictimError             error
	victims                       map[string][]*corev1.Pod
	clearNominatedNodeFailedMsg   map[string]string
}

func preemptionStateFromContext(ctx context.Context) *JobPreemptionState {
	jobPreemptionDiagnosis := ctx.Value(JobPreemptionStateContextKey{}).(*JobPreemptionState)
	return jobPreemptionDiagnosis
}

func contextWithJobPreemptionState(ctx context.Context, preemptionState *JobPreemptionState) context.Context {
	ctx = context.WithValue(ctx, JobPreemptionStateContextKey{}, preemptionState)
	return ctx
}

// Preempt returns a PostFilterResult carrying suggested nominatedNodeName, along with a Status.
// The semantics of returned <PostFilterResult, Status> varies on different scenarios:
//
//   - <nil, Error>. This denotes it's a transient/rare error that may be self-healed in future cycles.
//
//   - <nil, Unschedulable>. This status is mostly as expected like the preemptor is waiting for the
//     victims to be fully terminated.
//
//   - In both cases above, a nil PostFilterResult is returned to keep the pod's nominatedNodeName unchanged.
//
//   - <non-nil PostFilterResult, Unschedulable>. It indicates the pod cannot be scheduled even with preemption.
//     In this case, a non-nil PostFilterResult is returned and result.NominatingMode instructs how to deal with
//     the nominatedNodeName.
//
//   - <non-nil PostFilterResult, Success>. It's the regular happy path
//     and the non-empty nominatedNodeName will be applied to the preemptor pod.

func (ev *preemptionEvaluatorImpl) Preempt(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, m framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	preemptionState := &JobPreemptionState{
		terminatingPodOnNominatedNode: map[string]string{},
		clearNominatedNodeFailedMsg:   map[string]string{},
	}
	defer func() {
		// TODO log preemptionState?
	}()
	return ev.preempt(contextWithJobPreemptionState(ctx, preemptionState), state, pod, m)
}

func (ev *preemptionEvaluatorImpl) preempt(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, m framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	preemptionState := preemptionStateFromContext(ctx)
	triggerPodKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
	preemptionState.triggerPodKey = triggerPodKey
	gangContext := ev.gangContextHolder.getCurrentGangSchedulingContext()
	if gangContext != nil {
		if gangContext.preemptionMessage != "" {
			preemptionState.reason = gangContext.preemptionMessage
			return nil, framework.NewStatus(framework.Unschedulable, gangContext.preemptionMessage)
		}
		defer func() {
			gangContext.preemptionMessage = fmt.Sprintf(ReasonAlreadyPreempted, triggerPodKey, preemptionState.reason)
		}()
		preemptionState.preemptorKey = gangContext.gangGroupID
		preemptionState.allPendingPods = ev.gangCache.getPendingPods(gangContext.gangGroup.UnsortedList())
		preemptionState.allPods = append(preemptionState.allPods, preemptionState.allPendingPods...)
		preemptionState.allWaitingPods = ev.gangCache.getWaitingPods(gangContext.gangGroup.UnsortedList())
		if len(preemptionState.allWaitingPods) > 0 {
			preemptionState.allPods = append(preemptionState.allPods, preemptionState.allWaitingPods...)
		}
	} else {
		preemptionState.preemptorKey = preemptionState.triggerPodKey
		preemptionState.allPendingPods = []*corev1.Pod{pod}
		preemptionState.allPods = []*corev1.Pod{pod}
	}

	if len(preemptionState.allPendingPods) == 0 {
		preemptionState.reason = ReasonNoPendingPods
		return nil, framework.NewStatus(framework.Unschedulable, ReasonNoPendingPods)
	}

	if ok, msg := ev.jobEligibleToPreemptOthers(ctx, pod, preemptionState.allPendingPods, m); !ok {
		preemptionState.reason = msg
		return nil, framework.NewStatus(framework.Unschedulable, msg)
	}

	allNodes, err := ev.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		preemptionState.reason = ReasonListNode
		return nil, framework.AsStatus(err)
	}
	if len(allNodes) == 0 {
		preemptionState.reason = ReasonNoNodesAvailable
		return nil, framework.AsStatus(errors.New(ReasonNoNodesAvailable))
	}

	var allPendingPodUIDs []string
	for _, pendingPod := range preemptionState.allPendingPods {
		allPendingPodUIDs = append(allPendingPodUIDs, string(pendingPod.UID))
	}
	frameworkext.MakeNominatedPodsOfTheSameJob(state, allPendingPodUIDs)

	podToNominatedNode, candidates, nodeToStatusMap, err := ev.findCandidates(ctx, state, allNodes, pod, m)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	// Return a FitError only when there are no candidates that fit the pod.
	if len(podToNominatedNode) != len(preemptionState.allPendingPods) {
		fitError := &framework.FitError{Pod: pod, NumAllNodes: len(allNodes), Diagnosis: framework.Diagnosis{NodeToStatusMap: nodeToStatusMap}}
		preemptionState.reason = fitError.Error()
		ev.cancelNomination(ctx)
		return framework.NewPostFilterResultWithNominatedNode(""), framework.NewStatus(framework.Unschedulable, preemptionState.reason)
	}

	if status := ev.prepareCandidates(ctx, candidates, pod); !status.IsSuccess() {
		preemptionState.reason = status.Message()
		return nil, status
	}
	preemptionState.reason = fmt.Sprintf(ReasonTriggerPodPreemptSuccess, len(preemptionState.allWaitingPods), len(preemptionState.allPods))
	ev.makeNomination(ctx, podToNominatedNode)
	return framework.NewPostFilterResultWithNominatedNode(podToNominatedNode[triggerPodKey]), framework.NewStatus(framework.Success)
}

// jobEligibleToPreemptOthers returns one bool and one string. The bool
// indicates whether this pod should be considered for preempting other pods or
// not. The string includes the reason if this pod isn't eligible.
// There are several reasons:
//  1. The pod has a preemptionPolicy of Never.
//  2. The pod has already preempted other pods and the victims are in their graceful termination period.
//     Currently, we check the node that is nominated for this pod, and as long as there are
//     terminating pods on this node, we don't attempt to preempt more pods.
func (ev *preemptionEvaluatorImpl) jobEligibleToPreemptOthers(ctx context.Context, triggerPod *corev1.Pod, allPendingPods []*corev1.Pod, m framework.NodeToStatusMap) (bool, string) {
	if triggerPod.Spec.PreemptionPolicy != nil && *triggerPod.Spec.PreemptionPolicy == corev1.PreemptNever {
		// FIXME all pods of the same job must have the same preemptionPolicy
		return false, ReasonPreemptionPolicyNever
	}

	anyTerminatingPodOnNominatedNode := false
	for _, pod := range allPendingPods {
		if eligible, _ := ev.podEligibleToPreemptOthers(ctx, pod, m); !eligible {
			anyTerminatingPodOnNominatedNode = true
		}
	}
	if anyTerminatingPodOnNominatedNode {
		return false, ReasonTerminatingVictimOnNominatedNode
	}
	return true, ""
}

func (ev *preemptionEvaluatorImpl) podEligibleToPreemptOthers(ctx context.Context, pod *corev1.Pod, m framework.NodeToStatusMap) (bool, string) {
	jobPreemptionState := preemptionStateFromContext(ctx)
	nomNodeName := pod.Status.NominatedNodeName
	if len(nomNodeName) > 0 {
		if jobPreemptionState.triggerPodKey == framework.GetNamespacedName(pod.Namespace, pod.Name) {
			nominatedNodeStatus := m[nomNodeName]
			// If the pod's nominated node is considered as UnschedulableAndUnresolvable by the filters,
			// then the pod should be considered for preempting again.
			if nominatedNodeStatus.Code() == framework.UnschedulableAndUnresolvable {
				return true, ""
			}
		}
		nodeInfos := ev.handle.SnapshotSharedLister().NodeInfos()
		if nodeInfo, _ := nodeInfos.Get(nomNodeName); nodeInfo != nil {
			for _, p := range nodeInfo.Pods {
				if ev.isPreemptionAllowed(nodeInfo, p, pod) && podTerminatingByPreemption(p.Pod) {
					terminatingPodKey := framework.GetNamespacedName(p.Pod.Namespace, p.Pod.Name)
					jobPreemptionState.terminatingPodOnNominatedNode[terminatingPodKey] = nomNodeName
					// There is a terminating pod on the nominated node.
					return false, ReasonTerminatingVictimOnNominatedNode
				}
			}
		}
	}
	return true, ""
}

// isPreemptionAllowed returns whether the victim residing on nodeInfo can be preempted by the preemptor
func (ev *preemptionEvaluatorImpl) isPreemptionAllowed(nodeInfo *framework.NodeInfo, victim *framework.PodInfo, preemptor *corev1.Pod) bool {
	// The victim must have lower priority than the preemptor, in addition to any filtering implemented by IsEligiblePod
	return corev1helpers.PodPriority(victim.Pod) < corev1helpers.PodPriority(preemptor) && ev.IsEligiblePod(nodeInfo, victim, preemptor)
}

// podTerminatingByPreemption returns true if the pod is in the termination state caused by scheduler preemption.
func podTerminatingByPreemption(p *corev1.Pod) bool {
	if p.DeletionTimestamp == nil {
		return false
	}

	for _, condition := range p.Status.Conditions {
		if condition.Type == corev1.DisruptionTarget {
			return condition.Status == corev1.ConditionTrue && condition.Reason == corev1.PodReasonPreemptionByScheduler
		}
	}
	return false
}

// FindCandidates calculates a slice of preemption candidates.
// Each candidate is executable to make the given <pod> schedulable.
func (ev *preemptionEvaluatorImpl) findCandidates(
	ctx context.Context,
	state *framework.CycleState,
	allNodes []*framework.NodeInfo,
	pod *corev1.Pod,
	m framework.NodeToStatusMap,
) (map[string]string, map[string][]*corev1.Pod, framework.NodeToStatusMap, error) {
	preemptionState := preemptionStateFromContext(ctx)

	startTime := time.Now()
	potentialNodes, unschedulableNodeStatus := nodesWherePreemptionMightHelp(allNodes, m)
	if len(potentialNodes) == 0 {
		return nil, nil, unschedulableNodeStatus, nil
	}
	preemptionState.durationOfNodeInfoClone = time.Since(startTime)
	startTime = time.Now()
	nodeLevelCycleState := make(map[string]*framework.CycleState, len(potentialNodes))
	for _, node := range potentialNodes {
		nodeLevelCycleState[node.Node().Name] = state.Clone()
	}
	preemptionState.durationOfCycleStateClone = time.Since(startTime)
	return ev.dryRunPreemption(ctx, pod, nodeLevelCycleState, potentialNodes)
}

// nodesWherePreemptionMightHelp returns a list of nodes with failed predicates
// that may be satisfied by removing pods from the node.
func nodesWherePreemptionMightHelp(nodes []*framework.NodeInfo, m framework.NodeToStatusMap) ([]*framework.NodeInfo, framework.NodeToStatusMap) {
	var potentialNodes []*framework.NodeInfo
	nodeStatuses := make(framework.NodeToStatusMap)
	unresolvableStatus := framework.NewStatus(framework.UnschedulableAndUnresolvable, "Preemption is not helpful for scheduling")
	for _, node := range nodes {
		nodeName := node.Node().Name
		// We only attempt preemption on nodes with status 'Unschedulable'. For
		// diagnostic purposes, we propagate UnschedulableAndUnresolvable if either
		// implied by absence in map or explicitly set.
		status, ok := m[nodeName]
		if status.Code() == framework.Unschedulable {
			// clone nodeInfo to avoid modifying the nodeInfoSnapshot
			potentialNodes = append(potentialNodes, node.Clone())
		} else if !ok || status.Code() == framework.UnschedulableAndUnresolvable {
			nodeStatuses[nodeName] = unresolvableStatus
		}
	}
	return potentialNodes, nodeStatuses
}

type podFunc = func(state *framework.CycleState, pod *corev1.Pod, podInfo *framework.PodInfo, nodeInfo *framework.NodeInfo) error

// TODO consider PDB violation

// dryRunPreemption simulates Preemption logic on <potentialNodes> in parallel,
// returns preemption candidates and a map indicating filtered nodes statuses.
func (ev *preemptionEvaluatorImpl) dryRunPreemption(
	ctx context.Context,
	triggerPod *corev1.Pod,
	cycleStates map[string]*framework.CycleState,
	potentialNodes []*framework.NodeInfo,
) (map[string]string, map[string][]*corev1.Pod, framework.NodeToStatusMap, error) {
	preemptionState := preemptionStateFromContext(ctx)

	removePod := func(state *framework.CycleState, toSchedulePod *corev1.Pod, rpi *framework.PodInfo, nodeInfo *framework.NodeInfo) error {
		if err := nodeInfo.RemovePod(rpi.Pod); err != nil {
			return err
		}
		status := ev.handle.RunPreFilterExtensionRemovePod(ctx, state, toSchedulePod, rpi, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}
	potentialVictims, statusMap := ev.removePossibleVictims(triggerPod, cycleStates, potentialNodes, removePod)
	if len(potentialVictims) == 0 {
		return nil, nil, statusMap, nil
	}
	preemptionState.possibleVictims = potentialVictims

	preemptionCosts := estimatePreemptionCost(potentialVictims)
	addPod := func(state *framework.CycleState, toSchedulePod *corev1.Pod, api *framework.PodInfo, nodeInfo *framework.NodeInfo) error {
		nodeInfo.AddPodInfo(api)
		status := ev.handle.RunPreFilterExtensionAddPod(ctx, state, triggerPod, api, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}
	pendingPods := preemptionState.allPendingPods
	podToNominatedNode, successPods, unschedulablePods := ev.placeToSchedulePods(ctx, pendingPods, cycleStates, potentialNodes, preemptionCosts, addPod, statusMap)
	preemptionState.podToNominatedNode = podToNominatedNode
	preemptionState.unschedulablePods = unschedulablePods
	preemptionState.statusMap = statusMap

	if len(unschedulablePods) > 0 {
		return nil, nil, statusMap, nil
	}

	victims, err := ev.selectVictims(ctx, potentialVictims, cycleStates, successPods, addPod, removePod)
	if err != nil {
		preemptionState.reason = ReasonSelectVictimsOnNode
		preemptionState.selectVictimError = err
		return nil, nil, nil, err
	}
	preemptionState.victims = victims
	return podToNominatedNode, victims, nil, nil
}

func (ev *preemptionEvaluatorImpl) removePossibleVictims(
	triggerPod *corev1.Pod,
	cycleStates map[string]*framework.CycleState,
	potentialNodes []*framework.NodeInfo,
	removePod podFunc,
) (map[string][]*framework.PodInfo, framework.NodeToStatusMap) {
	potentialVictims := make(map[string][]*framework.PodInfo, len(potentialNodes))
	victimLock := sync.Mutex{}
	statusMap := framework.NodeToStatusMap{}
	statusLock := sync.Mutex{}
	processNode := func(i int) {
		nodeInfo := potentialNodes[i]
		nodeName := nodeInfo.Node().Name
		cycleState := cycleStates[nodeName]
		var potentialVictimsOnNode []*framework.PodInfo
		for _, podInfo := range nodeInfo.Pods {
			if ev.isPreemptionAllowed(nodeInfo, podInfo, triggerPod) {
				potentialVictimsOnNode = append(potentialVictimsOnNode, podInfo)
				if err := removePod(cycleState, triggerPod, podInfo, nodeInfo); err != nil {
					statusLock.Lock()
					statusMap[nodeName] = framework.AsStatus(err)
					statusLock.Unlock()
				}
			}
		}
		if len(potentialVictimsOnNode) > 0 {
			victimLock.Lock()
			potentialVictims[nodeName] = potentialVictimsOnNode
			victimLock.Unlock()
		} else {
			statusLock.Lock()
			statusMap[nodeName] = framework.NewStatus(framework.UnschedulableAndUnresolvable, ReasonNoPotentialVictims)
			statusLock.Unlock()
		}
	}
	ev.handle.Parallelizer().Until(context.Background(), len(potentialNodes), processNode, OperationRemovePossibleVictims)
	return potentialVictims, statusMap
}

func estimatePreemptionCost(possibleVictims map[string][]*framework.PodInfo) map[string]int {
	allPrioritySets := sets.NewInt()
	for _, victims := range possibleVictims {
		for _, victim := range victims {
			priority := corev1helpers.PodPriority(victim.Pod)
			allPrioritySets.Insert(int(priority))
		}
	}
	sortedPriorities := allPrioritySets.List()
	sort.Ints(sortedPriorities)
	priorityCosts := make(map[int]int)
	for index, priority := range sortedPriorities {
		priorityCosts[priority] = index
	}
	result := make(map[string]int)
	for nodeName, victims := range possibleVictims {
		cost := 0
		jobs := sets.NewString()
		for _, victim := range victims {
			// estimate preemption cost in the job dimension
			jobId := extension.GetExplanationKey(victim.Pod.Labels)
			if !jobs.Has(jobId) {
				pri := corev1helpers.PodPriority(victim.Pod)
				cost += priorityCosts[int(pri)]
				jobs.Insert(jobId)
			}
		}
		result[nodeName] = cost
	}
	return result
}

type Placements struct {
	nodeName string
	nodeInfo *framework.NodeInfo
	pods     []*corev1.Pod
}

func (ev *preemptionEvaluatorImpl) placeToSchedulePods(
	ctx context.Context,
	toSchedulePods []*corev1.Pod,
	cycleStates map[string]*framework.CycleState,
	potentialNodes []*framework.NodeInfo,
	preemptionCosts map[string]int,
	addPod podFunc,
	statusMap framework.NodeToStatusMap,
) (podToNominatedNode map[string]string, successPods map[string]*Placements, unschedulablePods []*corev1.Pod) {
	podToNominatedNode = make(map[string]string, len(toSchedulePods))
	successPods = make(map[string]*Placements)
	feasibleNodes := potentialNodes
	assumedNodeInfos := make(map[string]*framework.NodeInfo)
	assumedCycleStates := make(map[string]*framework.CycleState)
	for i := range toSchedulePods {
		pod := toSchedulePods[i]
		feasibleNodes = ev.findFeasibleNodes(ctx, pod, cycleStates, feasibleNodes, assumedCycleStates, assumedNodeInfos, statusMap)
		if len(feasibleNodes) == 0 {
			unschedulablePods = toSchedulePods[i:]
			break
		}
		selectedNode := feasibleNodes[0]
		minCost := preemptionCosts[selectedNode.Node().Name]
		for _, node := range feasibleNodes {
			cost := preemptionCosts[node.Node().Name]
			if cost < minCost {
				selectedNode = node
				minCost = cost
			}
		}
		if i+1 < len(toSchedulePods) {
			podToSchedule := toSchedulePods[i+1]
			assumedPod := pod.DeepCopy()
			assumedPod.Spec.NodeName = selectedNode.Node().Name
			podInfoToAdd, _ := framework.NewPodInfo(assumedPod)
			assumedCycleState := assumedCycleStates[selectedNode.Node().Name]
			if assumedCycleState == nil {
				assumedCycleState = cycleStates[selectedNode.Node().Name].Clone()
			}
			assumedNodeInfo := assumedNodeInfos[selectedNode.Node().Name]
			if assumedNodeInfo == nil {
				assumedNodeInfo = selectedNode.Clone()
			}
			// TODO consider pod assume on reservation
			err := addPod(assumedCycleState, podToSchedule, podInfoToAdd, assumedNodeInfo)
			if err != nil {
				unschedulablePods = toSchedulePods[i:]
				statusMap[selectedNode.Node().Name] = framework.AsStatus(err)
				break
			}
		}
		successPodsOnNode := successPods[selectedNode.Node().Name]
		if successPodsOnNode == nil {
			successPodsOnNode = &Placements{
				nodeInfo: selectedNode,
				nodeName: selectedNode.Node().Name,
			}
			successPods[selectedNode.Node().Name] = successPodsOnNode
		}
		successPodsOnNode.pods = append(successPodsOnNode.pods, pod)
		podToNominatedNode[framework.GetNamespacedName(pod.Namespace, pod.Name)] = selectedNode.Node().Name
	}
	return
}

func (ev *preemptionEvaluatorImpl) findFeasibleNodes(
	ctx context.Context,
	toSchedulePod *corev1.Pod,
	cycleStates map[string]*framework.CycleState,
	potentialNodes []*framework.NodeInfo,
	assumedCycleStates map[string]*framework.CycleState,
	assumedNodeInfos map[string]*framework.NodeInfo,
	statusMap framework.NodeToStatusMap,
) (feasibleNodes []*framework.NodeInfo) {
	var statusesLock sync.Mutex
	feasibleNodes = make([]*framework.NodeInfo, len(potentialNodes))
	var feasibleNodesLen int32
	checkNode := func(i int) {
		nodeInfo := potentialNodes[i]
		if assumedNodeInfo := assumedNodeInfos[nodeInfo.Node().Name]; assumedNodeInfo != nil {
			nodeInfo = assumedNodeInfos[nodeInfo.Node().Name]
		}
		cycleState := cycleStates[nodeInfo.Node().Name]
		if assumedCycleState := assumedCycleStates[nodeInfo.Node().Name]; assumedCycleState != nil {
			cycleState = assumedCycleState
		}
		status := ev.handle.RunFilterPluginsWithNominatedPods(ctx, cycleState, toSchedulePod, nodeInfo)
		if status.IsSuccess() {
			length := atomic.AddInt32(&feasibleNodesLen, 1)
			feasibleNodes[length-1] = nodeInfo
		} else {
			statusesLock.Lock()
			statusMap[nodeInfo.Node().Name] = status
			statusesLock.Unlock()
		}
	}
	ev.handle.Parallelizer().Until(ctx, len(potentialNodes), checkNode, OperationFindFeasibleNodes)
	feasibleNodes = feasibleNodes[:feasibleNodesLen]
	sort.Slice(feasibleNodes, func(i, j int) bool { return feasibleNodes[i].Node().Name < feasibleNodes[j].Node().Name })
	return feasibleNodes
}

func (ev *preemptionEvaluatorImpl) selectVictims(
	ctx context.Context,
	possibleVictims map[string][]*framework.PodInfo,
	cycleStates map[string]*framework.CycleState,
	successPods map[string]*Placements,
	addPod podFunc,
	removePod podFunc,
) (victims map[string][]*corev1.Pod, err error) {
	reprievePod := func(state *framework.CycleState, pods []*corev1.Pod, pi *framework.PodInfo, nodeInfo *framework.NodeInfo) (bool, error) {
		if err := addPod(state, pods[0], pi, nodeInfo); err != nil {
			return false, err
		}
		assumedNodeInfo := nodeInfo.Clone()
		assumedCycleState := state.Clone()
		for i := range pods {
			pod := pods[i]
			status := ev.handle.RunFilterPluginsWithNominatedPods(ctx, assumedCycleState, pod, assumedNodeInfo)
			fits := status.IsSuccess()
			if !fits {
				if err := removePod(state, pods[0], pi, nodeInfo); err != nil {
					return false, err
				}
				return false, nil
			}
			if i+1 < len(pods) {
				assumedPod := pod.DeepCopy()
				assumedPod.Spec.NodeName = assumedNodeInfo.Node().Name
				podInfoToAdd, _ := framework.NewPodInfo(assumedPod)
				toSchedulePod := pods[i+1]
				if err := addPod(assumedCycleState, toSchedulePod, podInfoToAdd, assumedNodeInfo); err != nil {
					return false, err
				}
			}
		}
		return true, nil
	}
	var nominatedNodes []string
	for s := range successPods {
		nominatedNodes = append(nominatedNodes, s)
	}
	victimLock := sync.Mutex{}
	victims = make(map[string][]*corev1.Pod, len(nominatedNodes))
	var errs []error
	selectVictimsOnNode := func(i int) {
		nodeName := nominatedNodes[i]
		possibleVictimsOnNode := possibleVictims[nodeName]
		sortVictims(possibleVictimsOnNode)

		placements := successPods[nodeName]
		pods := placements.pods
		nodeInfo := placements.nodeInfo
		cycleState := cycleStates[nodeName]

		for _, pi := range possibleVictimsOnNode {
			fits, err := reprievePod(cycleState, pods, pi, nodeInfo)
			if err != nil {
				victimLock.Lock()
				errs = append(errs, err)
				victimLock.Unlock()
				break
			} else if !fits {
				victimLock.Lock()
				victims[nodeName] = append(victims[nodeName], pi.Pod)
				victimLock.Unlock()
			}
		}
	}
	ev.handle.Parallelizer().Until(ctx, len(nominatedNodes), selectVictimsOnNode, OperationSelectVictimsOnNode)
	return victims, utilerrors.NewAggregate(errs)
}

func sortVictims(victims []*framework.PodInfo) {
	sort.Slice(victims, func(i, j int) bool {
		pod1 := victims[i].Pod
		pod2 := victims[j].Pod
		p1 := corev1helpers.PodPriority(pod1)
		p2 := corev1helpers.PodPriority(pod2)
		if p1 != p2 {
			return p1 > p2
		}
		jobId1 := extension.GetExplanationKey(pod1.Labels)
		jobId2 := extension.GetExplanationKey(pod2.Labels)
		if jobId1 != jobId2 {
			return jobId1 < jobId2
		}
		return pod1.Name < pod2.Name
	})
}

func (ev *preemptionEvaluatorImpl) prepareCandidates(ctx context.Context, candidatesByNode map[string][]*corev1.Pod, triggerPod *corev1.Pod) *framework.Status {
	var candidates []*corev1.Pod
	for _, pods := range candidatesByNode {
		for i := range pods {
			candidates = append(candidates, pods[i])
		}
	}
	preemptionState := preemptionStateFromContext(ctx)

	cs := ev.handle.ClientSet()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logger := klog.FromContext(ctx)
	errCh := parallelize.NewErrorChannel()
	ev.handle.Parallelizer().Until(ctx, len(candidates), func(i int) {
		victimPod := candidates[i]
		if victimPod.DeletionTimestamp != nil {
			// If the victim Pod is already being deleted, we don't have to make another deletion api call.
			logger.V(2).Info("Victim Pod is already deleted, skipping the API call for it", "triggerPod", preemptionState.triggerPodKey, "preemptor", preemptionState.preemptorKey, "node", victimPod.Spec.NodeName, "victim", klog.KObj(victimPod))
			return
		}

		if err := ev.preemptPod(ctx, triggerPod, victimPod, frameworkext.JobRejectPlugin); err != nil && !apierrors.IsNotFound(err) {
			errCh.SendErrorWithCancel(err, cancel)
		}
	}, OperationPreemptPod)
	if err := errCh.ReceiveError(); err != nil {
		return framework.AsStatus(err)
	}

	metrics.PreemptionVictims.Observe(float64(len(candidates)))

	// Lower priority pods nominated to run on this node, may no longer fit on
	// this node. So, we should remove their nomination. Removing their
	// nomination updates these pods and moves them to the active queue. It
	// lets scheduler find another place for them.
	nominatedPods := ev.getLowerPriorityNominatedPods(triggerPod, candidatesByNode)
	if err := ev.clearNominatedNodeName(ctx, cs, nominatedPods...); err != nil {
		logger.V(5).Error(err, "Cannot clear 'NominatedNodeName' field")
		// We do not return as this error is not critical.
	}

	return nil
}

func (ev *preemptionEvaluatorImpl) preemptPod(ctx context.Context, preemptor, victim *corev1.Pod, pluginName string) error {
	logger := klog.FromContext(ctx)
	preemptionState := preemptionStateFromContext(ctx)

	// If the victim is a WaitingPod, send a reject message to the PermitPlugin.
	// Otherwise, we should delete the victim.
	if waitingPod := ev.handle.GetWaitingPod(victim.UID); waitingPod != nil {
		waitingPod.Reject(pluginName, "preempted")
		logger.V(2).Info("Preemptor pod rejected a waiting pod", "triggerPod", preemptionState.triggerPodKey, "preemptor", preemptionState.preemptorKey, "waitingPod", klog.KObj(victim), "node", victim.Spec.NodeName)
	} else {
		condition := &corev1.PodCondition{
			Type:    corev1.DisruptionTarget,
			Status:  corev1.ConditionTrue,
			Reason:  corev1.PodReasonPreemptionByScheduler,
			Message: fmt.Sprintf("%s: preempting to accommodate higher priority pods, preemptor: %s, triggerPod: %s", preemptor.Spec.SchedulerName, preemptionState.preemptorKey, preemptionState.triggerPodKey),
		}
		newStatus := victim.Status.DeepCopy()
		updated := apipod.UpdatePodCondition(newStatus, condition)
		if updated {
			if err := schedulerutil.PatchPodStatus(ctx, ev.handle.ClientSet(), victim, newStatus); err != nil {
				logger.Error(err, "Could not add DisruptionTarget condition due to preemption", "pod", klog.KObj(victim), "triggerPod", preemptionState.triggerPodKey, "preemptor", preemptionState.preemptorKey)
				return err
			}
		}
		if err := schedulerutil.DeletePod(ctx, ev.handle.ClientSet(), victim); err != nil {
			if apierrors.IsNotFound(err) {
				logger.V(2).Info("Victim Pod is already deleted", "triggerPod", preemptionState.triggerPodKey, "preemptor", preemptionState.preemptorKey, "victim", klog.KObj(victim), "node", victim.Spec.NodeName)
			} else {
				logger.Error(err, "Tried to preempted pod", "pod", klog.KObj(victim), "triggerPod", preemptionState.triggerPodKey, "preemptor", preemptionState.preemptorKey)
			}
			return err
		}
		logger.V(2).Info("Preemptor Pod preempted victim Pod", "triggerPod", preemptionState.triggerPodKey, "preemptor", preemptionState.preemptorKey, "victim", klog.KObj(victim), "node", victim.Spec.NodeName)
	}

	ev.handle.EventRecorder().Eventf(victim, preemptor, corev1.EventTypeNormal, "Preempted", "Preempting", "Preempted by pod %v on node %v", preemptor.UID, victim.Spec.NodeName)
	return nil
}

// getLowerPriorityNominatedPods returns pods whose priority is smaller than the
// priority of the given "pod" and are nominated to run on the given node.
// Note: We could possibly check if the nominated lower priority pods still fit
// and return those that no longer fit, but that would require lots of
// manipulation of NodeInfo and PreFilter state per nominated pod. It may not be
// worth the complexity, especially because we generally expect to have a very
// small number of nominated pods per node.
func (ev *preemptionEvaluatorImpl) getLowerPriorityNominatedPods(triggerPod *corev1.Pod, candidatesByNode map[string][]*corev1.Pod) []*corev1.Pod {
	var lowerPriorityPods []*corev1.Pod
	for nodeName := range candidatesByNode {
		podInfos := ev.handle.NominatedPodsForNode(nodeName)
		if len(podInfos) == 0 {
			return nil
		}
		podPriority := corev1helpers.PodPriority(triggerPod)
		for _, pi := range podInfos {
			if corev1helpers.PodPriority(pi.Pod) < podPriority {
				lowerPriorityPods = append(lowerPriorityPods, pi.Pod)
			}
		}
	}
	return lowerPriorityPods
}

// clearNominatedNodeName internally submit a patch request to API server
// to set each pods[*].Status.NominatedNodeName> to "".
func (ev *preemptionEvaluatorImpl) clearNominatedNodeName(ctx context.Context, cs clientset.Interface, pods ...*corev1.Pod) utilerrors.Aggregate {
	var errs []error
	var errLock sync.Mutex
	ev.handle.Parallelizer().Until(ctx, len(pods), func(i int) {
		pod := pods[i]
		if len(pod.Status.NominatedNodeName) == 0 {
			return
		}
		podStatusCopy := pod.Status.DeepCopy()
		podStatusCopy.NominatedNodeName = ""
		if err := schedulerutil.PatchPodStatus(ctx, cs, pod, podStatusCopy); err != nil {
			errLock.Lock()
			errs = append(errs, err)
			errLock.Unlock()
		}
	}, OperationClearNominatedNode)
	return utilerrors.NewAggregate(errs)
}

func (ev *preemptionEvaluatorImpl) makeNomination(ctx context.Context, podToNominatedNode map[string]string) {
	preemptionState := preemptionStateFromContext(ctx)
	waitingPodToNominatedNode := makeWaitingPodToNominatedNode(preemptionState.allWaitingPods)
	ev.rejectAllWaitingPod(ctx, preemptionState.allWaitingPods, frameworkext.JobPreemptionSuccessPlugin, preemptionState.failedMessage)
	ev.setAllNominatedNode(ctx, preemptionState.allPods, podToNominatedNode, waitingPodToNominatedNode)
}

func makeWaitingPodToNominatedNode(allWaitingPods []*corev1.Pod) map[string]string {
	if len(allWaitingPods) == 0 {
		return nil
	}
	podNominatedNodes := make(map[string]string, len(allWaitingPods))
	for _, pod := range allWaitingPods {
		podNominatedNodes[framework.GetNamespacedName(pod.Namespace, pod.Name)] = pod.Spec.NodeName
	}
	return podNominatedNodes
}

func (ev *preemptionEvaluatorImpl) cancelNomination(ctx context.Context) {
	preemptionState := preemptionStateFromContext(ctx)
	ev.rejectAllWaitingPod(ctx, preemptionState.allWaitingPods, frameworkext.JobPreemptionFailurePlugin, preemptionState.failedMessage)
	ev.setAllNominatedNode(ctx, preemptionState.allPods, nil, nil)
}

func (ev *preemptionEvaluatorImpl) rejectAllWaitingPod(ctx context.Context, allWaitingPods []*corev1.Pod, pluginName, msg string) {
	for _, pod := range allWaitingPods {
		if waitingPod := ev.handle.GetWaitingPod(pod.UID); waitingPod != nil {
			waitingPod.Reject(pluginName, msg)
		}
	}
}

func (ev *preemptionEvaluatorImpl) setAllNominatedNode(ctx context.Context, allPods []*corev1.Pod, podNominatedNodes, waitingPodToNominatedNode map[string]string) {
	logger := klog.FromContext(ctx)
	jobPreemptionDiagnosis := preemptionStateFromContext(ctx)
	client := ev.handle.ClientSet()
	patchStatusLock := sync.Mutex{}
	ev.handle.Parallelizer().Until(ctx, len(allPods), func(i int) {
		pod := allPods[i]
		podKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
		nominatedNode := podNominatedNodes[podKey]
		if nominatedNode == "" {
			nominatedNode = waitingPodToNominatedNode[podKey]
		}
		if nominatedNode == "" {
			ev.handle.DeleteNominatedPodIfExists(pod)
			if extendedHandle, ok := ev.handle.(frameworkext.FrameworkExtender); ok {
				nominator := extendedHandle.GetReservationNominator()
				if nominator != nil {
					if reservation.IsReservePod(pod) {
						nominator.DeleteNominatedReservePod(pod)
					} else {
						nominator.RemoveNominatedReservations(pod)
					}
				}
			}
		} else {
			ev.handle.AddNominatedPod(logger, &framework.PodInfo{Pod: pod},
				&framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: nominatedNode})
			// TODO nominated reservationRelated
		}
		if nominatedNode == pod.Status.NominatedNodeName {
			return
		}
		podStatusCopy := pod.Status.DeepCopy()
		podStatusCopy.NominatedNodeName = nominatedNode
		err := schedulerutil.PatchPodStatus(ctx, client, pod, podStatusCopy)
		if err != nil {
			patchStatusLock.Lock()
			jobPreemptionDiagnosis.clearNominatedNodeFailedMsg[podKey] = err.Error()
			patchStatusLock.Unlock()
		}
	}, OperationSetNominatedNode)
}

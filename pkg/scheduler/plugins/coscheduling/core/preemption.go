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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	schedulermetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

const (
	ReasonAlreadyPreempted                 = "preemption already attempted by %s with message %s"
	ReasonNoPendingPods                    = "no pending pods"
	ReasonPreemptionPolicyNever            = "not eligible due to preemptionPolicy=Never."
	ReasonTerminatingVictimOnNominatedNode = "not eligible due to terminating pod on the nominated node."
	ReasonListNode                         = "list nodes from snapshot err"
	ReasonNoNodesAvailable                 = "no nodes available"
	ReasonNoPotentialVictims               = "no potential victims"
	ReasonPreemptionNotHelpful             = "preemption not helpful"
	ReasonSelectVictimsOnNodeError         = "select victims on node error"
	ReasonPrepareCandidatesError           = "prepare candidates error"
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

	handle                frameworkext.ExtendedHandle
	gangCache             *GangCache
	gangContextHolder     *GangSchedulingContextHolder
	networkTopologySolver NetworkTopologySolver
}

func NewPreemptionEvaluator(handle framework.Handle, gangCache *GangCache, gangContextHolder *GangSchedulingContextHolder, networkTopologySolver NetworkTopologySolver) PreemptionEvaluator {
	if handle == nil {
		return nil
	}
	return &preemptionEvaluatorImpl{
		IsEligiblePod: func(nodeInfo *framework.NodeInfo, victim *framework.PodInfo, preemptor *corev1.Pod) bool {
			return extension.IsPodPreemptible(victim.Pod) && !extension.IsPodNonPreemptible(victim.Pod)
		},
		handle:                handle.(frameworkext.ExtendedHandle),
		gangCache:             gangCache,
		gangContextHolder:     gangContextHolder,
		networkTopologySolver: networkTopologySolver,
	}
}

type JobPreemptionStateContextKey struct {
}

type JobPreemptionState struct {
	TriggerPodKey string `json:"TriggerPodKey,omitempty"`
	PreemptorKey  string `json:"preemptorKey,omitempty"`

	gangSchedulingContext *GangSchedulingContext

	allPendingPods []*corev1.Pod
	allWaitingPods []*corev1.Pod
	allPods        []*corev1.Pod

	Reason                          string            `json:"reason,omitempty"`
	Message                         string            `json:"message,omitempty"`
	TerminatingPodOnNominatedNode   map[string]string `json:"terminatingPodOnNominatedNode,omitempty"`
	DurationOfNodeInfoClone         metav1.Duration   `json:"durationOfNodeInfoClone,omitempty"`
	DurationOfCycleStateClone       metav1.Duration   `json:"durationOfCycleStateClone,omitempty"`
	possibleVictims                 map[string][]*framework.PodInfo
	DurationOfRemovePossibleVictims metav1.Duration   `json:"durationOfRemovePossibleVictims,omitempty"`
	PodToNominatedNode              map[string]string `json:"podToNominatedNode,omitempty"`
	DurationOfPlaceToSchedulePods   metav1.Duration   `json:"durationOfPlaceToSchedulePods,omitempty"`
	statusMap                       framework.NodeToStatusMap
	unschedulablePods               []*corev1.Pod
	selectVictimError               error
	DurationOfSelectVictimsOnNode   metav1.Duration `json:"durationOfSelectVictimsOnNode,omitempty"`
	victims                         map[string][]*corev1.Pod
	DurationOfPrepareCandidates     metav1.Duration   `json:"durationOfPrepareCandidates,omitempty"`
	ClearNominatedNodeFailedMsg     map[string]string `json:"clearNominatedNodeFailedMsg,omitempty"`
	DurationOfMakeNomination        metav1.Duration   `json:"durationOfMakeNomination,omitempty"`
	DurationOfCancelNomination      metav1.Duration   `json:"durationOfCancelNomination,omitempty"`

	SchedulingMode  frameworkext.SchedulingMode
	NodeToOfferSlot map[string]int

	PossibleVictims         []v1alpha1.NodePossibleVictim `json:"possibleVictims,omitempty"`
	UnschedulablePodsNumber int                           `json:"unschedulablePodsNumber,omitempty"`
	NodeFailedDetail        []v1alpha1.NodeFailedDetail   `json:"nodeFailedDetail,omitempty"`
	SelectVictimError       string                        `json:"selectVictimError,omitempty"`
	Victims                 []v1alpha1.NodePossibleVictim `json:"victims,omitempty"`
}

func (s *JobPreemptionState) addMoreDetailForStateToMarshal() {
	s.UnschedulablePodsNumber = len(s.unschedulablePods)
	if s.selectVictimError != nil {
		s.SelectVictimError = s.selectVictimError.Error()
	}
	for node, victimsOnNode := range s.victims {
		nodePossibleVictims := v1alpha1.NodePossibleVictim{NodeName: node}
		for _, pod := range victimsOnNode {
			nodePossibleVictims.PossibleVictims = append(nodePossibleVictims.PossibleVictims, v1alpha1.PossibleVictim{
				NamespacedName: v1alpha1.NamespacedName{
					Name:      pod.Name,
					Namespace: pod.Namespace,
					UID:       string(pod.UID),
				},
			})
		}
		sort.Slice(nodePossibleVictims.PossibleVictims, func(i, j int) bool {
			return nodePossibleVictims.PossibleVictims[i].NamespacedName.Name < nodePossibleVictims.PossibleVictims[j].NamespacedName.Name
		})
		s.Victims = append(s.Victims, nodePossibleVictims)
	}
	sort.Slice(s.Victims, func(i, j int) bool { return s.Victims[i].NodeName < s.Victims[j].NodeName })
	if klog.V(5).Enabled() {
		for node, status := range s.statusMap {
			nodeFailedDetail := v1alpha1.NodeFailedDetail{
				NodeName:         node,
				FailedPlugin:     status.FailedPlugin(),
				Reason:           status.Message(),
				PreemptMightHelp: false,
			}
			s.NodeFailedDetail = append(s.NodeFailedDetail, nodeFailedDetail)
		}
		sort.Slice(s.NodeFailedDetail, func(i, j int) bool { return s.NodeFailedDetail[i].NodeName < s.NodeFailedDetail[j].NodeName })
	}
	if klog.V(6).Enabled() {
		for node, victimsOnNode := range s.possibleVictims {
			nodePossibleVictims := v1alpha1.NodePossibleVictim{NodeName: node}
			for _, pod := range victimsOnNode {
				nodePossibleVictims.PossibleVictims = append(nodePossibleVictims.PossibleVictims, v1alpha1.PossibleVictim{
					NamespacedName: v1alpha1.NamespacedName{
						Name:      pod.Pod.Name,
						Namespace: pod.Pod.Namespace,
						UID:       string(pod.Pod.UID),
					},
				})
			}
			sort.Slice(nodePossibleVictims.PossibleVictims, func(i, j int) bool {
				return nodePossibleVictims.PossibleVictims[i].NamespacedName.Name < nodePossibleVictims.PossibleVictims[j].NamespacedName.Name
			})
			s.PossibleVictims = append(s.PossibleVictims, nodePossibleVictims)
		}
	}
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
		TerminatingPodOnNominatedNode: map[string]string{},
		ClearNominatedNodeFailedMsg:   map[string]string{},
	}
	startTime := time.Now()
	defer func() {
		metrics.PreemptionAttempts.Inc()
		schedulermetrics.RecordJobPreemptionDuration(preemptionState.PreemptorKey, preemptionState.Reason, time.Since(startTime))
	}()
	defer func() {
		preemptionState.addMoreDetailForStateToMarshal()
		scheduleDiagnosis := frameworkext.GetDiagnosis(state)
		scheduleDiagnosis.PreemptionDiagnosis = preemptionState
	}()
	return ev.preempt(contextWithJobPreemptionState(ctx, preemptionState), state, pod, m)
}

// preempt implements the core preemption logic to help a high-priority pod (preemptor)
// find space by evicting lower-priority pods (victims) when no node can schedule it.
//
// This method performs the following steps:
// 1. Checks eligibility for preemption, considering PreemptionPolicy and ongoing terminations.
// 2. For gang-scheduled workloads, retrieves all associated pending/waiting pods in the same job.
// 3. Filters nodes where preemption may help (nodes with Unschedulable status).
// 4. Simulates removal of potential victims on candidate nodes ("dry-run").
// 5. Evaluates if scheduling becomes feasible after victim removal.
// 6. Selects optimal victims per node based on priority, job grouping, and cost estimation.
// 7. Triggers eviction of selected victims and nominates nodes for every member of the preemptor.
//
// The behavior differs slightly between regular and gang scheduling modes:
// - In regular mode: only the given pod is considered.
// - In gang mode: all pending pods in the same gang are evaluated together to maintain co-scheduling guarantees.
//
// Side effects include:
// - Updating NominatedNodeName for the preemptor and related pods.
// - Clearing nominations for lower-priority pods that may no longer fit.
// - Sending reject signals to waiting pods via Permit plugins.
func (ev *preemptionEvaluatorImpl) preempt(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, m framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	preemptionState := preemptionStateFromContext(ctx)
	triggerPodKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
	preemptionState.TriggerPodKey = triggerPodKey
	gangContext := ev.gangContextHolder.getCurrentGangSchedulingContext()
	if gangContext != nil {
		preemptionState.gangSchedulingContext = gangContext
		if gangContext.preemptionMessage != "" {
			preemptionState.Reason = ReasonAlreadyPreempted
			preemptionState.Message = gangContext.preemptionMessage
			return nil, framework.NewStatus(framework.Unschedulable, gangContext.preemptionMessage)
		}
		defer func() {
			if preemptionState.Message == "" {
				preemptionState.Message = preemptionState.Reason
			}
			gangContext.preemptionMessage = fmt.Sprintf(ReasonAlreadyPreempted, triggerPodKey, preemptionState.Message)
		}()
		preemptionState.PreemptorKey = gangContext.gangGroupID
		preemptionState.allPendingPods = ev.gangCache.getPendingPods(gangContext.gangGroup.UnsortedList())
		preemptionState.allPods = append(preemptionState.allPods, preemptionState.allPendingPods...)
		preemptionState.allWaitingPods = ev.gangCache.getWaitingPods(gangContext.gangGroup.UnsortedList())
		if len(preemptionState.allWaitingPods) > 0 {
			preemptionState.allPods = append(preemptionState.allPods, preemptionState.allWaitingPods...)
		}
	} else {
		preemptionState.PreemptorKey = preemptionState.TriggerPodKey
		preemptionState.allPendingPods = []*corev1.Pod{pod}
		preemptionState.allPods = []*corev1.Pod{pod}
	}

	if len(preemptionState.allPendingPods) == 0 {
		preemptionState.Reason = ReasonNoPendingPods
		return nil, framework.NewStatus(framework.Unschedulable, ReasonNoPendingPods)
	}

	diagnosis := frameworkext.GetDiagnosis(state)
	if diagnosis.ScheduleDiagnosis != nil && diagnosis.ScheduleDiagnosis.SchedulingMode == frameworkext.JobSchedulingMode {
		m = diagnosis.ScheduleDiagnosis.NodeToStatusMap
	}

	if ok, msg := ev.jobEligibleToPreemptOthers(ctx, pod, preemptionState.allPendingPods, m); !ok {
		preemptionState.Reason = msg
		return nil, framework.NewStatus(framework.Unschedulable, msg)
	}

	allNodes, err := ev.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		preemptionState.Reason = ReasonListNode
		return nil, framework.AsStatus(err)
	}
	if len(allNodes) == 0 {
		preemptionState.Reason = ReasonNoNodesAvailable
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
		if preemptionState.Message == "" {
			fitError := &framework.FitError{Pod: pod, NumAllNodes: len(allNodes), Diagnosis: framework.Diagnosis{NodeToStatusMap: nodeToStatusMap}}
			preemptionState.Reason = ReasonPreemptionNotHelpful
			preemptionState.Message = fitError.Error()
		}
		ev.cancelNomination(ctx)
		return framework.NewPostFilterResultWithNominatedNode(""), framework.NewStatus(framework.Unschedulable, preemptionState.Message)
	}

	if status := ev.prepareCandidates(ctx, candidates, pod); !status.IsSuccess() {
		preemptionState.Reason = ReasonPrepareCandidatesError
		preemptionState.Message = status.Message()
		return nil, status
	}
	preemptionState.Reason = ReasonTriggerPodPreemptSuccess
	preemptionState.Message = fmt.Sprintf(ReasonTriggerPodPreemptSuccess, len(preemptionState.allWaitingPods), len(preemptionState.allPods))
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
		// TODO all pods of the same job must have the same preemptionPolicy, add a webhook for it.
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
		if jobPreemptionState.TriggerPodKey == framework.GetNamespacedName(pod.Namespace, pod.Name) {
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
					jobPreemptionState.TerminatingPodOnNominatedNode[terminatingPodKey] = nomNodeName
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
	preemptionState.DurationOfNodeInfoClone = metav1.Duration{Duration: time.Since(startTime)}
	startTime = time.Now()
	nodeLevelCycleState := make(map[string]*framework.CycleState, len(potentialNodes))
	for _, node := range potentialNodes {
		nodeLevelCycleState[node.Node().Name] = state.Clone()
	}
	preemptionState.DurationOfCycleStateClone = metav1.Duration{Duration: time.Since(startTime)}
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

// dryRunPreemption simulates the preemption process on a set of potential nodes in parallel.
// It performs a "what-if" analysis by removing potential victim pods from node resources
// and checking if the preemptor pod (and its associated gang pods) can then be scheduled.
//
// This method executes the following steps:
//  1. Removes all possible victims from each candidate node's NodeInfo.
//  2. Estimates a preemption cost per node based on victim priorities and job grouping.
//  3. Attempts to schedule pending pods (e.g., gang members) on the modified nodes,
//     considering assumed pods for sequential scheduling simulation.
//  4. Identifies feasible nodes where all required pods can fit after preemption.
//  5. Selects the best victims per feasible node by re-adding pods one-by-one and testing feasibility.
//
// The simulation uses cloned CycleState and NodeInfo objects to avoid affecting real scheduling state.
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
	startTime := time.Now()
	potentialVictims, statusMap := ev.removePossibleVictims(triggerPod, cycleStates, potentialNodes, removePod)
	if len(potentialVictims) == 0 {
		return nil, nil, statusMap, nil
	}
	preemptionState.possibleVictims = potentialVictims
	preemptionState.DurationOfRemovePossibleVictims = metav1.Duration{Duration: time.Since(startTime)}
	preemptionCosts := estimatePreemptionCost(potentialVictims)
	addPod := func(state *framework.CycleState, toSchedulePod *corev1.Pod, api *framework.PodInfo, nodeInfo *framework.NodeInfo) error {
		nodeInfo.AddPodInfo(api)
		status := ev.handle.RunPreFilterExtensionAddPod(ctx, state, toSchedulePod, api, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}
	pendingPods := preemptionState.allPendingPods
	startTime = time.Now()
	var podToNominatedNode map[string]string
	var successPods map[string]*Placements
	if preemptionState.gangSchedulingContext != nil && preemptionState.gangSchedulingContext.networkTopologySpec != nil {
		networkTopologySpec := preemptionState.gangSchedulingContext.networkTopologySpec
		var status *framework.Status
		podToNominatedNode, successPods, statusMap, status = ev.PlanNodes(ctx, networkTopologySpec, pendingPods, potentialNodes, cycleStates, addPod, preemptionCosts)
		preemptionState.statusMap = statusMap
		preemptionState.DurationOfPlaceToSchedulePods = metav1.Duration{Duration: time.Since(startTime)}
		preemptionState.Reason = ReasonPreemptionNotHelpful
		preemptionState.Message = status.Message()
		if !status.IsSuccess() {
			return nil, nil, statusMap, nil
		}
	} else {
		var unschedulablePods []*corev1.Pod
		podToNominatedNode, successPods, unschedulablePods = ev.placeToSchedulePods(ctx, pendingPods, cycleStates, potentialNodes, preemptionCosts, addPod, statusMap)
		preemptionState.statusMap = statusMap
		preemptionState.DurationOfPlaceToSchedulePods = metav1.Duration{Duration: time.Since(startTime)}
		if len(unschedulablePods) > 0 {
			return nil, nil, statusMap, nil
		}
	}
	preemptionState.PodToNominatedNode = podToNominatedNode
	startTime = time.Now()
	victims, err := ev.selectVictims(ctx, potentialVictims, cycleStates, successPods, addPod, removePod)
	preemptionState.DurationOfSelectVictimsOnNode = metav1.Duration{Duration: time.Since(startTime)}
	if err != nil {
		preemptionState.Reason = ReasonSelectVictimsOnNodeError
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
	preemptionState := preemptionStateFromContext(ctx)
	preemptionState.SchedulingMode = frameworkext.PodSchedulingMode

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
				// The lifecycle of assumedCycleState is one job schedule.
				// During job preemption, there are two job schedules:
				// 1. one to determine whether preemption is effective after removing all victims, and
				// 2. another to determine the victim on the candidate node.
				// Here we choose clone to avoid the modification of cycleState affecting the subsequent determination of Victim
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
	preemptionState.unschedulablePods = unschedulablePods
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
	startTime := time.Now()
	defer func() {
		preemptionState.DurationOfPrepareCandidates = metav1.Duration{Duration: time.Since(startTime)}
	}()

	cs := ev.handle.ClientSet()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logger := klog.FromContext(ctx)
	errCh := parallelize.NewErrorChannel()
	ev.handle.Parallelizer().Until(ctx, len(candidates), func(i int) {
		victimPod := candidates[i]
		if victimPod.DeletionTimestamp != nil {
			// If the victim Pod is already being deleted, we don't have to make another deletion api call.
			logger.V(2).Info("Victim Pod is already deleted, skipping the API call for it", "triggerPod", preemptionState.TriggerPodKey, "preemptor", preemptionState.PreemptorKey, "node", victimPod.Spec.NodeName, "victim", klog.KObj(victimPod))
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
		logger.V(2).Info("Preemptor pod rejected a waiting pod", "triggerPod", preemptionState.TriggerPodKey, "preemptor", preemptionState.PreemptorKey, "waitingPod", klog.KObj(victim), "node", victim.Spec.NodeName)
	} else {
		condition := &corev1.PodCondition{
			Type:    corev1.DisruptionTarget,
			Status:  corev1.ConditionTrue,
			Reason:  corev1.PodReasonPreemptionByScheduler,
			Message: fmt.Sprintf("%s: preempting to accommodate higher priority pods, preemptor: %s, triggerPod: %s", preemptor.Spec.SchedulerName, preemptionState.PreemptorKey, preemptionState.TriggerPodKey),
		}
		newStatus := victim.Status.DeepCopy()
		updated := apipod.UpdatePodCondition(newStatus, condition)
		if updated {
			if err := schedulerutil.PatchPodStatus(ctx, ev.handle.ClientSet(), victim, newStatus); err != nil {
				logger.Error(err, "Could not add DisruptionTarget condition due to preemption", "pod", klog.KObj(victim), "triggerPod", preemptionState.TriggerPodKey, "preemptor", preemptionState.PreemptorKey)
				return err
			}
		}
		if err := schedulerutil.DeletePod(ctx, ev.handle.ClientSet(), victim); err != nil {
			if apierrors.IsNotFound(err) {
				logger.V(2).Info("Victim Pod is already deleted", "triggerPod", preemptionState.TriggerPodKey, "preemptor", preemptionState.PreemptorKey, "victim", klog.KObj(victim), "node", victim.Spec.NodeName)
			} else {
				logger.Error(err, "Tried to preempted pod", "pod", klog.KObj(victim), "triggerPod", preemptionState.TriggerPodKey, "preemptor", preemptionState.PreemptorKey)
			}
			return err
		}
		logger.V(2).Info("Preemptor Pod preempted victim Pod", "triggerPod", preemptionState.TriggerPodKey, "preemptor", preemptionState.PreemptorKey, "victim", klog.KObj(victim), "node", victim.Spec.NodeName)
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
	startTime := time.Now()
	defer func() {
		preemptionState.DurationOfMakeNomination = metav1.Duration{Duration: time.Since(startTime)}
	}()
	waitingPodToNominatedNode := makeWaitingPodToNominatedNode(preemptionState.allWaitingPods)
	ev.rejectAllWaitingPod(ctx, preemptionState.allWaitingPods, frameworkext.JobPreemptionSuccessPlugin, preemptionState.Message)
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
	startTime := time.Now()
	defer func() {
		preemptionState.DurationOfCancelNomination = metav1.Duration{Duration: time.Since(startTime)}
	}()
	ev.rejectAllWaitingPod(ctx, preemptionState.allWaitingPods, frameworkext.JobPreemptionFailurePlugin, preemptionState.Message)
	ev.setAllNominatedNode(ctx, preemptionState.allPods, nil, nil)
}

func (ev *preemptionEvaluatorImpl) rejectAllWaitingPod(_ context.Context, allWaitingPods []*corev1.Pod, pluginName, msg string) {
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
					nominator.DeleteNominatedReservePodOrReservation(pod)
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
			jobPreemptionDiagnosis.ClearNominatedNodeFailedMsg[podKey] = err.Error()
			patchStatusLock.Unlock()
		}
	}, OperationSetNominatedNode)
}

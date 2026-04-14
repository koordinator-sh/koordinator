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

package core

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

const (
	ErrNoClusterNetworkTopology = "no cluster network topology"
	ErrorNoPlannedNodes         = "no planned pods"
	ErrorNoPendingPods          = "no pending pods"
	ErrorInvalidPlan            = "plan become invalid, wait for the next round"
)

// FIXME Currently, our workflow and solver only supports scenarios where
// 1. there are no Bound member Pods
// 2. the total number of Job member Pods is equal to the minimum number.
// 3. pods have no other topological requirements

func (pgMgr *PodGroupManager) PreFilter(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodes []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext == nil ||
		gangSchedulingContext.networkTopologySpec == nil {
		return nil, nil
	}
	if gangSchedulingContext.failedMessage != "" {
		// this shouldn't happen. cause BeforePreFilter will return fwktype.UnschedulableAndUnresolvable if failedMessage != ""
		return nil, nil
	}

	if len(gangSchedulingContext.alreadyAttemptedPods) > 1 {
		plannedNode := gangSchedulingContext.networkTopologyPlannedNodes[framework.GetNamespacedName(pod.Namespace, pod.Name)]
		if plannedNode == "" {
			// this shouldn't happen. If happens, return err to exposing problems
			return nil, fwktype.NewStatus(fwktype.Error, ErrorNoPlannedNodes)
		}
		return &fwktype.PreFilterResult{NodeNames: sets.New(plannedNode)}, nil
	}
	// first pod, the FindOneNode will take care of it
	return nil, nil
}

func (pgMgr *PodGroupManager) FindOneNode(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, preRes *fwktype.PreFilterResult) (string, *fwktype.Status) {
	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext == nil ||
		gangSchedulingContext.failedMessage != "" ||
		gangSchedulingContext.networkTopologySpec == nil {
		return "", fwktype.NewStatus(fwktype.Skip)
	}

	if len(gangSchedulingContext.alreadyAttemptedPods) > 1 {
		if len(gangSchedulingContext.networkTopologyPlannedNodes) == 0 {
			// this shouldn't happen. If happens, return err to exposing problems
			return "", fwktype.NewStatus(fwktype.Error, ErrorNoPlannedNodes)
		}
		// not first pod, the PreFilter will take care of it
		return "", fwktype.NewStatus(fwktype.Skip)
	}

	allPendingPods := pgMgr.cache.getPendingPods(gangSchedulingContext.gangGroup.UnsortedList())
	if len(allPendingPods) == 0 {
		return "", fwktype.NewStatus(fwktype.Error, ErrorNoPendingPods)
	}
	extension.SortPodsByIndex(allPendingPods)
	var allPendingPodUIDs []string
	for _, pendingPod := range allPendingPods {
		allPendingPodUIDs = append(allPendingPodUIDs, string(pendingPod.UID))
	}
	frameworkext.MakeNominatedPodsOfTheSameJob(cycleState, allPendingPodUIDs)

	var nodes []fwktype.NodeInfo
	if !preRes.AllNodes() {
		nodes = make([]fwktype.NodeInfo, 0, len(preRes.NodeNames))
		for n := range preRes.NodeNames {
			nInfo, err := pgMgr.handle.SnapshotSharedLister().NodeInfos().Get(n)
			if err != nil {
				return "", fwktype.AsStatus(err)
			}
			nodes = append(nodes, nInfo.Snapshot())
		}
	} else {
		nInfos, err := pgMgr.handle.SnapshotSharedLister().NodeInfos().List()
		if err != nil {
			return "", fwktype.AsStatus(err)
		}
		for _, nInfo := range nInfos {
			nodes = append(nodes, nInfo.Snapshot())
		}
	}
	nodeLevelCycleState := make(map[string]fwktype.CycleState, len(nodes))
	for _, node := range nodes {
		nodeLevelCycleState[node.Node().Name] = cycleState.Clone()
	}

	addPod := func(state fwktype.CycleState, toSchedulePod *corev1.Pod, api fwktype.PodInfo, nodeInfo fwktype.NodeInfo) error {
		nodeInfo.AddPodInfo(api)
		status := pgMgr.handle.RunPreFilterExtensionAddPod(ctx, state, toSchedulePod, api, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}

	topologyState := &TopologyState{
		JobTopologyRequirements: &JobTopologyRequirements{
			TopologyLayerMustGather: GetMustGatherLayer(gangSchedulingContext.networkTopologySpec, gangSchedulingContext.networkTopologySnapshot.IsAncestor),
			DesiredOfferSlot:        len(allPendingPods),
			LayerPodCountMultiple:   GetLayerPodCountMultiple(gangSchedulingContext.networkTopologySpec),
		},
	}
	defer func() {
		diagnosis := frameworkext.GetDiagnosis(cycleState)
		diagnosis.TopologyKeyToExplain = string(topologyState.JobTopologyRequirements.TopologyLayerMustGather)
		if diagnosis.ScheduleDiagnosis == nil {
			diagnosis.ScheduleDiagnosis = &frameworkext.ScheduleDiagnosis{}
		}
		diagnosis.ScheduleDiagnosis.SchedulingMode = frameworkext.JobSchedulingMode
		diagnosis.ScheduleDiagnosis.NodeOfferSlot = topologyState.NodeOfferSlot
		diagnosis.ScheduleDiagnosis.NodeToStatusMap = topologyState.NodeToStatusMap
	}()
	ctx = ContextWithTopologyState(ctx, topologyState)
	// TODO: fill clusterNetworkTopology
	plannedNodes, status := pgMgr.networkTopologySolver.PlacePods(
		ctx,
		nodeLevelCycleState,
		allPendingPods,
		nodes,
		addPod,
		topologyState.JobTopologyRequirements,
		networktopology.DeepCopyTreeNode(gangSchedulingContext.networkTopologySnapshot.TreeNode, nil),
		nil,
	)
	if !status.IsSuccess() {
		return "", status
	}
	gangSchedulingContext.networkTopologyPlannedNodes = plannedNodes
	podKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
	return plannedNodes[podKey], nil
}

func (ev *preemptionEvaluatorImpl) PlanNodes(
	ctx context.Context,
	networkTopologySpec *extension.NetworkTopologySpec,
	allPendingPods []*corev1.Pod,
	nodes []fwktype.NodeInfo,
	cycleStates map[string]fwktype.CycleState,
	addPod podFunc,
	preemptionCosts map[string]int,
) (podToNominatedNode map[string]string, successPods map[string]*Placements, statusMap map[string]*fwktype.Status, status *fwktype.Status) {
	preemptionState := preemptionStateFromContext(ctx)
	preemptionState.SchedulingMode = frameworkext.JobSchedulingMode

	if len(preemptionState.gangSchedulingContext.alreadyAttemptedPods) > 1 {
		return nil, nil, nil, fwktype.NewStatus(fwktype.Unschedulable, ErrorInvalidPlan)
	}

	extension.SortPodsByIndex(allPendingPods)
	clonedCycleStates := make(map[string]fwktype.CycleState, len(cycleStates))
	for k, v := range cycleStates {
		clonedCycleStates[k] = v.Clone()
	}
	clonedNodes := make([]fwktype.NodeInfo, len(nodes))
	nodesMap := make(map[string]fwktype.NodeInfo, len(nodes))
	for i, node := range nodes {
		clonedNodes[i] = node.Snapshot()
		nodesMap[node.Node().Name] = node
	}

	nodeToScore := make(map[string]int, len(nodes))
	for nodeName, cost := range preemptionCosts {
		nodeToScore[nodeName] = -cost
	}

	topologyState := &TopologyState{
		JobTopologyRequirements: &JobTopologyRequirements{
			TopologyLayerMustGather: GetMustGatherLayer(networkTopologySpec, preemptionState.gangSchedulingContext.networkTopologySnapshot.IsAncestor),
			DesiredOfferSlot:        len(allPendingPods),
			LayerPodCountMultiple:   GetLayerPodCountMultiple(networkTopologySpec),
		},
	}
	ctx = ContextWithTopologyState(ctx, topologyState)
	// TODO: fill clusterNetworkTopology
	plannedNodes, status := ev.networkTopologySolver.PlacePods(
		ctx,
		cycleStates,
		allPendingPods,
		nodes,
		addPod,
		topologyState.JobTopologyRequirements,
		networktopology.DeepCopyTreeNode(preemptionState.gangSchedulingContext.networkTopologySnapshot.TreeNode, nil),
		preemptionCosts,
	)
	if !status.IsSuccess() {
		preemptionState.statusMap = topologyState.NodeToStatusMap
		preemptionState.NodeToOfferSlot = topologyState.NodeOfferSlot
		return nil, nil, topologyState.NodeToStatusMap, status
	}
	successPods = make(map[string]*Placements, len(plannedNodes))

	for i := range allPendingPods {
		pod := allPendingPods[i]
		podKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
		nodeName := plannedNodes[podKey]
		successPodsOnNode := successPods[nodeName]
		if successPodsOnNode == nil {
			successPodsOnNode = &Placements{
				nodeInfo: nodesMap[nodeName],
				nodeName: nodeName,
			}
			successPods[nodeName] = successPodsOnNode
		}
		successPodsOnNode.pods = append(successPodsOnNode.pods, pod)
	}
	return plannedNodes, successPods, nil, nil
}

const (
	preScoreStateKey = Name + "/pre-score-state"
)

type PreScoreState struct {
	nodesIndex map[string]int
}

func (p *PreScoreState) Clone() fwktype.StateData {
	return p
}

func (pgMgr *PodGroupManager) PreScore(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodes []fwktype.NodeInfo) *fwktype.Status {
	podSelector := extension.GetPodNetworkTopologySelector(pod)
	if podSelector == "" {
		return fwktype.NewStatus(fwktype.Skip)
	}
	nodeInfos, err := pgMgr.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("failed to get all allNodes: %v", err))
	}
	extendedHandle := pgMgr.handle.(frameworkext.ExtendedHandle)
	clusterNetworkTopology := extendedHandle.GetNetworkTopologyTreeManager().GetSnapshot()
	nodes = pgMgr.sortNodesByTopology(ctx, clusterNetworkTopology, podSelector, nodes, nodeInfos)
	nodeIndex := make(map[string]int, len(nodes))
	for i, node := range nodes {
		nodeIndex[node.Node().Name] = i
	}
	cycleState.Write(preScoreStateKey, &PreScoreState{
		nodesIndex: nodeIndex,
	})
	return nil
}

func (pgMgr *PodGroupManager) sortNodesByTopology(
	ctx context.Context,
	clusterNetworkTopology *networktopology.TreeSnapshot,
	podSelector string,
	candidateNodes []fwktype.NodeInfo,
	nodeInfos []fwktype.NodeInfo,
) []fwktype.NodeInfo {
	// FIXME here we assert that every node only accommodates one pod
	nodeOfferSlot := make(map[string]int, len(candidateNodes))
	for _, node := range candidateNodes {
		nodeOfferSlot[node.Node().Name] = 1
	}
	nodeExistingPodNum := calculateNodeExistingPodsNum(ctx, pgMgr.handle.Parallelizer().(parallelize.Parallelizer), podSelector, nodeInfos)
	nodeLayeredTopologyNodes := enumerateNodeTopologyNode(clusterNetworkTopology.TreeNode, len(nodeInfos))
	evaluateTopologyNode(nodeLayeredTopologyNodes, nodeOfferSlot, nil, nodeExistingPodNum)
	sort.Slice(candidateNodes, func(i, j int) bool {
		treeNodeA := nodeLayeredTopologyNodes[candidateNodes[i].Node().Name]
		treeNodeB := nodeLayeredTopologyNodes[candidateNodes[j].Node().Name]
		// Compare ExistingPodNum layer by layer from the current node
		for nodeA, nodeB := treeNodeA, treeNodeB; nodeA != nil && nodeB != nil; nodeA, nodeB = nodeA.Parent, nodeB.Parent {
			if nodeA.ExistingPodNum != nodeB.ExistingPodNum {
				return nodeA.ExistingPodNum > nodeB.ExistingPodNum
			}
		}
		// Compare OfferSlot layer by layer from the current node
		for nodeA, nodeB := treeNodeA, treeNodeB; nodeA != nil && nodeB != nil; nodeA, nodeB = nodeA.Parent, nodeB.Parent {
			if nodeA.OfferSlot != nodeB.OfferSlot {
				return nodeA.OfferSlot < nodeB.OfferSlot
			}
		}
		return treeNodeA.Name < treeNodeB.Name
	})
	return candidateNodes
}

func (pgMgr *PodGroupManager) Score(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) (int64, *fwktype.Status) {
	networkTopologySelectorKey := extension.GetPodNetworkTopologySelector(pod)
	if networkTopologySelectorKey == "" {
		return 0, nil
	}
	preScoreState, err := state.Read(preScoreStateKey)
	if err != nil {
		return 0, fwktype.NewStatus(fwktype.Error, fmt.Sprintf("failed to read pre score state: %v", err))
	}
	return int64(preScoreState.(*PreScoreState).nodesIndex[nodeInfo.Node().Name]), nil
}

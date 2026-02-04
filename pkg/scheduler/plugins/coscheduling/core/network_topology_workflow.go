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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/scheduler/framework"

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

func (pgMgr *PodGroupManager) PreFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext == nil ||
		gangSchedulingContext.networkTopologySpec == nil {
		return nil, nil
	}
	if gangSchedulingContext.failedMessage != "" {
		// this shouldn't happen. cause BeforePreFilter will return framework.UnschedulableAndUnresolvable if failedMessage != ""
		return nil, nil
	}

	if len(gangSchedulingContext.alreadyAttemptedPods) > 1 {
		plannedNode := gangSchedulingContext.networkTopologyPlannedNodes[framework.GetNamespacedName(pod.Namespace, pod.Name)]
		if plannedNode == "" {
			// this shouldn't happen. If happens, return err to exposing problems
			return nil, framework.AsStatus(fmt.Errorf(ErrorNoPlannedNodes))
		}
		return &framework.PreFilterResult{NodeNames: sets.New(plannedNode)}, nil
	}
	// first pod, the FindOneNode will take care of it
	return nil, nil
}

func (pgMgr *PodGroupManager) FindOneNode(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, preRes *framework.PreFilterResult) (string, *framework.Status) {
	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext == nil ||
		gangSchedulingContext.failedMessage != "" ||
		gangSchedulingContext.networkTopologySpec == nil {
		return "", framework.NewStatus(framework.Skip)
	}

	if len(gangSchedulingContext.alreadyAttemptedPods) > 1 {
		if len(gangSchedulingContext.networkTopologyPlannedNodes) == 0 {
			// this shouldn't happen. If happens, return err to exposing problems
			return "", framework.AsStatus(fmt.Errorf(ErrorNoPlannedNodes))
		}
		// not first pod, the PreFilter will take care of it
		return "", framework.NewStatus(framework.Skip)
	}

	allPendingPods := pgMgr.cache.getPendingPods(gangSchedulingContext.gangGroup.UnsortedList())
	if len(allPendingPods) == 0 {
		return "", framework.AsStatus(fmt.Errorf(ErrorNoPendingPods))
	}
	extension.SortPodsByIndex(allPendingPods)
	var allPendingPodUIDs []string
	for _, pendingPod := range allPendingPods {
		allPendingPodUIDs = append(allPendingPodUIDs, string(pendingPod.UID))
	}
	frameworkext.MakeNominatedPodsOfTheSameJob(cycleState, allPendingPodUIDs)

	var nodes []*framework.NodeInfo
	if !preRes.AllNodes() {
		nodes = make([]*framework.NodeInfo, 0, len(preRes.NodeNames))
		for n := range preRes.NodeNames {
			nInfo, err := pgMgr.handle.SnapshotSharedLister().NodeInfos().Get(n)
			if err != nil {
				return "", framework.AsStatus(err)
			}
			nodes = append(nodes, nInfo.Clone())
		}
	} else {
		nInfos, err := pgMgr.handle.SnapshotSharedLister().NodeInfos().List()
		if err != nil {
			return "", framework.AsStatus(err)
		}
		for _, nInfo := range nInfos {
			nodes = append(nodes, nInfo.Clone())
		}
	}
	nodeLevelCycleState := make(map[string]*framework.CycleState, len(nodes))
	for _, node := range nodes {
		nodeLevelCycleState[node.Node().Name] = cycleState.Clone()
	}

	addPod := func(state *framework.CycleState, toSchedulePod *corev1.Pod, api *framework.PodInfo, nodeInfo *framework.NodeInfo) error {
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
			LayerSlotMultiple:       GetLayerSlotMultiple(gangSchedulingContext.networkTopologySpec),
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
	nodes []*framework.NodeInfo,
	cycleStates map[string]*framework.CycleState,
	addPod podFunc,
	preemptionCosts map[string]int,
) (podToNominatedNode map[string]string, successPods map[string]*Placements, stausMap framework.NodeToStatusMap, status *framework.Status) {
	preemptionState := preemptionStateFromContext(ctx)
	preemptionState.SchedulingMode = frameworkext.JobSchedulingMode

	if len(preemptionState.gangSchedulingContext.alreadyAttemptedPods) > 1 {
		return nil, nil, nil, framework.NewStatus(framework.Unschedulable, ErrorInvalidPlan)
	}

	extension.SortPodsByIndex(allPendingPods)
	clonedCycleStates := make(map[string]*framework.CycleState, len(cycleStates))
	for k, v := range cycleStates {
		clonedCycleStates[k] = v.Clone()
	}
	clonedNodes := make([]*framework.NodeInfo, len(nodes))
	nodesMap := make(map[string]*framework.NodeInfo, len(nodes))
	for i, node := range nodes {
		clonedNodes[i] = node.Clone()
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
			LayerSlotMultiple:       GetLayerSlotMultiple(networkTopologySpec),
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

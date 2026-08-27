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
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
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
	if gangSchedulingContext == nil {
		// no gang scheduling cycle is ongoing. the pod may be a late member of an
		// already-satisfied topology-aware gang (e.g. recreated after the batch
		// was scheduled), which bypassed BeforePreFilter. confine it to the
		// topology domain of the existing members, or reject it.
		return pgMgr.preFilterLateMemberOfSatisfiedGang(ctx, pod)
	}
	if gangSchedulingContext.networkTopologySpec == nil {
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

func (pgMgr *PodGroupManager) FindOneNode(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, preRes *fwktype.PreFilterResult) (*frameworkext.BatchScheduleResult, *fwktype.Status) {
	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext == nil ||
		gangSchedulingContext.failedMessage != "" ||
		gangSchedulingContext.networkTopologySpec == nil {
		return nil, fwktype.NewStatus(fwktype.Skip)
	}

	if len(gangSchedulingContext.alreadyAttemptedPods) > 1 {
		if len(gangSchedulingContext.networkTopologyPlannedNodes) == 0 {
			// this shouldn't happen. If happens, return err to exposing problems
			return nil, fwktype.NewStatus(fwktype.Error, ErrorNoPlannedNodes)
		}
		// not first pod, the PreFilter will take care of it
		return nil, fwktype.NewStatus(fwktype.Skip)
	}

	allPendingPods := pgMgr.cache.getPendingPods(gangSchedulingContext.gangGroup.UnsortedList())
	if len(allPendingPods) == 0 {
		return nil, fwktype.NewStatus(fwktype.Error, ErrorNoPendingPods)
	}
	extension.SortPodsByIndex(allPendingPods)
	allPendingPodUIDs := sets.New[string]()
	for _, pendingPod := range allPendingPods {
		allPendingPodUIDs.Insert(string(pendingPod.UID))
	}
	frameworkext.MakeNominatedPodsOfTheSameJob(cycleState, allPendingPodUIDs)

	var nodes []fwktype.NodeInfo
	if !preRes.AllNodes() {
		nodes = make([]fwktype.NodeInfo, 0, len(preRes.NodeNames))
		for n := range preRes.NodeNames {
			nInfo, err := pgMgr.handle.SnapshotSharedLister().NodeInfos().Get(n)
			if err != nil {
				return nil, fwktype.AsStatus(err)
			}
			nodes = append(nodes, nInfo.Snapshot())
		}
	} else {
		nInfos, err := pgMgr.handle.SnapshotSharedLister().NodeInfos().List()
		if err != nil {
			return nil, fwktype.AsStatus(err)
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
		return nil, status
	}
	gangSchedulingContext.networkTopologyPlannedNodes = plannedNodes
	recordBatchPlacement(gangSchedulingContext.gangGroupID,
		gangSchedulingContext.networkTopologySpec, gangSchedulingContext.networkTopologySnapshot, plannedNodes)
	return &frameworkext.BatchScheduleResult{
		Pods:          allPendingPods,
		PodToNodeName: plannedNodes,
	}, nil
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
	recordBatchPlacement(preemptionState.gangSchedulingContext.gangGroupID,
		networkTopologySpec, preemptionState.gangSchedulingContext.networkTopologySnapshot, plannedNodes)
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

// preFilterLateMemberOfSatisfiedGang handles late members (typically recreated
// pods/reservations) of an already-satisfied topology-aware gang. Such pods bypassed
// BeforePreFilter because the gang is once-satisfied, so no gang scheduling context was
// created for them. It confines the candidate nodes to the must-gather topology domain
// where the bound members are placed. If the domain cannot be determined, the pod is
// rejected instead of being placed arbitrarily.
func (pgMgr *PodGroupManager) preFilterLateMemberOfSatisfiedGang(ctx context.Context, pod *corev1.Pod) (*fwktype.PreFilterResult, *fwktype.Status) {
	if !util.IsPodNeedGang(pod) {
		return nil, nil
	}
	gang := pgMgr.GetGangByPod(pod)
	if gang == nil || gang.NetworkTopologySpec == nil {
		return nil, nil
	}
	if gang.getGangMatchPolicy() != extension.GangMatchPolicyOnceSatisfied || !gang.isGangOnceResourceSatisfied() {
		return nil, nil
	}
	podKey := util.GetId(pod.Namespace, pod.Name)
	extendedHandle, ok := pgMgr.handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, nil
	}
	treeManager := extendedHandle.GetNetworkTopologyTreeManager()
	var snapshot *networktopology.TreeSnapshot
	if treeManager != nil {
		snapshot = treeManager.GetSnapshot()
	}
	if snapshot == nil || snapshot.TreeNode == nil {
		return nil, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable,
			fmt.Sprintf("pod %q belongs to the satisfied topology-aware gang group %q, but %s",
				podKey, gang.GangGroupId, ErrNoClusterNetworkTopology))
	}
	mustGatherLayer := GetMustGatherLayer(gang.NetworkTopologySpec, snapshot.IsAncestor)
	if mustGatherLayer == "" {
		// no must-gather requirement, the topology is only a soft preference, don't confine
		return nil, nil
	}
	domain, status := pgMgr.getMustGatherDomainOfBoundMembers(gang, pod, snapshot, mustGatherLayer)
	if !status.IsSuccess() {
		return nil, status
	}
	domainNodeNames := sets.New[string]()
	collectLeafNodeNames(domain, domainNodeNames)
	if len(domainNodeNames) == 0 {
		return nil, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable,
			fmt.Sprintf("pod %q belongs to the satisfied topology-aware gang group %q, but the must-gather topology domain %s/%s has no node",
				podKey, gang.GangGroupId, domain.Layer, domain.Name))
	}

	klog.Infof("confine late member %q of satisfied gang group %q to the must-gather topology domain %s/%s",
		podKey, gang.GangGroupId, domain.Layer, domain.Name)
	return &fwktype.PreFilterResult{NodeNames: domainNodeNames}, nil
}

// getMustGatherDomainOfBoundMembers returns the single must-gather topology domain that all
// bound members of the gang group are placed in. It returns a rejection status when the domain
// cannot be determined or the bound members span multiple domains.
func (pgMgr *PodGroupManager) getMustGatherDomainOfBoundMembers(gang *Gang, pod *corev1.Pod, snapshot *networktopology.TreeSnapshot, mustGatherLayer schedulingv1alpha1.TopologyLayer) (*networktopology.TreeNode, *fwktype.Status) {
	podKey := util.GetId(pod.Namespace, pod.Name)
	boundPods := make([]*corev1.Pod, 0)
	for _, gangID := range gang.getGangGroup() {
		memberGang := pgMgr.cache.getGangFromCacheByGangId(gangID, false)
		if memberGang == nil {
			continue
		}
		boundPods = append(boundPods, memberGang.getBoundChildrenFromGang()...)
	}

	nodeToTreeNode := enumerateNodeTopologyNode(snapshot.TreeNode, len(boundPods))
	domains := map[networktopology.TreeNodeMeta]*networktopology.TreeNode{}
	for _, boundPod := range boundPods {
		if boundPod.UID == pod.UID || boundPod.Spec.NodeName == "" {
			continue
		}
		leafNode := nodeToTreeNode[boundPod.Spec.NodeName]
		if leafNode == nil {
			return nil, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable,
				fmt.Sprintf("pod %q belongs to the satisfied topology-aware gang group %q, but bound member %q is on node %q which is not in the cluster network topology",
					podKey, gang.GangGroupId, util.GetId(boundPod.Namespace, boundPod.Name), boundPod.Spec.NodeName))
		}
		domain := findAncestorAtLayer(leafNode, mustGatherLayer)
		if domain == nil {
			return nil, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable,
				fmt.Sprintf("pod %q belongs to the satisfied topology-aware gang group %q, but cannot find the must-gather layer %q for node %q",
					podKey, gang.GangGroupId, mustGatherLayer, boundPod.Spec.NodeName))
		}
		domains[domain.TreeNodeMeta] = domain
	}
	if len(domains) == 0 {
		return nil, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable,
			fmt.Sprintf("pod %q belongs to the satisfied topology-aware gang group %q, but no bound member is found to determine the must-gather topology domain",
				podKey, gang.GangGroupId))
	}
	if len(domains) > 1 {
		domainNames := make([]string, 0, len(domains))
		for domainMeta := range domains {
			domainNames = append(domainNames, fmt.Sprintf("%s/%s", domainMeta.Layer, domainMeta.Name))
		}
		sort.Strings(domainNames)
		return nil, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable,
			fmt.Sprintf("pod %q belongs to the satisfied topology-aware gang group %q, but the bound members span multiple must-gather topology domains %v",
				podKey, gang.GangGroupId, domainNames))
	}
	for _, domain := range domains {
		return domain, nil
	}
	return nil, nil
}

// findAncestorAtLayer walks up from the given tree node and returns the ancestor (or itself) at the target layer.
func findAncestorAtLayer(treeNode *networktopology.TreeNode, layer schedulingv1alpha1.TopologyLayer) *networktopology.TreeNode {
	for cur := treeNode; cur != nil; cur = cur.Parent {
		if cur.Layer == layer {
			return cur
		}
	}
	return nil
}

// collectLeafNodeNames collects the names of all node-layer descendants of the given tree node.
func collectLeafNodeNames(treeNode *networktopology.TreeNode, names sets.Set[string]) {
	if treeNode.Layer == schedulingv1alpha1.NodeTopologyLayer {
		names.Insert(treeNode.Name)
		return
	}
	for _, child := range treeNode.Children {
		collectLeafNodeNames(child, names)
	}
}

// recordBatchPlacement logs a structured summary of where each pod of the gang group is placed,
// grouped by the must-gather topology domain, so that unexpected placements (e.g. a batch expected
// to land in the same rack being scattered across racks) can be diagnosed.
func recordBatchPlacement(gangGroupID string, spec *extension.NetworkTopologySpec, snapshot *networktopology.TreeSnapshot, podToNode map[string]string) {
	if len(podToNode) == 0 {
		return
	}
	mustGatherLayer, domainToPlacements := groupPlacementByMustGatherDomain(spec, snapshot, podToNode)
	if mustGatherLayer == "" {
		placements := flattenPlacements(domainToPlacements)
		klog.InfoS("Network topology batch placement",
			"gangGroup", gangGroupID, "podCount", len(placements), "placements", placements)
		return
	}
	domains := make([]networktopology.TreeNodeMeta, 0, len(domainToPlacements))
	for domainMeta := range domainToPlacements {
		domains = append(domains, domainMeta)
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Name < domains[j].Name })
	for _, domainMeta := range domains {
		domainPlacements := domainToPlacements[domainMeta]
		sort.Strings(domainPlacements)
		klog.InfoS("Network topology batch placement",
			"gangGroup", gangGroupID,
			"mustGatherLayer", string(mustGatherLayer),
			"topologyDomain", fmt.Sprintf("%s/%s", domainMeta.Layer, domainMeta.Name),
			"podCount", len(domainPlacements),
			"placements", domainPlacements,
		)
	}
	if len(domainToPlacements) > 1 {
		domainNames := make([]string, 0, len(domains))
		for _, domainMeta := range domains {
			domainNames = append(domainNames, fmt.Sprintf("%s/%s", domainMeta.Layer, domainMeta.Name))
		}
		klog.Warningf("gang group %s with must-gather layer %q landed in %d topology domains %v, placements: %v",
			gangGroupID, mustGatherLayer, len(domainToPlacements), domainNames, flattenPlacements(domainToPlacements))
	}
}

// groupPlacementByMustGatherDomain groups the pod-to-node placements by the must-gather topology
// domain of each node. It returns an empty layer when there is no must-gather requirement or the
// topology tree is unavailable; in that case all placements are grouped under an empty domain.
func groupPlacementByMustGatherDomain(
	spec *extension.NetworkTopologySpec,
	snapshot *networktopology.TreeSnapshot,
	podToNode map[string]string,
) (schedulingv1alpha1.TopologyLayer, map[networktopology.TreeNodeMeta][]string) {
	domainToPlacements := map[networktopology.TreeNodeMeta][]string{}
	var mustGatherLayer schedulingv1alpha1.TopologyLayer
	if spec != nil && snapshot != nil && snapshot.TreeNode != nil {
		mustGatherLayer = GetMustGatherLayer(spec, snapshot.IsAncestor)
	}
	if mustGatherLayer == "" || snapshot == nil || snapshot.TreeNode == nil {
		for podKey, nodeName := range podToNode {
			domainToPlacements[networktopology.TreeNodeMeta{}] = append(domainToPlacements[networktopology.TreeNodeMeta{}], podKey+" -> "+nodeName)
		}
		return "", domainToPlacements
	}
	nodeToTreeNode := enumerateNodeTopologyNode(snapshot.TreeNode, len(podToNode))
	for podKey, nodeName := range podToNode {
		placement := podKey + " -> " + nodeName
		var domainMeta networktopology.TreeNodeMeta
		found := false
		if leafNode := nodeToTreeNode[nodeName]; leafNode != nil {
			if domain := findAncestorAtLayer(leafNode, mustGatherLayer); domain != nil {
				domainMeta = domain.TreeNodeMeta
				found = true
			}
		}
		if !found {
			domainMeta = networktopology.TreeNodeMeta{Layer: mustGatherLayer, Name: "unknown"}
		}
		domainToPlacements[domainMeta] = append(domainToPlacements[domainMeta], placement)
	}
	return mustGatherLayer, domainToPlacements
}

func flattenPlacements(domainToPlacements map[networktopology.TreeNodeMeta][]string) []string {
	placements := make([]string, 0)
	for _, domainPlacements := range domainToPlacements {
		placements = append(placements, domainPlacements...)
	}
	sort.Strings(placements)
	return placements
}

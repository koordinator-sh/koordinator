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

package reservation

import (
	"context"
	"fmt"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	schedulingcorev1 "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/component-helpers/scheduling/corev1/nodeaffinity"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func (pl *Plugin) BeforePreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *framework.Status) {
	var (
		state    *stateData
		restored bool
		status   *framework.Status
	)
	if reservationutil.IsReservePod(pod) {
		state, restored, status = pl.prepareMatchReservationStateForReservePod(ctx, cycleState, pod)
	} else {
		state, restored, status = pl.prepareMatchReservationStateForNormalPod(ctx, cycleState, pod)
	}
	if !status.IsSuccess() {
		return nil, false, status
	}
	cycleState.Write(stateKey, state)
	return pod, restored, nil
}

func (pl *Plugin) AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, preRes *framework.PreFilterResult) *framework.Status {
	// Since restoring reserved resources is inefficient, the feature handles it with PreFilter result to reduce the
	// unnecessary restorations.
	if !pl.enableLazyReservationRestore {
		return nil
	}

	state := getStateData(cycleState)

	var allNodes []string
	var skipRestoreNodeInfo bool
	hintState, hasHint := getSchedulingHint(cycleState)
	if hasHint { // use the hint nodes if exists
		allNodes = hintState.PreFilterNodeInfos
		skipRestoreNodeInfo = hintState.SkipRestoreNodeInfo
	} else if !preRes.AllNodes() { // use the PreFilter result if exists
		allNodes = preRes.NodeNames.UnsortedList()
	} else { // If the PreFilterResult does not restrict the scope, we try all the valid nodeReservationStates to restore.
		allNodes = make([]string, 0, len(state.nodeReservationStates))
		for node := range state.nodeReservationStates {
			allNodes = append(allNodes, node)
		}
	}

	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := parallelize.NewErrorChannel()
	nodeInfoLister := pl.handle.SnapshotSharedLister().NodeInfos()
	checkNode := func(i int) {
		nodeInfo, err := nodeInfoLister.Get(allNodes[i])
		if err != nil {
			klog.Warningf("Failed to get NodeInfo of %s during reservation's AfterPreFilter for pod %v, err: %s", allNodes[i], klog.KObj(pod), err)
			return
		}
		node := nodeInfo.Node()
		if node == nil {
			klog.V(4).InfoS("AfterPreFilter failed to get node", "pod", klog.KObj(pod), "nodeInfo", nodeInfo)
			return
		}

		nodeRState := state.nodeReservationStates[node.Name]
		if nodeRState == nil {
			nodeRState = &nodeReservationState{}
		}
		if !nodeRState.finalRestored && (len(nodeRState.matchedOrIgnored) > 0 || len(nodeRState.unmatched) > 0 || len(nodeRState.preAllocatablePods) > 0) {
			extender := pl.handle.(frameworkext.FrameworkExtender)
			_, status := restoreReservationResourcesForNode(ctx, cycleState, extender, pod, state.rInfo, nodeInfo, nodeRState, skipRestoreNodeInfo)
			if !status.IsSuccess() {
				err = status.AsError()
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}
	}
	pl.handle.Parallelizer().Until(parallelCtx, len(allNodes), checkNode, "transformNodesWithReservationAfterPreFilter")
	err := errCh.ReceiveError()
	if err != nil {
		klog.ErrorS(err, "Failed to restore reservation resources for pod", "pod", klog.KObj(pod))
		return framework.AsStatus(err)
	}

	cycleState.Write(stateKey, state)
	klog.V(6).InfoS("AfterPreFilter restore reservation restores for pod", "pod", klog.KObj(pod), "nodes", len(allNodes))
	return nil
}

func (pl *Plugin) prepareMatchReservationStateForNormalPod(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*stateData, bool, *framework.Status) {
	logger := klog.FromContext(ctx)
	reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(pod)
	if err != nil {
		klog.ErrorS(err, "Failed to parse reservation affinity", "pod", klog.KObj(pod))
		return nil, false, framework.AsStatus(err)
	}
	specificNodes, status := parseSpecificNodesFromAffinity(pod)
	if !status.IsSuccess() {
		return nil, false, status
	}
	exactMatchReservationSpec, err := extension.GetExactMatchReservationSpec(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "Failed to parse exact match reservation spec", "pod", klog.KObj(pod))
		return nil, false, framework.AsStatus(err)
	}
	affinityReservationName := reservationAffinity.GetName()
	isReservationIgnored := extension.IsReservationIgnored(pod)
	requiredNodeAffinity := nodeaffinity.GetRequiredNodeAffinity(pod)
	podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	// check if the node-level preRestore is required for all nodes in the BeforePreFilter
	isNodePreRestoreRequired := isPodAllNodesPreRestoreRequired(pod)

	extender, _ := pl.handle.(frameworkext.FrameworkExtender)
	if extender != nil { // global preRestore
		status := extender.RunReservationExtensionPreRestoreReservation(ctx, cycleState, pod)
		if !status.IsSuccess() {
			return nil, false, status
		}
	}

	var stateIndex, diagnosisIndex int32
	var allNodes []string
	var skipRestoreNodeInfo bool
	hintState, hasHint := getSchedulingHint(cycleState)
	if !hasHint {
		allNodes = pl.reservationCache.ListAllNodes(true)
	} else { // use the hint nodes if exists
		allNodes = hintState.PreFilterNodeInfos
		skipRestoreNodeInfo = hintState.SkipRestoreNodeInfo
	}
	allNodeReservationStates := make([]*nodeReservationState, len(allNodes))
	allNodeDiagnosisStates := make([]*nodeDiagnosisState, len(allNodes))
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := parallelize.NewErrorChannel()
	nodeInfoLister := pl.handle.SnapshotSharedLister().NodeInfos()
	processNode := func(i int) {
		nodeInfo, err := nodeInfoLister.Get(allNodes[i])
		if err != nil {
			klog.Warningf("Failed to get NodeInfo of %s during reservation's BeforePreFilter for pod: %v, err: %v", allNodes[i], klog.KObj(pod), err)
			return
		}
		node := nodeInfo.Node()
		if node == nil {
			klog.V(4).InfoS("BeforePreFilter failed to get node", "pod", klog.KObj(pod), "nodeInfo", nodeInfo)
			return
		}
		nodeName := node.Name

		if specificNodes.Len() > 0 {
			if !specificNodes.Has(nodeName) {
				return
			}
		} else if match, _ := requiredNodeAffinity.Match(node); !match {
			return
		}

		var unmatched, matchedOrIgnored []*frameworkext.ReservationInfo
		diagnosisState := &nodeDiagnosisState{
			nodeName:                 nodeName,
			ignored:                  0,
			ownerMatched:             0,
			nameUnmatched:            0,
			isUnschedulableUnmatched: 0,
			affinityUnmatched:        0,
			notExactMatched:          0,
			taintsUnmatched:          0,
			taintsUnmatchedReasons:   map[string]int{},
		}

		status := pl.reservationCache.ForEachMatchableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
			// check if the reservation matches or can be ignored by the pod
			isMatchedOrIgnored := checkReservationMatchedOrIgnored(pod, rInfo, diagnosisState, node, podRequests, reservationAffinity, exactMatchReservationSpec, affinityReservationName, isReservationIgnored)

			if isMatchedOrIgnored { // reservation is matched or ignored for the pod
				matchedOrIgnored = append(matchedOrIgnored, rInfo.Clone())
			} else if rInfo.GetAllocatedPods() > 0 { // reservation is unmatched and not ignored
				unmatched = append(unmatched, rInfo.Clone())
			}

			return true, nil
		})
		if !status.IsSuccess() {
			err = status.AsError()
			klog.ErrorS(err, "Failed to forEach reservations on node", "pod", klog.KObj(pod), "node", nodeName)
			errCh.SendErrorWithCancel(err, cancel)
			return
		}

		if diagnosisState.ignored > 0 || diagnosisState.ownerMatched > 0 {
			idx := atomic.AddInt32(&diagnosisIndex, 1)
			allNodeDiagnosisStates[idx-1] = diagnosisState
		}

		if len(matchedOrIgnored) == 0 && len(unmatched) == 0 {
			return
		}

		// The Pod declares a ReservationAffinity, which means that the Pod must reuse the Reservation resources,
		// but there are no matching Reservations, which means that the node itself does not need to be processed.
		// We can end early to avoid meaningless operations.
		if reservationAffinity != nil && len(matchedOrIgnored) == 0 {
			return
		}

		nodeRState := &nodeReservationState{
			nodeName:         nodeName,
			matchedOrIgnored: matchedOrIgnored,
			unmatched:        unmatched,
		}

		// LazyReservationRestore indicates whether to restore reserved resources for the scheduling pod lazily.
		// If it is disabled, the reserved resources are ensured to restore in BeforePreFilter/PreFilter phase, where all
		// nodes related to reservations will restore reserved resources and refresh node snapshots in the next cycle.
		// If it is enabled, the reserved resources are delayed to restore in Filter phase when the pod does not specify
		// any pod affinity/anti-affinity or topologySpreadConstraints, it can reduce resource restoration overhead
		// especially when there are a large scale of reservations. However, it does not ensure the correctness of the
		// existing pod affinities, so it is disabled by default.
		if !pl.enableLazyReservationRestore {
			_, status = restoreReservationResourcesForNode(ctx, cycleState, extender, pod, nil, nodeInfo, nodeRState, skipRestoreNodeInfo)
			if !status.IsSuccess() {
				err = status.AsError()
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		} else if isNodePreRestoreRequired { // the pre restoration is required in the BeforePreFilter
			err = preRestoreReservationResourcesForNode(logger, extender, pod, nil, nodeInfo, nodeRState, skipRestoreNodeInfo)
			if err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}

		if len(matchedOrIgnored) > 0 || len(unmatched) > 0 {
			index := atomic.AddInt32(&stateIndex, 1)
			allNodeReservationStates[index-1] = nodeRState
		}
	}
	pl.handle.Parallelizer().Until(parallelCtx, len(allNodes), processNode, "transformNodesWithReservation")
	err = errCh.ReceiveError()
	if err != nil {
		klog.ErrorS(err, "Failed to find matched or unmatched reservations", "pod", klog.KObj(pod))
		return nil, false, framework.AsStatus(err)
	}

	allNodeReservationStates = allNodeReservationStates[:stateIndex]
	allNodeDiagnosisStates = allNodeDiagnosisStates[:diagnosisIndex]
	podRequestResources := framework.NewResource(podRequests)
	state := &stateData{
		schedulingStateData: schedulingStateData{
			hasAffinity:              reservationAffinity != nil,
			reservationName:          affinityReservationName,
			podRequests:              podRequests,
			podRequestsResources:     podRequestResources,
			podResourceNames:         quotav1.ResourceNames(podRequests),
			preemptible:              map[string]corev1.ResourceList{},
			preemptibleInRRs:         map[string]map[types.UID]corev1.ResourceList{},
			nodeReservationStates:    map[string]*nodeReservationState{},
			nodeReservationDiagnosis: map[string]*nodeDiagnosisState{},
		},
	}
	for index := range allNodeReservationStates {
		v := allNodeReservationStates[index]
		state.nodeReservationStates[v.nodeName] = v
	}
	for i := range allNodeDiagnosisStates {
		v := allNodeDiagnosisStates[i]
		state.nodeReservationDiagnosis[v.nodeName] = v
	}

	return state, len(allNodeReservationStates) > 0, nil
}

func (pl *Plugin) prepareMatchReservationStateForReservePod(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*stateData, bool, *framework.Status) {
	logger := klog.FromContext(ctx)

	specificNodes, status := parseSpecificNodesFromAffinity(pod)
	if !status.IsSuccess() {
		return nil, false, status
	}
	requiredNodeAffinity := nodeaffinity.GetRequiredNodeAffinity(pod)
	podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	// check if the node-level preRestore is required for all nodes in the BeforePreFilter
	isNodePreRestoreRequired := isPodAllNodesPreRestoreRequired(pod)

	isPreAllocation := reservationutil.IsReservePodPreAllocation(pod)
	rName := reservationutil.GetReservationNameFromReservePod(pod)
	r, err := pl.rLister.Get(rName)
	if err != nil {
		return nil, false, framework.AsStatus(err)
	}
	rInfo := frameworkext.NewReservationInfo(r)
	isPreAllocationRequired := extension.IsPreAllocationRequired(pod.Labels)

	// by default list all nodes existing a reservation
	// merge with pre-allocatable pods for the pre-allocation
	var allNodes []string
	var skipRestoreNodeInfo bool
	hintState, hasHint := getSchedulingHint(cycleState)
	if !hasHint {
		allNodes = pl.reservationCache.ListAllNodes(false) // only care about allocated reservations to restore
	} else {
		allNodes = hintState.PreFilterNodeInfos
		skipRestoreNodeInfo = hintState.SkipRestoreNodeInfo
	}

	preAllocatableCandidatesOnNode := map[string][]*corev1.Pod{}
	extender, _ := pl.handle.(frameworkext.FrameworkExtender)
	if extender != nil { // global preRestore
		status := extender.RunReservationExtensionPreRestoreReservation(ctx, cycleState, pod)
		if !status.IsSuccess() {
			return nil, false, status
		}
		if isPreAllocation {
			// NOTE: Currently, the PreAllocation is only available on a Restricted Reservation.
			if rInfo.GetAllocatePolicy() != schedulingv1alpha1.ReservationAllocatePolicyRestricted {
				return nil, false, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationPreAllocationUnsupported)
			}
			if rInfo.ParseError != nil {
				klog.ErrorS(rInfo.ParseError, "Failed to PreFilter the reserve pod due to invalid owner", "pod", klog.KObj(pod), "reservation", rName)
				return nil, false, framework.NewStatus(framework.UnschedulableAndUnresolvable, rInfo.ParseError.Error())
			}
			preAllocationMode := reservationutil.GetPreAllocationMode(rInfo.Reservation)
			if preAllocationMode == schedulingv1alpha1.PreAllocationModeCluster && !pl.enablePreAllocationClusterMode {
				return nil, false, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationClusterModeDisabled)
			}

			status = extender.RunReservationExtensionPreRestoreReservationPreAllocation(ctx, cycleState, rInfo)
			if !status.IsSuccess() {
				return nil, false, status
			}

			preAllocatableCandidatesOnNode, err = pl.listPreAllocatableCandidates(preAllocationMode, rInfo)
			if err != nil {
				return nil, false, framework.AsStatus(err)
			}
			if len(preAllocatableCandidatesOnNode) > 0 && !hasHint { // if no hint, merge nodes with pre-allocatable
				nodeMap := make(map[string]struct{}, len(allNodes))
				for _, node := range allNodes {
					nodeMap[node] = struct{}{}
				}
				for node := range preAllocatableCandidatesOnNode {
					if _, ok := nodeMap[node]; !ok {
						allNodes = append(allNodes, node)
					}
				}
				if klog.V(6).Enabled() {
					klog.InfoS("listPreAllocatableCandidates got candidate pods", "pod", klog.KObj(pod), "reservation", rName, "preAllocatable nodes", len(preAllocatableCandidatesOnNode), "all nodes", len(allNodes))
				}
			}
		}
	}

	var stateIndex, diagnosisIndex int32
	allNodeReservationStates := make([]*nodeReservationState, len(allNodes))
	allNodeDiagnosisStates := make([]*nodeDiagnosisState, len(allNodes))
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := parallelize.NewErrorChannel()
	nodeInfoLister := pl.handle.SnapshotSharedLister().NodeInfos()
	processNode := func(i int) {
		nodeInfo, err := nodeInfoLister.Get(allNodes[i])
		if err != nil {
			klog.Warningf("Failed to get NodeInfo of %s during reservation's BeforePreFilter for pod: %v, err: %v", allNodes[i], klog.KObj(pod), err)
			return
		}
		node := nodeInfo.Node()
		if node == nil {
			klog.V(4).InfoS("BeforePreFilter failed to get node", "pod", klog.KObj(pod), "nodeInfo", nodeInfo)
			return
		}
		nodeName := node.Name

		if specificNodes.Len() > 0 {
			if !specificNodes.Has(nodeName) {
				return
			}
		} else if match, _ := requiredNodeAffinity.Match(node); !match {
			return
		}

		var unmatched []*frameworkext.ReservationInfo
		diagnosisState := &nodeDiagnosisState{
			nodeName:                 nodeName,
			ignored:                  0,
			ownerMatched:             0,
			nameUnmatched:            0,
			isUnschedulableUnmatched: 0,
			affinityUnmatched:        0,
			notExactMatched:          0,
			taintsUnmatched:          0,
			taintsUnmatchedReasons:   map[string]int{},
		}

		status := pl.reservationCache.ForEachMatchableReservationOnNode(nodeName, func(availableRInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
			unmatched = append(unmatched, availableRInfo.Clone())

			return true, nil
		})
		if !status.IsSuccess() {
			err = status.AsError()
			klog.ErrorS(err, "Failed to forEach reservations on node", "pod", klog.KObj(pod), "node", nodeName)
			errCh.SendErrorWithCancel(err, cancel)
			return
		}

		// handle pre-allocation
		var preAllocatablePods []*corev1.Pod
		if preAllocatableCandidates := preAllocatableCandidatesOnNode[nodeName]; len(preAllocatableCandidates) > 0 {
			preAllocatablePods = make([]*corev1.Pod, 0, len(preAllocatableCandidates))
			for _, candidatePod := range preAllocatableCandidatesOnNode[nodeName] {
				matched, err := checkPreAllocatableMatched(rInfo, candidatePod, diagnosisState, node)
				if err != nil {
					klog.ErrorS(err, "Failed to check pre-allocatable pod for reservation", "pod", klog.KObj(pod), "node", nodeName,
						"reservation", rName, "preAllocatable", klog.KObj(candidatePod))
					continue
				}
				if matched {
					preAllocatablePods = append(preAllocatablePods, candidatePod.DeepCopy())
				}
			}
		}
		if klog.V(6).Enabled() {
			klog.InfoS("filter pre-allocatable pods", "reservation", rInfo.GetName(), "node", nodeName, "candidate", len(preAllocatableCandidatesOnNode[nodeName]), "filtered", len(preAllocatablePods))
		}

		if diagnosisState.ownerMatched > 0 {
			idx := atomic.AddInt32(&diagnosisIndex, 1)
			allNodeDiagnosisStates[idx-1] = diagnosisState
		}

		if len(unmatched) == 0 && len(preAllocatablePods) == 0 {
			return
		}

		// The reservation specifies required to pre-allocation, but there is no pre-allocatable pods on the node.
		if len(preAllocatablePods) == 0 && isPreAllocationRequired {
			return
		}

		nodeRState := &nodeReservationState{
			nodeName:           nodeName,
			matchedOrIgnored:   nil,
			unmatched:          unmatched,
			preAllocatablePods: preAllocatablePods,
		}

		// LazyReservationRestore indicates whether to restore reserved resources for the scheduling pod lazily.
		// If it is disabled, the reserved resources are ensured to restore in BeforePreFilter/PreFilter phase, where all
		// nodes related to reservations will restore reserved resources and refresh node snapshots in the next cycle.
		// If it is enabled, the reserved resources are delayed to restore in Filter phase when the pod does not specify
		// any pod affinity/anti-affinity or topologySpreadConstraints, it can reduce resource restoration overhead
		// especially when there are a large scale of reservations. However, it does not ensure the correctness of the
		// existing pod affinities, so it is disabled by default.
		if !pl.enableLazyReservationRestore {
			_, status = restoreReservationResourcesForNode(ctx, cycleState, extender, pod, rInfo, nodeInfo, nodeRState, skipRestoreNodeInfo)
			if !status.IsSuccess() {
				err = status.AsError()
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		} else if isNodePreRestoreRequired { // the pre restoration is required in the BeforePreFilter
			err = preRestoreReservationResourcesForNode(logger, extender, pod, rInfo, nodeInfo, nodeRState, skipRestoreNodeInfo)
			if err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}

		if len(unmatched) > 0 || len(preAllocatablePods) > 0 {
			index := atomic.AddInt32(&stateIndex, 1)
			allNodeReservationStates[index-1] = nodeRState
		}
	}
	pl.handle.Parallelizer().Until(parallelCtx, len(allNodes), processNode, "transformNodesWithReservation")
	err = errCh.ReceiveError()
	if err != nil {
		klog.ErrorS(err, "Failed to find matched or unmatched reservations", "pod", klog.KObj(pod))
		return nil, false, framework.AsStatus(err)
	}

	allNodeReservationStates = allNodeReservationStates[:stateIndex]
	allNodeDiagnosisStates = allNodeDiagnosisStates[:diagnosisIndex]
	podRequestResources := framework.NewResource(podRequests)
	state := &stateData{
		schedulingStateData: schedulingStateData{
			isPreAllocationRequired:  isPreAllocationRequired,
			podRequests:              podRequests,
			podRequestsResources:     podRequestResources,
			preemptible:              map[string]corev1.ResourceList{},
			preemptibleInRRs:         map[string]map[types.UID]corev1.ResourceList{},
			nodeReservationStates:    map[string]*nodeReservationState{},
			nodeReservationDiagnosis: map[string]*nodeDiagnosisState{},
		},
		rInfo: rInfo,
	}
	for index := range allNodeReservationStates {
		v := allNodeReservationStates[index]
		state.nodeReservationStates[v.nodeName] = v
	}
	for i := range allNodeDiagnosisStates {
		v := allNodeDiagnosisStates[i]
		state.nodeReservationDiagnosis[v.nodeName] = v
	}

	return state, len(allNodeReservationStates) > 0, nil
}

func (pl *Plugin) listPreAllocatableCandidates(preAllocationMode schedulingv1alpha1.PreAllocationMode,
	rInfo *frameworkext.ReservationInfo) (map[string][]*corev1.Pod, error) {
	// Cluster mode: retrieve from cache which already has sorted candidates
	if preAllocationMode == schedulingv1alpha1.PreAllocationModeCluster {
		// In cluster mode, candidates are cached and sorted in reservationCache
		// We retrieve them directly from cache instead of listing from podLister
		// The cache is maintained by pod event handlers and updated incrementally
		// Returns early with cached data (already grouped by node and sorted)
		return pl.reservationCache.getAllPreAllocatableCandidates(), nil
	}

	// Default mode: use OwnerMatchers
	preAllocatableCandidatesOnNode := map[string][]*corev1.Pod{}
	podMap := map[types.UID]struct{}{}
	// TODO: Reduce the overhead of the finding pre-allocatable pods.
	for _, ownerMatcher := range rInfo.OwnerMatchers {
		if ownerMatcher.Selector == nil {
			continue
		}
		// FIXME: This step also list the unassigned pods.
		podList, err := pl.podLister.Pods(metav1.NamespaceAll).List(ownerMatcher.Selector)
		if err != nil {
			return nil, fmt.Errorf("list pods failed, ownerMatcher %+v, err: %w", ownerMatcher, err)
		}
		for i := range podList {
			candidatePod := podList[i]
			if candidatePod.Spec.NodeName == "" {
				continue
			}
			if candidatePod.Annotations != nil && candidatePod.Annotations[extension.AnnotationReservationAllocated] != "" {
				continue
			}
			if reservationutil.IsReservePod(candidatePod) {
				continue
			}
			if !ownerMatcher.Match(candidatePod) {
				continue
			}
			if _, ok := podMap[candidatePod.UID]; ok {
				continue
			}

			nodeName := candidatePod.Spec.NodeName
			podMap[candidatePod.UID] = struct{}{}
			preAllocatableCandidatesOnNode[nodeName] = append(preAllocatableCandidatesOnNode[nodeName], candidatePod)
		}
	}
	return preAllocatableCandidatesOnNode, nil
}

// checkReservationMatchedOrIgnored checks if the reservation is matched or can be ignored by the pod and
// updates the node diagnosis states.
func checkReservationMatchedOrIgnored(pod *corev1.Pod, rInfo *frameworkext.ReservationInfo, diagnosisState *nodeDiagnosisState, node *corev1.Node, podRequests corev1.ResourceList,
	reservationAffinity *reservationutil.RequiredReservationAffinity, exactMatchReservationSpec *extension.ExactMatchReservationSpec, affinityReservationName string, isReservationIgnored bool) bool {
	// pod specifies reservation ignored
	if isReservationIgnored {
		diagnosisState.ignored++
		return true
	}

	// pod matches the reservation owners
	if rInfo.MatchOwners(pod) {
		diagnosisState.ownerMatched++
		// Check the conditions by the following order:
		// 1. check the condition if it has a higher priority than others
		// 2. check the more common and fast conditions
		// 3. check the more complex conditions
		if len(affinityReservationName) > 0 {
			// If reservation name is specified, no longer check the unschedulable, affinity and taints.
			if !reservationAffinity.MatchName(rInfo.GetName()) {
				// Actually, the reservation name should be unique in the cluster. So if the pod specifies the
				// name, only the name matched reservation will check the conditions below.
				diagnosisState.nameUnmatched++
			} else if !extension.ExactMatchReservation(podRequests, rInfo.Allocatable, exactMatchReservationSpec) { // exactMatchSpec unmatched
				diagnosisState.notExactMatched++
			} else { // name matched
				diagnosisState.nameMatched++
				return true
			}
		} else if rInfo.IsUnschedulable() { // isUnschedulable
			diagnosisState.isUnschedulableUnmatched++
		} else if firstUnmatchedTaint, isTaintsUntolerated := reservationAffinity.FindMatchingUntoleratedTaint(rInfo.GetTaints(),
			reservationutil.DoNotScheduleTaintsFilter); isTaintsUntolerated { // taints not tolerated
			// TODO: support effect=PreferNoSchedule
			diagnosisState.taintsUnmatched++
			diagnosisState.taintsUnmatchedReasons[getDiagnosisTaintKey(&firstUnmatchedTaint)]++
		} else if !rInfo.MatchReservationAffinity(reservationAffinity, node) { // ReservationAffinity unmatched
			diagnosisState.affinityUnmatched++
		} else if !extension.ExactMatchReservation(podRequests, rInfo.Allocatable, exactMatchReservationSpec) { // exactMatchSpec unmatched
			diagnosisState.notExactMatched++
		} else { // matched
			return true
		}
	}

	return false
}

// NOTE: This function is coupled with listPreAllocatableCandidates for pre-allocatable pod selection:
//   - Default mode: OwnerMatchers (ObjectRef, Controller, Labels) are validated in listPreAllocatableCandidates
//     via ownerMatcher.Match(), so no need to re-check here.
//   - Cluster mode: Pods are retrieved from a global cache without reservation-specific OwnerMatchers validation.
//     The cache contains all pods matching the pre-allocatable label, and the reservation's owners field
//     is not used for filtering in cluster mode by design.
func checkPreAllocatableMatched(rInfo *frameworkext.ReservationInfo, candidatePod *corev1.Pod, diagnosisState *nodeDiagnosisState, node *corev1.Node) (bool, error) {
	diagnosisState.ownerMatched++
	reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(candidatePod)
	if err != nil {
		diagnosisState.errUnmatched++
		return false, fmt.Errorf("parse reservation affinity failed, err: %w", err)
	}
	exactMatchReservationSpec, err := extension.GetExactMatchReservationSpec(candidatePod.Annotations)
	if err != nil {
		diagnosisState.errUnmatched++
		return false, fmt.Errorf("parse exact match reservation spec failed, err: %w", err)
	}

	affinityReservationName := reservationAffinity.GetName()
	candidatePodRequests := resourceapi.PodRequests(candidatePod, resourceapi.PodResourcesOptions{})
	if len(affinityReservationName) > 0 {
		// If reservation name is specified, no longer check the unschedulable, affinity and taints.
		if !reservationAffinity.MatchName(rInfo.GetName()) {
			diagnosisState.nameUnmatched++
			return false, nil
		}
		if !extension.ExactMatchReservation(candidatePodRequests, rInfo.Allocatable, exactMatchReservationSpec) {
			diagnosisState.notExactMatched++
			return false, nil
		}

		return true, nil
	}

	if firstUnmatchedTaint, isTaintsUntolerated := reservationAffinity.FindMatchingUntoleratedTaint(rInfo.GetTaints(),
		reservationutil.DoNotScheduleTaintsFilter); isTaintsUntolerated { // taints not tolerated
		diagnosisState.taintsUnmatched++
		diagnosisState.taintsUnmatchedReasons[getDiagnosisTaintKey(&firstUnmatchedTaint)]++
		return false, nil
	}
	if !rInfo.MatchReservationAffinity(reservationAffinity, node) { // ReservationAffinity unmatched
		diagnosisState.affinityUnmatched++
		return false, nil
	}
	if !extension.ExactMatchReservation(candidatePodRequests, rInfo.Allocatable, exactMatchReservationSpec) { // exactMatchSpec unmatched
		diagnosisState.notExactMatched++
		return false, nil
	} // matched

	return true, nil
}

func preRestoreReservationResourcesForNode(logger klog.Logger, extender frameworkext.FrameworkExtender, pod *corev1.Pod,
	rInfo *frameworkext.ReservationInfo, nodeInfo *framework.NodeInfo, nodeRState *nodeReservationState, skipRestoreNodeInfo bool) error {
	matchedOrIgnored := nodeRState.matchedOrIgnored
	unmatched := nodeRState.unmatched
	preAllocatable := nodeRState.preAllocatablePods
	node := nodeInfo.Node()
	if klog.V(5).Enabled() {
		defer klog.InfoS("preRestoreReservationResourcesForNode succeeded", "pod", klog.KObj(pod), "node", node.Name, "matched", len(matchedOrIgnored), "unmatched", len(unmatched), "preAllocatable", len(preAllocatable))
	}

	// When the nodeInfo restore is skipped, we only record for the reservation restore state.
	if skipRestoreNodeInfo {
		var podRequested *framework.Resource
		if nodeInfo.Requested != nil {
			podRequested = nodeInfo.Requested.Clone()
		}
		rAllocated := corev1.ResourceList{}
		for _, rInfo := range matchedOrIgnored {
			util.AddResourceList(rAllocated, rInfo.Allocated)
		}

		nodeRState.rAllocated = framework.NewResource(rAllocated)
		nodeRState.podRequested = podRequested
		nodeRState.preRestored = true // no more pre-restore in the same cycle
		klog.V(5).InfoS("Skipping restore node info", "pod", klog.KObj(pod), "node", node.Name)
		return nil
	}

	if err := extender.Scheduler().GetCache().InvalidNodeInfo(logger, node.Name); err != nil {
		klog.ErrorS(err, "Failed to InvalidNodeInfo", "pod", klog.KObj(pod), "node", node.Name)
		return fmt.Errorf("invalidate NodeInfo failed, err: %w", err)
	}

	for _, rInfo := range unmatched {
		if err := restoreUnmatchedReservations(nodeInfo, rInfo); err != nil {
			klog.ErrorS(err, "Failed to restore unmatched reservations",
				"pod", klog.KObj(pod), "node", node.Name, "reservation", rInfo.GetName())
			return fmt.Errorf("restore unmatched reservation failed, err: %w", err)
		}
	}

	// Save requested state after trimmed by unmatched to support reservation allocate policy.
	var podRequested *framework.Resource
	if nodeInfo.Requested != nil {
		podRequested = nodeInfo.Requested.Clone()
	}

	rAllocated := corev1.ResourceList{}
	for _, rInfo := range matchedOrIgnored {
		if err := restoreMatchedReservation(nodeInfo, rInfo); err != nil {
			klog.ErrorS(err, "Failed to restore matched reservations",
				"pod", klog.KObj(pod), "node", node.Name, "reservation", rInfo.GetName())
			return fmt.Errorf("restore matched reservation failed, err: %w", err)
		}

		util.AddResourceList(rAllocated, rInfo.Allocated)
	}

	for _, preAllocatablePod := range preAllocatable {
		if err := restorePreAllocatablePods(nodeInfo, rInfo, preAllocatablePod); err != nil {
			klog.ErrorS(err, "Failed to restore pre-allocatable pods",
				"pod", klog.KObj(pod), "node", node.Name, "reservation", rInfo.GetName(), "preAllocatable", klog.KObj(preAllocatablePod))
			return fmt.Errorf("restore pre-allocatable pods failed, err: %w", err)
		}
	}

	nodeRState.rAllocated = framework.NewResource(rAllocated)
	nodeRState.podRequested = podRequested
	nodeRState.preRestored = true // no more pre-restore in the same cycle

	return nil
}

func restoreReservationResourcesForNode(ctx context.Context, cycleState *framework.CycleState, extender frameworkext.FrameworkExtender,
	pod *corev1.Pod, rInfo *frameworkext.ReservationInfo, nodeInfo *framework.NodeInfo, nodeRState *nodeReservationState, skipRestoreNodeInfo bool) (bool, *framework.Status) {
	matchedOrIgnored := nodeRState.matchedOrIgnored
	unmatched := nodeRState.unmatched
	preAllocatable := nodeRState.preAllocatablePods
	node := nodeInfo.Node()
	logger := klog.FromContext(ctx)
	if klog.V(5).Enabled() {
		defer klog.InfoS("restoreReservationResourcesForNode succeeded", "pod", klog.KObj(pod), "node", node.Name, "matched", len(matchedOrIgnored), "unmatched", len(unmatched), "preAllocatable", len(preAllocatable))
	}

	// Some attributes like podAffinity and topologySpreadConstraints is pre-processed in the PreFilter phase,
	// so we cannot delay the restoration to the Filter.
	if !nodeRState.preRestored {
		err := preRestoreReservationResourcesForNode(logger, extender, pod, rInfo, nodeInfo, nodeRState, skipRestoreNodeInfo)
		if err != nil {
			return false, framework.AsStatus(err)
		}
	}

	var status *framework.Status
	_, status = extender.RunReservationExtensionRestoreReservation(ctx, cycleState, pod, matchedOrIgnored, unmatched, nodeInfo)
	if !status.IsSuccess() {
		klog.ErrorS(status.AsError(), "Failed to run RestoreReservation",
			"pod", klog.KObj(pod), "node", node.Name,
			"matchedOrIgnored", len(matchedOrIgnored), "unmatched", len(unmatched))
		return false, status
	}

	if len(preAllocatable) > 0 {
		_, status = extender.RunReservationExtensionRestoreReservationPreAllocation(ctx, cycleState, rInfo, preAllocatable, nodeInfo)
		if !status.IsSuccess() {
			klog.ErrorS(status.AsError(), "Failed to run RestoreReservationPreAllocation",
				"pod", klog.KObj(pod), "node", node.Name, "preAllocatable", len(preAllocatable))
			return false, status
		}
	}

	nodeRState.finalRestored = true // no more restore in the same cycle
	return true, nil
}

func restoreMatchedReservation(nodeInfo *framework.NodeInfo, rInfo *frameworkext.ReservationInfo) error {
	reservePod := rInfo.GetReservePod()

	// Retain ports that are not used by other Pods. These ports need to be erased from NodeInfo.UsedPorts,
	// otherwise it may cause Pod port conflicts
	allocatablePorts := util.CloneHostPorts(rInfo.AllocatablePorts)
	if len(allocatablePorts) > 0 {
		reservePod = reservePod.DeepCopy()
		util.RemoveHostPorts(allocatablePorts, rInfo.AllocatedPorts)
		util.ResetHostPorts(reservePod, allocatablePorts)
	}

	// When AllocateOnce is disabled, some resources may have been allocated,
	// and an additional resource record will be accumulated at this time.
	// Even if the Reservation is not bound by the Pod (e.g. Reservation is enabled with AllocateOnce),
	// these resources held by the Reservation need to be returned, to ensure that
	// the Pod can pass through each filter plugin during scheduling.
	// The returned resources include scalar resources such as CPU/Memory, ports etc..
	if err := nodeInfo.RemovePod(reservePod); err != nil {
		return err
	}

	return nil
}

func restoreUnmatchedReservations(nodeInfo *framework.NodeInfo, rInfo *frameworkext.ReservationInfo) error {
	// Here len(rInfo.AssignedPods) == 0 is always false because it was checked before.
	if rInfo.GetAllocatedPods() == 0 {
		return nil
	}

	// Reservations and Pods that consume the Reservations are cumulative in resource accounting.
	// For example, on a 32C machine, ReservationA reserves 8C, and then PodA uses ReservationA to allocate 4C,
	// then the record on NodeInfo is that 12C is allocated. But in fact it should be calculated according to 8C,
	// so we need to return some resources.
	updateNodeInfoRequestedForUnmatched(nodeInfo, rInfo)
	return nil
}

func restorePreAllocatablePods(nodeInfo *framework.NodeInfo, rInfo *frameworkext.ReservationInfo, preAllocatablePod *corev1.Pod) error {
	if err := nodeInfo.RemovePod(preAllocatablePod); err != nil {
		return err
	}

	return nil
}

func isPodAllNodesPreRestoreRequired(pod *corev1.Pod) bool {
	// If a pod specifies required topologySpreadConstraints, podAffinities and podAntiAffinities, we should do the
	// node-level preRestore in the BeforePreFilter for each node, even when the LazyReservationRestore is enabled.
	// FIXME: The existing podAffinities/podAntiAffinities on nodes are not considered.
	if pod.Spec.Affinity != nil && (pod.Spec.Affinity.PodAffinity != nil && pod.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil ||
		pod.Spec.Affinity.PodAntiAffinity != nil && pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil) {
		return true
	}
	for _, c := range pod.Spec.TopologySpreadConstraints {
		if c.WhenUnsatisfiable == corev1.DoNotSchedule {
			return true
		}
	}
	return false
}

func updateNodeInfoRequestedForUnmatched(n *framework.NodeInfo, rInfo *frameworkext.ReservationInfo) {
	res, non0MilliCPU, non0Mem := rInfo.GetAllocatedResource()
	n.Requested.MilliCPU -= res.MilliCPU
	n.Requested.Memory -= res.Memory
	n.Requested.EphemeralStorage -= res.EphemeralStorage
	if n.Requested.ScalarResources == nil && len(res.ScalarResources) > 0 {
		n.Requested.ScalarResources = map[corev1.ResourceName]int64{}
	}
	for rName, rQuant := range res.ScalarResources {
		n.Requested.ScalarResources[rName] -= rQuant
	}
	n.NonZeroRequested.MilliCPU -= non0MilliCPU
	n.NonZeroRequested.Memory -= non0Mem
}

func parseSpecificNodesFromAffinity(pod *corev1.Pod) (sets.String, *framework.Status) {
	affinity := pod.Spec.Affinity
	if affinity == nil ||
		affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil ||
		len(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		return nil, nil
	}

	// Check if there is affinity to a specific node and return it.
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	var nodeNames sets.String
	for _, t := range terms {
		var termNodeNames sets.String
		for _, r := range t.MatchFields {
			if r.Key == metav1.ObjectNameField && r.Operator == corev1.NodeSelectorOpIn {
				// The requirements represent ANDed constraints, and so we need to
				// find the intersection of nodes.
				s := sets.NewString(r.Values...)
				if termNodeNames == nil {
					termNodeNames = s
				} else {
					termNodeNames = termNodeNames.Intersection(s)
				}
			}
		}
		if termNodeNames == nil {
			// If this term has no node.Name field affinity,
			// then all nodes are eligible because the terms are ORed.
			return nil, nil
		}
		// If the set is empty, it means the terms had affinity to different
		// sets of nodes, and since they are ANDed, then the pod will not match any node.
		if len(termNodeNames) == 0 {
			return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, "pod affinity terms conflict")
		}
		nodeNames = nodeNames.Union(termNodeNames)
	}
	return nodeNames, nil
}

func getDiagnosisTaintKey(taint *corev1.Taint) string {
	return fmt.Sprintf("{%s: %s}", taint.Key, taint.Value)
}

func (pl *Plugin) BeforeFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) (*corev1.Pod, *framework.NodeInfo, bool, *framework.Status) {
	// Both the reserve pod or the normal pod should consider the nominated reserve pods.
	nominatedReservationInfos := pl.nominator.NominatedReservePodForNode(nodeInfo.Node().Name)
	if len(nominatedReservationInfos) == 0 {
		return pod, nodeInfo, false, nil
	}

	if nodeInfo.Node() == nil {
		// This may happen only in tests.
		return pod, nodeInfo, false, nil
	}

	nodeInfoOut := nodeInfo.Clone()

	for _, rInfo := range nominatedReservationInfos {
		if schedulingcorev1.PodPriority(rInfo.Pod) >= schedulingcorev1.PodPriority(pod) && rInfo.Pod.UID != pod.UID {
			pInfo, _ := framework.NewPodInfo(rInfo.Pod)
			nodeInfoOut.AddPodInfo(pInfo)
			status := pl.handle.RunPreFilterExtensionAddPod(ctx, cycleState, pod, pInfo, nodeInfoOut)
			if !status.IsSuccess() {
				return pod, nodeInfo, false, status
			}
			klog.V(4).Infof("nodeName %s, to schedule pod %s (reserve pod %s) with nominated reservation %s",
				nodeInfo.Node().Name, klog.KObj(pod),
				reservationutil.GetReservationNameFromReservePod(pod),
				reservationutil.GetReservationNameFromReservePod(rInfo.Pod))
		}
	}

	return pod, nodeInfoOut, true, nil
}

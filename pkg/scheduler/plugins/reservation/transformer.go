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
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func (pl *Plugin) BeforePreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *framework.Status) {
	state, restored, status := pl.prepareMatchReservationState(ctx, cycleState, pod)
	if !status.IsSuccess() {
		return nil, false, status
	}
	cycleState.Write(stateKey, state)
	return pod, restored, nil
}

func (pl *Plugin) prepareMatchReservationState(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*stateData, bool, *framework.Status) {
	logger := klog.FromContext(ctx)
	reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(pod)
	if err != nil {
		klog.ErrorS(err, "Failed to parse reservation affinity", "pod", klog.KObj(pod))
		return nil, false, framework.AsStatus(err)
	}
	affinityReservationName := reservationAffinity.GetName()
	isReservationIgnored := extension.IsReservationIgnored(pod)

	specificNodes, status := parseSpecificNodesFromAffinity(pod)
	if !status.IsSuccess() {
		return nil, false, status
	}
	requiredNodeAffinity := nodeaffinity.GetRequiredNodeAffinity(pod)

	podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	exactMatchReservationSpec, err := extension.GetExactMatchReservationSpec(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "Failed to parse exact match reservation spec", "pod", klog.KObj(pod))
		return nil, false, framework.AsStatus(err)
	}

	var stateIndex, diagnosisIndex int32
	allNodes := pl.reservationCache.listAllNodes()
	allNodeReservationStates := make([]*nodeReservationState, len(allNodes))
	allNodeDiagnosisStates := make([]*nodeDiagnosisState, len(allNodes))
	allPluginToRestoreState := make([]frameworkext.PluginToReservationRestoreStates, len(allNodes))

	isReservedPod := reservationutil.IsReservePod(pod)
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := parallelize.NewErrorChannel()

	extender, _ := pl.handle.(frameworkext.FrameworkExtender)
	if extender != nil {
		status := extender.RunReservationExtensionPreRestoreReservation(ctx, cycleState, pod)
		if !status.IsSuccess() {
			return nil, false, status
		}
	}

	// checkReservationMatchedOrIgnored checks if the reservation is matched or can be ignored by the pod and
	// updates the node diagnosis states.
	checkReservationMatchedOrIgnored := func(rInfo *frameworkext.ReservationInfo, node *corev1.Node, diagnosisState *nodeDiagnosisState) bool {
		// Since nest reservation is not supported, a reserve pod cannot match a reservation.
		if isReservedPod {
			return false
		}

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
				taintKey := getDiagnosisTaintKey(&firstUnmatchedTaint)
				diagnosisState.taintsUnmatchedReasons[taintKey]++
			} else if !matchReservationAffinity(node, rInfo, reservationAffinity) { // ReservationAffinity unmatched
				diagnosisState.affinityUnmatched++
			} else if !extension.ExactMatchReservation(podRequests, rInfo.Allocatable, exactMatchReservationSpec) { // exactMatchSpec unmatched
				diagnosisState.notExactMatched++
			} else { // matched
				return true
			}
		}

		return false
	}

	processNode := func(i int) {
		nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(allNodes[i])
		if err != nil {
			klog.Warningf("Failed to get NodeInfo of %s during reservation's BeforePreFilter for pod: %v, err: %v", allNodes[i], klog.KObj(pod), err)
			return
		}
		node := nodeInfo.Node()
		if node == nil {
			klog.V(4).InfoS("BeforePreFilter failed to get node", "pod", klog.KObj(pod), "nodeInfo", nodeInfo)
			return
		}

		if specificNodes.Len() > 0 {
			if !specificNodes.Has(node.Name) {
				return
			}
		} else if match, _ := requiredNodeAffinity.Match(node); !match {
			return
		}

		var unmatched, matchedOrIgnored []*frameworkext.ReservationInfo
		diagnosisState := &nodeDiagnosisState{
			nodeName:                 node.Name,
			ignored:                  0,
			ownerMatched:             0,
			nameUnmatched:            0,
			isUnschedulableUnmatched: 0,
			affinityUnmatched:        0,
			notExactMatched:          0,
			taintsUnmatched:          0,
			taintsUnmatchedReasons:   map[string]int{},
		}

		status := pl.reservationCache.forEachAvailableReservationOnNode(node.Name, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
			if !rInfo.IsAvailable() || rInfo.ParseError != nil {
				return true, nil
			}

			// In this case, the Controller has not yet updated the status of the Reservation to Succeeded,
			// but in fact it can no longer be used for allocation. So it's better to skip first.
			if rInfo.IsAllocateOnce() && rInfo.GetAllocatedPods() > 0 {
				return true, nil
			}

			// check if the reservation matches or can be ignored by the pod
			isMatchedOrIgnored := checkReservationMatchedOrIgnored(rInfo, node, diagnosisState)

			if isMatchedOrIgnored { // reservation is matched or ignored for the pod
				matchedOrIgnored = append(matchedOrIgnored, rInfo.Clone())
			} else if rInfo.GetAllocatedPods() > 0 { // reservation is unmatched and not ignored
				unmatched = append(unmatched, rInfo.Clone())
			}

			return true, nil
		})
		if !status.IsSuccess() {
			err = status.AsError()
			klog.ErrorS(err, "Failed to forEach reservations on node", "pod", klog.KObj(pod), "node", node.Name)
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

		if err := extender.Scheduler().GetCache().InvalidNodeInfo(logger, node.Name); err != nil {
			klog.ErrorS(err, "Failed to InvalidNodeInfo", "pod", klog.KObj(pod), "node", node.Name)
			errCh.SendErrorWithCancel(err, cancel)
			return
		}

		for _, rInfo := range unmatched {
			if err = restoreUnmatchedReservations(nodeInfo, rInfo); err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}
		// Save requested state after trimmed by unmatched to support reservation allocate policy.
		podRequested := nodeInfo.Requested.Clone()

		podInfoMap := make(map[types.UID]*framework.PodInfo)
		for _, podInfo := range nodeInfo.Pods {
			if !reservationutil.IsReservePod(podInfo.Pod) {
				podInfoMap[podInfo.Pod.UID] = podInfo
			}
		}

		rAllocated := corev1.ResourceList{}
		for _, rInfo := range matchedOrIgnored {
			if err = restoreMatchedReservation(nodeInfo, rInfo, podInfoMap); err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}

			util.AddResourceList(rAllocated, rInfo.Allocated)
		}

		var pluginToRestoreState frameworkext.PluginToReservationRestoreStates
		if extender != nil {
			var status *framework.Status
			pluginToRestoreState, status = extender.RunReservationExtensionRestoreReservation(ctx, cycleState, pod, matchedOrIgnored, unmatched, nodeInfo)
			if !status.IsSuccess() {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}

		if len(matchedOrIgnored) > 0 || len(unmatched) > 0 {
			index := atomic.AddInt32(&stateIndex, 1)
			allNodeReservationStates[index-1] = &nodeReservationState{
				nodeName:         node.Name,
				matchedOrIgnored: matchedOrIgnored,
				podRequested:     podRequested,
				rAllocated:       framework.NewResource(rAllocated),
			}
			allPluginToRestoreState[index-1] = pluginToRestoreState
		}
	}
	pl.handle.Parallelizer().Until(parallelCtx, len(allNodes), processNode, "transformNodesWithReservation")
	err = errCh.ReceiveError()
	if err != nil {
		klog.ErrorS(err, "Failed to find matched or unmatched reservations", "pod", klog.KObj(pod))
		return nil, false, framework.AsStatus(err)
	}

	allNodeReservationStates = allNodeReservationStates[:stateIndex]
	allPluginToRestoreState = allPluginToRestoreState[:stateIndex]
	allNodeDiagnosisStates = allNodeDiagnosisStates[:diagnosisIndex]
	podRequestResources := framework.NewResource(podRequests)
	state := &stateData{
		schedulingStateData: schedulingStateData{
			hasAffinity:              reservationAffinity != nil,
			reservationName:          affinityReservationName,
			podRequests:              podRequests,
			podRequestsResources:     podRequestResources,
			preemptible:              map[string]corev1.ResourceList{},
			preemptibleInRRs:         map[string]map[types.UID]corev1.ResourceList{},
			nodeReservationStates:    map[string]nodeReservationState{},
			nodeReservationDiagnosis: map[string]*nodeDiagnosisState{},
		},
	}
	pluginToNodeReservationRestoreState := frameworkext.PluginToNodeReservationRestoreStates{}
	for index := range allNodeReservationStates {
		v := allNodeReservationStates[index]
		state.nodeReservationStates[v.nodeName] = *v
		for pluginName, pluginState := range allPluginToRestoreState[index] {
			if pluginState == nil {
				continue
			}
			nodeRestoreStates := pluginToNodeReservationRestoreState[pluginName]
			if nodeRestoreStates == nil {
				nodeRestoreStates = frameworkext.NodeReservationRestoreStates{}
				pluginToNodeReservationRestoreState[pluginName] = nodeRestoreStates
			}
			nodeRestoreStates[v.nodeName] = pluginState
		}
	}
	for i := range allNodeDiagnosisStates {
		v := allNodeDiagnosisStates[i]
		state.nodeReservationDiagnosis[v.nodeName] = v
	}
	if extender != nil {
		status := extender.RunReservationExtensionFinalRestoreReservation(ctx, cycleState, pod, pluginToNodeReservationRestoreState)
		if !status.IsSuccess() {
			return nil, false, status
		}
	}

	return state, len(allNodeReservationStates) > 0, nil
}

func (pl *Plugin) AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	return nil
}

func restoreMatchedReservation(nodeInfo *framework.NodeInfo, rInfo *frameworkext.ReservationInfo, podInfoMap map[types.UID]*framework.PodInfo) error {
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
	reservePod := rInfo.GetReservePod()
	updateNodeInfoRequested(nodeInfo, reservePod, -1)
	remainedResource := quotav1.SubtractWithNonNegativeResult(rInfo.Allocatable, rInfo.Allocated)
	if !quotav1.IsZero(remainedResource) {
		reservePod = &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{Requests: remainedResource},
					},
				},
			},
		}
		updateNodeInfoRequested(nodeInfo, reservePod, 1)
	}
	return nil
}

func updateNodeInfoRequested(n *framework.NodeInfo, pod *corev1.Pod, sign int64) {
	res, non0CPU, non0Mem := calculateResource(pod)
	n.Requested.MilliCPU += sign * res.MilliCPU
	n.Requested.Memory += sign * res.Memory
	n.Requested.EphemeralStorage += sign * res.EphemeralStorage
	if n.Requested.ScalarResources == nil && len(res.ScalarResources) > 0 {
		n.Requested.ScalarResources = map[corev1.ResourceName]int64{}
	}
	for rName, rQuant := range res.ScalarResources {
		n.Requested.ScalarResources[rName] += sign * rQuant
	}
	n.NonZeroRequested.MilliCPU += sign * non0CPU
	n.NonZeroRequested.Memory += sign * non0Mem
}

func max(a, b int64) int64 {
	if a >= b {
		return a
	}
	return b
}

// resourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers) + overHead
func calculateResource(pod *corev1.Pod) (res framework.Resource, non0CPU int64, non0Mem int64) {
	resPtr := &res
	for _, c := range pod.Spec.Containers {
		resPtr.Add(c.Resources.Requests)
		non0CPUReq, non0MemReq := schedutil.GetNonzeroRequests(&c.Resources.Requests)
		non0CPU += non0CPUReq
		non0Mem += non0MemReq
		// No non-zero resources for GPUs or opaque resources.
	}

	for _, ic := range pod.Spec.InitContainers {
		resPtr.SetMaxResource(ic.Resources.Requests)
		non0CPUReq, non0MemReq := schedutil.GetNonzeroRequests(&ic.Resources.Requests)
		non0CPU = max(non0CPU, non0CPUReq)
		non0Mem = max(non0Mem, non0MemReq)
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil {
		resPtr.Add(pod.Spec.Overhead)
		if _, found := pod.Spec.Overhead[corev1.ResourceCPU]; found {
			non0CPU += pod.Spec.Overhead.Cpu().MilliValue()
		}

		if _, found := pod.Spec.Overhead[corev1.ResourceMemory]; found {
			non0Mem += pod.Spec.Overhead.Memory().Value()
		}
	}

	return
}

// matchReservationAffinity returns the statuses of whether the reservation affinity matches, whether the reservation
// taints are tolerated, and whether the reservation name matches.
func matchReservationAffinity(node *corev1.Node, reservation *frameworkext.ReservationInfo, reservationAffinity *reservationutil.RequiredReservationAffinity) bool {
	if reservationAffinity != nil {
		// NOTE: There are some special scenarios.
		// For example, the AZ where the Pod wants to select the Reservation is cn-hangzhou, but the Reservation itself
		// does not have this information, so it needs to perceive the label of the Node when Matching Affinity.
		// FIXME(saintube): clean up the default node labels casting and preserve optional labels
		// https://github.com/koordinator-sh/koordinator/issues/2208
		fakeNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   reservation.GetName(),
				Labels: map[string]string{},
			},
		}
		for k, v := range node.Labels {
			fakeNode.Labels[k] = v
		}
		for k, v := range reservation.GetObject().GetLabels() {
			fakeNode.Labels[k] = v
		}
		return reservationAffinity.MatchAffinity(fakeNode)
	}
	return true
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

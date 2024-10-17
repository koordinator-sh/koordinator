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

package nodenumaresource

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const reservationRestoreStateKey = Name + "/reservationRestoreState"

type reservationRestoreStateData struct {
	lock        sync.RWMutex
	nodeToState frameworkext.NodeReservationRestoreStates
}

type nodeReservationRestoreStateData struct {
	matched   map[types.UID]reservationAlloc
	unmatched map[types.UID]reservationAlloc

	mergedMatchedRemainCPUs    cpuset.CPUSet
	mergedMatchedAllocatedCPUs cpuset.CPUSet
	mergedMatchedAllocatable   map[int]corev1.ResourceList
	mergedMatchedAllocated     map[int]corev1.ResourceList
	mergedUnmatchedUsed        map[int]corev1.ResourceList
}

type reservationAlloc struct {
	rInfo *frameworkext.ReservationInfo

	allocatableCPUs cpuset.CPUSet // allocatable/total reserved CPUs
	allocatedCPUs   cpuset.CPUSet // allocated CPUs
	remainedCPUs    cpuset.CPUSet // unallocated reserved CPUs

	allocatable map[int]corev1.ResourceList
	allocated   map[int]corev1.ResourceList
	remained    map[int]corev1.ResourceList
}

func getReservationRestoreState(cycleState *framework.CycleState) *reservationRestoreStateData {
	var state *reservationRestoreStateData
	value, err := cycleState.Read(reservationRestoreStateKey)
	if err == nil {
		state, _ = value.(*reservationRestoreStateData)
	}
	if state == nil || state.nodeToState == nil {
		state = &reservationRestoreStateData{
			nodeToState: map[string]interface{}{},
		}
	}
	return state
}

func (s *reservationRestoreStateData) Clone() framework.StateData {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s
}

func (s *reservationRestoreStateData) getNodeState(nodeName string) *nodeReservationRestoreStateData {
	s.lock.RLock()
	defer s.lock.RUnlock()
	val := s.nodeToState[nodeName]
	ns, ok := val.(*nodeReservationRestoreStateData)
	if !ok {
		ns = &nodeReservationRestoreStateData{}
	}
	return ns
}

func (s *reservationRestoreStateData) setNodeState(nodeName string, nodeState interface{}) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.nodeToState[nodeName] = nodeState
}

func (s *reservationRestoreStateData) clearData() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.nodeToState = map[string]interface{}{}
}

func (rs *nodeReservationRestoreStateData) mergeReservationAllocations() {
	unmatched := rs.unmatched
	if len(unmatched) > 0 {
		mergedUnmatchedUsed := map[int]corev1.ResourceList{}
		for _, alloc := range unmatched {
			used := subtractAllocated(copyAllocated(alloc.allocatable), alloc.remained, true)
			mergedUnmatchedUsed = appendAllocated(mergedUnmatchedUsed, used)
		}
		rs.mergedUnmatchedUsed = mergedUnmatchedUsed
	}

	matched := rs.matched
	if len(matched) > 0 {
		mergedMatchedAllocatable := map[int]corev1.ResourceList{}
		mergedMatchedAllocated := map[int]corev1.ResourceList{}
		mergedRemainCPUSetBuilder := cpuset.NewCPUSetBuilder()
		mergedAllocatedCPUSetBuilder := cpuset.NewCPUSetBuilder()
		for _, alloc := range matched {
			mergedMatchedAllocatable = appendAllocated(mergedMatchedAllocatable, alloc.allocatable)
			mergedMatchedAllocated = appendAllocated(mergedMatchedAllocated, alloc.allocated)
			mergedRemainCPUSetBuilder.Add(alloc.remainedCPUs.ToSliceNoSort()...)
			mergedAllocatedCPUSetBuilder.Add(alloc.allocatedCPUs.ToSliceNoSort()...)
		}
		rs.mergedMatchedAllocatable = mergedMatchedAllocatable
		rs.mergedMatchedAllocated = mergedMatchedAllocated
		rs.mergedMatchedRemainCPUs = mergedRemainCPUSetBuilder.Result()
		rs.mergedMatchedAllocatedCPUs = mergedAllocatedCPUSetBuilder.Result()
	}

	return
}

func copyAllocated(m map[int]corev1.ResourceList) map[int]corev1.ResourceList {
	result := map[int]corev1.ResourceList{}
	for numaNodeID, numaResource := range m {
		if numaResource != nil {
			result[numaNodeID] = numaResource.DeepCopy()
		}
	}
	return result
}

func subtractAllocated(m map[int]corev1.ResourceList, allocated map[int]corev1.ResourceList, withNonNegativeResult bool) map[int]corev1.ResourceList {
	if m == nil {
		m = map[int]corev1.ResourceList{}
	}
	for numaNodeID, numaResource := range allocated {
		if withNonNegativeResult {
			m[numaNodeID] = quotav1.SubtractWithNonNegativeResult(m[numaNodeID], numaResource)
		} else {
			m[numaNodeID] = quotav1.Subtract(m[numaNodeID], numaResource)
		}
	}
	return m
}

func appendAllocated(m map[int]corev1.ResourceList, allocatedList ...map[int]corev1.ResourceList) map[int]corev1.ResourceList {
	if m == nil {
		m = map[int]corev1.ResourceList{}
	}
	for _, allocated := range allocatedList {
		for numaNodeID, numaResource := range allocated {
			m[numaNodeID] = quotav1.Add(m[numaNodeID], numaResource)
		}
	}
	return m
}

func (p *Plugin) PreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	state := getReservationRestoreState(cycleState)
	cycleState.Write(reservationRestoreStateKey, state)
	return nil
}

func (p *Plugin) RestoreReservation(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, matched []*frameworkext.ReservationInfo, unmatched []*frameworkext.ReservationInfo, nodeInfo *framework.NodeInfo) (interface{}, *framework.Status) {
	nodeName := nodeInfo.Node().Name
	filterFn := func(reservations []*frameworkext.ReservationInfo) map[types.UID]reservationAlloc {
		if len(reservations) == 0 {
			return nil
		}
		result := make(map[types.UID]reservationAlloc, len(reservations))
		for _, rInfo := range reservations {
			reservePod := rInfo.GetReservePod()
			var allocatable, allocatedNUMAResource, remained map[int]corev1.ResourceList
			func() {
				allocatableNUMAResource, ok := p.resourceManager.GetAllocatedNUMAResource(nodeName, reservePod.UID)
				if !ok || len(allocatableNUMAResource) == 0 {
					return
				}
				allocatable = allocatableNUMAResource
				for _, pod := range rInfo.AssignedPods {
					podNUMAResources, ok := p.resourceManager.GetAllocatedNUMAResource(nodeName, pod.UID)
					if !ok || len(podNUMAResources) == 0 {
						continue
					}
					allocatedNUMAResource = appendAllocated(allocatedNUMAResource, podNUMAResources)
				}
				remained = subtractAllocated(copyAllocated(allocatableNUMAResource), allocatedNUMAResource, false)
			}()
			var remainCPUs, allocatableCPUs, allocatedCPUs cpuset.CPUSet
			func() {
				reservedCPUs, ok := p.resourceManager.GetAllocatedCPUSet(nodeName, reservePod.UID)
				if !ok || reservedCPUs.IsEmpty() {
					return
				}
				allocatableCPUs = reservedCPUs.Clone()
				allocatedCPUs = reservedCPUs.Clone()
				for _, pod := range rInfo.AssignedPods {
					podCPUs, ok := p.resourceManager.GetAllocatedCPUSet(nodeName, pod.UID)
					if !ok || podCPUs.IsEmpty() {
						continue
					}
					allocatedCPUs = allocatedCPUs.Union(allocatableCPUs.Intersection(podCPUs))
					reservedCPUs = reservedCPUs.Difference(podCPUs)
				}
				remainCPUs = reservedCPUs
			}()
			if allocatable != nil || !remainCPUs.IsEmpty() || !allocatableCPUs.IsEmpty() {
				result[rInfo.UID()] = reservationAlloc{
					rInfo:           rInfo,
					allocatableCPUs: allocatableCPUs,
					allocatedCPUs:   allocatedCPUs,
					remainedCPUs:    remainCPUs,
					allocatable:     allocatable,
					allocated:       allocatedNUMAResource,
					remained:        remained,
				}
			}
		}
		return result
	}
	filteredMatched := filterFn(matched)
	filteredUnmatched := filterFn(unmatched)
	if len(filteredMatched) == 0 && len(filteredUnmatched) == 0 {
		return nil, nil
	}
	s := &nodeReservationRestoreStateData{
		matched:   filteredMatched,
		unmatched: filteredUnmatched,
	}
	s.mergeReservationAllocations()

	// also complete the nodeRestoreState in cycleState
	state := getReservationRestoreState(cycleState)
	state.setNodeState(nodeName, s)
	cycleState.Write(reservationRestoreStateKey, state)

	return s, nil
}

// DEPRECATED
func (p *Plugin) FinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeToStates frameworkext.NodeReservationRestoreStates) *framework.Status {
	state := &reservationRestoreStateData{
		nodeToState: nodeToStates,
	}
	cycleState.Write(reservationRestoreStateKey, state)
	return nil
}

func tryAllocateFromReservation(
	manager ResourceManager,
	restoreState *nodeReservationRestoreStateData,
	resourceOptions *ResourceOptions,
	matchedOrIgnoredReservations map[types.UID]reservationAlloc,
	pod *corev1.Pod,
	node *corev1.Node,
) (*PodAllocation, *framework.Status) {
	if len(matchedOrIgnoredReservations) == 0 {
		return nil, nil
	}

	if extension.IsReservationIgnored(pod) {
		return tryAllocateIgnoreReservation(manager, restoreState, resourceOptions, matchedOrIgnoredReservations, pod, node)
	}

	var hasSatisfiedReservation bool
	var result *PodAllocation
	var status *framework.Status

	reusableResource := appendAllocated(nil, restoreState.mergedUnmatchedUsed, restoreState.mergedMatchedAllocated)
	preferredCPUs := restoreState.mergedMatchedAllocatedCPUs.Clone()
	preemptibleCPUs := cpuset.NewCPUSet()

	// update with node preemption state
	var nodePreemptionAlloc *preemptibleAlloc
	if resourceOptions.nodePreemptionState != nil && resourceOptions.nodePreemptionState.nodeAlloc != nil {
		nodePreemptionAlloc = resourceOptions.nodePreemptionState.nodeAlloc
		reusableResource = nodePreemptionAlloc.AppendNUMAResources(reusableResource)
		preemptibleCPUs = nodePreemptionAlloc.AppendCPUSet(preemptibleCPUs)
	}

	var reservationReasons []*framework.Status
	for _, alloc := range matchedOrIgnoredReservations {
		rInfo := alloc.rInfo

		resourceOptions.reusableResources = appendAllocated(nil, reusableResource, alloc.remained)
		resourceOptions.preferredCPUs = preferredCPUs.Union(alloc.remainedCPUs)
		resourceOptions.preemptibleCPUs = preemptibleCPUs
		resourceOptions.requiredResources = nil

		// update with reservation preemption state
		var reservationPreemptionAlloc *preemptibleAlloc
		if resourceOptions.nodePreemptionState != nil && resourceOptions.nodePreemptionState.reservationsAlloc != nil &&
			resourceOptions.nodePreemptionState.reservationsAlloc[rInfo.UID()] != nil {
			reservationPreemptionAlloc = resourceOptions.nodePreemptionState.reservationsAlloc[rInfo.UID()]
			resourceOptions.reusableResources = reservationPreemptionAlloc.AppendNUMAResources(resourceOptions.reusableResources)
			resourceOptions.preemptibleCPUs = reservationPreemptionAlloc.AppendCPUSet(resourceOptions.preemptibleCPUs)
		}

		allocatePolicy := rInfo.GetAllocatePolicy()
		// TODO: Currently the ReservationAllocatePolicyDefault is actually implemented as
		//       ReservationAllocatePolicyAligned. Need to re-visit the policies.
		if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyDefault ||
			allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyAligned {
			result, status = manager.Allocate(node, pod, resourceOptions)
			if !status.IsSuccess() {
				klog.V(5).InfoS("failed to allocated from reservation",
					"reservation", rInfo.Reservation.Name, "pod", pod.Name, "node", node.Name,
					"status", status.Message(), "hint", resourceOptions.hint,
					"reusableResources", resourceOptions.reusableResources,
					"preferredCPUs", resourceOptions.preferredCPUs,
					"preemptibleCPUs", resourceOptions.preemptibleCPUs)
				reservationReasons = append(reservationReasons, status)
				continue
			}

			hasSatisfiedReservation = true
			break

		} else if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
			// TODO 这里仿照 deviceShare 的逻辑这样处理，后续需要再琢磨一下是否有必要
			_, status = manager.Allocate(node, pod, resourceOptions)
			if !status.IsSuccess() {
				klog.V(5).InfoS("failed to allocated from reservation",
					"reservation", rInfo.Reservation.Name, "pod", pod.Name, "node", node.Name,
					"policy", allocatePolicy, "status", status.Message(), "hint", resourceOptions.hint,
					"reusableResources", resourceOptions.reusableResources,
					"preferredCPUs", resourceOptions.preferredCPUs,
					"preemptibleCPUs", resourceOptions.preemptibleCPUs)
				if klog.V(6).Enabled() {
					logStruct(reflect.ValueOf(resourceOptions), "options", 6)
					logStruct(reflect.ValueOf(restoreState), "restoreState", 6)
				}
				reservationReasons = append(reservationReasons, status)
				continue
			}

			reservedCPUs := alloc.remainedCPUs
			// For a Restricted reservation, pod should allocate CPUSet cpus only from it.
			resourceOptions.requiredResources = alloc.remained
			resourceOptions.preferredCPUs = alloc.remainedCPUs
			resourceOptions.preemptibleCPUs = cpuset.NewCPUSet()
			if nodePreemptionAlloc != nil {
				// Considering the reservation-ignored pod does not count in allocatedCPUs, we try preempting node CPUs
				// which are overlapped with the reservation, while the preferred CPUs are already ready.
				nodePreemptionAlloc = resourceOptions.nodePreemptionState.nodeAlloc
				nodePreemptibleCPUs := nodePreemptionAlloc.AppendCPUSet(cpuset.NewCPUSet())
				nodePreemptibleCPUs = alloc.allocatableCPUs.Intersection(nodePreemptibleCPUs)
				resourceOptions.preemptibleCPUs = resourceOptions.preemptibleCPUs.Union(nodePreemptibleCPUs)
			}
			if reservationPreemptionAlloc != nil {
				resourceOptions.requiredResources = reservationPreemptionAlloc.AppendNUMAResources(resourceOptions.requiredResources)
				// For a Restricted reservation, we should restore the preemptible allocated CPUs twice, where one
				// reference is by the reservation itself and the another is by the preempted owner pods.
				reservationPreemptibleCPUs := reservationPreemptionAlloc.AppendCPUSet(cpuset.NewCPUSet())
				reservationPreemptibleCPUs = alloc.allocatableCPUs.Intersection(reservationPreemptibleCPUs)
				resourceOptions.preemptibleCPUs = resourceOptions.preemptibleCPUs.Union(reservationPreemptibleCPUs)
				resourceOptions.preferredCPUs = resourceOptions.preferredCPUs.Union(reservationPreemptibleCPUs)
				reservedCPUs = reservedCPUs.Union(reservationPreemptibleCPUs)
			}

			if resourceOptions.requestCPUBind && resourceOptions.numCPUsNeeded > reservedCPUs.Size() {
				reservationReasons = append(reservationReasons, framework.NewStatus(framework.Unschedulable, "not enough cpus available to satisfy request"))
				klog.V(5).InfoS("failed to allocated from reservation, not enough cpus available to satisfy request",
					"reservation", rInfo.Reservation.Name, "pod", pod.Name, "node", node.Name,
					"policy", allocatePolicy, "numCPUsNeeded", resourceOptions.numCPUsNeeded,
					"reservedCPUs", reservedCPUs.String(), "remainedCPUs", alloc.remainedCPUs.String(),
					"preemptibleCPUs", resourceOptions.preemptibleCPUs.String())
				continue
			}

			result, status = manager.Allocate(node, pod, resourceOptions)
			if !status.IsSuccess() {
				klog.V(5).InfoS("failed to allocated from reservation",
					"reservation", rInfo.Reservation.Name, "pod", pod.Name, "node", node.Name,
					"policy", allocatePolicy, "status", status.Message(), "hint", resourceOptions.hint,
					"reusableResources", resourceOptions.reusableResources,
					"preferredCPUs", resourceOptions.preferredCPUs, "preemptibleCPUs", resourceOptions.preemptibleCPUs)
				if klog.V(6).Enabled() {
					logStruct(reflect.ValueOf(resourceOptions), "options", 6)
					logStruct(reflect.ValueOf(restoreState), "restoreState", 6)
				}
				reservationReasons = append(reservationReasons, status)
				continue
			}

			if result.CPUSet.Size() > reservedCPUs.Size() {
				reservationReasons = append(reservationReasons, framework.NewStatus(framework.Unschedulable, "not enough cpus available to satisfy request"))
				klog.V(5).InfoS("failed to allocated from reservation, not enough cpus available to satisfy request",
					"reservation", rInfo.Reservation.Name, "pod", pod.Name, "node", node.Name,
					"policy", allocatePolicy, "allocateCPUs", result.CPUSet.String(),
					"reservedCPUs", reservedCPUs.String(), "remainedCPUs", alloc.remainedCPUs.String())
				continue
			}

			hasSatisfiedReservation = true
			break
		}
	}
	if !hasSatisfiedReservation && resourceOptions.requiredFromReservation {
		return nil, framework.NewStatus(framework.Unschedulable, makeReasonsByReservation(reservationReasons)...)
	}
	return result, nil
}

func makeReasonsByReservation(reservationReasons []*framework.Status) []string {
	var reasons []string
	for _, status := range reservationReasons {
		for _, r := range status.Reasons() {
			reasons = append(reasons, fmt.Sprintf("Reservation(s) %s", r))
		}
	}
	return reasons
}

// tryAllocateIgnoreReservation will try to allocate where the reserved resources of the node ignored.
func tryAllocateIgnoreReservation(manager ResourceManager,
	restoreState *nodeReservationRestoreStateData,
	resourceOptions *ResourceOptions,
	ignoredReservations map[types.UID]reservationAlloc,
	pod *corev1.Pod,
	node *corev1.Node,
) (*PodAllocation, *framework.Status) {
	reusableResourcesFromIgnored := appendAllocated(nil, restoreState.mergedUnmatchedUsed, restoreState.mergedMatchedAllocated)
	reservedCPUsFromIgnored := restoreState.mergedMatchedAllocatedCPUs.Clone()
	preemptibleCPUs := cpuset.NewCPUSet()

	// update with node preemption state
	var nodePreemptionAlloc *preemptibleAlloc
	if resourceOptions.nodePreemptionState != nil && resourceOptions.nodePreemptionState.nodeAlloc != nil {
		nodePreemptionAlloc = resourceOptions.nodePreemptionState.nodeAlloc
		reusableResourcesFromIgnored = nodePreemptionAlloc.AppendNUMAResources(reusableResourcesFromIgnored)
		preemptibleCPUs = nodePreemptionAlloc.AppendCPUSet(preemptibleCPUs)
	}

	// accumulate all ignored reserved resources which are not allocated to any owner pods
	for _, alloc := range ignoredReservations {
		reusableResourcesFromIgnored = appendAllocated(reusableResourcesFromIgnored, alloc.remained)
		reservedCPUsFromIgnored = reservedCPUsFromIgnored.Union(alloc.remainedCPUs)
		if resourceOptions.nodePreemptionState != nil && resourceOptions.nodePreemptionState.reservationsAlloc != nil &&
			resourceOptions.nodePreemptionState.reservationsAlloc[alloc.rInfo.UID()] != nil { // update with reservation preemption state
			reservationPreemptionAlloc := resourceOptions.nodePreemptionState.reservationsAlloc[alloc.rInfo.UID()]
			reusableResourcesFromIgnored = reservationPreemptionAlloc.AppendNUMAResources(reusableResourcesFromIgnored)
			reservationPreemptibleCPUs := reservationPreemptionAlloc.AppendCPUSet(cpuset.NewCPUSet())
			reservationPreemptibleCPUs = alloc.allocatableCPUs.Intersection(reservationPreemptibleCPUs)
			preemptibleCPUs = preemptibleCPUs.Union(reservationPreemptibleCPUs)
		}
	}

	// For a reservation-ignored pod, the pod can allocate resources from:
	// (1) the node unallocated and unreserved;
	// (2) the unallocated resources from the matched reservations.
	// Since the nodeInfo snapshot double-calculate the allocated resources of the scheduled reservations,
	// we should add this part to calculate the (1).
	resourceOptions.reusableResources = reusableResourcesFromIgnored
	resourceOptions.preferredCPUs = reservedCPUsFromIgnored
	resourceOptions.preemptibleCPUs = preemptibleCPUs
	resourceOptions.requiredResources = nil

	podAllocation, status := manager.Allocate(node, pod, resourceOptions)
	if !status.IsSuccess() {
		klog.V(5).InfoS("failed to allocated with reservation ignored",
			"pod", pod.Name, "node", node.Name,
			"status", status.Message(), "hint", resourceOptions.hint,
			"reusableResources", resourceOptions.reusableResources,
			"preferredCPUs", resourceOptions.preferredCPUs,
			"preemptibleCPUs", resourceOptions.preemptibleCPUs)
	}
	return podAllocation, status
}

func (p *Plugin) allocateWithNominatedReservation(
	restoreState *nodeReservationRestoreStateData,
	resourceOptions *ResourceOptions,
	pod *corev1.Pod,
	node *corev1.Node,
) (*PodAllocation, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		return nil, nil
	}

	// if the pod is reservation-ignored, it should allocate the node unallocated resources and all the reserved
	// unallocated resources.
	if extension.IsReservationIgnored(pod) {
		return tryAllocateIgnoreReservation(p.resourceManager, restoreState, resourceOptions, restoreState.matched, pod, node)
	}

	rInfo := p.handle.GetReservationNominator().GetNominatedReservation(pod, node.Name)
	if rInfo == nil {
		if resourceOptions.requiredFromReservation {
			return nil, framework.NewStatus(framework.Unschedulable, "no nominated reservation")
		}

		return nil, nil
	}
	nominatedReservationAlloc, ok := restoreState.matched[rInfo.UID()]
	if !ok {
		klog.V(5).Infof("nominated reservation %v doesn't reserve numa resource or cpuset", klog.KObj(rInfo))
		return nil, nil
	}
	return tryAllocateFromReservation(p.resourceManager, restoreState, resourceOptions, map[types.UID]reservationAlloc{rInfo.UID(): nominatedReservationAlloc}, pod, node)
}

func (p *Plugin) getPodNominatedReservationInfo(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	rCache := reservation.GetReservationCache()
	if rCache == nil {
		return nil
	}
	rInfo := rCache.GetReservationInfoByPod(pod, nodeName)
	if rInfo != nil {
		return rInfo
	}
	nominator := p.handle.GetReservationNominator()
	if nominator != nil {
		return nominator.GetNominatedReservation(pod, nodeName)
	}
	return nil
}

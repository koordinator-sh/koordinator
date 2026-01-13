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

package deviceshare

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const reservationRestoreStateKey = Name + "/reservationRestoreState"

type reservationRestoreStateData struct {
	lock        sync.RWMutex
	skip        bool
	nodeToState frameworkext.NodeReservationRestoreStates
}

type nodeReservationRestoreStateData struct {
	matched            []reusableAlloc               // matched reservations or pre-allocatable podds
	unmatched          []reusableAlloc               // unmatched reservations
	preAllocationRInfo *frameworkext.ReservationInfo // pre-allocating reservation info

	mergedMatchedAllocatable map[schedulingv1alpha1.DeviceType]deviceResources
	mergedMatchedAllocated   map[schedulingv1alpha1.DeviceType]deviceResources
	mergedUnmatchedUsed      map[schedulingv1alpha1.DeviceType]deviceResources
}

// reusableAlloc represents the allocatable/total reserved devices and allocated devices of a reservation or pre-allocatable pod.
type reusableAlloc struct {
	rInfo          *frameworkext.ReservationInfo // comparing reservation, i.e. the available reservation to allocate or the pre-allocating reservation
	preAllocatable *corev1.Pod
	allocatable    map[schedulingv1alpha1.DeviceType]deviceResources
	allocated      map[schedulingv1alpha1.DeviceType]deviceResources
	remained       map[schedulingv1alpha1.DeviceType]deviceResources
}

func getReservationRestoreState(cycleState *framework.CycleState) *reservationRestoreStateData {
	var state *reservationRestoreStateData
	value, err := cycleState.Read(reservationRestoreStateKey)
	if err == nil {
		state, _ = value.(*reservationRestoreStateData)
	}
	if state == nil || state.nodeToState == nil {
		state = &reservationRestoreStateData{
			skip:        true,
			nodeToState: map[string]interface{}{},
		}
	}
	return state
}

func cleanReservationRestoreState(cycleState *framework.CycleState) {
	cycleState.Delete(reservationRestoreStateKey)
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

func (rs *nodeReservationRestoreStateData) mergeReservationAllocations() {
	unmatched := rs.unmatched
	if len(unmatched) > 0 {
		mergedUnmatchedUsed := map[schedulingv1alpha1.DeviceType]deviceResources{}
		for _, alloc := range unmatched {
			used := subtractAllocated(copyDeviceResources(alloc.allocatable), alloc.remained, true)
			mergedUnmatchedUsed = appendAllocated(mergedUnmatchedUsed, used)
		}
		rs.mergedUnmatchedUsed = mergedUnmatchedUsed
	}

	matched := rs.matched
	if len(matched) > 0 {
		mergedMatchedAllocatable := map[schedulingv1alpha1.DeviceType]deviceResources{}
		mergedMatchedAllocated := map[schedulingv1alpha1.DeviceType]deviceResources{}
		for _, alloc := range matched {
			mergedMatchedAllocatable = appendAllocated(mergedMatchedAllocatable, alloc.allocatable)
			mergedMatchedAllocated = appendAllocated(mergedMatchedAllocated, alloc.allocated)
		}
		rs.mergedMatchedAllocatable = mergedMatchedAllocatable
		rs.mergedMatchedAllocated = mergedMatchedAllocated
	}

	return
}

func (p *Plugin) PreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	requests, err := GetPodDeviceRequests(pod)
	if err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	state := getReservationRestoreState(cycleState)
	state.skip = len(requests) == 0
	cycleState.Write(reservationRestoreStateKey, state)
	return nil
}

func (p *Plugin) RestoreReservation(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, matched []*frameworkext.ReservationInfo, unmatched []*frameworkext.ReservationInfo, nodeInfo *framework.NodeInfo) (interface{}, *framework.Status) {
	state := getReservationRestoreState(cycleState)
	if state.skip {
		return nil, nil
	}

	nodeName := nodeInfo.Node().Name
	nd := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nd == nil {
		return nil, nil
	}

	filterFn := func(reservations []*frameworkext.ReservationInfo) []reusableAlloc {
		if len(reservations) == 0 {
			return nil
		}

		nd.lock.RLock()
		defer nd.lock.RUnlock()

		result := make([]reusableAlloc, 0, len(reservations))
		for _, rInfo := range reservations {
			reservePod := rInfo.GetReservePod()
			allocatable := nd.getUsed(reservePod.Namespace, reservePod.Name)
			if len(allocatable) == 0 {
				continue
			}
			minorHints := newDeviceMinorMap(allocatable)
			var allocated map[schedulingv1alpha1.DeviceType]deviceResources
			for _, podRequirement := range rInfo.AssignedPods {
				podAllocated := nd.getUsed(podRequirement.Namespace, podRequirement.Name)
				if len(podAllocated) > 0 {
					allocated = appendAllocatedByHints(minorHints, allocated, podAllocated)
				}
			}
			remained := subtractAllocated(copyDeviceResources(allocatable), allocated, false)

			result = append(result, reusableAlloc{
				rInfo:       rInfo,
				allocatable: allocatable,
				allocated:   allocated,
				remained:    remained,
			})
		}
		return result
	}
	filteredMatched := filterFn(matched)
	filteredUnmatched := filterFn(unmatched)
	s := &nodeReservationRestoreStateData{
		matched:   filteredMatched,
		unmatched: filteredUnmatched,
	}
	s.mergeReservationAllocations()

	// also complete the nodeRestoreState in cycleState
	state.setNodeState(nodeName, s)
	cycleState.Write(reservationRestoreStateKey, state)

	return s, nil
}

func (p *Plugin) PreRestoreReservationPreAllocation(ctx context.Context, cycleState *framework.CycleState, r *frameworkext.ReservationInfo) *framework.Status {
	requests, err := GetPodDeviceRequests(r.GetReservePod())
	if err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	state := getReservationRestoreState(cycleState)
	state.skip = len(requests) == 0
	cycleState.Write(reservationRestoreStateKey, state)
	return nil
}

func (p *Plugin) RestoreReservationPreAllocation(ctx context.Context, cycleState *framework.CycleState, r *frameworkext.ReservationInfo, preAllocatable []*corev1.Pod, nodeInfo *framework.NodeInfo) (interface{}, *framework.Status) {
	// retrieve reusable for pre-allocatable pods
	state := getReservationRestoreState(cycleState)
	if state.skip {
		return nil, nil
	}
	if len(preAllocatable) <= 0 {
		return nil, nil
	}

	nodeName := nodeInfo.Node().Name
	nd := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nd == nil {
		return nil, nil
	}

	preAllocatableAllocs := make([]reusableAlloc, 0, len(preAllocatable))
	nd.lock.RLock()
	for _, pod := range preAllocatable {
		allocatable := nd.getUsed(pod.Namespace, pod.Name)
		if len(allocatable) <= 0 {
			continue
		}
		preAllocatableAllocs = append(preAllocatableAllocs, reusableAlloc{
			preAllocatable: pod,
			allocatable:    allocatable,
		})
	}
	nd.lock.RUnlock()

	for i := range preAllocatableAllocs {
		preAllocatableAllocs[i].rInfo = r
		preAllocatableAllocs[i].remained = copyDeviceResources(preAllocatableAllocs[i].allocatable)
	}

	// merge with nodeState
	nodeState := state.getNodeState(nodeName)
	if nodeState.matched == nil {
		nodeState.matched = preAllocatableAllocs
	} else {
		nodeState.matched = append(nodeState.matched, preAllocatableAllocs...)
	}
	nodeState.preAllocationRInfo = r
	nodeState.mergeReservationAllocations()
	state.setNodeState(nodeName, nodeState)
	cycleState.Write(reservationRestoreStateKey, state)

	klog.V(5).InfoS("Completed RestoreReservationPreAllocation",
		"reservation", r.GetName(), "node", nodeName,
		"preAllocatablePods", len(preAllocatable),
		"restoredPods", len(preAllocatableAllocs),
		"preAllocationRInfo", nodeState.preAllocationRInfo.GetName(),
	)

	return nodeState, nil
}

// DEPRECATED
func (p *Plugin) FinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeToStates frameworkext.NodeReservationRestoreStates) *framework.Status {
	state := getReservationRestoreState(cycleState)
	if state.skip {
		return nil
	}
	state.nodeToState = nodeToStates
	return nil
}

func (p *Plugin) tryAllocateFromReusable(
	allocator *AutopilotAllocator,
	state *preFilterState,
	restoreState *nodeReservationRestoreStateData,
	matchedReusableAllocs []reusableAlloc,
	pod *corev1.Pod,
	node *corev1.Node,
	basicPreemptible map[schedulingv1alpha1.DeviceType]deviceResources,
	requiredFromReservation bool,
) (apiext.DeviceAllocations, *framework.Status) {
	if len(matchedReusableAllocs) == 0 {
		return nil, nil
	}

	if apiext.IsReservationIgnored(pod) {
		return p.tryAllocateIgnoreReservation(allocator, state, restoreState, restoreState.matched, node, basicPreemptible)
	}

	var hasSatisfiedReservation bool
	var result apiext.DeviceAllocations
	var status *framework.Status

	basicPreemptible = appendAllocated(nil, basicPreemptible, restoreState.mergedMatchedAllocated)

	isPreAllocation := restoreState.preAllocationRInfo != nil // use the cycle state to avoid misunderstanding

	var reservationReasons []*framework.Status
	for _, alloc := range matchedReusableAllocs {
		rInfo := alloc.rInfo
		preemptibleInRR := state.preemptibleInRRs[node.Name][rInfo.UID()]
		preferred := newDeviceMinorMap(alloc.allocatable)

		//
		// The Aligned and Restricted Policies only allow Pods to be allocated from current Reservation
		// and the remaining resources of the node.
		// And the Restricted policy requires that if the device resources requested by the Pod overlap with
		// the device resources reserved by the current Reservation, such devices can only be allocated from the current Reservation.
		// The formula for calculating the remaining amount per device instance in this scenario is as follows:
		// basicPreemptible = sum(allocated(unmatched reservations)) + preemptible(node)
		// free = total - (used - basicPreemptible - sum(allocated(matched reservations)) - sum(remained(currentReservation)) - preemptible(currentReservation))
		//
		preemptible := appendAllocated(nil, basicPreemptible, alloc.remained, preemptibleInRR)

		allocatePolicy := rInfo.GetAllocatePolicy()
		// TODO: Currently the ReservationAllocatePolicyDefault is actually implemented as
		//       ReservationAllocatePolicyAligned. Need to re-visit the policies.
		if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyDefault ||
			allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyAligned {
			result, status = allocator.Allocate(nil, preferred, nil, preemptible)
			if !status.IsSuccess() {
				reservationReasons = append(reservationReasons, status)
				continue
			}

			hasSatisfiedReservation = true
			break

		} else if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
			//
			// It is necessary to check separately whether the remaining resources of the device instance
			// reserved by the Restricted Reservation meet the requirements of the Pod, to ensure that
			// the intersecting resources do not exceed the reserved range of the Restricted Reservation.
			//
			// Example: the node has reservation-ignored pod, matched reservations R1, R2, ..., Ri,
			// unmatched reservations U1, U2, ..., Uj, and pods P1, P2, ..., Pk.
			// The free device resources for the scheduling pod P0 is:
			// min(NodeTotal - P1 - P2 - ... - Pk - U1 - U2 - ... - Uj, R1)
			requiredDeviceResources := calcRequiredDeviceResources(&alloc, preemptibleInRR)
			if isPreAllocation {
				// pre-allocating reservation can allocate more than the pre-allocatable pod remained
				requiredDeviceResources = nil
			}
			result, status = allocator.Allocate(preferred, preferred, requiredDeviceResources, preemptible)
			if !status.IsSuccess() {
				reservationReasons = append(reservationReasons, status)
				continue
			}

			// For pre-allocation, validate that the allocated devices include all devices from the pre-allocatable pod
			if isPreAllocation {
				if !isDeviceAllocationsInclude(result, alloc.allocatable) {
					reservationReasons = append(reservationReasons, framework.NewStatus(framework.Unschedulable, ErrInsufficientGPUDevices))
					if klog.V(5).Enabled() {
						klog.InfoS("failed to pre-allocate from pod, allocated devices are not a superset of pre-allocatable pod's devices",
							"reservation", rInfo.GetName(), "pod", klog.KObj(pod), "pre-allocatable", klog.KObj(alloc.preAllocatable),
							"allocated", result, "required", alloc.allocatable)
					}
					continue
				}
			}

			hasSatisfiedReservation = true
			break
		}
	}
	if !hasSatisfiedReservation && requiredFromReservation {
		return nil, framework.NewStatus(framework.Unschedulable, p.makeReasonsByReservation(reservationReasons)...)
	}
	return result, nil
}

// tryAllocateIgnoreReservation will try to allocate where the reserved resources of the node ignored.
func (p *Plugin) tryAllocateIgnoreReservation(
	allocator *AutopilotAllocator,
	state *preFilterState,
	restoreState *nodeReservationRestoreStateData,
	ignoredReservations []reusableAlloc,
	node *corev1.Node,
	basicPreemptible map[schedulingv1alpha1.DeviceType]deviceResources,
) (apiext.DeviceAllocations, *framework.Status) {
	preemptibleFromIgnored := map[schedulingv1alpha1.DeviceType]deviceResources{}

	// accumulate all ignored reserved resources which are not allocated to any owner pods
	for _, alloc := range ignoredReservations {
		preemptibleFromIgnored = appendAllocated(preemptibleFromIgnored,
			state.preemptibleInRRs[node.Name][alloc.rInfo.UID()], alloc.remained)
	}

	preemptibleFromIgnored = appendAllocated(preemptibleFromIgnored, basicPreemptible, restoreState.mergedMatchedAllocated)

	return allocator.Allocate(nil, nil, nil, preemptibleFromIgnored)
}

func (p *Plugin) makeReasonsByReservation(reservationReasons []*framework.Status) []string {
	var reasons []string
	for _, status := range reservationReasons {
		for _, r := range status.Reasons() {
			reasons = append(reasons, reservationutil.NewReservationReason(r))
		}
	}
	return reasons
}

// scoreWithReservation combine the reservation with the node's resource usage to calculate the reservation score.
func (p *Plugin) scoreWithReservation(
	allocator *AutopilotAllocator,
	state *preFilterState,
	restoreState *nodeReservationRestoreStateData,
	alloc *reusableAlloc,
	nodeName string,
	basicPreemptible map[schedulingv1alpha1.DeviceType]deviceResources,
) (int64, *framework.Status) {
	if alloc == nil {
		return 0, nil
	}

	rInfo := alloc.rInfo
	basicPreemptible = appendAllocated(nil, basicPreemptible, restoreState.mergedMatchedAllocated)
	preemptibleInRR := state.preemptibleInRRs[nodeName][rInfo.UID()]
	preemptible := appendAllocated(nil, basicPreemptible, alloc.remained, preemptibleInRR)
	var requiredDeviceResources map[schedulingv1alpha1.DeviceType]deviceResources
	allocatePolicy := rInfo.GetAllocatePolicy()
	if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
		requiredDeviceResources = calcRequiredDeviceResources(alloc, preemptibleInRR)
	}
	return allocator.score(requiredDeviceResources, preemptible)
}

func calcRequiredDeviceResources(alloc *reusableAlloc, preemptibleInRR map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	var required map[schedulingv1alpha1.DeviceType]deviceResources
	minorHints := newDeviceMinorMap(alloc.allocatable)
	required = appendAllocatedByHints(minorHints, required, alloc.remained, preemptibleInRR)
	if len(required) == 0 {
		// required is empty to indicate that there are no resources left.
		// A valid object must be constructed with no remaining capacity.
		if required == nil {
			required = map[schedulingv1alpha1.DeviceType]deviceResources{}
		}
		for deviceType, minors := range minorHints {
			resources := deviceResources{}
			for minor := range minors.UnsortedList() {
				resources[minor] = corev1.ResourceList{}
			}
			required[deviceType] = resources
		}
	}
	return required
}

func (p *Plugin) allocateWithNominated(
	allocator *AutopilotAllocator,
	state *preFilterState,
	restoreState *nodeReservationRestoreStateData,
	node *corev1.Node,
	pod *corev1.Pod,
	basicPreemptible map[schedulingv1alpha1.DeviceType]deviceResources,
) (apiext.DeviceAllocations, *framework.Status) {
	if reservationutil.IsReservePod(pod) && !reservationutil.IsReservePodPreAllocation(pod) {
		return nil, nil
	}

	// if the pod is reservation-ignored, it should allocate the node unallocated resources and all the reserved
	// unallocated resources.
	if apiext.IsReservationIgnored(pod) {
		return p.tryAllocateIgnoreReservation(allocator, state, restoreState, restoreState.matched, node, basicPreemptible)
	}

	nominatedReusableAlloc, status := p.getNominatedReusableAlloc(restoreState, pod, node)
	if !status.IsSuccess() {
		return nil, status
	}

	result, status := p.tryAllocateFromReusable(
		allocator,
		state,
		restoreState,
		nominatedReusableAlloc,
		pod,
		node,
		basicPreemptible,
		false,
	)
	return result, status
}

func (p *Plugin) scoreWithNominatedReservation(
	allocator *AutopilotAllocator,
	state *preFilterState,
	restoreState *nodeReservationRestoreStateData,
	nodeName string,
	pod *corev1.Pod,
	basicPreemptible map[schedulingv1alpha1.DeviceType]deviceResources,
	reservationInfo *frameworkext.ReservationInfo,
) (int64, *framework.Status) {
	if reservationutil.IsReservePod(pod) || reservationInfo == nil {
		return 0, nil
	}

	allocIndex := -1
	for i, v := range restoreState.matched {
		if v.rInfo.UID() == reservationInfo.UID() {
			allocIndex = i
			break
		}
	}
	if allocIndex == -1 {
		return 0, nil
	}

	score, status := p.scoreWithReservation(
		allocator,
		state,
		restoreState,
		&restoreState.matched[allocIndex],
		nodeName,
		basicPreemptible,
	)
	return score, status
}

func (p *Plugin) getNominatedReusableAlloc(restoreState *nodeReservationRestoreStateData, pod *corev1.Pod, node *corev1.Node) ([]reusableAlloc, *framework.Status) {
	if !reservationutil.IsReservePod(pod) {
		reservation := p.handle.GetReservationNominator().GetNominatedReservation(pod, node.Name)
		if reservation == nil {
			return nil, nil
		}

		for i, v := range restoreState.matched {
			if v.rInfo.UID() == reservation.UID() {
				return restoreState.matched[i : i+1], nil
			}
		}
		klog.V(5).Infof("nominated reservation %v doesn't reserve any device resource, pod %s, node %s", klog.KObj(reservation), klog.KObj(pod), node.Name)
		return nil, nil
	}

	if !reservationutil.IsReservePodPreAllocation(pod) {
		return nil, nil
	}

	if restoreState.preAllocationRInfo == nil {
		klog.V(5).Infof("node has no pre-allocatable device resource, pod %s, node %s", klog.KObj(pod), node.Name)
		return nil, nil
	}

	preAllocatable := p.handle.GetReservationNominator().GetNominatedPreAllocation(restoreState.preAllocationRInfo, node.Name)
	if preAllocatable == nil {
		return nil, nil
	}
	for i, v := range restoreState.matched {
		if v.preAllocatable.GetUID() == preAllocatable.GetUID() {
			return restoreState.matched[i : i+1], nil
		}
	}
	klog.V(5).Infof("nominated pre-allocatable %v doesn't reserve any device resource, pod %s, node %s", klog.KObj(preAllocatable), klog.KObj(pod), node.Name)
	return nil, nil
}

// isDeviceAllocationsInclude checks if allocations include all devices from required
func isDeviceAllocationsInclude(allocations apiext.DeviceAllocations, required map[schedulingv1alpha1.DeviceType]deviceResources) bool {
	for deviceType, requiredDevices := range required {
		allocated := allocations[deviceType]
		if len(allocated) == 0 {
			return false
		}
		// Create a map of allocated device minors
		allocatedMinors := make(map[int32]bool)
		for _, alloc := range allocated {
			allocatedMinors[alloc.Minor] = true
		}
		// Check if all required device minors are in allocated
		for minor := range requiredDevices {
			if !allocatedMinors[int32(minor)] {
				return false
			}
		}
	}
	return true
}

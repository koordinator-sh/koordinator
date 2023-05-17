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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const reservationRestoreStateKey = Name + "/reservationRestoreState"

type reservationRestoreStateData struct {
	skip        bool
	nodeToState frameworkext.NodeReservationRestoreStates
}

type nodeReservationRestoreStateData struct {
	matched   []reservationAlloc
	unmatched []reservationAlloc

	mergedMatchedAllocatable map[schedulingv1alpha1.DeviceType]deviceResources
	mergedUnmatchedUsed      map[schedulingv1alpha1.DeviceType]deviceResources
}

type reservationAlloc struct {
	rInfo       *frameworkext.ReservationInfo
	allocatable map[schedulingv1alpha1.DeviceType]deviceResources
	allocated   map[schedulingv1alpha1.DeviceType]deviceResources
	remained    map[schedulingv1alpha1.DeviceType]deviceResources
}

func getReservationRestoreState(cycleState *framework.CycleState) *reservationRestoreStateData {
	var state *reservationRestoreStateData
	value, err := cycleState.Read(reservationRestoreStateKey)
	if err == nil {
		state, _ = value.(*reservationRestoreStateData)
	}
	if state == nil {
		state = &reservationRestoreStateData{
			skip: true,
		}
	}
	return state
}

func (s *reservationRestoreStateData) Clone() framework.StateData {
	return s
}

func (s *reservationRestoreStateData) getNodeState(nodeName string) *nodeReservationRestoreStateData {
	val := s.nodeToState[nodeName]
	ns, ok := val.(*nodeReservationRestoreStateData)
	if !ok {
		ns = &nodeReservationRestoreStateData{}
	}
	return ns
}

func (rs *nodeReservationRestoreStateData) mergeReservationAllocations() {
	unmatched := rs.unmatched
	if len(unmatched) > 0 {
		mergedUnmatchedUsed := map[schedulingv1alpha1.DeviceType]deviceResources{}
		for _, alloc := range unmatched {
			used := subtractAllocated(copyDeviceResources(alloc.allocatable), alloc.remained)
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
	}

	return
}

func (p *Plugin) PreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	skip, _, status := preparePod(pod)
	if !status.IsSuccess() {
		return status
	}
	cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{skip: skip})
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

	nd.lock.RLock()
	defer nd.lock.RUnlock()

	filterFn := func(reservations []*frameworkext.ReservationInfo) []reservationAlloc {
		if len(reservations) == 0 {
			return nil
		}
		result := make([]reservationAlloc, 0, len(reservations))
		for _, rInfo := range reservations {
			namespacedName := reservationutil.GetReservePodNamespacedName(rInfo.Reservation)
			allocatable := nd.getUsed(namespacedName.Namespace, namespacedName.Name)
			if len(allocatable) == 0 {
				continue
			}
			allocated := map[schedulingv1alpha1.DeviceType]deviceResources{}
			for k := range allocatable {
				allocated[k] = deviceResources{}
			}
			for _, podRequirement := range rInfo.Pods {
				podAllocated := nd.getUsed(podRequirement.Namespace, podRequirement.Name)
				if len(podAllocated) > 0 {
					allocated = appendAllocatedIntersectionDeviceType(allocated, podAllocated)
				}
			}
			result = append(result, reservationAlloc{
				rInfo:       rInfo,
				allocatable: allocatable,
				allocated:   allocated,
				remained:    subtractAllocated(copyDeviceResources(allocatable), allocated),
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
	return s, nil
}

func (p *Plugin) FinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeToStates frameworkext.NodeReservationRestoreStates) *framework.Status {
	state := getReservationRestoreState(cycleState)
	if state.skip {
		return nil
	}
	state.nodeToState = nodeToStates
	return nil
}

func (p *Plugin) tryAllocateFromReservation(state *preFilterState, nodeDeviceInfo *nodeDevice, nodeName string, pod *corev1.Pod, restoreState *nodeReservationRestoreStateData) (apiext.DeviceAllocations, error) {
	matchedReservations := restoreState.matched
	if len(matchedReservations) == 0 {
		return nil, nil
	}
	for _, alloc := range matchedReservations {
		rInfo := alloc.rInfo
		preferred := newDeviceMinorMap(alloc.allocatable)
		preemptible := appendAllocated(nil,
			restoreState.mergedUnmatchedUsed,
			restoreState.mergedMatchedAllocatable,
			state.preemptibleDevices[nodeName],
			state.preemptibleInRRs[nodeName][rInfo.Reservation.UID],
		)
		result, err := p.allocator.Allocate(nodeName, pod, state.podRequests, nodeDeviceInfo, nil, preferred, preemptible)
		if len(result) > 0 && err == nil {
			return result, nil
		}
	}
	return nil, nil
}

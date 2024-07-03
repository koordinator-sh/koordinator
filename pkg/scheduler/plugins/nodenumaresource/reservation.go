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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const reservationRestoreStateKey = Name + "/reservationRestoreState"

type reservationRestoreStateData struct {
	nodeToState frameworkext.NodeReservationRestoreStates
}

type nodeReservationRestoreStateData struct {
	matched   map[types.UID]reservationAlloc
	unmatched map[types.UID]reservationAlloc

	mergedMatchedReservedCPUs cpuset.CPUSet
	mergedMatchedAllocatable  map[int]corev1.ResourceList
	mergedMatchedAllocated    map[int]corev1.ResourceList
	mergedUnmatchedUsed       map[int]corev1.ResourceList
}

type reservationAlloc struct {
	rInfo *frameworkext.ReservationInfo

	// TODO CPUSet 预留目前尚未支持, 这里只是计算的时候留一个接口，后续再梳理
	reservedCPUs cpuset.CPUSet

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
	if state == nil {
		state = &reservationRestoreStateData{}
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
		mergedCPUSetBuilder := cpuset.NewCPUSetBuilder()
		for _, alloc := range matched {
			mergedMatchedAllocatable = appendAllocated(mergedMatchedAllocatable, alloc.allocatable)
			mergedMatchedAllocated = appendAllocated(mergedMatchedAllocated, alloc.allocated)
			mergedCPUSetBuilder.Add(alloc.reservedCPUs.ToSliceNoSort()...)
		}
		rs.mergedMatchedAllocatable = mergedMatchedAllocatable
		rs.mergedMatchedAllocated = mergedMatchedAllocated
		rs.mergedMatchedReservedCPUs = mergedCPUSetBuilder.Result()
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
			var reservedCPUs cpuset.CPUSet
			func() {
				allocatedCPUs, ok := p.resourceManager.GetAllocatedCPUSet(nodeName, reservePod.UID)
				if !ok || allocatedCPUs.IsEmpty() {
					return
				}
				for _, pod := range rInfo.AssignedPods {
					podCPUs, ok := p.resourceManager.GetAllocatedCPUSet(nodeName, pod.UID)
					if !ok || podCPUs.IsEmpty() {
						continue
					}
					allocatedCPUs = allocatedCPUs.Difference(podCPUs)
				}
				reservedCPUs = allocatedCPUs
			}()
			if allocatable != nil || !reservedCPUs.IsEmpty() {
				result[rInfo.UID()] = reservationAlloc{
					rInfo:        rInfo,
					reservedCPUs: reservedCPUs,
					allocatable:  allocatable,
					allocated:    allocatedNUMAResource,
					remained:     remained,
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
	return s, nil
}

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
	matchedReservations map[types.UID]reservationAlloc,
	pod *corev1.Pod,
	node *corev1.Node,
) (*PodAllocation, *framework.Status) {
	if len(matchedReservations) == 0 {
		return nil, nil
	}

	var hasSatisfiedReservation bool
	var result *PodAllocation
	var status *framework.Status

	reusableResource := appendAllocated(nil, restoreState.mergedUnmatchedUsed, restoreState.mergedMatchedAllocated)

	var reservationReasons []*framework.Status
	for _, alloc := range matchedReservations {
		rInfo := alloc.rInfo

		reservationReusableResource := appendAllocated(nil, reusableResource, alloc.remained)
		resourceOptions.reusableResources = reservationReusableResource
		resourceOptions.preferredCPUs = alloc.reservedCPUs
		resourceOptions.requiredResources = nil

		allocatePolicy := rInfo.GetAllocatePolicy()
		if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyDefault ||
			allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyAligned {
			result, status = manager.Allocate(node, pod, resourceOptions)
			if status.IsSuccess() {
				hasSatisfiedReservation = true
				break
			}
			reservationReasons = append(reservationReasons, status)
		} else if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
			// TODO 这里仿照 deviceShare 的逻辑这样处理，后续需要再琢磨一下是否有必要
			_, status := manager.Allocate(node, pod, resourceOptions)
			if status.IsSuccess() {
				resourceOptions.requiredResources = alloc.remained
				if resourceOptions.requestCPUBind && resourceOptions.numCPUsNeeded > alloc.reservedCPUs.Size() {
					reservationReasons = append(reservationReasons, framework.NewStatus(framework.Unschedulable, "not enough cpus available to satisfy request"))
					continue
				}
				result, status = manager.Allocate(node, pod, resourceOptions)
				if status.IsSuccess() {
					if result.CPUSet.Size() > alloc.reservedCPUs.Size() {
						reservationReasons = append(reservationReasons, framework.NewStatus(framework.Unschedulable, "not enough cpus available to satisfy request"))
						continue
					}
					hasSatisfiedReservation = true
					break
				} else {
					klog.V(5).InfoS("failed to allocated from reservation", "reservation", rInfo.Reservation.Name, "pod", pod.Name, "node", node.Name, "status", status.Message(), "numaNode", resourceOptions.hint.NUMANodeAffinity.String())
					logStruct(reflect.ValueOf(resourceOptions), "options", 6)
					logStruct(reflect.ValueOf(restoreState), "restoreState", 6)
				}
			} else {
				klog.V(5).InfoS("failed to allocated from reservation", "reservation", rInfo.Reservation.Name, "pod", pod.Name, "node", node.Name, "status", status.Message(), "numaNode", resourceOptions.hint.NUMANodeAffinity.String())
				logStruct(reflect.ValueOf(resourceOptions), "options", 6)
				logStruct(reflect.ValueOf(restoreState), "restoreState", 6)
			}

			reservationReasons = append(reservationReasons, status)
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

func (p *Plugin) allocateWithNominatedReservation(
	restoreState *nodeReservationRestoreStateData,
	resourceOptions *ResourceOptions,
	pod *corev1.Pod,
	node *corev1.Node,
) (*PodAllocation, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		return nil, nil
	}
	reservation := p.handle.GetReservationNominator().GetNominatedReservation(pod, node.Name)
	if reservation == nil {
		if resourceOptions.requiredFromReservation {
			return nil, framework.NewStatus(framework.Unschedulable, "no nominated reservation")
		}
		return nil, nil
	}
	nominatedReservationAlloc, ok := restoreState.matched[reservation.UID()]
	if !ok {
		klog.V(5).Infof("nominated reservation %v doesn't reserve numa resource or cpuset", klog.KObj(reservation))
		return nil, nil
	}
	return tryAllocateFromReservation(p.resourceManager, restoreState, resourceOptions, map[types.UID]reservationAlloc{reservation.UID(): nominatedReservationAlloc}, pod, node)
}

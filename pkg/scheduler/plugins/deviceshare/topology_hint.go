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
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

const (
	ErrInsufficientNUMAScopedDevices = "Insufficient NUMA Scoped Devices"

	defaultNUMAScore = 500
)

func (p *Plugin) GetPodTopologyHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (map[string][]topologymanager.NUMATopologyHint, *framework.Status) {
	if p.disableDeviceNUMATopologyAlignment {
		return nil, nil
	}
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return nil, status
	}

	if state.skip {
		return nil, nil
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	node := nodeInfo.Node()

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nodeDeviceInfo == nil {
		return nil, nil
	}

	return p.generateTopologyHints(cycleState, state, nodeDeviceInfo, node, pod)
}

func (p *Plugin) Allocate(ctx context.Context, cycleState *framework.CycleState, affinity topologymanager.NUMATopologyHint, pod *corev1.Pod, nodeName string) *framework.Status {
	if p.disableDeviceNUMATopologyAlignment {
		return nil
	}
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}

	if state.skip {
		return nil
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return framework.AsStatus(err)
	}
	node := nodeInfo.Node()

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(node.Name)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[node.Name])

	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nodeDeviceInfo,
		node:       node,
		pod:        pod,
		numaNodes:  affinity.NUMANodeAffinity,
	}

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()
	allocateResult, status := p.tryAllocateFromReservation(allocator, state, restoreState, restoreState.matched, pod, node, preemptible, state.hasReservationAffinity)
	if !status.IsSuccess() {
		return status
	}
	if len(allocateResult) > 0 {
		return nil
	}

	preemptible = appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
	_, status = allocator.Allocate(nil, nil, nil, preemptible)
	if status.IsSuccess() {
		return nil
	}
	return status
}

func (p *Plugin) generateTopologyHints(cycleState *framework.CycleState, state *preFilterState, nodeDevice *nodeDevice, node *corev1.Node, pod *corev1.Pod) (map[string][]topologymanager.NUMATopologyHint, *framework.Status) {
	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(node.Name)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[node.Name])

	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nodeDevice,
		node:       node,
		pod:        pod,
	}

	nodeDevice.lock.RLock()
	numaTopology := nodeDevice.numaTopology
	nodeDevice.lock.RUnlock()
	numaNodes := make([]int, 0, len(numaTopology.nodes))
	for nodeID := range numaTopology.nodes {
		numaNodes = append(numaNodes, nodeID)
	}
	sort.Ints(numaNodes)

	var minAffinitySize map[corev1.ResourceName]int
	var statusUnsatisfied *framework.Status
	var bestAllocationResult apiext.DeviceAllocations
	var feasibleAllocationResults []*numaScopedAllocation

	bitmask.IterateBitMasks(numaNodes, func(mask bitmask.BitMask) {
		nodeDevice.lock.RLock()
		defer nodeDevice.lock.RUnlock()

		var status *framework.Status
		var allocateResult apiext.DeviceAllocations
		if mask.Count() == len(numaNodes) {
			defer func() {
				statusUnsatisfied = status
				bestAllocationResult = allocateResult
			}()
		}

		allocator.numaNodes = mask
		if status = allocator.Prepare(); !status.IsSuccess() {
			return
		}

		maskNodes := mask.GetBits()
		totalDevices := calcTotalDevicesByNUMA(nodeDevice, maskNodes)
		for deviceType, wanted := range allocator.desiredCountPerDeviceType {
			if totalCount, exists := totalDevices[deviceType]; exists && totalCount < wanted {
				status = framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrInsufficientNUMAScopedDevices)
				return
			}
		}
		if minAffinitySize == nil {
			minAffinitySize = map[corev1.ResourceName]int{}
			for deviceType := range allocator.requestsPerInstance {
				minAffinitySize[corev1.ResourceName(deviceType)] = len(numaNodes)
			}
		}

		allocateResult, status = p.tryAllocateFromReservation(allocator, state, restoreState, restoreState.matched, pod, node, preemptible, state.hasReservationAffinity)
		if !status.IsSuccess() {
			return
		}
		if len(allocateResult) == 0 {
			preemptible := appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
			allocateResult, status = allocator.Allocate(nil, nil, nil, preemptible)
			if !status.IsSuccess() || len(allocateResult) == 0 {
				return
			}
		}

		nodeCount := mask.Count()
		for resourceName, affinitySize := range minAffinitySize {
			if nodeCount < affinitySize {
				minAffinitySize[resourceName] = nodeCount
			}
		}
		feasibleAllocationResults = append(feasibleAllocationResults, &numaScopedAllocation{
			mask:             mask,
			allocationResult: allocateResult,
		})
	})

	bestAllocationHash := hashAllocateResult(bestAllocationResult)
	hints := map[string][]topologymanager.NUMATopologyHint{}

	for _, feasibleAllocationResult := range feasibleAllocationResults {
		score := 0
		if hashAllocateResult(feasibleAllocationResult.allocationResult) == bestAllocationHash {
			// we just use a score bigger than 100 to make that device numa preference take precedence over cpu
			score = defaultNUMAScore
		}
		for resourceName := range minAffinitySize {
			hints[string(resourceName)] = append(hints[string(resourceName)], topologymanager.NUMATopologyHint{
				NUMANodeAffinity: feasibleAllocationResult.mask,
				Score:            int64(score),
			})
		}
	}

	// update hints preferred according to multiNUMAGroups, in case when it wasn't provided, the default
	// behavior to prefer the minimal amount of NUMA nodes will be used
	for resourceName, size := range minAffinitySize {
		for i, hint := range hints[string(resourceName)] {
			hints[string(resourceName)][i].Preferred = len(hint.NUMANodeAffinity.GetBits()) == size
		}

		h := hints[string(resourceName)]
		if h == nil {
			// no possible NUMA affinities for resource, just return status
			return nil, statusUnsatisfied
		}
	}
	if !statusUnsatisfied.IsSuccess() {
		return nil, statusUnsatisfied
	}
	return hints, nil
}

type numaScopedAllocation struct {
	mask             bitmask.BitMask
	allocationResult apiext.DeviceAllocations
}

func hashAllocateResult(allocations apiext.DeviceAllocations) int {
	gpuAllocations := allocations[schedulingv1alpha1.GPU]
	var minor []int
	for _, gpu := range gpuAllocations {
		minor = append(minor, int(gpu.Minor))
	}
	return hashMinors(minor)
}

func calcTotalDevicesByNUMA(nd *nodeDevice, numaNodes []int) map[schedulingv1alpha1.DeviceType]int {
	m := map[schedulingv1alpha1.DeviceType]int{}
	for _, node := range numaNodes {
		pcies := nd.numaTopology.nodes[node]
		for _, v := range pcies {
			for deviceTypes, minors := range v.devices {
				m[deviceTypes] += len(minors)
			}
		}
	}
	return m
}

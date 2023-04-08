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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func (p *Plugin) GetPodTopologyHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) map[string][]frameworkext.NUMATopologyHint {
	// TODO: should support reservation and preemption

	podRequests, _ := resource.PodRequestsAndLimits(pod)
	cpuRequest := podRequests[corev1.ResourceCPU]
	if cpuRequest.IsZero() {
		return nil
	}

	topologyOptions := p.topologyManager.GetCPUTopologyOptions(nodeName)
	availableCPUs, _, err := p.cpuManager.GetAvailableCPUs(nodeName)
	if err != nil {
		return nil
	}

	cpuHints := p.generateCPUTopologyHints(topologyOptions.CPUTopology, availableCPUs, cpuset.NewCPUSet(), int(cpuRequest.MilliValue()/1000))
	return map[string][]frameworkext.NUMATopologyHint{
		string(corev1.ResourceCPU): cpuHints,
	}
}

func (p *Plugin) Allocate(ctx context.Context, cycleState *framework.CycleState, hint frameworkext.NUMATopologyHint, pod *corev1.Pod, nodeName string, assume bool) error {
	podRequests, _ := resource.PodRequestsAndLimits(pod)
	cpuRequest := podRequests[corev1.ResourceCPU]
	if cpuRequest.IsZero() {
		return nil
	}

	numCPUs := int(cpuRequest.MilliValue() / 1000)
	availableCPUs, allocatedCPUs, err := p.cpuManager.GetAvailableCPUs(nodeName)
	if err != nil {
		return err
	}
	topologyOptions := p.topologyManager.GetCPUTopologyOptions(nodeName)
	var result cpuset.CPUSet
	numaAffinity := hint.NUMANodeAffinity
	if numaAffinity != nil {
		alignedCPUs := cpuset.NewCPUSet()
		for _, numaNodeID := range numaAffinity.GetBits() {
			alignedCPUs = alignedCPUs.Union(availableCPUs.Intersection(topologyOptions.CPUTopology.CPUDetails.CPUsInNUMANodes(numaNodeID)))
		}

		numAlignedToAlloc := alignedCPUs.Size()
		if numCPUs < numAlignedToAlloc {
			numAlignedToAlloc = numCPUs
		}

		alignedCPUs, err := takeCPUs(topologyOptions.CPUTopology, topologyOptions.MaxRefCount, alignedCPUs, allocatedCPUs, numAlignedToAlloc, schedulingconfig.CPUBindPolicyDefault, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMAMostAllocated)
		if err != nil {
			return err
		}

		result = result.Union(alignedCPUs)
	}

	remainingCPUs, err := takeCPUs(topologyOptions.CPUTopology, topologyOptions.MaxRefCount, availableCPUs.Difference(result), allocatedCPUs, numCPUs-result.Size(), schedulingconfig.CPUBindPolicyDefault, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMAMostAllocated)
	if err != nil {
		return err
	}
	result = result.Union(remainingCPUs)
	if assume {
		state, status := getPreFilterState(cycleState)
		if !status.IsSuccess() {
			return status.AsError()
		}
		if state.skip {
			return nil
		}

		p.cpuManager.UpdateAllocatedCPUSet(nodeName, pod.UID, result, state.preferredCPUExclusivePolicy)
		state.allocatedCPUs = result
	}
	return nil
}

func (p *Plugin) generateCPUTopologyHints(topology *CPUTopology, availableCPUs cpuset.CPUSet, reusableCPUs cpuset.CPUSet, request int) []frameworkext.NUMATopologyHint {
	// Initialize minAffinitySize to include all NUMA Nodes.
	minAffinitySize := topology.CPUDetails.NUMANodes().Size()

	// Iterate through all combinations of numa nodes bitmask and build hints from them.
	hints := []frameworkext.NUMATopologyHint{}
	bitmask.IterateBitMasks(topology.CPUDetails.NUMANodes().ToSlice(), func(mask bitmask.BitMask) {
		// First, update minAffinitySize for the current request size.
		cpusInMask := topology.CPUDetails.CPUsInNUMANodes(mask.GetBits()...).Size()
		if cpusInMask >= request && mask.Count() < minAffinitySize {
			minAffinitySize = mask.Count()
		}

		// Then check to see if we have enough CPUs available on the current
		// numa node bitmask to satisfy the CPU request.
		numMatching := 0
		for _, c := range reusableCPUs.ToSlice() {
			// Disregard this mask if its NUMANode isn't part of it.
			if !mask.IsSet(topology.CPUDetails[c].NodeID) {
				return
			}
			numMatching++
		}

		// Finally, check to see if enough available CPUs remain on the current
		// NUMA node combination to satisfy the CPU request.
		for _, c := range availableCPUs.ToSlice() {
			if mask.IsSet(topology.CPUDetails[c].NodeID) {
				numMatching++
			}
		}

		// If they don't, then move onto the next combination.
		if numMatching < request {
			return
		}

		// Otherwise, create a new hint from the numa node bitmask and add it to the
		// list of hints.  We set all hint preferences to 'false' on the first
		// pass through.
		hints = append(hints, frameworkext.NUMATopologyHint{
			NUMANodeAffinity: mask,
			Preferred:        false,
		})
	})

	// Loop back through all hints and update the 'Preferred' field based on
	// counting the number of bits sets in the affinity mask and comparing it
	// to the minAffinitySize. Only those with an equal number of bits set (and
	// with a minimal set of numa nodes) will be considered preferred.
	for i := range hints {
		if hints[i].NUMANodeAffinity.Count() == minAffinitySize {
			hints[i].Preferred = true
		}
	}

	return hints
}

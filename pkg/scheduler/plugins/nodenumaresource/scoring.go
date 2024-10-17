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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// resourceStrategyTypeMap maps strategy to scorer implementation
var resourceStrategyTypeMap = map[schedulingconfig.ScoringStrategyType]scorer{
	schedulingconfig.LeastAllocated: func(args *schedulingconfig.NodeNUMAResourceArgs) *resourceAllocationScorer {
		resToWeightMap := resourcesToWeightMap(args.ScoringStrategy.Resources)
		return &resourceAllocationScorer{
			Name:                string(schedconfig.LeastAllocated),
			scorer:              leastResourceScorer(resToWeightMap),
			resourceToWeightMap: resToWeightMap,
		}
	},
	schedulingconfig.MostAllocated: func(args *schedulingconfig.NodeNUMAResourceArgs) *resourceAllocationScorer {
		resToWeightMap := resourcesToWeightMap(args.ScoringStrategy.Resources)
		return &resourceAllocationScorer{
			Name:                string(schedconfig.MostAllocated),
			scorer:              mostResourceScorer(resToWeightMap),
			resourceToWeightMap: resToWeightMap,
		}
	},
}

func (p *Plugin) PreScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return framework.NewStatus(framework.Skip)
	}
	return nil
}

func (p *Plugin) Score(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return 0, status
	}
	if state.skip {
		return 0, nil
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}
	node := nodeInfo.Node()
	topologyOptions := p.topologyOptionsManager.GetTopologyOptions(node.Name)
	nodeCPUBindPolicy := extension.GetNodeCPUBindPolicy(node.Labels, topologyOptions.Policy)
	podNUMATopologyPolicy := state.podNUMATopologyPolicy
	numaTopologyPolicy := getNUMATopologyPolicy(node.Labels, topologyOptions.NUMATopologyPolicy)
	// we have check in filter, so we will not get error in reserve
	numaTopologyPolicy, _ = mergeTopologyPolicy(numaTopologyPolicy, podNUMATopologyPolicy)
	requestCPUBind, status := requestCPUBind(state, nodeCPUBindPolicy)
	if !status.IsSuccess() {
		return 0, status
	}
	if requestCPUBind && !topologyOptions.CPUTopology.IsValid() {
		return 0, nil
	}

	store := topologymanager.GetStore(cycleState)
	affinity, _ := store.GetAffinity(nodeName)
	resourceOptions, err := p.getResourceOptions(state, node, pod, requestCPUBind, affinity, topologyOptions)
	if err != nil {
		return 0, nil
	}

	if numaTopologyPolicy == extension.NUMATopologyPolicyNone {
		return p.scoreWithAmplifiedCPUs(state, nodeInfo, resourceOptions)
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)
	podAllocation, status := p.allocateWithNominatedReservation(restoreState, resourceOptions, pod, node)
	if !status.IsSuccess() {
		return 0, status
	}
	if podAllocation == nil {
		podAllocation, status = tryAllocateFromNode(p.resourceManager, restoreState, resourceOptions, pod, node)
		if !status.IsSuccess() {
			return 0, status
		}
	}

	allocatable, requested := p.calculateAllocatableAndRequested(node.Name, nodeInfo, podAllocation, resourceOptions)
	return p.scorer.score(requested, allocatable, framework.NewResource(resourceOptions.requests))
}

func (p *Plugin) scoreWithAmplifiedCPUs(state *preFilterState, nodeInfo *framework.NodeInfo, resourceOptions *ResourceOptions) (int64, *framework.Status) {
	quantity := state.requests[corev1.ResourceCPU]
	cpuAmplificationRatio := resourceOptions.topologyOptions.AmplificationRatios[corev1.ResourceCPU]
	if quantity.IsZero() || cpuAmplificationRatio <= 1 {
		return p.scorer.score(nodeInfo.Requested, nodeInfo.Allocatable, framework.NewResource(resourceOptions.requests))
	}

	node := nodeInfo.Node()
	_, allocated, err := p.resourceManager.GetAvailableCPUs(node.Name, resourceOptions.preferredCPUs)
	if err != nil {
		return 0, nil
	}
	allocatedMilliCPU := int64(allocated.CPUs().Size() * 1000)
	requested := nodeInfo.Requested.Clone()
	requested.MilliCPU -= allocatedMilliCPU
	requested.MilliCPU += extension.Amplify(allocatedMilliCPU, cpuAmplificationRatio)
	return p.scorer.score(requested, nodeInfo.Allocatable, framework.NewResource(resourceOptions.requests))
}

func (p *Plugin) calculateAllocatableAndRequested(
	nodeName string,
	nodeInfo *framework.NodeInfo,
	podAllocation *PodAllocation,
	resourceOptions *ResourceOptions,
) (allocatable, requested *framework.Resource) {
	nodeAllocation := p.resourceManager.GetNodeAllocation(nodeName)
	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()

	topologyOptions := resourceOptions.topologyOptions

	if len(podAllocation.NUMANodeResources) > 0 {
		totalAllocatable := corev1.ResourceList{}
		totalRequested := corev1.ResourceList{}

		_, allocatedByNode := nodeAllocation.getAvailableNUMANodeResources(topologyOptions, resourceOptions.reusableResources)
		for _, v := range podAllocation.NUMANodeResources {
			if allocated := allocatedByNode[v.Node]; len(allocated) > 0 {
				util.AddResourceList(totalRequested, allocated)
			}

			for _, vv := range topologyOptions.NUMANodeResources {
				if vv.Node == v.Node {
					util.AddResourceList(totalAllocatable, vv.Resources)
					break
				}
			}
		}
		allocatable = framework.NewResource(totalAllocatable)
		requested = framework.NewResource(totalRequested)
	} else {
		allocatable = nodeInfo.Allocatable
		requested = nodeInfo.Requested
		if !podAllocation.CPUSet.IsEmpty() {
			requested = requested.Clone()
		}
	}

	if !podAllocation.CPUSet.IsEmpty() {
		preferred := resourceOptions.preferredCPUs.Difference(podAllocation.CPUSet)
		_, allocatedCPUSets := nodeAllocation.getAvailableCPUs(topologyOptions.CPUTopology, topologyOptions.MaxRefCount, topologyOptions.ReservedCPUs, preferred)
		cpus := allocatedCPUSets.CPUs().Size()
		requested.MilliCPU = extension.Amplify(int64(cpus*1000), topologyOptions.AmplificationRatios[corev1.ResourceCPU])
	}
	return
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// resourceToWeightMap contains resource name and weight.
type resourceToWeightMap map[corev1.ResourceName]int64

// scorer is decorator for resourceAllocationScorer
type scorer func(args *schedulingconfig.NodeNUMAResourceArgs) *resourceAllocationScorer

// resourceAllocationScorer contains information to calculate resource allocation score.
type resourceAllocationScorer struct {
	Name                string
	scorer              func(requested, allocatable resourceToValueMap) int64
	resourceToWeightMap resourceToWeightMap
}

// resourceToValueMap is keyed with resource name and valued with quantity.
type resourceToValueMap map[corev1.ResourceName]int64

// score will use `scorer` function to calculate the score.
func (r *resourceAllocationScorer) score(totalRequested, totalAllocatable, podRequests *framework.Resource) (int64, *framework.Status) {
	if r.resourceToWeightMap == nil {
		return 0, framework.NewStatus(framework.Error, "resources not found")
	}

	requested := make(resourceToValueMap)
	allocatable := make(resourceToValueMap)
	for resourceName := range r.resourceToWeightMap {
		alloc, req := calculateResourceAllocatableRequest(totalAllocatable, totalRequested, podRequests, resourceName)
		if alloc != 0 {
			// Only fill the extended resource entry when it's non-zero.
			allocatable[resourceName], requested[resourceName] = alloc, req
		}
	}
	score := r.scorer(requested, allocatable)
	return score, nil
}

func calculateResourceAllocatableRequest(allocatable, requested, podRequests *framework.Resource, resourceName corev1.ResourceName) (int64, int64) {
	podRequest := getResourceQuantity(podRequests, resourceName)
	// If it's an extended resource, and the pod doesn't request it. We return (0, 0)
	// as an implication to bypass scoring on this resource.
	if podRequest == 0 && schedutil.IsScalarResourceName(resourceName) {
		return 0, 0
	}
	switch resourceName {
	case corev1.ResourceCPU:
		return allocatable.MilliCPU, requested.MilliCPU + podRequest
	case corev1.ResourceMemory:
		return allocatable.Memory, requested.Memory + podRequest
	case corev1.ResourceEphemeralStorage:
		return allocatable.EphemeralStorage, requested.EphemeralStorage + podRequest
	default:
		if _, exists := allocatable.ScalarResources[resourceName]; exists {
			return allocatable.ScalarResources[resourceName], requested.ScalarResources[resourceName] + podRequest
		}
	}
	klog.V(10).InfoS("Requested resource is omitted for node score calculation", "resourceName", resourceName)
	return 0, 0
}

func getResourceQuantity(m *framework.Resource, resourceName corev1.ResourceName) int64 {
	switch resourceName {
	case corev1.ResourceCPU:
		return m.MilliCPU
	case corev1.ResourceMemory:
		return m.Memory
	case corev1.ResourceEphemeralStorage:
		return m.EphemeralStorage
	default:
		if _, exists := m.ScalarResources[resourceName]; exists {
			return m.ScalarResources[resourceName]
		}
	}
	return 0
}

// resourcesToWeightMap make weightmap from resources spec
func resourcesToWeightMap(resources []schedconfig.ResourceSpec) resourceToWeightMap {
	resourceToWeightMap := make(resourceToWeightMap)
	for _, resourceSpec := range resources {
		resourceToWeightMap[corev1.ResourceName(resourceSpec.Name)] = resourceSpec.Weight
	}
	return resourceToWeightMap
}

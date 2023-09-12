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
	"k8s.io/apimachinery/pkg/api/resource"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"

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
	if state.requestCPUBind && (topologyOptions.CPUTopology == nil || !topologyOptions.CPUTopology.IsValid()) {
		return 0, nil
	}

	policyType := getNUMATopologyPolicy(node.Labels, topologyOptions.NUMATopologyPolicy)
	if policyType != extension.NUMATopologyPolicyNone {
		store := topologymanager.GetStore(cycleState)
		affinity := store.GetAffinity(nodeName)
		podAllocation, status := p.allocateByHint(ctx, cycleState, pod, nodeName, affinity, topologyOptions, false)
		if !status.IsSuccess() {
			return 0, nil
		}

		totalAllocatable, totalAllocated, allocatedCPUSets := p.calculateAllocatableRequestByNUMA(node.Name, state.requests, podAllocation, topologyOptions)
		if state.requestCPUBind {
			totalAllocated[corev1.ResourceCPU] = *resource.NewMilliQuantity(int64(allocatedCPUSets*1000), resource.DecimalSI)
		}
		return p.scorer.score(totalAllocated, totalAllocatable)
	}

	if state.requestCPUBind {
		resourceOptions, err := p.getResourceOptions(cycleState, state, node, pod, topologymanager.NUMATopologyHint{}, topologyOptions)
		if err != nil {
			return 0, nil
		}
		podAllocation, err := p.resourceManager.Allocate(node, pod, resourceOptions)
		if err != nil {
			return 0, nil
		}
		allocatedCPUSets := p.calculateRequestedCPUSets(nodeName, podAllocation)
		allocated := corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewMilliQuantity(int64(allocatedCPUSets)*1000, resource.DecimalSI),
		}
		allocatable := corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewMilliQuantity(nodeInfo.Allocatable.MilliCPU, resource.DecimalSI),
		}
		return p.scorer.score(allocated, allocatable)
	}

	return 0, nil
}

func (p *Plugin) calculateAllocatableRequestByNUMA(
	nodeName string,
	requests corev1.ResourceList,
	podAllocation *PodAllocation,
	topologyOptions TopologyOptions,
) (totalAllocatable, totalAllocated corev1.ResourceList, allocatedCPUSets int) {
	nodeAllocation := p.resourceManager.GetNodeAllocation(nodeName)
	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()

	totalAllocatable = corev1.ResourceList{}
	totalAllocated = corev1.ResourceList{}

	var nodes []int
	for _, v := range podAllocation.NUMANodeResources {
		nodes = append(nodes, v.Node)
		allocated := nodeAllocation.allocatedResources[v.Node]
		if allocated != nil {
			util.AddResourceList(totalAllocated, allocated.Resources)
		}
		for _, vv := range topologyOptions.NUMANodeResources {
			if vv.Node == v.Node {
				util.AddResourceList(totalAllocatable, vv.Resources)
				break
			}
		}
	}
	util.AddResourceList(totalAllocated, requests)
	allocatedCPUSets = podAllocation.CPUSet.Union(nodeAllocation.allocatedCPUs.CPUsInNUMANodes(nodes...)).Size()
	return
}

func (p *Plugin) calculateRequestedCPUSets(nodeName string, podAllocation *PodAllocation) int {
	nodeAllocation := p.resourceManager.GetNodeAllocation(nodeName)
	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()

	return podAllocation.CPUSet.Union(nodeAllocation.allocatedCPUs.CPUs()).Size()
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
func (r *resourceAllocationScorer) score(totalRequested, totalAllocatable corev1.ResourceList) (int64, *framework.Status) {
	if r.resourceToWeightMap == nil {
		return 0, framework.NewStatus(framework.Error, "resources not found")
	}

	requested := make(resourceToValueMap)
	allocatable := make(resourceToValueMap)
	for resourceName := range r.resourceToWeightMap {
		alloc, req := getResourceQuantity(totalAllocatable, resourceName), getResourceQuantity(totalRequested, resourceName)
		if alloc != 0 {
			// Only fill the extended resource entry when it's non-zero.
			allocatable[resourceName], requested[resourceName] = alloc, req
		}
	}
	score := r.scorer(requested, allocatable)
	return score, nil
}

func getResourceQuantity(m corev1.ResourceList, resourceName corev1.ResourceName) int64 {
	quantity := m[resourceName]
	if quantity.IsZero() {
		return 0
	}
	switch resourceName {
	case corev1.ResourceCPU:
		return quantity.MilliValue()
	default:
		return quantity.Value()
	}
}

// resourcesToWeightMap make weightmap from resources spec
func resourcesToWeightMap(resources []schedconfig.ResourceSpec) resourceToWeightMap {
	resourceToWeightMap := make(resourceToWeightMap)
	for _, resourceSpec := range resources {
		resourceToWeightMap[corev1.ResourceName(resourceSpec.Name)] = resourceSpec.Weight
	}
	return resourceToWeightMap
}

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
	fwktype "k8s.io/kube-scheduler/framework"
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

func (p *Plugin) PreScore(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodes []fwktype.NodeInfo) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return fwktype.NewStatus(fwktype.Skip)
	}
	return nil
}

func (p *Plugin) Score(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) (int64, *fwktype.Status) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return 0, status
	}
	if state.skip {
		return 0, nil
	}

	nodeName := nodeInfo.Node().Name
	nodeInfoSnapshot, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, fwktype.NewStatus(fwktype.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}
	node := nodeInfoSnapshot.Node()
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
	resourceOptions, err := p.getResourceOptions(state, node, requestCPUBind, affinity, topologyOptions)
	if err != nil {
		return 0, nil
	}

	if numaTopologyPolicy == extension.NUMATopologyPolicyNone {
		return p.scoreWithAmplifiedCPUs(state, nodeInfoSnapshot, resourceOptions)
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)
	podAllocation, status := p.allocateWithNominated(restoreState, resourceOptions, pod, node)
	if !status.IsSuccess() {
		// Score-layer contract: the pod has already passed Filter on this node, so returning a
		// non-Success status here would be upgraded to framework.Error by the scheduling framework
		// and surface as SchedulerError on the Pod condition. Degrade to score=0 with nil status
		// so the scheduler can continue selecting other candidates naturally, and record a warning
		// for later investigation (it implies a mismatch between Filter and Score strictness).
		klog.Warningf("Score: allocateWithNominated failed after Filter passed, degrading to score=0; pod=%s node=%s reason=%s", klog.KObj(pod), node.Name, status.Message())
		return 0, nil
	}
	if podAllocation == nil {
		podAllocation, status = tryAllocateFromNode(p.resourceManager, nil, restoreState, resourceOptions, pod, node)
		if !status.IsSuccess() {
			// Same reasoning as above: degrade to score=0 instead of propagating Unschedulable/Error.
			klog.Warningf("Score: tryAllocateFromNode failed after Filter passed, degrading to score=0; pod=%s node=%s reason=%s", klog.KObj(pod), node.Name, status.Message())
			return 0, nil
		}
	}

	allocatable, requested := p.calculateAllocatableAndRequested(node.Name, nodeInfoSnapshot, podAllocation, resourceOptions)
	return p.scorer.score(requested, allocatable, framework.NewResource(resourceOptions.requests))
}

func (p *Plugin) scoreWithAmplifiedCPUs(state *preFilterState, nodeInfo fwktype.NodeInfo, resourceOptions *ResourceOptions) (int64, *fwktype.Status) {
	quantity := state.requests[corev1.ResourceCPU]
	cpuAmplificationRatio := resourceOptions.topologyOptions.AmplificationRatios[corev1.ResourceCPU]
	if quantity.IsZero() || cpuAmplificationRatio <= 1 {
		requestedFwk := nodeInfo.GetRequested().(*framework.Resource)
		allocatableFwk := nodeInfo.GetAllocatable().(*framework.Resource)
		return p.scorer.score(requestedFwk, allocatableFwk, framework.NewResource(resourceOptions.requests))
	}

	node := nodeInfo.Node()
	_, allocated, err := p.resourceManager.GetAvailableCPUs(node.Name, resourceOptions.preferredCPUs)
	if err != nil {
		return 0, nil
	}
	allocatedMilliCPU := int64(allocated.CPUs().Size() * 1000)
	requested := nodeInfo.GetRequested().(*framework.Resource).Clone()
	requested.MilliCPU -= allocatedMilliCPU
	requested.MilliCPU += extension.Amplify(allocatedMilliCPU, cpuAmplificationRatio)
	return p.scorer.score(requested, nodeInfo.GetAllocatable().(*framework.Resource), framework.NewResource(resourceOptions.requests))
}

func (p *Plugin) calculateAllocatableAndRequested(
	nodeName string,
	nodeInfo fwktype.NodeInfo,
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
		allocatable = nodeInfo.GetAllocatable().(*framework.Resource)
		requested = nodeInfo.GetRequested().(*framework.Resource)
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

func (p *Plugin) ScoreExtensions() fwktype.ScoreExtensions {
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
func (r *resourceAllocationScorer) score(totalRequested, totalAllocatable, podRequests *framework.Resource) (int64, *fwktype.Status) {
	if r.resourceToWeightMap == nil {
		return 0, fwktype.NewStatus(fwktype.Error, "resources not found")
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

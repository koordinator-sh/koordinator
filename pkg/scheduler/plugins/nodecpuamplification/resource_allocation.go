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

/*
This file contains code derived and modified from Kubernetes
which is licensed under below license:

Copyright 2017 The Kubernetes Authors.

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

package nodecpuamplification

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

// resourceToWeightMap contains resource name and weight.
type resourceToWeightMap map[corev1.ResourceName]int64

// scorer is decorator for resourceAllocationScorer
type scorer func(args *config.NodeCPUAmplificationArgs) *resourceAllocationScorer

// resourceAllocationScorer contains information to calculate resource allocation score.
type resourceAllocationScorer struct {
	Name string
	// used to decide whether to use Requested or NonZeroRequested for
	// cpu and memory.
	useRequested        bool
	scorer              func(requested, allocatable resourceToValueMap) int64
	resourceToWeightMap resourceToWeightMap
}

// resourceToValueMap is keyed with resource name and valued with quantity.
type resourceToValueMap map[corev1.ResourceName]int64

// score will use `scorer` function to calculate the score.
func (r *resourceAllocationScorer) score(
	pod *corev1.Pod,
	nodeInfo *framework.NodeInfo) (int64, *framework.Status) {
	node := nodeInfo.Node()
	if node == nil {
		return 0, framework.NewStatus(framework.Error, "node not found")
	}
	if r.resourceToWeightMap == nil {
		return 0, framework.NewStatus(framework.Error, "resources not found")
	}

	cpuAmpRatio, err := extension.GetNodeResourceAmplificationRatio(node, corev1.ResourceCPU)
	if err != nil {
		return 0, framework.AsStatus(err)
	}
	if cpuAmpRatio == -1 {
		// CPU amplification disabled is semantically equal to CPU amplification ratio 1
		cpuAmpRatio = 1
	}

	requested := make(resourceToValueMap)
	allocatable := make(resourceToValueMap)
	for resource := range r.resourceToWeightMap {
		alloc, req := r.calculateResourceAllocatableRequest(nodeInfo, pod, resource, cpuAmpRatio)
		if alloc != 0 {
			// Only fill the extended resource entry when it's non-zero.
			allocatable[resource], requested[resource] = alloc, req
		}
	}

	score := r.scorer(requested, allocatable)

	klog.V(10).InfoS("Listing internal info for allocatable resources, requested resources and score with Node CPU amplification",
		"pod", klog.KObj(pod), "node", klog.KObj(node), "resourceAllocationScorer", r.Name,
		"allocatableResource", allocatable, "requestedResource", requested, "resourceScore", score,
	)

	return score, nil
}

// calculateResourceAllocatableRequest returns 2 parameters:
// - 1st param: quantity of allocatable resource on the node.
// - 2nd param: aggregated quantity of requested resource on the node.
// Note: if it's an extended resource, and the pod doesn't request it, (0, 0) is returned.
func (r *resourceAllocationScorer) calculateResourceAllocatableRequest(nodeInfo *framework.NodeInfo, pod *corev1.Pod, resource corev1.ResourceName, cpuAmpRatio extension.Ratio) (int64, int64) {
	requested := nodeInfo.NonZeroRequested
	if r.useRequested {
		requested = nodeInfo.Requested
	}

	podRequest := calculatePodAmplifiedResourceRequest(pod, resource, cpuAmpRatio, !r.useRequested)
	// If it's an extended resource, and the pod doesn't request it. We return (0, 0)
	// as an implication to bypass scoring on this resource.
	if podRequest == 0 && schedutil.IsScalarResourceName(resource) {
		return 0, 0
	}
	switch resource {
	case corev1.ResourceCPU:
		return nodeInfo.Allocatable.MilliCPU, calculateNodeAmplifiedCPURequested(nodeInfo, cpuAmpRatio, !r.useRequested) + podRequest
	case corev1.ResourceMemory:
		return nodeInfo.Allocatable.Memory, requested.Memory + podRequest
	case corev1.ResourceEphemeralStorage:
		return nodeInfo.Allocatable.EphemeralStorage, nodeInfo.Requested.EphemeralStorage + podRequest
	default:
		if _, exists := nodeInfo.Allocatable.ScalarResources[resource]; exists {
			return nodeInfo.Allocatable.ScalarResources[resource], nodeInfo.Requested.ScalarResources[resource] + podRequest
		}
	}
	klog.V(10).InfoS("Requested resource is omitted for node score calculation", "resourceName", resource)
	return 0, 0
}

// resourcesToWeightMap make weightmap from resources spec
func resourcesToWeightMap(resources []schedconfig.ResourceSpec) resourceToWeightMap {
	resourceToWeightMap := make(resourceToWeightMap)
	for _, resource := range resources {
		resourceToWeightMap[corev1.ResourceName(resource.Name)] = resource.Weight
	}
	return resourceToWeightMap
}

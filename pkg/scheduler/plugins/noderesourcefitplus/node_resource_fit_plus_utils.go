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

package noderesourcesfitplus

import (
	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	k8sConfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

type ResourceAllocationPriority struct {
	scorer func(nodeName string, args *config.NodeResourcesFitPlusArgs, requestedMap, allocatableMap map[v1.ResourceName]int64) int64
}

func mostRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		requested = capacity
	}

	return requested * framework.MaxNodeScore / capacity
}

func leastRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return ((capacity - requested) * framework.MaxNodeScore) / capacity
}

func resourceScorer(nodeName string, args *config.NodeResourcesFitPlusArgs, requestedMap, allocatableMap map[v1.ResourceName]int64) int64 {
	var nodeScore int64
	var weightSum int64

	for resourceName, requested := range requestedMap {
		if _, ok := args.Resources[resourceName]; !ok {
			continue
		}
		resourceArgs := args.Resources[resourceName]

		var resourceScore int64

		switch resourceArgs.Type {
		case k8sConfig.MostAllocated:
			resourceScore = mostRequestedScore(requested, allocatableMap[resourceName])
		case k8sConfig.LeastAllocated:
			resourceScore = leastRequestedScore(requested, allocatableMap[resourceName])
		}
		nodeScore += resourceScore * resourceArgs.Weight
		weightSum += resourceArgs.Weight

	}
	if weightSum == 0 {
		return framework.MaxNodeScore
	}

	i := nodeScore / weightSum

	return i
}

func (r *ResourceAllocationPriority) getResourceScore(args *config.NodeResourcesFitPlusArgs, podRequestNames []v1.ResourceName, pod *v1.Pod, nodeInfo *framework.NodeInfo, nodeName string) int64 {
	requested := make(resourceToValueMap, len(podRequestNames))
	allocatable := make(resourceToValueMap, len(podRequestNames))
	for _, resourceName := range podRequestNames {
		allocatable[resourceName], requested[resourceName] = calculateResourceAllocatableRequest(nodeInfo, pod, resourceName)
	}

	score := r.scorer(nodeName, args, requested, allocatable)

	return score
}

func computePodResourceRequest(pod *v1.Pod) *preScoreState {
	// pod hasn't scheduled yet so we don't need to worry about InPlacePodVerticalScalingEnabled
	reqs := resource.PodRequests(pod, resource.PodResourcesOptions{})
	result := &preScoreState{}
	result.SetMaxResource(reqs)
	result.ResourceName = fitsPodRequestName(result.Resource)
	return result
}

// resourceToValueMap contains resource name and score.
type resourceToValueMap map[v1.ResourceName]int64

// calculateResourceAllocatableRequest returns resources Allocatable and Requested values
func calculateResourceAllocatableRequest(nodeInfo *framework.NodeInfo, pod *v1.Pod, resource v1.ResourceName) (int64, int64) {
	podRequest := calculatePodResourceRequest(pod, resource)
	switch resource {
	case v1.ResourceCPU:
		return nodeInfo.Allocatable.MilliCPU, nodeInfo.NonZeroRequested.MilliCPU + podRequest
	case v1.ResourceMemory:
		return nodeInfo.Allocatable.Memory, nodeInfo.NonZeroRequested.Memory + podRequest

	case v1.ResourceEphemeralStorage:
		return nodeInfo.Allocatable.EphemeralStorage, nodeInfo.Requested.EphemeralStorage + podRequest
	default:
		if schedutil.IsScalarResourceName(resource) {
			return nodeInfo.Allocatable.ScalarResources[resource], nodeInfo.Requested.ScalarResources[resource] + podRequest
		}
	}
	if klog.V(10).Enabled() {
		klog.Infof("requested resource %v not considered for node score calculation",
			resource,
		)
	}
	return 0, 0
}

// calculatePodResourceRequest returns the total non-zero requests. If Overhead is defined for the pod and the
// PodOverhead feature is enabled, the Overhead is added to the result.
// podResourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers) + overHead
func calculatePodResourceRequest(pod *v1.Pod, resource v1.ResourceName) int64 {
	var podRequest int64
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		value := GetNonzeroRequestForResource(resource, &container.Resources.Requests)
		podRequest += value
	}

	for i := range pod.Spec.InitContainers {
		initContainer := &pod.Spec.InitContainers[i]
		value := GetNonzeroRequestForResource(resource, &initContainer.Resources.Requests)
		if podRequest < value {
			podRequest = value
		}
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil && utilfeature.DefaultFeatureGate.Enabled("PodOverhead") {
		if quantity, found := pod.Spec.Overhead[resource]; found {
			podRequest += quantity.Value()
		}
	}

	return podRequest
}

// GetNonzeroRequestForResource returns the default resource request if none is found or
// what is provided on the request.
func GetNonzeroRequestForResource(resource v1.ResourceName, requests *v1.ResourceList) int64 {
	switch resource {
	case v1.ResourceCPU:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[v1.ResourceCPU]; !found {
			return schedutil.DefaultMilliCPURequest
		}
		return requests.Cpu().MilliValue()
	case v1.ResourceMemory:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[v1.ResourceMemory]; !found {
			return schedutil.DefaultMemoryRequest
		}
		return requests.Memory().Value()
	case v1.ResourceEphemeralStorage:
		quantity, found := (*requests)[v1.ResourceEphemeralStorage]
		if !found {
			return 0
		}
		return quantity.Value()
	default:
		if schedutil.IsScalarResourceName(resource) {
			quantity, found := (*requests)[resource]
			if !found {
				return 0
			}
			return quantity.Value()
		}
	}
	return 0
}

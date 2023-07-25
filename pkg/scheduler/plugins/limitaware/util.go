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

package limitaware

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func getPodResourceLimit(pod *corev1.Pod, nonZero bool) (result *framework.Resource) {
	result = &framework.Resource{}
	non0CPU, non0Mem := int64(0), int64(0)
	for _, container := range pod.Spec.Containers {
		limit := quotav1.Max(container.Resources.Limits, container.Resources.Requests)
		result.Add(limit)
		non0CPUReq, non0MemReq := schedutil.GetNonzeroRequests(&limit)
		non0CPU += non0CPUReq
		non0Mem += non0MemReq
	}
	// take max_resource(sum_pod, any_init_container)
	for _, container := range pod.Spec.InitContainers {
		limit := quotav1.Max(container.Resources.Limits, container.Resources.Requests)
		result.SetMaxResource(limit)
		non0CPUReq, non0MemReq := schedutil.GetNonzeroRequests(&limit)
		non0CPU = max(non0CPU, non0CPUReq)
		non0Mem = max(non0Mem, non0MemReq)
	}
	// If Overhead is being utilized, add to the total limits for the pod
	if pod.Spec.Overhead != nil {
		result.Add(pod.Spec.Overhead)
		if _, found := pod.Spec.Overhead[corev1.ResourceCPU]; found {
			non0CPU += pod.Spec.Overhead.Cpu().MilliValue()
		}
		if _, found := pod.Spec.Overhead[corev1.ResourceMemory]; found {
			non0Mem += pod.Spec.Overhead.Memory().Value()
		}
	}
	if nonZero {
		result.MilliCPU = non0CPU
		result.Memory = non0Mem
	}
	return
}

func max(a, b int64) int64 {
	if a >= b {
		return a
	}
	return b
}

func getNodeLimitCapacity(ratio extension.LimitToAllocatableRatio, nodeAllocatable *framework.Resource) (*framework.Resource, error) {
	milliCPU, err := ratio.GetLimitForResource(corev1.ResourceCPU, nodeAllocatable.MilliCPU)
	if err != nil {
		return nil, err
	}
	memory, err := ratio.GetLimitForResource(corev1.ResourceMemory, nodeAllocatable.Memory)
	if err != nil {
		return nil, err
	}
	ephemeralStorage, err := ratio.GetLimitForResource(corev1.ResourceEphemeralStorage, nodeAllocatable.EphemeralStorage)
	if err != nil {
		return nil, err
	}
	nodeLimitCapacity := &framework.Resource{
		MilliCPU:         milliCPU,
		Memory:           memory,
		EphemeralStorage: ephemeralStorage,
	}
	for resourceName, quantity := range nodeAllocatable.ScalarResources {
		limit, err := ratio.GetLimitForResource(resourceName, quantity)
		if err != nil {
			return nil, err
		}
		if nodeLimitCapacity.ScalarResources == nil {
			nodeLimitCapacity.ScalarResources = map[corev1.ResourceName]int64{}
		}
		nodeLimitCapacity.ScalarResources[resourceName] = limit
	}
	return nodeLimitCapacity, nil
}

// isDaemonSetPod returns true if the pod is a IsDaemonSetPod.
func isDaemonSetPod(ownerRefList []metav1.OwnerReference) bool {
	for _, ownerRef := range ownerRefList {
		if ownerRef.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

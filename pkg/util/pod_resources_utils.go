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

package util

import (
	corev1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	resourcehelper "k8s.io/component-helpers/resource"
)

// NOTE: functions in this file can be overwritten for extension

func GetPodMilliCPULimit(pod *corev1.Pod) int64 {
	podCPUMilliLimit := int64(0)
	for _, container := range pod.Spec.Containers {
		containerCPUMilliLimit := GetContainerMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			return -1
		}
		podCPUMilliLimit += containerCPUMilliLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliLimit := GetContainerMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			return -1
		}
		podCPUMilliLimit = MaxInt64(podCPUMilliLimit, containerCPUMilliLimit)
	}
	if podCPUMilliLimit <= 0 {
		return -1
	}
	return podCPUMilliLimit
}

func GetPodRequest(pod *corev1.Pod, resourceNames ...corev1.ResourceName) corev1.ResourceList {
	result := resourcehelper.PodRequests(pod, resourcehelper.PodResourcesOptions{})
	if len(resourceNames) > 0 {
		result = quotav1.Mask(result, resourceNames)
	}
	return result
}

// GetPodBEMilliCPURequest returns the Batch (BE) milli-CPU request for a given pod.
// It aligns with standard Kubernetes execution semantics which computes pod CPU request as:
// max(sum(standard containers), maximum of init containers) + pod overhead.
func GetPodBEMilliCPURequest(pod *corev1.Pod) int64 {
	podCPUMilliReq := int64(0)
	for _, container := range pod.Spec.Containers {
		containerCPUMilliReq := GetContainerBatchMilliCPURequest(&container)
		if containerCPUMilliReq <= 0 {
			containerCPUMilliReq = 0
		}
		podCPUMilliReq += containerCPUMilliReq
	}

	initCPUMilliReq := int64(0)
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliReq := GetContainerBatchMilliCPURequest(&container)
		if containerCPUMilliReq <= 0 {
			containerCPUMilliReq = 0
		}
		if containerCPUMilliReq > initCPUMilliReq {
			initCPUMilliReq = containerCPUMilliReq
		}
	}

	if initCPUMilliReq > podCPUMilliReq {
		podCPUMilliReq = initCPUMilliReq
	}

	if pod.Spec.Overhead != nil {
		if overheadCPU, ok := pod.Spec.Overhead[apiext.BatchCPU]; ok {
			podCPUMilliReq += overheadCPU.MilliValue()
		} else if overheadCPU, ok := pod.Spec.Overhead[corev1.ResourceCPU]; ok {
			podCPUMilliReq += overheadCPU.MilliValue()
		}
	}

	return podCPUMilliReq
}

// GetPodBEMilliCPULimit returns the Batch (BE) milli-CPU limit for a given pod.
// Calculates pod limit as: max(sum(standard containers), maximum of init containers) + pod overhead.
func GetPodBEMilliCPULimit(pod *corev1.Pod) int64 {
	podCPUMilliLimit := int64(0)
	for _, container := range pod.Spec.Containers {
		containerCPUMilliLimit := GetContainerBatchMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			podCPUMilliLimit = -1
			break
		}
		podCPUMilliLimit += containerCPUMilliLimit
	}

	initCPUMilliLimit := int64(0)
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliLimit := GetContainerBatchMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			initCPUMilliLimit = -1
			break
		}
		if containerCPUMilliLimit > initCPUMilliLimit {
			initCPUMilliLimit = containerCPUMilliLimit
		}
	}

	if podCPUMilliLimit == -1 || initCPUMilliLimit == -1 {
		podCPUMilliLimit = -1
	} else if initCPUMilliLimit > podCPUMilliLimit {
		podCPUMilliLimit = initCPUMilliLimit
	}

	if podCPUMilliLimit > 0 && pod.Spec.Overhead != nil {
		if overheadCPU, ok := pod.Spec.Overhead[apiext.BatchCPU]; ok {
			podCPUMilliLimit += overheadCPU.MilliValue()
		} else if overheadCPU, ok := pod.Spec.Overhead[corev1.ResourceCPU]; ok {
			podCPUMilliLimit += overheadCPU.MilliValue()
		}
	}

	if podCPUMilliLimit <= 0 {
		return -1
	}
	return podCPUMilliLimit
}

// GetPodBEMemoryByteRequestIgnoreUnlimited returns the Batch (BE) memory user request in bytes, 
// ignoring unlimited requests, while properly evaluating max init container needs and pod overhead.
func GetPodBEMemoryByteRequestIgnoreUnlimited(pod *corev1.Pod) int64 {
	podMemoryByteRequest := int64(0)
	for _, container := range pod.Spec.Containers {
		containerMemByteRequest := GetContainerBatchMemoryByteRequest(&container)
		if containerMemByteRequest < 0 {
			// consider request of unlimited container as 0
			continue
		}
		podMemoryByteRequest += containerMemByteRequest
	}

	initMemoryByteRequest := int64(0)
	for _, container := range pod.Spec.InitContainers {
		containerMemByteRequest := GetContainerBatchMemoryByteRequest(&container)
		if containerMemByteRequest < 0 {
			continue
		}
		if containerMemByteRequest > initMemoryByteRequest {
			initMemoryByteRequest = containerMemByteRequest
		}
	}

	if initMemoryByteRequest > podMemoryByteRequest {
		podMemoryByteRequest = initMemoryByteRequest
	}

	if pod.Spec.Overhead != nil {
		if overheadMem, ok := pod.Spec.Overhead[apiext.BatchMemory]; ok {
			podMemoryByteRequest += overheadMem.Value()
		} else if overheadMem, ok := pod.Spec.Overhead[corev1.ResourceMemory]; ok {
			podMemoryByteRequest += overheadMem.Value()
		}
	}

	return podMemoryByteRequest
}

// GetPodBEMemoryByteLimit returns the Batch (BE) memory limit in bytes for a pod summing containers 
// and init containers limits with overhead.
func GetPodBEMemoryByteLimit(pod *corev1.Pod) int64 {
	podMemoryByteLimit := int64(0)
	for _, container := range pod.Spec.Containers {
		containerMemByteLimit := GetContainerBatchMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			podMemoryByteLimit = -1
			break
		}
		podMemoryByteLimit += containerMemByteLimit
	}

	initMemoryByteLimit := int64(0)
	for _, container := range pod.Spec.InitContainers {
		containerMemByteLimit := GetContainerBatchMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			initMemoryByteLimit = -1
			break
		}
		if containerMemByteLimit > initMemoryByteLimit {
			initMemoryByteLimit = containerMemByteLimit
		}
	}

	if podMemoryByteLimit == -1 || initMemoryByteLimit == -1 {
		podMemoryByteLimit = -1
	} else if initMemoryByteLimit > podMemoryByteLimit {
		podMemoryByteLimit = initMemoryByteLimit
	}

	if podMemoryByteLimit > 0 && pod.Spec.Overhead != nil {
		if overheadMem, ok := pod.Spec.Overhead[apiext.BatchMemory]; ok {
			podMemoryByteLimit += overheadMem.Value()
		} else if overheadMem, ok := pod.Spec.Overhead[corev1.ResourceMemory]; ok {
			podMemoryByteLimit += overheadMem.Value()
		}
	}

	if podMemoryByteLimit <= 0 {
		return -1
	}
	return podMemoryByteLimit
}

// AddResourceList adds the resources in newList to list.
func AddResourceList(list, newList corev1.ResourceList) {
	for name, quantity := range newList {
		if value, ok := list[name]; !ok {
			list[name] = quantity.DeepCopy()
		} else {
			value.Add(quantity)
			list[name] = value
		}
	}
}

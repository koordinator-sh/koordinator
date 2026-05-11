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

func GetPodBEMilliCPURequest(pod *corev1.Pod) int64 {
	podCPUMilliReq := int64(0)
	for _, container := range pod.Spec.Containers {
		containerCPUMilliReq := GetContainerBatchMilliCPURequest(&container)
		if containerCPUMilliReq <= 0 {
			containerCPUMilliReq = 0
		}
		podCPUMilliReq += containerCPUMilliReq
	}
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliReq := GetContainerBatchMilliCPURequest(&container)
		if containerCPUMilliReq <= 0 {
			containerCPUMilliReq = 0
		}
		podCPUMilliReq = MaxInt64(podCPUMilliReq, containerCPUMilliReq)
	}
	if overheadCPUMilliReq := GetBatchMilliCPUFromResourceList(pod.Spec.Overhead); overheadCPUMilliReq > 0 {
		podCPUMilliReq += overheadCPUMilliReq
	}

	return podCPUMilliReq
}

func GetPodBEMilliCPULimit(pod *corev1.Pod) int64 {
	podCPUMilliLimit := int64(0)
	for _, container := range pod.Spec.Containers {
		containerCPUMilliLimit := GetContainerBatchMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			return -1
		}
		podCPUMilliLimit += containerCPUMilliLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliLimit := GetContainerBatchMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			return -1
		}
		podCPUMilliLimit = MaxInt64(podCPUMilliLimit, containerCPUMilliLimit)
	}
	if podCPUMilliLimit <= 0 {
		return -1
	}
	if overheadCPUMilliLimit := GetBatchMilliCPUFromResourceList(pod.Spec.Overhead); overheadCPUMilliLimit > 0 {
		podCPUMilliLimit += overheadCPUMilliLimit
	}
	return podCPUMilliLimit
}

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
	for _, container := range pod.Spec.InitContainers {
		containerMemByteRequest := GetContainerBatchMemoryByteRequest(&container)
		if containerMemByteRequest < 0 {
			// consider request of unlimited container as 0
			containerMemByteRequest = 0
		}
		podMemoryByteRequest = MaxInt64(podMemoryByteRequest, containerMemByteRequest)
	}
	if overheadMemByteRequest := GetBatchMemoryFromResourceList(pod.Spec.Overhead); overheadMemByteRequest > 0 {
		podMemoryByteRequest += overheadMemByteRequest
	}
	return podMemoryByteRequest
}

func GetPodBEMemoryByteLimit(pod *corev1.Pod) int64 {
	podMemoryByteLimit := int64(0)
	for _, container := range pod.Spec.Containers {
		containerMemByteLimit := GetContainerBatchMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			return -1
		}
		podMemoryByteLimit += containerMemByteLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerMemByteLimit := GetContainerBatchMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			return -1
		}
		podMemoryByteLimit = MaxInt64(podMemoryByteLimit, containerMemByteLimit)
	}
	if podMemoryByteLimit <= 0 {
		return -1
	}
	if overheadMemByteLimit := GetBatchMemoryFromResourceList(pod.Spec.Overhead); overheadMemByteLimit > 0 {
		podMemoryByteLimit += overheadMemByteLimit
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

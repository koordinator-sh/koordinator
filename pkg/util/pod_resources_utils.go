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
	sidecarCPUMilliLimit := int64(0)
	maxInitCPUMilliLimit := int64(0)

	for _, container := range pod.Spec.Containers {
		containerCPUMilliLimit := GetContainerMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			podCPUMilliLimit = -1
			break
		}
		podCPUMilliLimit += containerCPUMilliLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliLimit := GetContainerMilliCPULimit(&container)
		if IsSidecarContainer(&container) {
			if containerCPUMilliLimit <= 0 {
				sidecarCPUMilliLimit = -1
			} else if sidecarCPUMilliLimit >= 0 {
				sidecarCPUMilliLimit += containerCPUMilliLimit
			}
		} else {
			if containerCPUMilliLimit <= 0 {
				maxInitCPUMilliLimit = -1
			} else if maxInitCPUMilliLimit >= 0 {
				maxInitCPUMilliLimit = MaxInt64(maxInitCPUMilliLimit, containerCPUMilliLimit)
			}
		}
	}

	if podCPUMilliLimit >= 0 && sidecarCPUMilliLimit >= 0 {
		podCPUMilliLimit += sidecarCPUMilliLimit
	} else {
		podCPUMilliLimit = -1
	}

	if podCPUMilliLimit < 0 || maxInitCPUMilliLimit < 0 {
		return -1
	}
	return MaxInt64(podCPUMilliLimit, maxInitCPUMilliLimit)
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
	sidecarCPUMilliReq := int64(0)
	maxInitCPUMilliReq := int64(0)

	for _, container := range pod.Spec.Containers {
		containerCPUMilliReq := GetContainerBatchMilliCPURequest(&container)
		if containerCPUMilliReq > 0 {
			podCPUMilliReq += containerCPUMilliReq
		}
	}
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliReq := GetContainerBatchMilliCPURequest(&container)
		if IsSidecarContainer(&container) {
			if containerCPUMilliReq > 0 {
				sidecarCPUMilliReq += containerCPUMilliReq
			}
		} else {
			if containerCPUMilliReq > 0 {
				maxInitCPUMilliReq = MaxInt64(maxInitCPUMilliReq, containerCPUMilliReq)
			}
		}
	}

	return MaxInt64(maxInitCPUMilliReq, podCPUMilliReq+sidecarCPUMilliReq)
}

func GetPodBEMilliCPULimit(pod *corev1.Pod) int64 {
	podCPUMilliLimit := int64(0)
	sidecarCPUMilliLimit := int64(0)
	maxInitCPUMilliLimit := int64(0)

	for _, container := range pod.Spec.Containers {
		containerCPUMilliLimit := GetContainerBatchMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			podCPUMilliLimit = -1
			break
		}
		podCPUMilliLimit += containerCPUMilliLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliLimit := GetContainerBatchMilliCPULimit(&container)
		if IsSidecarContainer(&container) {
			if containerCPUMilliLimit <= 0 {
				sidecarCPUMilliLimit = -1
			} else if sidecarCPUMilliLimit >= 0 {
				sidecarCPUMilliLimit += containerCPUMilliLimit
			}
		} else {
			if containerCPUMilliLimit <= 0 {
				maxInitCPUMilliLimit = -1
			} else if maxInitCPUMilliLimit >= 0 {
				maxInitCPUMilliLimit = MaxInt64(maxInitCPUMilliLimit, containerCPUMilliLimit)
			}
		}
	}

	if podCPUMilliLimit >= 0 && sidecarCPUMilliLimit >= 0 {
		podCPUMilliLimit += sidecarCPUMilliLimit
	} else {
		podCPUMilliLimit = -1
	}

	if podCPUMilliLimit < 0 || maxInitCPUMilliLimit < 0 {
		return -1
	}
	return MaxInt64(podCPUMilliLimit, maxInitCPUMilliLimit)
}

func GetPodBEMemoryByteRequestIgnoreUnlimited(pod *corev1.Pod) int64 {
	podMemoryByteRequest := int64(0)
	sidecarMemoryByteRequest := int64(0)
	maxInitMemoryByteRequest := int64(0)

	for _, container := range pod.Spec.Containers {
		containerMemByteRequest := GetContainerBatchMemoryByteRequest(&container)
		if containerMemByteRequest > 0 {
			podMemoryByteRequest += containerMemByteRequest
		}
	}
	for _, container := range pod.Spec.InitContainers {
		containerMemByteRequest := GetContainerBatchMemoryByteRequest(&container)
		if IsSidecarContainer(&container) {
			if containerMemByteRequest > 0 {
				sidecarMemoryByteRequest += containerMemByteRequest
			}
		} else {
			if containerMemByteRequest > 0 {
				maxInitMemoryByteRequest = MaxInt64(maxInitMemoryByteRequest, containerMemByteRequest)
			}
		}
	}
	return MaxInt64(maxInitMemoryByteRequest, podMemoryByteRequest+sidecarMemoryByteRequest)
}

func GetPodBEMemoryByteLimit(pod *corev1.Pod) int64 {
	podMemoryByteLimit := int64(0)
	sidecarMemoryByteLimit := int64(0)
	maxInitMemoryByteLimit := int64(0)

	for _, container := range pod.Spec.Containers {
		containerMemByteLimit := GetContainerBatchMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			podMemoryByteLimit = -1
			break
		}
		podMemoryByteLimit += containerMemByteLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerMemByteLimit := GetContainerBatchMemoryByteLimit(&container)
		if IsSidecarContainer(&container) {
			if containerMemByteLimit <= 0 {
				sidecarMemoryByteLimit = -1
			} else if sidecarMemoryByteLimit >= 0 {
				sidecarMemoryByteLimit += containerMemByteLimit
			}
		} else {
			if containerMemByteLimit <= 0 {
				maxInitMemoryByteLimit = -1
			} else if maxInitMemoryByteLimit >= 0 {
				maxInitMemoryByteLimit = MaxInt64(maxInitMemoryByteLimit, containerMemByteLimit)
			}
		}
	}

	if podMemoryByteLimit >= 0 && sidecarMemoryByteLimit >= 0 {
		podMemoryByteLimit += sidecarMemoryByteLimit
	} else {
		podMemoryByteLimit = -1
	}

	if podMemoryByteLimit < 0 || maxInitMemoryByteLimit < 0 {
		return -1
	}
	return MaxInt64(podMemoryByteLimit, maxInitMemoryByteLimit)
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

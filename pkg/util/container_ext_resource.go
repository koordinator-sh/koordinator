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

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func GetContainerBatchMilliCPURequest(c *corev1.Container) int64 {
	if cpuRequest, ok := c.Resources.Requests[extension.BatchCPU]; ok {
		return cpuRequest.Value()
	}
	return -1
}

func GetContainerBatchMilliCPULimit(c *corev1.Container) int64 {
	if cpuLimit, ok := c.Resources.Limits[extension.BatchCPU]; ok {
		return cpuLimit.Value()
	}
	return -1
}

func GetContainerBatchMemoryByteRequest(c *corev1.Container) int64 {
	if memLimit, ok := c.Resources.Requests[extension.BatchMemory]; ok {
		return memLimit.Value()
	}
	return -1
}

func GetContainerBatchMemoryByteLimit(c *corev1.Container) int64 {
	if memLimit, ok := c.Resources.Limits[extension.BatchMemory]; ok {
		return memLimit.Value()
	}
	return -1
}

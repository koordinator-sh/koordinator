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

// NOTE: functions in this file can be overwritten for extension

var ExtendedResourceNames = []corev1.ResourceName{
	extension.BatchCPU,
	extension.BatchMemory,
	extension.MidCPU,
	extension.MidMemory,
}

func GetExtendedMilliCPUFromResourceList(r corev1.ResourceList) int64 {
	// assert r != nil
	// suppose only one of mid-cpu and batch-cpu exists
	if milliCPU, ok := r[extension.MidCPU]; ok {
		return milliCPU.Value()
	}
	if milliCPU, ok := r[extension.BatchCPU]; ok {
		return milliCPU.Value()
	}
	return -1
}

func GetExtendedMemoryFromResourceList(r corev1.ResourceList) int64 {
	// assert r != nil
	// suppose only one of mid-memory and batch-memory exists
	if memory, ok := r[extension.MidMemory]; ok {
		return memory.Value()
	}
	if memory, ok := r[extension.BatchMemory]; ok {
		return memory.Value()
	}
	return -1
}

func GetContainerBatchMilliCPURequest(c *corev1.Container) int64 {
	return GetExtendedMilliCPUFromResourceList(c.Resources.Requests)
}

func GetContainerBatchMilliCPULimit(c *corev1.Container) int64 {
	return GetExtendedMilliCPUFromResourceList(c.Resources.Limits)
}

func GetContainerBatchMemoryByteRequest(c *corev1.Container) int64 {
	return GetExtendedMemoryFromResourceList(c.Resources.Requests)
}

func GetContainerBatchMemoryByteLimit(c *corev1.Container) int64 {
	return GetExtendedMemoryFromResourceList(c.Resources.Limits)
}

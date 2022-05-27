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

package extension

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	BatchCPU    corev1.ResourceName = DomainPrefix + "batch-cpu"
	BatchMemory corev1.ResourceName = DomainPrefix + "batch-memory"
)

var (
	ResourceNameMap = map[PriorityClass]map[corev1.ResourceName]corev1.ResourceName{
		PriorityBatch: {
			corev1.ResourceCPU:    BatchCPU,
			corev1.ResourceMemory: BatchMemory,
		},
	}
)

// TranslateResourceNameByPriorityClass translates defaultResourceName to extend resourceName by PriorityClass
func TranslateResourceNameByPriorityClass(priorityClass PriorityClass, defaultResourceName corev1.ResourceName) corev1.ResourceName {
	if priorityClass == PriorityProd || priorityClass == PriorityNone {
		return defaultResourceName
	}
	return ResourceNameMap[priorityClass][defaultResourceName]
}

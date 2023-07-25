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
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// AnnotationLimitToAllocatable The limit to allocatable ratio used by limit aware scheduler plugin, represent by percentage
	AnnotationLimitToAllocatable = NodeDomainPrefix + "/limit-to-allocatable"
)

type LimitToAllocatableRatio map[corev1.ResourceName]intstr.IntOrString

func (l LimitToAllocatableRatio) GetLimitForResource(resourceName corev1.ResourceName, allocatable int64) (int64, error) {
	percent := 100
	if ratioPercentage, ok := l[resourceName]; ok {
		var err error
		percent, err = intstr.GetScaledValueFromIntOrPercent(&ratioPercentage, 100, false)
		if err != nil {
			return 0, err
		}
	}
	return int64(percent) * allocatable / 100, nil
}

func GetNodeLimitToAllocatableRatio(annotations map[string]string, defaultLimitToAllocatableRatio LimitToAllocatableRatio) (LimitToAllocatableRatio, error) {
	data, ok := annotations[AnnotationLimitToAllocatable]
	if !ok {
		return defaultLimitToAllocatableRatio, nil
	}
	limitToAllocatableRatio := LimitToAllocatableRatio{}
	err := json.Unmarshal([]byte(data), &limitToAllocatableRatio)
	if err != nil {
		return nil, err
	}
	return limitToAllocatableRatio, nil
}

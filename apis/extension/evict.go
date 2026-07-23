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
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

func PodEvictEnabled(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	if pod.Labels == nil {
		return false
	}
	if enable, ok := pod.Labels[LabelPodEvictEnabled]; !ok || enable != "true" {
		return false
	}
	return true
}

// GetPodEvictionPriority parses the eviction priority from the pod annotations.
// It returns the implicit priority 0 when the annotation is missing or invalid.
func GetPodEvictionPriority(pod *corev1.Pod) (int32, error) {
	if pod == nil || pod.Annotations == nil {
		return 0, nil
	}
	value, exist := pod.Annotations[AnnotationPodEvictionPriority]
	if !exist {
		return 0, nil
	}
	i, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		// make sure we default to 0 on error
		return 0, fmt.Errorf("invalid value %q, err: %w", value, err)
	}
	return int32(i), nil
}

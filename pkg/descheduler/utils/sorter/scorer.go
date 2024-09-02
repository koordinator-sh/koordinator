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

package sorter

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func ResourceUsageScorer(resToWeightMap map[corev1.ResourceName]int64) func(requested, allocatable corev1.ResourceList) int64 {
	return func(requested, allocatable corev1.ResourceList) int64 {
		var nodeScore, weightSum int64
		for resourceName, quantity := range requested {
			weight := resToWeightMap[resourceName]
			resourceScore := mostRequestedScore(getResourceValue(resourceName, quantity), getResourceValue(resourceName, allocatable[resourceName]))
			nodeScore += resourceScore * weight
			weightSum += weight
		}
		if weightSum == 0 {
			return 0
		}
		return nodeScore / weightSum
	}
}

func ResourceUsageScorerPod(resToWeightMap map[corev1.ResourceName]int64) func(requested corev1.ResourceList, allocatable corev1.ResourceList) float64 {
	return func(requested, allocatable corev1.ResourceList) float64 {
		var nodeScore float64
		var weightSum int64
		for resourceName, quantity := range requested {
			weight := resToWeightMap[resourceName]
			resourceScore := mostRequestedScorePod(getResourceValue(resourceName, quantity), getResourceValue(resourceName, allocatable[resourceName]))
			nodeScore += resourceScore * float64(weight)
			weightSum += weight
		}
		if weightSum == 0 {
			return 0
		}
		return nodeScore / float64(weightSum)
	}
}

func mostRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		// `requested` might be greater than `capacity` because pods with no
		// requests get minimum values.
		requested = capacity
	}

	return (requested * 1000) / capacity
}

// mostRequestedScorePod The closer the request is to the capacity, the higher the score.The score will be higher when requested >= ratio
func mostRequestedScorePod(requested, capacity int64) float64 {
	if capacity == 0 {
		return 0
	}
	ratio := float64(requested) / float64(capacity)
	if ratio >= 1 {
		return 1/ratio + 1
	} else {
		return ratio
	}
}

func getResourceValue(resourceName corev1.ResourceName, quantity resource.Quantity) int64 {
	if resourceName == corev1.ResourceCPU {
		return quantity.MilliValue()
	}
	return quantity.Value()
}

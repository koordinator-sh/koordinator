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

package loadaware

import (
	"math"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	lowPriority      = int32(0)
	highPriority     = int32(10000)
	extendedResource = corev1.ResourceName("example.com/foo")
)

func TestResourceUsagePercentages(t *testing.T) {
	resourceUsagePercentage := resourceUsagePercentages(&NodeUsage{
		node: &corev1.Node{
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3977868*1024, resource.BinarySI),
					corev1.ResourcePods:   *resource.NewQuantity(29, resource.BinarySI),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(1930, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3287692*1024, resource.BinarySI),
					corev1.ResourcePods:   *resource.NewQuantity(29, resource.BinarySI),
				},
			},
		},
		usage: map[corev1.ResourceName]*resource.Quantity{
			corev1.ResourceCPU:    resource.NewMilliQuantity(1220, resource.DecimalSI),
			corev1.ResourceMemory: resource.NewQuantity(3038982964, resource.BinarySI),
			corev1.ResourcePods:   resource.NewQuantity(11, resource.BinarySI),
		},
	})

	expectedUsageInIntPercentage := map[corev1.ResourceName]float64{
		corev1.ResourceCPU:    63,
		corev1.ResourceMemory: 90,
		corev1.ResourcePods:   37,
	}

	for resourceName, percentage := range expectedUsageInIntPercentage {
		if math.Floor(resourceUsagePercentage[resourceName]) != percentage {
			t.Errorf("Incorrect percentange computation, expected %v, got math.Floor(%v) instead", percentage, resourceUsagePercentage[resourceName])
		}
	}

	t.Logf("resourceUsagePercentage: %#v\n", resourceUsagePercentage)
}

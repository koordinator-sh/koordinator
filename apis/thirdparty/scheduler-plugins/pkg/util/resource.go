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
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// ResourceList returns a resource list of this resource.
// Note: this code used to exist in k/k, but removed in k/k#101465.
func ResourceList(r *framework.Resource) v1.ResourceList {
	result := v1.ResourceList{
		v1.ResourceCPU:              *resource.NewMilliQuantity(r.MilliCPU, resource.DecimalSI),
		v1.ResourceMemory:           *resource.NewQuantity(r.Memory, resource.BinarySI),
		v1.ResourcePods:             *resource.NewQuantity(int64(r.AllowedPodNumber), resource.BinarySI),
		v1.ResourceEphemeralStorage: *resource.NewQuantity(r.EphemeralStorage, resource.BinarySI),
	}
	for rName, rQuant := range r.ScalarResources {
		if v1helper.IsHugePageResourceName(rName) {
			result[rName] = *resource.NewQuantity(rQuant, resource.BinarySI)
		} else {
			result[rName] = *resource.NewQuantity(rQuant, resource.DecimalSI)
		}
	}
	return result
}

// GetPodEffectiveRequest gets the effective request resource of a pod to the origin resource.
// The Pod's effective request is the higher of:
// - the sum of all app containers(spec.Containers) request for a resource.
// - the effective init containers(spec.InitContainers) request for a resource.
// The effective init containers request is the highest request on all init containers.
func GetPodEffectiveRequest(pod *v1.Pod) v1.ResourceList {
	initResources := make(v1.ResourceList)
	resources := make(v1.ResourceList)

	for _, container := range pod.Spec.InitContainers {
		for name, quantity := range container.Resources.Requests {
			if q, ok := initResources[name]; ok && quantity.Cmp(q) <= 0 {
				continue
			}
			initResources[name] = quantity
		}
	}
	for _, container := range pod.Spec.Containers {
		for name, quantity := range container.Resources.Requests {
			if q, ok := resources[name]; ok {
				quantity.Add(q)
			}
			resources[name] = quantity
		}
	}
	for name, quantity := range initResources {
		if q, ok := resources[name]; ok && quantity.Cmp(q) <= 0 {
			continue
		}
		resources[name] = quantity
	}
	return resources
}

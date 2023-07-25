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

package limitaware

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	preFilterStateKey = "preFilter" + Name
)

// preFilterStateData computed at PreFilter and used at Filter.
type preFilterStateData struct {
	resource *framework.Resource
	skip     bool
}

// Clone the prefilter state.
func (s *preFilterStateData) Clone() framework.StateData {
	return &preFilterStateData{resource: s.resource.Clone(), skip: true}
}

func (p *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	preFilterState := &preFilterStateData{}
	if isDaemonSetPod(pod.OwnerReferences) {
		preFilterState.skip = true
	}
	preFilterState.resource = getPodResourceLimit(pod, false)
	cycleState.Write(preFilterStateKey, preFilterState)
	return nil, nil
}

func getPreFilterState(cycleState *framework.CycleState) (*preFilterStateData, *framework.Status) {
	value, err := cycleState.Read(preFilterStateKey)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	state := value.(*preFilterStateData)
	return state, nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (p *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if status != nil {
		return status
	}
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}
	if state.skip {
		return nil
	}
	ratio, err := extension.GetNodeLimitToAllocatableRatio(node.Annotations, p.defaultLimitToAllocatableRatio)
	if err != nil {
		return framework.AsStatus(err)
	}
	nodeLimitCapacity, err := getNodeLimitCapacity(ratio, nodeInfo.Allocatable)
	if err != nil {
		return framework.AsStatus(err)
	}
	nodeLimitAllocated := p.nodeLimitsCache.GetNodeLimits(node.Name)
	insufficientResources := fitsLimit(ratio, state.resource, nodeLimitAllocated, nodeLimitCapacity)

	if len(insufficientResources) != 0 {
		// We will keep all failure reasons.
		failureReasons := make([]string, 0, len(insufficientResources))
		for i := range insufficientResources {
			failureReasons = append(failureReasons, insufficientResources[i].Reason)
		}
		return framework.NewStatus(framework.Unschedulable, failureReasons...)
	}
	return nil
}

func fitsLimit(resourceNameMap extension.LimitToAllocatableRatio, podLimits, nodeLimitAllocated, nodeLimitCapacity *framework.Resource) []noderesources.InsufficientResource {
	insufficientResources := make([]noderesources.InsufficientResource, 0, 2)
	if podLimits.MilliCPU == 0 &&
		podLimits.Memory == 0 &&
		podLimits.EphemeralStorage == 0 &&
		len(podLimits.ScalarResources) == 0 {
		return insufficientResources
	}
	if _, ok := resourceNameMap[corev1.ResourceCPU]; ok {
		if podLimits.MilliCPU > (nodeLimitCapacity.MilliCPU - nodeLimitAllocated.MilliCPU) {
			insufficientResources = append(insufficientResources, noderesources.InsufficientResource{
				ResourceName: corev1.ResourceCPU,
				Reason:       "Insufficient cpu limit",
				Requested:    podLimits.MilliCPU,
				Used:         nodeLimitAllocated.MilliCPU,
				Capacity:     nodeLimitCapacity.MilliCPU,
			})
		}
	}
	if _, ok := resourceNameMap[corev1.ResourceMemory]; ok {
		if podLimits.Memory > (nodeLimitCapacity.Memory - nodeLimitAllocated.Memory) {
			insufficientResources = append(insufficientResources, noderesources.InsufficientResource{
				ResourceName: corev1.ResourceMemory,
				Reason:       "Insufficient memory limit",
				Requested:    podLimits.Memory,
				Used:         nodeLimitAllocated.Memory,
				Capacity:     nodeLimitCapacity.Memory,
			})
		}
	}
	if _, ok := resourceNameMap[corev1.ResourceEphemeralStorage]; ok {
		if podLimits.EphemeralStorage > (nodeLimitCapacity.EphemeralStorage - nodeLimitAllocated.EphemeralStorage) {
			insufficientResources = append(insufficientResources, noderesources.InsufficientResource{
				ResourceName: corev1.ResourceEphemeralStorage,
				Reason:       "Insufficient ephemeral-storage limit",
				Requested:    podLimits.EphemeralStorage,
				Used:         nodeLimitAllocated.EphemeralStorage,
				Capacity:     nodeLimitCapacity.EphemeralStorage,
			})
		}
	}

	for rName, rQuant := range podLimits.ScalarResources {
		limitAllocated := int64(0)
		if nodeLimitAllocated.ScalarResources != nil {
			limitAllocated = nodeLimitAllocated.ScalarResources[rName]
		}
		if _, ok := resourceNameMap[rName]; ok {
			if rQuant > (nodeLimitCapacity.ScalarResources[rName] - limitAllocated) {
				insufficientResources = append(insufficientResources, noderesources.InsufficientResource{
					ResourceName: rName,
					Reason:       fmt.Sprintf("Insufficient %v limit", rName),
					Requested:    podLimits.ScalarResources[rName],
					Used:         nodeLimitAllocated.ScalarResources[rName],
					Capacity:     nodeLimitCapacity.ScalarResources[rName],
				})
			}
		}
	}
	return insufficientResources
}

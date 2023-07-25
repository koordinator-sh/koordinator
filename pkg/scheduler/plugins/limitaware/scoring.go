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
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	preScoreStateKey = "preScore" + Name
)

// preScoreStateData computed at PreScore and used at Score.
type preScoreStateData struct {
	nonZeroResource *framework.Resource
}

// Clone the prefilter state.
func (s *preScoreStateData) Clone() framework.StateData {
	return &preScoreStateData{nonZeroResource: s.nonZeroResource.Clone()}
}

func getPreScoreState(cycleState *framework.CycleState) (*preScoreStateData, *framework.Status) {
	value, err := cycleState.Read(preScoreStateKey)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	state := value.(*preScoreStateData)
	return state, nil
}

type resourceToValueMap map[corev1.ResourceName]int64

func (p *Plugin) PreScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) *framework.Status {
	cycleState.Write(preScoreStateKey, &preScoreStateData{nonZeroResource: getPodResourceLimit(pod, true)})
	return nil
}

func (p *Plugin) Score(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	state, status := getPreScoreState(cycleState)
	if status != nil {
		return 0, status
	}
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.AsStatus(fmt.Errorf("getting node %q from Snapshot: %w", nodeName, err))
	}
	node := nodeInfo.Node()
	if node == nil {
		return 0, framework.NewStatus(framework.Error, "node not found")
	}
	ratio, err := extension.GetNodeLimitToAllocatableRatio(node.Annotations, p.defaultLimitToAllocatableRatio)
	if err != nil {
		return 0, framework.AsStatus(err)
	}
	nodeLimitCapacity, err := getNodeLimitCapacity(ratio, nodeInfo.Allocatable)
	if err != nil {
		return 0, framework.AsStatus(err)
	}
	nodeLimitAllocated := p.nodeLimitsCache.GetNonZeroNodeLimits(node.Name)
	nodeAllocated := make(resourceToValueMap)
	nodeCapacity := make(resourceToValueMap)
	for resource := range p.resourceToWeightMap {
		if _, ok := ratio[resource]; ok {
			capacity, allocated := p.calculateResourceLimitCapacityAllocated(nodeLimitAllocated, nodeLimitCapacity, state.nonZeroResource, resource)
			if capacity != 0 {
				// Only fill the extended resource entry when it's non-zero.
				nodeCapacity[resource], nodeAllocated[resource] = capacity, allocated
			}
		}
	}

	score := p.leastResourceScorer(nodeAllocated, nodeCapacity)
	return score, nil
}

func (p *Plugin) calculateResourceLimitCapacityAllocated(nodeLimitAllocated, nodeLimitCapacity, podLimitRequested *framework.Resource, resourceName corev1.ResourceName) (int64, int64) {
	switch resourceName {
	case corev1.ResourceCPU:
		return nodeLimitCapacity.MilliCPU, nodeLimitAllocated.MilliCPU + podLimitRequested.MilliCPU
	case corev1.ResourceMemory:
		return nodeLimitCapacity.Memory, nodeLimitAllocated.Memory + podLimitRequested.Memory
	case corev1.ResourceEphemeralStorage:
		return nodeLimitCapacity.EphemeralStorage, nodeLimitAllocated.EphemeralStorage + podLimitRequested.EphemeralStorage
	default:
		if _, exists := nodeLimitCapacity.ScalarResources[resourceName]; exists {
			limitAllocated := int64(0)
			if nodeLimitAllocated.ScalarResources != nil {
				limitAllocated = nodeLimitAllocated.ScalarResources[resourceName]
			}
			return nodeLimitCapacity.ScalarResources[resourceName], limitAllocated + podLimitRequested.ScalarResources[resourceName]
		}
	}
	return 0, 0
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return p
}

func (p *Plugin) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, scores framework.NodeScoreList) *framework.Status {
	var minCount int64 = math.MaxInt64
	var maxCount int64 = -math.MaxInt64
	for i := range scores {
		score := scores[i].Score
		if score > maxCount {
			maxCount = score
		}
		if score < minCount {
			minCount = score
		}
	}
	maxMinDiff := maxCount - minCount
	for i := range scores {
		fScore := float64(0)
		if maxMinDiff > 0 {
			fScore = float64(framework.MaxNodeScore) * (float64(scores[i].Score-minCount) / float64(maxMinDiff))
		}
		scores[i].Score = int64(fScore)
	}
	return nil
}

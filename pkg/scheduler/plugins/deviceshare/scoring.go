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

package deviceshare

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	pluginhelper "k8s.io/kubernetes/pkg/scheduler/framework/plugins/helper"

	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func (p *Plugin) Score(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return 0, status
	}
	if state.skip {
		return 0, nil
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return 0, nil
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[nodeName])

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()

	reservationInfo := frameworkext.GetNominatedReservation(cycleState, nodeName)
	if reservationInfo != nil {
		score, status := p.scoreWithNominatedReservation(state, restoreState, nodeDeviceInfo, nodeName, pod, preemptible, reservationInfo)
		if status.IsSuccess() {
			return score, nil
		}
		klog.ErrorS(status.AsError(), "Failed to scoreWithNominatedReservation of DeviceShare",
			"pod", klog.KObj(pod), "reservation", klog.KObj(reservationInfo), "node", nodeName)
	}

	preemptible = appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
	score, err := p.allocator.Score(nodeName, pod, state.podRequests, nodeDeviceInfo, nil, preemptible, p.scorer)
	if err != nil {
		klog.ErrorS(status.AsError(), "Failed to score of DeviceShare",
			"pod", klog.KObj(pod), "reservation", klog.KObj(reservationInfo), "node", nodeName)
		return 0, framework.AsStatus(err)
	}
	return score, nil
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return p
}

func (p *Plugin) NormalizeScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, scores framework.NodeScoreList) *framework.Status {
	return pluginhelper.DefaultNormalizeScore(framework.MaxNodeScore, false, scores)
}

func (p *Plugin) ScoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeName string) (int64, *framework.Status) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return 0, status
	}
	if state.skip {
		return 0, nil
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return 0, nil
	}

	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[nodeName])

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()

	return p.scoreWithNominatedReservation(state, restoreState, nodeDeviceInfo, nodeName, pod, preemptible, reservationInfo)
}

func (p *Plugin) ReservationScoreExtensions() frameworkext.ReservationScoreExtensions {
	return p
}

func (p *Plugin) NormalizeReservationScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, scores frameworkext.ReservationScoreList) *framework.Status {
	return frameworkext.DefaultReservationNormalizeScore(frameworkext.MaxReservationScore, false, scores)
}

// deviceResourceStrategyTypeMap maps strategy to scorer implementation
var deviceResourceStrategyTypeMap = map[schedulerconfig.ScoringStrategyType]scorer{
	schedulerconfig.LeastAllocated: func(args *schedulerconfig.DeviceShareArgs) *resourceAllocationScorer {
		resToWeightMap := resourcesToWeightMap(args.ScoringStrategy.Resources)
		return &resourceAllocationScorer{
			Name:                string(schedconfig.LeastAllocated),
			scorer:              leastResourceScorer(resToWeightMap),
			resourceToWeightMap: resToWeightMap,
		}
	},
	schedulerconfig.MostAllocated: func(args *schedulerconfig.DeviceShareArgs) *resourceAllocationScorer {
		resToWeightMap := resourcesToWeightMap(args.ScoringStrategy.Resources)
		return &resourceAllocationScorer{
			Name:                string(schedconfig.MostAllocated),
			scorer:              mostResourceScorer(resToWeightMap),
			resourceToWeightMap: resToWeightMap,
		}
	},
}

// resourceToWeightMap contains resource name and weight.
type resourceToWeightMap map[corev1.ResourceName]int64

// scorer is decorator for resourceAllocationScorer
type scorer func(args *schedulerconfig.DeviceShareArgs) *resourceAllocationScorer

// resourceAllocationScorer contains information to calculate resource allocation score.
type resourceAllocationScorer struct {
	Name                string
	scorer              func(requested, allocatable resourceToValueMap) int64
	resourceToWeightMap resourceToWeightMap
}

// resourceToValueMap is keyed with resource name and valued with quantity.
type resourceToValueMap map[corev1.ResourceName]int64

// scoreDevice will use `scorer` function to calculate the score per device.
func (r *resourceAllocationScorer) scoreDevice(podRequest corev1.ResourceList, total, free corev1.ResourceList) int64 {
	if r.resourceToWeightMap == nil {
		return 0
	}

	requested := make(resourceToValueMap)
	allocatable := make(resourceToValueMap)
	for resourceName := range r.resourceToWeightMap {
		totalQuantity := total[resourceName]
		if totalQuantity.IsZero() {
			continue
		}
		freeQuantity := free[resourceName]

		req := totalQuantity.DeepCopy()
		if totalQuantity.Cmp(freeQuantity) >= 0 {
			req.Sub(freeQuantity)
			req.Add(podRequest[resourceName])
		}

		allocatable[resourceName], requested[resourceName] = totalQuantity.Value(), req.Value()
	}

	score := r.scorer(requested, allocatable)
	return score
}

func (r *resourceAllocationScorer) scoreNode(podRequest corev1.ResourceList, totalDeviceResources, freeDeviceResources deviceResources) int64 {
	if r.resourceToWeightMap == nil {
		return 0
	}

	requested := make(resourceToValueMap)
	allocatable := make(resourceToValueMap)
	for resourceName := range r.resourceToWeightMap {
		var total resource.Quantity
		for _, deviceRes := range totalDeviceResources {
			total.Add(deviceRes[resourceName])
		}
		if total.IsZero() {
			continue
		}
		var free resource.Quantity
		for _, deviceRes := range freeDeviceResources {
			free.Add(deviceRes[resourceName])
		}

		req := total.DeepCopy()
		if total.Cmp(free) >= 0 {
			req.Sub(free)
			req.Add(podRequest[resourceName])
		}
		allocatable[resourceName], requested[resourceName] = total.Value(), req.Value()
	}

	score := r.scorer(requested, allocatable)
	return score
}

// resourcesToWeightMap make weightmap from resources spec
func resourcesToWeightMap(resourceSpecs []schedconfig.ResourceSpec) resourceToWeightMap {
	resourceToWeightMap := make(resourceToWeightMap)
	for _, resourceSpec := range resourceSpecs {
		resourceToWeightMap[corev1.ResourceName(resourceSpec.Name)] = resourceSpec.Weight
	}
	return resourceToWeightMap
}

func leastResourceScorer(resToWeightMap resourceToWeightMap) func(resourceToValueMap, resourceToValueMap) int64 {
	return func(requested, allocatable resourceToValueMap) int64 {
		var nodeScore, weightSum int64
		for resourceName := range requested {
			weight := resToWeightMap[resourceName]
			resourceScore := leastRequestedScore(requested[resourceName], allocatable[resourceName])
			nodeScore += resourceScore * weight
			weightSum += weight
		}
		if weightSum == 0 {
			return 0
		}
		return nodeScore / weightSum
	}
}

func leastRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return ((capacity - requested) * framework.MaxNodeScore) / capacity
}

func mostResourceScorer(resToWeightMap resourceToWeightMap) func(requested, allocable resourceToValueMap) int64 {
	return func(requested, allocatable resourceToValueMap) int64 {
		var nodeScore, weightSum int64
		for resourceName := range requested {
			weight := resToWeightMap[resourceName]
			resourceScore := mostRequestedScore(requested[resourceName], allocatable[resourceName])
			nodeScore += resourceScore * weight
			weightSum += weight
		}
		if weightSum == 0 {
			return 0
		}
		return nodeScore / weightSum
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

	return (requested * framework.MaxNodeScore) / capacity
}

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
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// FragmentationMetrics represents GPU fragmentation metrics for a node
type FragmentationMetrics struct {
	// Score is the fragmentation score (0-100)
	Score int64
	// FragmentedGPUs is the number of fragmented GPUs
	FragmentedGPUs int
	// TotalGPUs is the total number of GPUs
	TotalGPUs int
	// AverageUtilization is the average GPU utilization
	AverageUtilization float64
	// FragmentationDegree is the standard deviation of utilization
	FragmentationDegree float64
}

// scoreNodeByFragmentation calculates fragmentation score for a node
func (p *Plugin) scoreNodeByFragmentation(
	ctx context.Context,
	state *framework.CycleState,
	pod *corev1.Pod,
	nodeName string,
) (int64, *framework.Status) {

	if p.args.AllocationStrategy == nil {
		return 0, nil
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return 0, nil
	}

	// Get pod priority
	podPriority := getPodPriority(pod)

	// Check if we should reserve complete resources for high-priority tasks
	if p.shouldReserveCompleteResources(nodeName, podPriority) {
		// If this is a reserved node and pod priority is not high enough, lower the score
		if !p.isHighPriorityPod(podPriority) {
			return 0, nil
		}
	}

	// Calculate fragmentation metrics
	fragmentationMetrics := p.calculateFragmentationMetrics(nodeDeviceInfo)

	// Decide scoring strategy based on configuration
	var score int64
	if p.args.AllocationStrategy.PreferFragmentedNodes {
		// Higher fragmentation = higher score
		score = fragmentationMetrics.Score
	} else {
		// Lower fragmentation = higher score (default)
		score = 100 - fragmentationMetrics.Score
	}

	// Apply weight
	weight := p.args.AllocationStrategy.FragmentationWeight
	if weight == 0 {
		weight = 50 // default weight
	}

	finalScore := (score * int64(weight)) / 100

	klog.V(5).InfoS("Node fragmentation score",
		"node", nodeName,
		"pod", klog.KObj(pod),
		"fragmentationScore", fragmentationMetrics.Score,
		"finalScore", finalScore,
		"fragmentedGPUs", fragmentationMetrics.FragmentedGPUs,
		"totalGPUs", fragmentationMetrics.TotalGPUs,
	)

	return finalScore, nil
}

// calculateFragmentationMetrics calculates fragmentation metrics for a node
func (p *Plugin) calculateFragmentationMetrics(nodeDevice *nodeDevice) FragmentationMetrics {
	metrics := FragmentationMetrics{}

	gpuDevices, ok := nodeDevice.deviceTotal[schedulingv1alpha1.GPU]
	if !ok || len(gpuDevices) == 0 {
		return metrics
	}

	metrics.TotalGPUs = len(gpuDevices)

	var totalUtilization float64
	var utilizationList []float64

	for minor, totalRes := range gpuDevices {
		usedRes, ok := nodeDevice.deviceUsed[schedulingv1alpha1.GPU][minor]
		if !ok {
			usedRes = corev1.ResourceList{}
		}

		totalMem := totalRes[apiext.ResourceGPUMemory]
		usedMem := usedRes[apiext.ResourceGPUMemory]

		if totalMem.IsZero() {
			continue
		}

		utilization := float64(usedMem.Value()) / float64(totalMem.Value())

		// Check if GPU is fragmented (partially used)
		if usedMem.Value() > 0 && usedMem.Value() < totalMem.Value() {
			metrics.FragmentedGPUs++
		}

		totalUtilization += utilization
		utilizationList = append(utilizationList, utilization)
	}

	if len(utilizationList) == 0 {
		return metrics
	}

	// Calculate average utilization
	metrics.AverageUtilization = totalUtilization / float64(len(utilizationList))

	// Calculate standard deviation (measure of fragmentation)
	var variance float64
	for _, util := range utilizationList {
		diff := util - metrics.AverageUtilization
		variance += diff * diff
	}
	metrics.FragmentationDegree = math.Sqrt(variance / float64(len(utilizationList)))

	// Calculate comprehensive fragmentation score
	// Factors:
	// 1. Ratio of fragmented GPUs (weight 40%)
	// 2. Unevenness of utilization (weight 60%)
	fragmentedRatio := float64(metrics.FragmentedGPUs) / float64(metrics.TotalGPUs)
	metrics.Score = int64(fragmentedRatio*40 + metrics.FragmentationDegree*100*60)

	if metrics.Score > 100 {
		metrics.Score = 100
	}

	return metrics
}

// shouldReserveCompleteResources checks if complete resources should be reserved for high-priority tasks
func (p *Plugin) shouldReserveCompleteResources(nodeName string, podPriority int32) bool {
	if p.args.AllocationStrategy == nil ||
		p.args.AllocationStrategy.ReserveCompleteResources == nil ||
		!p.args.AllocationStrategy.ReserveCompleteResources.Enabled {
		return false
	}

	config := p.args.AllocationStrategy.ReserveCompleteResources

	// Check if node matches reserved selector
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return false
	}

	if len(config.ReservedNodeSelector) > 0 {
		for key, value := range config.ReservedNodeSelector {
			if nodeInfo.Node().Labels[key] != value {
				return false
			}
		}
	}

	return true
}

// isHighPriorityPod checks if a pod has high priority
func (p *Plugin) isHighPriorityPod(podPriority int32) bool {
	if p.args.AllocationStrategy == nil ||
		p.args.AllocationStrategy.ReserveCompleteResources == nil {
		return false
	}

	threshold := p.args.AllocationStrategy.ReserveCompleteResources.PriorityThreshold
	return podPriority >= threshold
}

// getPodPriority gets the priority of a pod
func getPodPriority(pod *corev1.Pod) int32 {
	if pod.Spec.Priority != nil {
		return *pod.Spec.Priority
	}
	return 0
}

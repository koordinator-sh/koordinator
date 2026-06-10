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

package fragmentationaware

import (
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	resourcehelper "k8s.io/component-helpers/resource"
)

// nodeRequests returns the total requests of a group of pods.
func nodeRequests(pods []*corev1.Pod, resources []corev1.ResourceName) corev1.ResourceList {
	totalReq := make(corev1.ResourceList)
	for _, r := range resources {
		totalReq[r] = resource.Quantity{}
	}

	for _, pod := range pods {
		reqs := resourcehelper.PodRequests(pod, resourcehelper.PodResourcesOptions{})
		for _, r := range resources {
			if q, ok := reqs[r]; ok {
				qty := totalReq[r]
				qty.Add(q)
				totalReq[r] = qty
			}
		}
	}
	return totalReq
}

// allocationFractions calculates requested / allocatable fractions for resources.
// It skips resources mapped to 0 allocatable to prevent divide-by-zero.
func allocationFractions(requests corev1.ResourceList, allocatable corev1.ResourceList, resources []corev1.ResourceName) []float64 {
	var fractions []float64
	for _, r := range resources {
		alloc := allocatable[r]
		if alloc.IsZero() {
			continue
		}
		req := requests[r]
		fraction := float64(req.MilliValue()) / float64(alloc.MilliValue())
		fractions = append(fractions, fraction)
	}
	return fractions
}

// stdDev computes the population standard deviation of the values list.
func stdDev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	var varianceSum float64
	for _, v := range values {
		diff := v - mean
		varianceSum += diff * diff
	}
	return math.Sqrt(varianceSum / float64(len(values)))
}

// scoreNodeImbalance computes the standard deviation of allocation fractions across resources.
func scoreNodeImbalance(node *corev1.Node, pods []*corev1.Pod, resources []corev1.ResourceName) float64 {
	if node == nil {
		return 0
	}
	requests := nodeRequests(pods, resources)
	fractions := allocationFractions(requests, node.Status.Allocatable, resources)
	return stdDev(fractions)
}

// scorePodRemovalGain computes the gain (before - after) in standard deviation when a given pod is removed.
// A higher, positive gain means removing the pod decreases the standard deviation significantly.
func scorePodRemovalGain(node *corev1.Node, pods []*corev1.Pod, pod *corev1.Pod, resources []corev1.ResourceName) float64 {
	if node == nil || pod == nil {
		return 0
	}
	stdBefore := scoreNodeImbalance(node, pods, resources)

	var podsAfter []*corev1.Pod
	for _, p := range pods {
		if p.UID != pod.UID {
			podsAfter = append(podsAfter, p)
		}
	}

	stdAfter := scoreNodeImbalance(node, podsAfter, resources)
	return stdBefore - stdAfter
}

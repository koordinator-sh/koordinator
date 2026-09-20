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
	resourcehelper "k8s.io/component-helpers/resource"

	deschedulernode "github.com/koordinator-sh/koordinator/pkg/descheduler/node"
)

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

// nodeImbalanceState caches per-resource utilization totals (as MilliValues) so
// that the imbalance score after removing a single pod can be derived via
// subtraction in O(R) instead of re-summing all pods in O(P·R).
type nodeImbalanceState struct {
	// baseMilli holds the total requested MilliValue per tracked resource,
	// summed across all pods on the node.
	baseMilli []int64
	// allocMilli holds the allocatable MilliValue per tracked resource.
	allocMilli []int64
	// resources is the ordered list of resource names that had non-zero
	// allocatable capacity (same order as baseMilli / allocMilli).
	resources []corev1.ResourceName
}

// newNodeImbalanceState computes aggregate utilization for node once and
// returns a reusable state handle. Returns nil when node is nil.
func newNodeImbalanceState(node *corev1.Node, pods []*corev1.Pod, resources []corev1.ResourceName) *nodeImbalanceState {
	if node == nil {
		return nil
	}
	utilization := deschedulernode.NodeUtilization(pods, resources)

	var tracked []corev1.ResourceName
	var baseMilli, allocMilli []int64
	for _, r := range resources {
		alloc := node.Status.Allocatable[r]
		if alloc.IsZero() {
			continue
		}
		tracked = append(tracked, r)
		allocMilli = append(allocMilli, alloc.MilliValue())
		if r == corev1.ResourcePods {
			// NodeUtilization seeds pods with len(pods); store as MilliValue.
			baseMilli = append(baseMilli, int64(len(pods))*1000)
		} else if qty, ok := utilization[r]; ok && qty != nil {
			baseMilli = append(baseMilli, qty.MilliValue())
		} else {
			baseMilli = append(baseMilli, 0)
		}
	}
	return &nodeImbalanceState{
		baseMilli:  baseMilli,
		allocMilli: allocMilli,
		resources:  tracked,
	}
}

// score returns the population standard deviation of the allocation fractions
// using the full (unmodified) utilization totals.
func (s *nodeImbalanceState) score() float64 {
	if s == nil || len(s.resources) == 0 {
		return 0
	}
	fractions := make([]float64, len(s.resources))
	for i := range s.resources {
		fractions[i] = float64(s.baseMilli[i]) / float64(s.allocMilli[i])
	}
	return stdDev(fractions)
}

// scoreWithout returns the imbalance score as if pod were removed from the
// node. It subtracts the pod's per-resource requests from the cached totals
// and computes the stdDev.
func (s *nodeImbalanceState) scoreWithout(pod *corev1.Pod) float64 {
	if s == nil || len(s.resources) == 0 {
		return 0
	}
	podReqs := resourcehelper.PodRequests(pod, resourcehelper.PodResourcesOptions{})
	fractions := make([]float64, len(s.resources))
	for i, r := range s.resources {
		var podMilli int64
		if r == corev1.ResourcePods {
			// PodRequests never returns a pods key; one pod always contributes 1.
			podMilli = 1000
		} else if qty, ok := podReqs[r]; ok {
			podMilli = qty.MilliValue()
		}
		fractions[i] = float64(s.baseMilli[i]-podMilli) / float64(s.allocMilli[i])
	}
	return stdDev(fractions)
}

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

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

var (
	lowPriority      = int32(0)
	highPriority     = int32(10000)
	extendedResource = corev1.ResourceName("example.com/foo")

	testNodeAllocatable = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("32"),
		corev1.ResourceMemory: resource.MustParse("32Gi"),
		corev1.ResourcePods:   *resource.NewQuantity(110, resource.DecimalSI),
	}

	testNode1 = NodeInfo{
		NodeUsage: &NodeUsage{
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: testNodeAllocatable,
				},
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
			},
			usage: map[corev1.ResourceName]*resource.Quantity{
				corev1.ResourceCPU:    resource.NewMilliQuantity(1730, resource.DecimalSI),
				corev1.ResourceMemory: resource.NewQuantity(3038982964, resource.BinarySI),
				corev1.ResourcePods:   resource.NewQuantity(25, resource.BinarySI),
			},
		},
	}
	testNode2 = NodeInfo{
		NodeUsage: &NodeUsage{
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: testNodeAllocatable,
				},
				ObjectMeta: metav1.ObjectMeta{Name: "node2"},
			},
			usage: map[corev1.ResourceName]*resource.Quantity{
				corev1.ResourceCPU:    resource.NewMilliQuantity(1220, resource.DecimalSI),
				corev1.ResourceMemory: resource.NewQuantity(3038982964, resource.BinarySI),
				corev1.ResourcePods:   resource.NewQuantity(11, resource.BinarySI),
			},
		},
	}
	testNode3 = NodeInfo{
		NodeUsage: &NodeUsage{
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: testNodeAllocatable,
				},
				ObjectMeta: metav1.ObjectMeta{Name: "node3"},
			},
			usage: map[corev1.ResourceName]*resource.Quantity{
				corev1.ResourceCPU:    resource.NewMilliQuantity(1530, resource.DecimalSI),
				corev1.ResourceMemory: resource.NewQuantity(5038982964, resource.BinarySI),
				corev1.ResourcePods:   resource.NewQuantity(20, resource.BinarySI),
			},
		},
	}
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

func TestSortNodesByUsageDescendingOrder(t *testing.T) {
	nodeList := []NodeInfo{testNode1, testNode2, testNode3}
	expectedNodeList := []NodeInfo{testNode3, testNode1, testNode2}
	weightMap := map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    1,
		corev1.ResourceMemory: 1,
		corev1.ResourcePods:   1,
	}
	sortNodesByUsage(nodeList, weightMap, false)

	assert.Equal(t, expectedNodeList, nodeList)
}

func TestSortNodesByUsageAscendingOrder(t *testing.T) {
	nodeList := []NodeInfo{testNode1, testNode2, testNode3}
	expectedNodeList := []NodeInfo{testNode2, testNode1, testNode3}
	weightMap := map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    1,
		corev1.ResourceMemory: 1,
		corev1.ResourcePods:   1,
	}
	sortNodesByUsage(nodeList, weightMap, true)

	assert.Equal(t, expectedNodeList, nodeList)
}
func TestSortPodsOnOneOverloadedNode(t *testing.T) {
	nodeInfo := NodeInfo{
		NodeUsage: &NodeUsage{
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: testNodeAllocatable,
				},
				ObjectMeta: metav1.ObjectMeta{Name: "node0"},
			},
			// only make cpu overused
			usage: map[corev1.ResourceName]*resource.Quantity{
				corev1.ResourceCPU:    resource.NewMilliQuantity(30000, resource.DecimalSI),
				corev1.ResourceMemory: resource.NewQuantity(998244353, resource.BinarySI),
			},
			podMetrics: map[types.NamespacedName]*slov1alpha1.ResourceMap{
				{
					Namespace: "ns",
					Name:      "pod1",
				}: {
					ResourceList: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(27487790694, resource.BinarySI),
					},
				},
				{
					Namespace: "ns",
					Name:      "pod2",
				}: {
					ResourceList: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(3000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(27487790694, resource.BinarySI),
					},
				},
				{
					Namespace: "ns",
					Name:      "pod3",
				}: {
					ResourceList: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(1, resource.BinarySI),
					},
				},
				{
					Namespace: "ns",
					Name:      "pod4",
				}: {
					ResourceList: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(1, resource.BinarySI),
					},
				},
			},
		},
		thresholds: NodeThresholds{
			lowResourceThreshold: nil,
			highResourceThreshold: map[corev1.ResourceName]*resource.Quantity{
				corev1.ResourceCPU:    resource.NewMilliQuantity(20000, resource.DecimalSI),
				corev1.ResourceMemory: resource.NewQuantity(27487790694, resource.BinarySI),
			},
		},
	}
	removablePods := []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "ns"}, Spec: corev1.PodSpec{NodeName: "node0"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "ns"}, Spec: corev1.PodSpec{NodeName: "node0"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "pod3", Namespace: "ns"}, Spec: corev1.PodSpec{NodeName: "node0"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "pod4", Namespace: "ns"}, Spec: corev1.PodSpec{NodeName: "node0"}},
	}
	expectedResult := make([]*corev1.Pod, 4)
	expectedResult[0] = removablePods[3]
	expectedResult[1] = removablePods[1]
	expectedResult[2] = removablePods[2]
	expectedResult[3] = removablePods[0]
	resourceWeights := map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    int64(1),
		corev1.ResourceMemory: int64(1),
	}
	sortPodsOnOneOverloadedNode(nodeInfo, removablePods, resourceWeights)
	assert.Equal(t, expectedResult, removablePods)
}

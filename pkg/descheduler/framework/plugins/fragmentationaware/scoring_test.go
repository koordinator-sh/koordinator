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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func makePod(uid string, cpu int64, mem int64) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID(uid),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(cpu, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(mem, resource.BinarySI),
						},
					},
				},
			},
		},
	}
}

func makeNode(cpu int64, mem int64) *corev1.Node {
	return &corev1.Node{
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpu, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(mem, resource.BinarySI),
			},
		},
	}
}

func TestScoreNodeImbalance(t *testing.T) {
	resources := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

	t.Run("nil node returns zero", func(t *testing.T) {
		stdDev := scoreNodeImbalance(nil, nil, resources)
		assert.Equal(t, 0.0, stdDev)
	})

	t.Run("no scored resources returns zero", func(t *testing.T) {
		node := makeNode(1000, 1024)

		stdDev := scoreNodeImbalance(node, nil, nil)
		assert.Equal(t, 0.0, stdDev)
	})

	t.Run("balanced CPU/memory node gives low stddev", func(t *testing.T) {
		node := makeNode(1000, 1024)
		pods := []*corev1.Pod{
			makePod("p1", 500, 512),
		}

		stdDev := scoreNodeImbalance(node, pods, resources)
		assert.True(t, stdDev < 0.01)
	})

	t.Run("CPU-heavy node gives high stddev", func(t *testing.T) {
		node := makeNode(1000, 1024)
		pods := []*corev1.Pod{
			makePod("p1", 900, 100),
		}

		stdDev := scoreNodeImbalance(node, pods, resources)
		assert.True(t, stdDev > 0.1)
	})

	t.Run("zero allocatable resource is skipped", func(t *testing.T) {
		node := makeNode(1000, 0) // Memory is 0
		pods := []*corev1.Pod{
			makePod("p1", 500, 512),
		}

		stdDev := scoreNodeImbalance(node, pods, resources)
		assert.True(t, stdDev == 0, "only CPU is considered, variance of 1 element is 0")
	})

	t.Run("custom resource works if configured", func(t *testing.T) {
		node := makeNode(1000, 1024)
		node.Status.Allocatable["example.com/gpu"] = *resource.NewQuantity(2, resource.DecimalSI)
		pods := []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{UID: types.UID("p1")},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"example.com/gpu": *resource.NewQuantity(1, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
		}

		customRes := []corev1.ResourceName{"example.com/gpu", corev1.ResourceCPU}
		stdDev := scoreNodeImbalance(node, pods, customRes)
		// GPU = 1/2 = 0.5, CPU = 0/1000 = 0
		// mean = 0.25
		// std = sqrt((0.5-0.25)^2 + (0-0.25)^2) / 2 = sqrt(0.0625) = 0.25
		assert.Equal(t, 0.25, stdDev)
	})
}

func TestScorePodRemovalGain(t *testing.T) {
	resources := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

	t.Run("nil node returns zero", func(t *testing.T) {
		pod := makePod("p1", 100, 100)

		gain := scorePodRemovalGain(nil, nil, pod, resources)
		assert.Equal(t, 0.0, gain)
	})

	t.Run("nil pod returns zero", func(t *testing.T) {
		node := makeNode(1000, 1024)

		gain := scorePodRemovalGain(node, nil, nil, resources)
		assert.Equal(t, 0.0, gain)
	})

	t.Run("removing CPU-heavy pod improves stddev", func(t *testing.T) {
		node := makeNode(1000, 1024)
		podCPUHeavy := makePod("cpu-heavy", 800, 100)
		podBalanced := makePod("balanced", 100, 100)

		pods := []*corev1.Pod{podCPUHeavy, podBalanced}

		gain := scorePodRemovalGain(node, pods, podCPUHeavy, resources)
		assert.True(t, gain > 0, "Gain should be positive for removing imbalanced pod")
	})

	t.Run("removing wrong pod gives low/negative gain", func(t *testing.T) {
		node := makeNode(1000, 1024)
		// Together they balance the node
		podA := makePod("podA", 200, 800)
		podB := makePod("podB", 600, 100)

		pods := []*corev1.Pod{podA, podB}

		gain := scorePodRemovalGain(node, pods, podB, resources)
		assert.True(t, gain < 0, "Gain should be negative for removing a pod that balances or keeps it worse")
	})
}

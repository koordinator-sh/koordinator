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

func TestImbalanceScoring(t *testing.T) {
	resources := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

	t.Run("nil node returns zero", func(t *testing.T) {
		stdDev := newNodeImbalanceState(nil, nil, resources).score()
		assert.Equal(t, 0.0, stdDev)
	})

	t.Run("no scored resources returns zero", func(t *testing.T) {
		node := makeNode(1000, 1024)

		stdDev := newNodeImbalanceState(node, nil, nil).score()
		assert.Equal(t, 0.0, stdDev)
	})

	t.Run("balanced CPU/memory node gives low stddev", func(t *testing.T) {
		node := makeNode(1000, 1024)
		pods := []*corev1.Pod{
			makePod("p1", 500, 512),
		}

		stdDev := newNodeImbalanceState(node, pods, resources).score()
		assert.True(t, stdDev < 0.01)
	})

	t.Run("CPU-heavy node gives high stddev", func(t *testing.T) {
		node := makeNode(1000, 1024)
		pods := []*corev1.Pod{
			makePod("p1", 900, 100),
		}

		stdDev := newNodeImbalanceState(node, pods, resources).score()
		assert.True(t, stdDev > 0.1)
	})

	t.Run("zero allocatable resource is skipped", func(t *testing.T) {
		node := makeNode(1000, 0) // Memory is 0
		pods := []*corev1.Pod{
			makePod("p1", 500, 512),
		}

		stdDev := newNodeImbalanceState(node, pods, resources).score()
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
		stdDev := newNodeImbalanceState(node, pods, customRes).score()
		// GPU = 1/2 = 0.5, CPU = 0/1000 = 0
		// mean = 0.25
		// std = sqrt((0.5-0.25)^2 + (0-0.25)^2) / 2 = sqrt(0.0625) = 0.25
		assert.Equal(t, 0.25, stdDev)
	})
}

func TestNodeImbalanceState(t *testing.T) {
	resources := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

	t.Run("nil node returns nil state", func(t *testing.T) {
		state := newNodeImbalanceState(nil, nil, resources)
		assert.Nil(t, state)
		assert.Equal(t, 0.0, (*nodeImbalanceState)(nil).score())
		assert.Equal(t, 0.0, (*nodeImbalanceState)(nil).scoreWithout(makePod("p", 100, 100)))
	})

	t.Run("score returns expected value", func(t *testing.T) {
		node := makeNode(4000, 4000)
		pods := []*corev1.Pod{
			makePod("p1", 2500, 500),
			makePod("p2", 500, 500),
		}
		state := newNodeImbalanceState(node, pods, resources)
		// CPU = 3000/4000 = 0.75, Memory = 1000/4000 = 0.25
		// mean = 0.5, stddev = sqrt(((0.75-0.5)^2 + (0.25-0.5)^2) / 2) = 0.25
		assert.InDelta(t, 0.25, state.score(), 1e-12)
	})

	t.Run("scoreWithout matches from-scratch scoring", func(t *testing.T) {
		node := makeNode(4000, 4000)
		pods := []*corev1.Pod{
			makePod("p1", 2500, 500),
			makePod("p2", 500, 500),
			makePod("p3", 200, 1000),
		}
		state := newNodeImbalanceState(node, pods, resources)

		for i, removePod := range pods {
			var remaining []*corev1.Pod
			for j, p := range pods {
				if j != i {
					remaining = append(remaining, p)
				}
			}
			fromScratch := newNodeImbalanceState(node, remaining, resources).score()
			incremental := state.scoreWithout(removePod)
			assert.InDelta(t, fromScratch, incremental, 1e-12,
				"mismatch when removing pod %d", i)
		}
	})

	t.Run("scoreWithout with zero allocatable resource", func(t *testing.T) {
		node := makeNode(4000, 0) // memory allocatable is 0
		pods := []*corev1.Pod{
			makePod("p1", 2000, 500),
			makePod("p2", 1000, 300),
		}
		state := newNodeImbalanceState(node, pods, resources)
		// Only CPU is tracked; stddev of a single value is 0
		assert.Equal(t, 0.0, state.score())

		remaining := []*corev1.Pod{pods[1]}
		assert.InDelta(t, newNodeImbalanceState(node, remaining, resources).score(),
			state.scoreWithout(pods[0]), 1e-12)
	})

	t.Run("scoreWithout with pods resource matches from-scratch", func(t *testing.T) {
		node := makeNode(4000, 4000)
		node.Status.Allocatable[corev1.ResourcePods] = *resource.NewQuantity(10, resource.DecimalSI)
		pods := []*corev1.Pod{
			makePod("p1", 2500, 500),
			makePod("p2", 500, 500),
			makePod("p3", 200, 1000),
		}
		withPods := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods}
		state := newNodeImbalanceState(node, pods, withPods)

		for i, removePod := range pods {
			var remaining []*corev1.Pod
			for j, p := range pods {
				if j != i {
					remaining = append(remaining, p)
				}
			}
			fromScratch := newNodeImbalanceState(node, remaining, withPods).score()
			incremental := state.scoreWithout(removePod)
			assert.InDelta(t, fromScratch, incremental, 1e-12,
				"mismatch when removing pod %d", i)
		}
	})
}

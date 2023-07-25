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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	st "k8s.io/kubernetes/pkg/scheduler/testing"
)

func TestScore(t *testing.T) {
	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		nodes                 []*corev1.Node
		existingPods          []*corev1.Pod
		amountOfBestEffort    bool
		bestEffortPod         *corev1.Pod
		expectedPriorities    framework.NodeScoreList
		expectNormalizedScore framework.NodeScoreList
	}{
		{
			name: "LeastAllocated",
			pod: st.MakePod().
				Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).
				Obj(),
			nodes: []*corev1.Node{
				st.MakeNode().Name("node1").Capacity(map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}).Obj(),
				st.MakeNode().Name("node2").Capacity(map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}).Obj(),
			},
			existingPods: []*corev1.Pod{
				st.MakePod().Name("pod-1").Node("node1").Req(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}).Obj(),
				st.MakePod().Name("pod-2").Node("node2").Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).Obj(),
			},
			expectedPriorities:    []framework.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 63}},
			expectNormalizedScore: []framework.NodeScore{{Name: "node1", Score: 0}, {Name: "node2", Score: 100}},
		},
		{
			name: "LeastAllocated, zero when set explicitly",
			pod: st.MakePod().
				Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).
				Obj(),
			nodes: []*corev1.Node{
				st.MakeNode().Name("node1").Capacity(map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}).Obj(),
				st.MakeNode().Name("node2").Capacity(map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}).Obj(),
			},
			existingPods: []*corev1.Pod{
				st.MakePod().Name("pod-1").Node("node1").Req(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}).Obj(),
				st.MakePod().Name("pod-2").Node("node2").Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).Obj(),
			},
			amountOfBestEffort:    true,
			bestEffortPod:         st.MakePod().Name(fmt.Sprintf("pod-1")).Node("node1").Req(map[corev1.ResourceName]string{"cpu": "0", "memory": "0"}).Obj(),
			expectedPriorities:    []framework.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 63}},
			expectNormalizedScore: []framework.NodeScore{{Name: "node1", Score: 0}, {Name: "node2", Score: 100}},
		},
		{
			name: "LeastAllocated, nonZero when not-set",
			pod: st.MakePod().
				Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).
				Obj(),
			nodes: []*corev1.Node{
				st.MakeNode().Name("node1").Capacity(map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}).Obj(),
				st.MakeNode().Name("node2").Capacity(map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}).Obj(),
			},
			existingPods: []*corev1.Pod{
				st.MakePod().Name("pod-1").Node("node1").Req(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}).Obj(),
				st.MakePod().Name("pod-2").Node("node2").Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).Obj(),
			},
			amountOfBestEffort:    true,
			bestEffortPod:         st.MakePod().Name(fmt.Sprintf("pod-1")).Node("node1").Req(map[corev1.ResourceName]string{"memory": "0"}).Obj(),
			expectedPriorities:    []framework.NodeScore{{Name: "node1", Score: 31}, {Name: "node2", Score: 63}},
			expectNormalizedScore: []framework.NodeScore{{Name: "node1", Score: 0}, {Name: "node2", Score: 100}},
		},
		{
			name: "Negative Score when allocated limit > allocatable limit",
			pod: st.MakePod().
				Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).
				Obj(),
			nodes: []*corev1.Node{
				st.MakeNode().Name("node1").Capacity(map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}).Obj(),
				st.MakeNode().Name("node2").Capacity(map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}).Obj(),
				st.MakeNode().Name("node3").Capacity(map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}).Obj(),
			},
			existingPods: []*corev1.Pod{
				st.MakePod().Name("pod-1").Node("node1").Req(map[corev1.ResourceName]string{"cpu": "6000", "memory": "4000"}).Obj(),
				st.MakePod().Name("pod-2").Node("node2").Req(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}).Obj(),
				st.MakePod().Name("pod-3").Node("node3").Req(map[corev1.ResourceName]string{"cpu": "2000", "memory": "2000"}).Obj(),
			},
			expectedPriorities:    []framework.NodeScore{{Name: "node1", Score: -17}, {Name: "node2", Score: 63}, {Name: "node3", Score: 55}},
			expectNormalizedScore: []framework.NodeScore{{Name: "node1", Score: 0}, {Name: "node2", Score: 100}, {Name: "node3", Score: 90}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, test.existingPods, test.nodes, defaultLimitToAllocatableRatio)
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			cache := p.(*Plugin).nodeLimitsCache
			for _, existingPod := range test.existingPods {
				cache.AddPod(existingPod.Spec.NodeName, existingPod)
			}

			if test.amountOfBestEffort {
				for i := 0; i < 1000; i++ {
					pod := test.bestEffortPod
					pod.Name = fmt.Sprintf("pod-1-%d", i)
					cache.AddPod(pod.Spec.NodeName, pod)
				}
			}

			state := framework.NewCycleState()
			preScoreStatus := p.(framework.PreScorePlugin).PreScore(context.Background(), state, test.pod, test.nodes)
			if !preScoreStatus.IsSuccess() {
				t.Errorf("prescore failed with status: %v", preScoreStatus)
			}

			var gotPriorities framework.NodeScoreList
			for _, n := range test.nodes {
				score, status := p.(framework.ScorePlugin).Score(context.Background(), state, test.pod, n.Name)
				if !status.IsSuccess() {
					t.Errorf("unexpected error: %v", status)
				}
				gotPriorities = append(gotPriorities, framework.NodeScore{Name: n.Name, Score: score})
			}

			if !reflect.DeepEqual(test.expectedPriorities, gotPriorities) {
				t.Errorf("expected:\n\t%+v,\ngot:\n\t%+v", test.expectedPriorities, gotPriorities)
			}

			status := p.(framework.ScoreExtensions).NormalizeScore(context.TODO(), state, test.pod, gotPriorities)
			if !status.IsSuccess() {
				t.Errorf("unexpected error: %v", status)
			}
			if !reflect.DeepEqual(test.expectNormalizedScore, gotPriorities) {
				t.Errorf("expected:\n\t%+v,\ngot:\n\t%+v", test.expectNormalizedScore, gotPriorities)
			}
		})
	}
}

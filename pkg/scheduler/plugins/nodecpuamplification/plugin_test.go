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

package nodecpuamplification

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	st "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

var (
	defaultResources = []schedconfig.ResourceSpec{
		{Name: string(corev1.ResourceCPU), Weight: 1},
		{Name: string(corev1.ResourceMemory), Weight: 1},
	}
	defaultScoringStrategy = &config.ScoringStrategy{
		Type:      config.LeastAllocated,
		Resources: defaultResources,
	}

	errReason = "Insufficient amplified cpu"
)

func makeNode(name string, capacity map[corev1.ResourceName]string, cpuAmpRatio extension.Ratio) *corev1.Node {
	node := st.MakeNode().Name(name).Capacity(capacity).Obj()
	_, _ = extension.SetNodeResourceAmplificationRatio(node, corev1.ResourceCPU, cpuAmpRatio)
	return node
}

func makePodOnNode(request map[corev1.ResourceName]string, node string, isCPUSet bool) *corev1.Pod {
	pod := st.MakePod().Req(request).Node(node).Obj()
	if isCPUSet {
		pod.Labels = map[string]string{
			extension.LabelPodQoS: string(extension.QoSLSR),
		}
	}
	return pod
}

func makePod(request map[corev1.ResourceName]string, isCPUSet bool) *corev1.Pod {
	return makePodOnNode(request, "", isCPUSet)
}

func TestFilter(t *testing.T) {
	filterTests := []struct {
		name                      string
		args                      config.NodeCPUAmplificationArgs
		pod                       *corev1.Pod
		nodeInfo                  *framework.NodeInfo
		nodeCPUAmplificationRatio extension.Ratio
		wantStatus                *framework.Status
	}{
		{
			name: "no resources requested always fits",
			pod:  &corev1.Pod{},
			nodeInfo: framework.NewNodeInfo(
				makePod(map[corev1.ResourceName]string{"cpu": "10000"}, false)),
			nodeCPUAmplificationRatio: 2.0,
		},
		{
			name: "no filtering without node cpu amplification",
			pod:  makePod(map[corev1.ResourceName]string{"cpu": "10000"}, false),
			nodeInfo: framework.NewNodeInfo(
				makePod(map[corev1.ResourceName]string{"cpu": "10000"}, false)),
			nodeCPUAmplificationRatio: 1.0,
		},
		{
			name: "cpu fits",
			pod:  makePod(map[corev1.ResourceName]string{"cpu": "5000"}, false),
			nodeInfo: framework.NewNodeInfo(
				makePod(map[corev1.ResourceName]string{"cpu": "5000"}, false)),
			nodeCPUAmplificationRatio: 2.0,
		},
		{
			name: "insufficient cpu",
			pod:  makePod(map[corev1.ResourceName]string{"cpu": "10000"}, false),
			nodeInfo: framework.NewNodeInfo(
				makePod(map[corev1.ResourceName]string{"cpu": "10000"}, false)),
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                framework.NewStatus(framework.Unschedulable, errReason),
		},
		{
			name: "insufficient cpu with cpuset pod on node",
			pod:  makePod(map[corev1.ResourceName]string{"cpu": "5000"}, false),
			nodeInfo: framework.NewNodeInfo(
				makePod(map[corev1.ResourceName]string{"cpu": "5000"}, true)),
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                framework.NewStatus(framework.Unschedulable, errReason),
		},
		{
			name: "insufficient cpu when scheduling cpuset pod",
			pod:  makePod(map[corev1.ResourceName]string{"cpu": "5000"}, true),
			nodeInfo: framework.NewNodeInfo(
				makePod(map[corev1.ResourceName]string{"cpu": "5000"}, false)),
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                framework.NewStatus(framework.Unschedulable, errReason),
		},
		{
			name: "insufficient cpu when scheduling cpuset pod with cpuset pod on node",
			pod:  makePod(map[corev1.ResourceName]string{"cpu": "5000"}, true),
			nodeInfo: framework.NewNodeInfo(
				makePod(map[corev1.ResourceName]string{"cpu": "5000"}, true)),
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                framework.NewStatus(framework.Unschedulable, errReason),
		},
	}

	for _, test := range filterTests {
		t.Run(test.name, func(t *testing.T) {
			node := makeNode("", map[corev1.ResourceName]string{"cpu": "10000", "memory": "20"}, test.nodeCPUAmplificationRatio)
			test.nodeInfo.SetNode(node)

			if test.args.ScoringStrategy == nil {
				test.args.ScoringStrategy = defaultScoringStrategy
			}

			p, err := New(&test.args, nil)
			if err != nil {
				t.Fatal(err)
			}
			cycleState := framework.NewCycleState()
			_, preFilterStatus := p.(framework.PreFilterPlugin).PreFilter(context.Background(), cycleState, test.pod)
			if !preFilterStatus.IsSuccess() {
				t.Errorf("prefilter failed with status: %v", preFilterStatus)
			}

			gotStatus := p.(framework.FilterPlugin).Filter(context.Background(), cycleState, test.pod, test.nodeInfo)
			if !reflect.DeepEqual(gotStatus, test.wantStatus) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantStatus)
			}
		})
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		name               string
		args               config.NodeCPUAmplificationArgs
		requestedPod       *corev1.Pod
		nodes              []*corev1.Node
		existingPods       []*corev1.Pod
		expectedPriorities framework.NodeScoreList
	}{
		{
			name:         "ScoringStrategy MostAllocated, no cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", false),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", false),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 67}, {Name: "node2", Score: 36}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy MostAllocated, cpuset pods on node",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", true),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 67}, {Name: "node2", Score: 45}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy MostAllocated, scheduling cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", false),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", false),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 67}, {Name: "node2", Score: 45}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy MostAllocated, cpuset pods on node, scheduling cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", true),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 67}, {Name: "node2", Score: 53}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.MostAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, no cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", false),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", false),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 63}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, cpuset pods on node",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, false),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", true),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 55}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, scheduling cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", false),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", false),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 55}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
		{
			name:         "ScoringStrategy LeastAllocated, cpuset pods on node,scheduling cpuset pod",
			requestedPod: makePod(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, true),
			nodes: []*corev1.Node{
				makeNode("node1", map[corev1.ResourceName]string{"cpu": "4000", "memory": "10000"}, 1.0),
				makeNode("node2", map[corev1.ResourceName]string{"cpu": "6000", "memory": "10000"}, 2.0),
			},
			existingPods: []*corev1.Pod{
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "2000", "memory": "4000"}, "node1", true),
				makePodOnNode(map[corev1.ResourceName]string{"cpu": "1000", "memory": "2000"}, "node2", true),
			},
			expectedPriorities: []framework.NodeScore{{Name: "node1", Score: 32}, {Name: "node2", Score: 46}},
			args: config.NodeCPUAmplificationArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Type:      config.LeastAllocated,
					Resources: defaultResources,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := framework.NewCycleState()
			snapshot := newTestSharedLister(test.existingPods, test.nodes)
			fh, _ := runtime.NewFramework(nil, nil, runtime.WithSnapshotSharedLister(snapshot))
			args := test.args
			p, err := New(&args, fh)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var gotPriorities framework.NodeScoreList
			for _, n := range test.nodes {
				score, status := p.(framework.ScorePlugin).Score(context.Background(), state, test.requestedPod, n.Name)
				if !status.IsSuccess() {
					t.Errorf("unexpected error: %v", status)
				}
				gotPriorities = append(gotPriorities, framework.NodeScore{Name: n.Name, Score: score})
			}

			if !reflect.DeepEqual(test.expectedPriorities, gotPriorities) {
				t.Errorf("expected:\n\t%+v,\ngot:\n\t%+v", test.expectedPriorities, gotPriorities)
			}
		})
	}
}

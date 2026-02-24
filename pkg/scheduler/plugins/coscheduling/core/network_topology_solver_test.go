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

package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

func TestTopologyNodeLessFunc(t *testing.T) {
	// Build topology tree:
	// Cluster (root)
	// ├── Spine: s1 (OfferSlot=3)
	// │   ├── Block: b1 (OfferSlot=2)
	// │   └── Block: b2 (OfferSlot=2)
	// └── Spine: s2 (OfferSlot=4)
	//     ├── Block: b3 (OfferSlot=2)
	//     └── Block: b4 (OfferSlot=3)

	root := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "cluster"},
		OfferSlot:    7,
	}
	s1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "s1"},
		Parent:       root,
		OfferSlot:    3,
	}
	s2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "s2"},
		Parent:       root,
		OfferSlot:    4,
	}
	b1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b1"},
		Parent:       s1,
		OfferSlot:    2,
		Score:        100,
	}
	b2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b2"},
		Parent:       s1,
		OfferSlot:    2,
		Score:        100,
	}
	b3 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b3"},
		Parent:       s2,
		OfferSlot:    2,
		Score:        100,
	}
	b4 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b4"},
		Parent:       s2,
		OfferSlot:    3,
		Score:        100,
	}

	tests := []struct {
		name           string
		a              *networktopology.TreeNode
		b              *networktopology.TreeNode
		lowerOfferSlot bool
		want           bool
	}{
		{
			name:           "different OfferSlot, lowerFirst=true, a < b",
			a:              b1,
			b:              b4,
			lowerOfferSlot: true,
			want:           true,
		},
		{
			name:           "different OfferSlot, lowerFirst=true, a > b",
			a:              b4,
			b:              b1,
			lowerOfferSlot: true,
			want:           false,
		},
		{
			name:           "different OfferSlot, lowerFirst=false, a < b",
			a:              b1,
			b:              b4,
			lowerOfferSlot: false,
			want:           false,
		},
		{
			name:           "different OfferSlot, lowerFirst=false, a > b",
			a:              b4,
			b:              b1,
			lowerOfferSlot: false,
			want:           true,
		},
		{
			name:           "same OfferSlot, different parent OfferSlot, lowerFirst=true",
			a:              b1,
			b:              b3,
			lowerOfferSlot: true,
			want:           true,
		},
		{
			name:           "same OfferSlot, different parent OfferSlot, lowerFirst=false",
			a:              b1,
			b:              b3,
			lowerOfferSlot: false,
			want:           false,
		},
		{
			name:           "same OfferSlot, same parent OfferSlot, fallback to name",
			a:              b1,
			b:              b2,
			lowerOfferSlot: true,
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topologyNodeLessFunc(tt.a, tt.b, tt.lowerOfferSlot)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTopologyNodeLessFunc_ScoreFallback(t *testing.T) {
	root := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "cluster"},
		OfferSlot:    4,
	}
	s1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "s1"},
		Parent:       root,
		OfferSlot:    2,
	}
	b1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b1"},
		Parent:       s1,
		OfferSlot:    1,
		Score:        200,
	}
	b2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b2"},
		Parent:       s1,
		OfferSlot:    1,
		Score:        100,
	}

	tests := []struct {
		name           string
		a              *networktopology.TreeNode
		b              *networktopology.TreeNode
		lowerOfferSlot bool
		want           bool
	}{
		{
			name:           "same OfferSlot chain, different Score, higher Score wins",
			a:              b1,
			b:              b2,
			lowerOfferSlot: true,
			want:           true,
		},
		{
			name:           "same OfferSlot chain, different Score, higher Score wins (reversed)",
			a:              b2,
			b:              b1,
			lowerOfferSlot: true,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topologyNodeLessFunc(tt.a, tt.b, tt.lowerOfferSlot)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTopologyNodeLessFunc_NilParent(t *testing.T) {
	a := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "a"},
		OfferSlot:    2,
		Score:        100,
	}
	b := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b"},
		OfferSlot:    2,
		Score:        100,
	}

	got := topologyNodeLessFunc(a, b, true)
	assert.True(t, got)
}

func Test_calculateNodeExistingPodsNum(t *testing.T) {
	type args struct {
		selectorKey string
		nodes       []*corev1.Node
		pods        []*corev1.Pod
	}
	tests := []struct {
		name string
		args args
		want map[string]int
	}{
		{
			name: "normal flow",
			args: args{
				selectorKey: "test-key",
				nodes: []*corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node-1",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node-2",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node-3",
						},
					},
				},
				pods: []*corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod-1",
							Annotations: map[string]string{
								extension.AnnotationPodNetworkTopologySelector: "test-key",
							},
						},
						Spec: corev1.PodSpec{
							NodeName: "node-1",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod-2",
							Annotations: map[string]string{
								extension.AnnotationPodNetworkTopologySelector: "test-key",
							},
						},
						Spec: corev1.PodSpec{
							NodeName: "node-2",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod-3",
							Annotations: map[string]string{
								extension.AnnotationPodNetworkTopologySelector: "test-key",
							},
						},
						Spec: corev1.PodSpec{
							NodeName: "node-2",
						},
					},
				},
			},
			want: map[string]int{
				"node-1": 1,
				"node-2": 2,
				"node-3": 0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var nodeInfos []*framework.NodeInfo
			for _, node := range tt.args.nodes {
				nodeInfo := framework.NewNodeInfo()
				nodeInfo.SetNode(node)
				for _, pod := range tt.args.pods {
					if pod.Spec.NodeName == node.Name {
						nodeInfo.AddPod(pod)
					}
				}
				nodeInfos = append(nodeInfos, nodeInfo)
			}
			assert.Equal(t, tt.want, calculateNodeExistingPodsNum(context.TODO(), parallelize.NewParallelizer(16), tt.args.selectorKey, nodeInfos))
		})
	}
}

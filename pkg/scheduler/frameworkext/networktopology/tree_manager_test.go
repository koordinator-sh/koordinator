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

package networktopology

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func Test_treeManager_Run(t *testing.T) {
	fakeClusterNetworkTopology := FakeClusterNetworkTopology.DeepCopy()
	fakeClusterNetworkTopology.Status = v1alpha1.ClusterNetworkTopologyStatus{}
	tests := []struct {
		name                             string
		nodes                            []*corev1.Node
		clusterNetworkTopology           *v1alpha1.ClusterNetworkTopology
		wantClusterNetworkTopologyStatus v1alpha1.ClusterNetworkTopologyStatus
	}{
		{
			name:                   "normal flow",
			clusterNetworkTopology: fakeClusterNetworkTopology,
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b1",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b1",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b2",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-4",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b2",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-5",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b3",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-6",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b3",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-7",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b4",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-8",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b4",
						},
					},
				},
			},
			wantClusterNetworkTopologyStatus: FakeClusterNetworkTopology.Status,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm, fakeTools := NewFakeTreeManager(tt.clusterNetworkTopology, tt.nodes)
			tm.Run(context.TODO())
			gotNetworkTopology, _ := fakeTools.KoordClient.SchedulingV1alpha1().ClusterNetworkTopologies().Get(context.TODO(), fakeClusterNetworkTopology.Name, metav1.GetOptions{})
			assert.Equal(t, tt.wantClusterNetworkTopologyStatus, gotNetworkTopology.Status)
		})
	}
}

func Test_treeManager_GetSnapshot(t *testing.T) {
	fakeClusterNetworkTopology := FakeClusterNetworkTopology.DeepCopy()
	tests := []struct {
		name                   string
		nodes                  []*corev1.Node
		clusterNetworkTopology *v1alpha1.ClusterNetworkTopology
		want                   *TreeSnapshot
	}{
		{
			name:                   "normal flow",
			clusterNetworkTopology: fakeClusterNetworkTopology,
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b1",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b1",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b2",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-4",
						Labels: map[string]string{
							FakeSpineLabel: "s1",
							FakeBlockLabel: "b2",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-5",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b3",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-6",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b3",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-7",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b4",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-8",
						Labels: map[string]string{
							FakeSpineLabel: "s2",
							FakeBlockLabel: "b4",
						},
					},
				},
			},
			want: &TreeSnapshot{
				TreeNode: &TreeNode{
					TreeNodeMeta: TreeNodeMeta{
						Layer: v1alpha1.ClusterTopologyLayer,
					},
					Children: map[string]*TreeNode{
						"s1": {
							TreeNodeMeta: TreeNodeMeta{
								Layer: "SpineLayer",
								Name:  "s1",
							},
							Children: map[string]*TreeNode{
								"b1": {
									TreeNodeMeta: TreeNodeMeta{
										Layer: "BlockLayer",
										Name:  "b1",
									},
									Children: map[string]*TreeNode{
										"node-1": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-1",
											},
										},
										"node-2": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-2",
											},
										},
									},
								},
								"b2": {
									TreeNodeMeta: TreeNodeMeta{
										Layer: "BlockLayer",
										Name:  "b2",
									},
									Children: map[string]*TreeNode{
										"node-3": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-3",
											},
										},
										"node-4": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-4",
											},
										},
									},
								},
							},
						},
						"s2": {
							TreeNodeMeta: TreeNodeMeta{
								Layer: "SpineLayer",
								Name:  "s2",
							},
							Children: map[string]*TreeNode{
								"b3": {
									TreeNodeMeta: TreeNodeMeta{
										Layer: "BlockLayer",
										Name:  "b3",
									},
									Children: map[string]*TreeNode{
										"node-5": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-5",
											},
										},
										"node-6": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-6",
											},
										},
									},
								},
								"b4": {
									TreeNodeMeta: TreeNodeMeta{
										Layer: "BlockLayer",
										Name:  "b4",
									},
									Children: map[string]*TreeNode{
										"node-7": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-7",
											},
										},
										"node-8": {
											TreeNodeMeta: TreeNodeMeta{
												Layer: v1alpha1.NodeTopologyLayer,
												Name:  "node-8",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm, _ := NewFakeTreeManager(tt.clusterNetworkTopology, tt.nodes)
			tm.Run(context.TODO())
			snapshot := tm.GetSnapshot()
			makeParentNil(snapshot.TreeNode)
			makeParentNil(tt.want.TreeNode)
			assert.Equal(t, tt.want.TreeNode, snapshot.TreeNode)
		})
	}
}

func makeParentNil(node *TreeNode) {
	node.Parent = nil
	for _, child := range node.Children {
		makeParentNil(child)
	}
}

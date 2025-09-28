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
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

type Tree interface {
	AddNode(*corev1.Node)
	GetSnapshot() *TreeSnapshot
}

type tree struct {
	lock  sync.RWMutex
	root  *TreeNode
	index map[TreeNodeMeta]*TreeNode

	sortedTopologySpec []schedulingv1alpha1.NetworkTopologySpec
	layerToIndex       map[schedulingv1alpha1.TopologyLayer]int
}

func NewTree(clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology) (Tree, error) {
	sortedNetworkTopologySpec, err := sortNetworkTopologySpecFromParentToChild(clusterNetworkTopology.Spec.NetworkTopologySpec)
	if err != nil {
		return nil, err
	}
	layerToIndex := make(map[schedulingv1alpha1.TopologyLayer]int, len(sortedNetworkTopologySpec))
	for i, spec := range sortedNetworkTopologySpec {
		layerToIndex[spec.TopologyLayer] = i
	}
	rootMeta := TreeNodeMeta{
		Layer: schedulingv1alpha1.ClusterTopologyLayer,
	}
	root := &TreeNode{
		TreeNodeMeta: rootMeta,
	}
	index := make(map[TreeNodeMeta]*TreeNode)
	index[rootMeta] = root
	return &tree{
		sortedTopologySpec: sortedNetworkTopologySpec,
		layerToIndex:       layerToIndex,
		root:               root,
		index:              index,
	}, nil
}

func sortNetworkTopologySpecFromParentToChild(networkTopologySpec []schedulingv1alpha1.NetworkTopologySpec) ([]schedulingv1alpha1.NetworkTopologySpec, error) {
	sorted := make([]schedulingv1alpha1.NetworkTopologySpec, len(networkTopologySpec))
	index := make(map[schedulingv1alpha1.TopologyLayer]*schedulingv1alpha1.NetworkTopologySpec)
	var (
		curLayer = schedulingv1alpha1.NodeTopologyLayer
		curSpec  *schedulingv1alpha1.NetworkTopologySpec
	)
	for i := range networkTopologySpec {
		spec := networkTopologySpec[i]
		if spec.TopologyLayer == curLayer {
			curSpec = &spec
		}
		index[spec.TopologyLayer] = &spec
	}
	for i := len(sorted) - 1; i >= 0; i-- {
		if curSpec == nil {
			return nil, fmt.Errorf("topology layer %q not found in spec", curLayer)
		}
		sorted[i] = *curSpec
		curLayer = curSpec.ParentTopologyLayer
		curSpec = index[curLayer]
	}
	return sorted, nil
}

type TreeNode struct {
	TreeNodeMeta
	Parent   *TreeNode
	Children map[string]*TreeNode

	OfferSlot int
	Score     int
}

type TreeNodeMeta struct {
	Layer schedulingv1alpha1.TopologyLayer
	Name  string
}

func (t *tree) AddNode(node *corev1.Node) {
	treeNodesMeta, err := t.getTreeNodesMeta(node)
	if err != nil {
		klog.Error(err)
		return
	}

	t.lock.Lock()
	defer t.lock.Unlock()
	t.addNode(treeNodesMeta, node)
}

func (t *tree) addNode(treeNodesMeta []TreeNodeMeta, node *corev1.Node) {
	parent := t.root
	for _, meta := range treeNodesMeta {
		treeNode, ok := t.index[meta]
		if !ok {
			treeNode = &TreeNode{
				TreeNodeMeta: meta,
				Parent:       parent,
			}
			if parent.Children == nil {
				parent.Children = make(map[string]*TreeNode)
			}
			parent.Children[meta.Name] = treeNode
			t.index[meta] = treeNode
		}
		parent = treeNode
	}
}

// getTreeNodesMeta extracts all related TreeNodeMeta from a K8s Node.
// The returned order is the same as the order of []NetworkTopologySpec.
func (t *tree) getTreeNodesMeta(node *corev1.Node) ([]TreeNodeMeta, error) {
	var treeNodesMeta []TreeNodeMeta
	for i, topo := range t.sortedTopologySpec {
		meta := TreeNodeMeta{
			Layer: topo.TopologyLayer,
		}
		if topo.TopologyLayer == schedulingv1alpha1.NodeTopologyLayer {
			meta.Name = node.Name
		} else {
			for _, key := range topo.LabelKey {
				value := node.Labels[key]
				if value != "" {
					meta.Name = value
					break
				}
			}
		}
		if meta.Name == "" {
			// currently we only allow node missing its direct parent layer (typically accelerators)
			if i == len(t.sortedTopologySpec)-2 {
				// make a virtual tree node so that we get a tree with each layer in the same tree level
				meta.Name = node.Name
			} else {
				return nil, fmt.Errorf("node %q missing network topology layer %q", node.Name, topo.TopologyLayer)
			}
		}
		treeNodesMeta = append(treeNodesMeta, meta)
	}
	return treeNodesMeta, nil
}

type IsLayerAncestorFunc func(a, b schedulingv1alpha1.TopologyLayer) bool

type TreeSnapshot struct {
	TreeNode   *TreeNode
	IsAncestor IsLayerAncestorFunc
}

func (t *tree) GetSnapshot() *TreeSnapshot {
	t.lock.RLock()
	defer t.lock.RUnlock()
	layerToIndex := make(map[schedulingv1alpha1.TopologyLayer]int, len(t.layerToIndex))
	for layer, index := range t.layerToIndex {
		layerToIndex[layer] = index
	}
	return &TreeSnapshot{
		TreeNode: DeepCopyTreeNode(t.root, nil),
		IsAncestor: func(a, b schedulingv1alpha1.TopologyLayer) bool {
			return layerToIndex[a] < layerToIndex[b]
		},
	}
}

func DeepCopyTreeNode(origin, parent *TreeNode) *TreeNode {
	if origin == nil {
		return nil
	}
	copied := &TreeNode{
		TreeNodeMeta: origin.TreeNodeMeta,
		Parent:       parent,
	}
	if len(origin.Children) > 0 {
		copied.Children = make(map[string]*TreeNode, len(origin.Children))
		for _, child := range origin.Children {
			copied.Children[child.Name] = DeepCopyTreeNode(child, copied)
		}
	}
	return copied
}

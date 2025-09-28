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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var _ TreeManager = &treeManager{}

type TreeManager interface {
	RefreshTree(clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology)
	DeleteTree(clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology)
	Tree
}

type treeManager struct {
	sync.RWMutex
	clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology
	tree                   RebuildableTree
}

func NewTreeManager() TreeManager {
	return &treeManager{}
}

func (t *treeManager) RefreshTree(clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology) {
	t.Lock()
	defer t.Unlock()
	if t.clusterNetworkTopology == nil ||
		t.clusterNetworkTopology.CreationTimestamp.Time.Before(clusterNetworkTopology.CreationTimestamp.Time) ||
		t.clusterNetworkTopology.ResourceVersion != clusterNetworkTopology.ResourceVersion {
		t.clusterNetworkTopology = clusterNetworkTopology
		newTree, err := NewTree(clusterNetworkTopology)
		if err != nil {
			klog.Errorf("failed to create tree: %v, name: %s, resourceVersion: %s", err, clusterNetworkTopology.Name, clusterNetworkTopology.ResourceVersion)
			t.tree = nil
			return
		}
		klog.V(4).Infof("success to create tree, name: %s, resourceVersion: %s", clusterNetworkTopology.Name, clusterNetworkTopology.ResourceVersion)
		t.tree = newTree
		t.tree.Rebuild()
	}
}

func (t *treeManager) DeleteTree(clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology) {
	t.Lock()
	defer t.Unlock()
	if t.clusterNetworkTopology != nil &&
		t.clusterNetworkTopology.Name == clusterNetworkTopology.Name {
		t.clusterNetworkTopology = nil
		t.tree = nil
	}
}

func (t *treeManager) AddNode(node *corev1.Node) {
	t.RLock()
	defer t.RUnlock()
	if t.tree != nil {
		t.tree.AddNode(node)
	}
}

func (t *treeManager) UpdateNode(node *corev1.Node, node2 *corev1.Node) {
	t.RLock()
	defer t.RUnlock()
	t.tree.UpdateNode(node, node2)
}

func (t *treeManager) DeleteNode(node *corev1.Node) {
	t.RLock()
	defer t.RUnlock()
	if t.tree != nil {
		t.tree.DeleteNode(node)
	}
}

func (t *treeManager) GetSnapshot() *TreeSnapshot {
	t.RLock()
	defer t.RUnlock()
	if t.tree == nil {
		return t.tree.GetSnapshot()
	}
	return nil
}

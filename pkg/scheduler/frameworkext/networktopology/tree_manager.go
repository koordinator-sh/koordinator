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
	"reflect"
	"sort"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	clientv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
)

const (
	Name = "NetworkTopology"

	defaultClusterNetworkTopologyName = "default"
	defaultRebuildTreeDuration        = time.Second * 10
)

var _ TreeManager = &treeManager{}

type TreeManager interface {
	Name() string
	Run(ctx context.Context)
	GetSnapshot() *TreeSnapshot
}

type treeManager struct {
	informerFactory      informers.SharedInformerFactory
	koordInformerFactory koordinatorinformers.SharedInformerFactory
	topologyLister       v1alpha1.ClusterNetworkTopologyLister
	topologyClient       clientv1alpha1.ClusterNetworkTopologyInterface

	sync.RWMutex
	tree Tree
}

func (tm *treeManager) Name() string {
	return Name
}

func NewTreeManager(
	koordInformerFactory koordinatorinformers.SharedInformerFactory,
	informerFactory informers.SharedInformerFactory,
	client koordclientset.Interface,
) TreeManager {
	return &treeManager{
		koordInformerFactory: koordInformerFactory,
		informerFactory:      informerFactory,
		topologyClient:       client.SchedulingV1alpha1().ClusterNetworkTopologies(),
	}
}

func (tm *treeManager) Run(ctx context.Context) {
	tm.informerFactory.Start(ctx.Done())
	tm.informerFactory.WaitForCacheSync(ctx.Done())
	_ = tm.koordInformerFactory.Scheduling().V1alpha1().ClusterNetworkTopologies().Informer()
	tm.topologyLister = tm.koordInformerFactory.Scheduling().V1alpha1().ClusterNetworkTopologies().Lister()
	tm.koordInformerFactory.Start(ctx.Done())
	tm.koordInformerFactory.WaitForCacheSync(ctx.Done())
	tm.run(ctx)
	go wait.Until(func() {
		tm.run(ctx)
	}, defaultRebuildTreeDuration, ctx.Done())
}

func (tm *treeManager) run(ctx context.Context) {
	t := tm.buildTree()
	if t == nil {
		return
	}
	tm.updateStatus(ctx, t)
	tm.cacheTree(t)
}

func (tm *treeManager) buildTree() Tree {
	nodeLister := tm.informerFactory.Core().V1().Nodes().Lister()
	selector := labels.Everything()
	allNodes, err := nodeLister.List(selector)
	if err != nil {
		klog.Errorf("[NetworkTopology] list node fail, err: %v", err.Error())
		return nil
	}
	clusterNetworkTopology, err := tm.topologyLister.Get(defaultClusterNetworkTopologyName)
	if err != nil {
		klog.Errorf("[NetworkTopology] get cluster network topology fail, clusterNetworkTopologyName: %v, err: %v",
			defaultClusterNetworkTopologyName, err.Error())
		return nil
	}
	t, err := NewTree(clusterNetworkTopology)
	if err != nil {
		klog.Errorf("[NetworkTopology] new tree fail, err: %v", err.Error())
		return nil
	}
	for _, node := range allNodes {
		t.AddNode(node)
	}
	return t
}

func (tm *treeManager) updateStatus(ctx context.Context, t Tree) {
	clusterNetworkTopology, err := tm.topologyLister.Get(defaultClusterNetworkTopologyName)
	if err != nil {
		klog.Errorf("[NetworkTopology] get cluster network topology fail, clusterNetworkTopologyName: %v, err: %v",
			defaultClusterNetworkTopologyName, err.Error())
		return
	}
	snapshot := t.GetSnapshot()
	toUpdateClusterTopology := clusterNetworkTopology.DeepCopy()
	toUpdateClusterTopology.Status.DetailStatus = make([]*schedulingv1alpha1.ClusterNetworkTopologyDetailStatus, 0)
	makeStatus(snapshot.TreeNode, toUpdateClusterTopology)
	if reflect.DeepEqual(toUpdateClusterTopology, clusterNetworkTopology) {
		return
	}

	_, err = tm.topologyClient.UpdateStatus(
		ctx, toUpdateClusterTopology, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("[NetworkTopology] Failed update ClusterNetworkTopology, err: %v", err.Error())
	}
}

func makeStatus(treeNode *TreeNode, clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology) (nodeNum int32) {
	status := &clusterNetworkTopology.Status.DetailStatus
	curStatus := &schedulingv1alpha1.ClusterNetworkTopologyDetailStatus{
		TopologyInfo: schedulingv1alpha1.TopologyInfo{
			TopologyLayer: treeNode.Layer,
			TopologyName:  treeNode.Name,
		},
	}
	if treeNode.Parent != nil {
		curStatus.ParentTopologyInfo = &schedulingv1alpha1.TopologyInfo{
			TopologyLayer: treeNode.Parent.Layer,
			TopologyName:  treeNode.Parent.Name,
		}
	}
	childTopologyNames := make([]string, 0, len(treeNode.Children))
	for _, childNode := range treeNode.Children {
		curStatus.ChildTopologyLayer = childNode.Layer
		childTopologyNames = append(childTopologyNames, childNode.TreeNodeMeta.Name)
	}
	sort.Strings(childTopologyNames)
	if len(childTopologyNames) > 0 && curStatus.ChildTopologyLayer != schedulingv1alpha1.NodeTopologyLayer {
		curStatus.ChildTopologyNames = childTopologyNames
	}

	if treeNode.Layer == schedulingv1alpha1.NodeTopologyLayer {
		curStatus.NodeNum = 1
		return 1
	}

	*status = append(*status, curStatus)
	for _, name := range childTopologyNames {
		childNode := treeNode.Children[name]
		childNodeNum := makeStatus(childNode, clusterNetworkTopology)
		nodeNum += childNodeNum
	}
	curStatus.NodeNum = nodeNum
	return nodeNum
}

func (tm *treeManager) cacheTree(t Tree) {
	tm.Lock()
	defer tm.Unlock()
	tm.tree = t
}

func (tm *treeManager) GetSnapshot() *TreeSnapshot {
	tm.RLock()
	defer tm.RUnlock()
	if tm.tree != nil {
		return tm.tree.GetSnapshot()
	}
	return nil
}

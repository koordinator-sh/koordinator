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

package elasticquota

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

func (g *Plugin) OnNodeAdd(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		return
	}
	if node.DeletionTimestamp != nil {
		klog.V(5).Infof("OnNodeAddFunc add:%v delete:%v", node.Name, node.DeletionTimestamp)
		return
	}

	g.groupQuotaManager.OnNodeAdd(node)
}

func (g *Plugin) OnNodeUpdate(oldObj, newObj interface{}) {
	newNode := newObj.(*corev1.Node)
	oldNode := oldObj.(*corev1.Node)

	if newNode.ResourceVersion == oldNode.ResourceVersion {
		klog.Warningf("update node warning, update version for the same, nodeName:%v", newNode.Name)
		return
	}
	if newNode.DeletionTimestamp != nil {
		klog.V(5).Infof("OnNodeUpdateFunc update:%v delete:%v", newNode.Name, newNode.DeletionTimestamp)
		return
	}

	g.groupQuotaManager.OnNodeUpdate(oldNode, newNode)
}

func (g *Plugin) OnNodeDelete(obj interface{}) {
	var node *corev1.Node
	switch t := obj.(type) {
	case *corev1.Node:
		node = t
	case cache.DeletedFinalStateUnknown:
		node, _ = t.Obj.(*corev1.Node)
	}
	if node == nil {
		return
	}

	g.groupQuotaManager.OnNodeDelete(node)
}

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

package framework

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

var (
	globalNodePrepareExtender       = map[string]NodePreparePlugin{}
	globalNodeSyncExtender          = map[string]NodeSyncPlugin{}
	globalResourceCalculateExtender = map[string]ResourceCalculatePlugin{}
)

// NodePreparePlugin implements node resource preparing for the calculated results.
// e.g. Assign extended resources in the node allocatable.
// It is invoked each time the controller tries updating the latest NodeResource object with calculated results.
type NodePreparePlugin interface {
	Execute(strategy *extension.ColocationStrategy, node *corev1.Node, nr *NodeResource) error
}

func RegisterNodePrepareExtender(name string, plugin NodePreparePlugin) error {
	if _, exist := globalNodePrepareExtender[name]; exist {
		return fmt.Errorf("node prepare plugin %s already exist", name)
	}
	globalNodePrepareExtender[name] = plugin
	return nil
}

func RunNodePrepareExtenders(strategy *extension.ColocationStrategy, node *corev1.Node, nr *NodeResource) {
	for name, plugin := range globalNodePrepareExtender {
		if err := plugin.Execute(strategy, node, nr); err != nil {
			klog.ErrorS(err, "run node prepare plugin failed", "plugin", name)
		} else {
			klog.V(5).InfoS("run node prepare plugin successfully", "plugin", name, "node", node)
		}
	}
}

func UnregisterNodePrepareExtender(name string) {
	delete(globalNodePrepareExtender, name)
}

// NodeSyncPlugin implements the check of resource updating.
// e.g. Update if the values of the current is more than 10% different with the former.
type NodeSyncPlugin interface {
	NeedSync(strategy *extension.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string)
}

func RegisterNodeSyncExtender(name string, plugin NodeSyncPlugin) error {
	if _, exist := globalNodeSyncExtender[name]; exist {
		return fmt.Errorf("node sync plugin %s already exist", name)
	}
	globalNodeSyncExtender[name] = plugin
	return nil
}

func UnregisterNodeSyncExtender(name string) {
	delete(globalNodeSyncExtender, name)
}

func RunNodeSyncExtenders(strategy *extension.ColocationStrategy, oldNode, newNode *corev1.Node) bool {
	for name, plugin := range globalNodeSyncExtender {
		needSync, msg := plugin.NeedSync(strategy, oldNode, newNode)
		if needSync {
			klog.V(4).InfoS("run node sync plugin, need sync", "plugin", name,
				"node", newNode.Name, "message", msg)
			return true
		} else {
			klog.V(6).InfoS("run node sync plugin, no need to sync", "plugin", name,
				"node", newNode.Name)
		}
	}
	return false
}

type ResourceResetPlugin interface {
	Reset(node *corev1.Node, message string) []ResourceItem
}

func RunResourceResetExtenders(nr *NodeResource, node *corev1.Node, message string) {
	for name, plugin := range globalResourceCalculateExtender {
		resourceItems := plugin.Reset(node, message)
		nr.Set(resourceItems...)
		klog.V(5).InfoS("run resource reset plugin successfully", "plugin", name,
			"resource items", resourceItems, "message", message)
	}
}

// ResourceCalculatePlugin implements resource counting and overcommitment algorithms.
// It implements Reset which can be invoked when the calculated resources need a reset.
type ResourceCalculatePlugin interface {
	ResourceResetPlugin
	Calculate(strategy *extension.ColocationStrategy, node *corev1.Node, podList *corev1.PodList, metrics *ResourceMetrics) ([]ResourceItem, error)
}

func RegisterResourceCalculateExtender(name string, plugin ResourceCalculatePlugin) error {
	if _, exist := globalResourceCalculateExtender[name]; exist {
		return fmt.Errorf("resource calculate plugin %s already exist", name)
	}
	globalResourceCalculateExtender[name] = plugin
	return nil
}

func UnregisterResourceCalculateExtender(name string) {
	delete(globalResourceCalculateExtender, name)
}

func RunResourceCalculateExtenders(nr *NodeResource, strategy *extension.ColocationStrategy, node *corev1.Node, podList *corev1.PodList, metrics *ResourceMetrics) {
	for name, plugin := range globalResourceCalculateExtender {
		resourceItems, err := plugin.Calculate(strategy, node, podList, metrics)
		if err != nil {
			klog.ErrorS(err, "run resource calculate plugin failed", "plugin", name)
		} else {
			nr.Set(resourceItems...)
			klog.V(5).InfoS("run resource calculate plugin successfully", "plugin", name, "resource items", resourceItems)
		}
	}
}

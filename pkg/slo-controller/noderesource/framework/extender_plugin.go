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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
)

// The plugins called in the node resource initialization:
// - Setup
//
// The plugins called order in the node resource reconcile loop:
// - Calculate/Reset -> NodePreUpdate -> NodeUpdate(NodePrepare -> NodeStatusCheck,NodeMetaCheck)
// The NodeUpdate stage can be called for multiple times with retries.
//
// For more info, please see the README.md.
var (
	globalSetupExtender             = NewRegistry("Setup")
	globalNodePreUpdateExtender     = NewRegistry("NodePreUpdate")
	globalNodePrepareExtender       = NewRegistry("NodePrepare")
	globalNodeStatusCheckExtender   = NewRegistry("NodeStatusCheck")
	globalNodeMetaCheckExtender     = NewRegistry("NodeMetaCheck")
	globalResourceCalculateExtender = NewRegistry("ResourceCalculate")
)

// Plugin has its name. Plugins in a registry are executed in order of the registration.
type Plugin interface {
	Name() string
}

// SetupPlugin implements setup for the plugin.
// The framework exposes the kube ClientSet and controller builder to the plugins thus the plugins can set up their
// necessary clients, add new watches and initialize their internal states.
// The Setup of each plugin will be called before other extension stages and invoked only once.
type SetupPlugin interface {
	Plugin
	Setup(opt *Option) error
}

func RegisterSetupExtender(filter FilterFn, plugins ...SetupPlugin) {
	ps := make([]Plugin, 0, len(plugins))
	for i := range plugins {
		if filter(plugins[i].Name()) {
			ps = append(ps, plugins[i])
		}
	}
	globalSetupExtender.MustRegister(ps...)
}

func RunSetupExtenders(opt *Option) {
	for _, p := range globalSetupExtender.GetAll() {
		plugin := p.(SetupPlugin)
		if err := plugin.Setup(opt); err != nil {
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), false, "Setup")
			klog.ErrorS(err, "run setup plugin failed", "plugin", plugin.Name())
		} else {
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), true, "Setup")
			klog.V(5).InfoS("run setup plugin successfully", "plugin", plugin.Name())
		}
	}
}

func UnregisterSetupExtender(name string) {
	globalSetupExtender.Unregister(name)
}

// NodePreUpdatePlugin implements preprocessing for the calculated results called before updating the Node.
// There are mainly two use cases for this stage:
// 1. A plugin may prepare and update some Objects like CRDs before updating the Node obj (NodePrepare and NodeXXXCheck).
// 2. A plugin may need to mutate the internal NodeResource object before updating the Node object.
// It differs from the NodePreparePlugin in that a NodePreUpdatePlugin will be invoked only once in one loop (so the
// plugin should consider implement a retry login itself if needed), while the NodePreparePlugin is not expected to
// update other objects or mutate the NodeResource.
type NodePreUpdatePlugin interface {
	Plugin
	PreUpdate(strategy *configuration.ColocationStrategy, node *corev1.Node, nr *NodeResource) error
}

func RegisterNodePreUpdateExtender(filter FilterFn, plugins ...NodePreUpdatePlugin) {
	ps := make([]Plugin, 0, len(plugins))
	for i := range plugins {
		if filter(plugins[i].Name()) {
			ps = append(ps, plugins[i])
		}
	}
	globalNodePreUpdateExtender.MustRegister(ps...)
}

func RunNodePreUpdateExtenders(strategy *configuration.ColocationStrategy, node *corev1.Node, nr *NodeResource) {
	for _, p := range globalNodePreUpdateExtender.GetAll() {
		plugin := p.(NodePreUpdatePlugin)
		if err := plugin.PreUpdate(strategy, node, nr); err != nil {
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), false, "NodePreUpdate")
			klog.ErrorS(err, "run node pre update plugin failed", "plugin", plugin.Name(),
				"node", node.Name)
		} else {
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), true, "NodePreUpdate")
			klog.V(5).InfoS("run node pre update plugin successfully", "plugin", plugin.Name(),
				"node", node.Name)
		}
	}
}

func UnregisterNodePreUpdateExtender(name string) {
	globalNodePreUpdateExtender.Unregister(name)
}

// NodePreparePlugin implements node resource preparing for the calculated results.
// For example, assign extended resources in the node allocatable.
// It is invoked each time the controller tries updating the latest NodeResource object with calculated results.
// NOTE: The Prepare should be idempotent since it can be called multiple times in one reconciliation.
type NodePreparePlugin interface {
	Plugin
	Prepare(strategy *configuration.ColocationStrategy, node *corev1.Node, nr *NodeResource) error
}

func RegisterNodePrepareExtender(filter FilterFn, plugins ...NodePreparePlugin) {
	ps := make([]Plugin, 0, len(plugins))
	for i := range plugins {
		if filter(plugins[i].Name()) {
			ps = append(ps, plugins[i])
		}
	}
	globalNodePrepareExtender.MustRegister(ps...)
}

func RunNodePrepareExtenders(strategy *configuration.ColocationStrategy, node *corev1.Node, nr *NodeResource) {
	for _, p := range globalNodePrepareExtender.GetAll() {
		plugin := p.(NodePreparePlugin)
		if err := plugin.Prepare(strategy, node, nr); err != nil {
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), false, "NodePrepare")
			klog.ErrorS(err, "run node prepare plugin failed", "plugin", plugin.Name(),
				"node", node.Name)
		} else {
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), true, "NodePrepare")
			klog.V(5).InfoS("run node prepare plugin successfully", "plugin", plugin.Name(),
				"node", node.Name)
		}
	}
}

func UnregisterNodePrepareExtender(name string) {
	globalNodePrepareExtender.Unregister(name)
}

// NodeStatusCheckPlugin implements the check of resource updating.
// For example, trigger an update if the values of the current is more than 10% different with the former.
type NodeStatusCheckPlugin interface {
	Plugin
	NeedSync(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string)
}

func RegisterNodeStatusCheckExtender(filter FilterFn, plugins ...NodeStatusCheckPlugin) {
	ps := make([]Plugin, 0, len(plugins))
	for i := range plugins {
		if filter(plugins[i].Name()) {
			ps = append(ps, plugins[i])
		}
	}
	globalNodeStatusCheckExtender.MustRegister(ps...)
}

func UnregisterNodeStatusCheckExtender(name string) {
	globalNodeStatusCheckExtender.Unregister(name)
}

func RunNodeStatusCheckExtenders(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) bool {
	for _, p := range globalNodeStatusCheckExtender.GetAll() {
		plugin := p.(NodeStatusCheckPlugin)
		needSync, msg := plugin.NeedSync(strategy, oldNode, newNode)
		metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), true, "NodeStatusCheck")
		if needSync {
			klog.V(4).InfoS("run node status check plugin, need sync", "plugin", plugin.Name(),
				"node", newNode.Name, "message", msg)
			return true
		} else {
			klog.V(6).InfoS("run node status check plugin, no need to sync", "plugin", plugin.Name(),
				"node", newNode.Name)
		}
	}
	return false
}

type NodeMetaCheckPlugin interface {
	Plugin
	NeedSyncMeta(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string)
}

func RegisterNodeMetaCheckExtender(filter FilterFn, plugins ...NodeMetaCheckPlugin) {
	ps := make([]Plugin, 0, len(plugins))
	for i := range plugins {
		if filter(plugins[i].Name()) {
			ps = append(ps, plugins[i])
		}
	}
	globalNodeMetaCheckExtender.MustRegister(ps...)
}

func UnregisterNodeMetaCheckExtender(name string) {
	globalNodeMetaCheckExtender.Unregister(name)
}

func RunNodeMetaCheckExtenders(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) bool {
	for _, p := range globalNodeMetaCheckExtender.GetAll() {
		plugin := p.(NodeMetaCheckPlugin)
		needSync, msg := plugin.NeedSyncMeta(strategy, oldNode, newNode)
		metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), true, "NodeStatusCheckMeta")
		if needSync {
			klog.V(4).InfoS("run node meta check plugin, need sync", "plugin", plugin.Name(),
				"node", newNode.Name, "message", msg)
			return true
		} else {
			klog.V(6).InfoS("run node meta check plugin, no need to sync",
				"plugin", plugin.Name(), "node", newNode.Name)
		}
	}
	return false
}

type ResourceResetPlugin interface {
	Plugin
	Reset(node *corev1.Node, message string) []ResourceItem
}

func RunResourceResetExtenders(nr *NodeResource, node *corev1.Node, message string) {
	for _, p := range globalResourceCalculateExtender.GetAll() {
		plugin := p.(ResourceCalculatePlugin)
		resourceItems := plugin.Reset(node, message)
		nr.Set(resourceItems...)
		metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), true, "ResourceReset")
		klog.V(5).InfoS("run resource reset plugin successfully", "plugin", plugin.Name(),
			"node", node.Name, "resource items", resourceItems, "message", message)
	}
}

// ResourceCalculatePlugin implements resource counting and overcommitment algorithms.
// It implements Reset which can be invoked when the calculated resources need a reset.
// A ResourceCalculatePlugin can handle the case when the metrics are abnormal by implementing degraded calculation.
type ResourceCalculatePlugin interface {
	ResourceResetPlugin
	Calculate(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList, metrics *ResourceMetrics) ([]ResourceItem, error)
}

func RegisterResourceCalculateExtender(filter FilterFn, plugins ...ResourceCalculatePlugin) {
	ps := make([]Plugin, 0, len(plugins))
	for i := range plugins {
		if filter(plugins[i].Name()) {
			ps = append(ps, plugins[i])
		}
	}
	globalResourceCalculateExtender.MustRegister(ps...)
}

func UnregisterResourceCalculateExtender(name string) {
	globalResourceCalculateExtender.Unregister(name)
}

func RunResourceCalculateExtenders(nr *NodeResource, strategy *configuration.ColocationStrategy, node *corev1.Node,
	podList *corev1.PodList, resourceMetrics *ResourceMetrics) {
	for _, p := range globalResourceCalculateExtender.GetAll() {
		plugin := p.(ResourceCalculatePlugin)
		resourceItems, err := plugin.Calculate(strategy, node, podList, resourceMetrics)
		if err != nil {
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), false, "ResourceCalculate")
			klog.ErrorS(err, "run resource calculate plugin failed", "plugin", plugin.Name(),
				"node", node.Name)
		} else {
			nr.Set(resourceItems...)
			metrics.RecordNodeResourceRunPluginStatus(plugin.Name(), true, "ResourceCalculate")
			klog.V(5).InfoS("run resource calculate plugin successfully",
				"plugin", plugin.Name(), "node", node.Name, "resource items", resourceItems)
		}
	}
}

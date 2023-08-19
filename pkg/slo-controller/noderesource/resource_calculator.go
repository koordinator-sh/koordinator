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

package noderesource

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func (r *NodeResourceReconciler) isColocationCfgDisabled(node *corev1.Node) bool {
	cfg := r.cfgCache.GetCfgCopy()
	if cfg.Enable == nil || !*cfg.Enable {
		return true
	}
	strategy := sloconfig.GetNodeColocationStrategy(cfg, node)
	if strategy == nil || strategy.Enable == nil {
		return true
	}
	return !(*strategy.Enable)
}

func (r *NodeResourceReconciler) resetNodeResource(node *corev1.Node, message string) error {
	nr := framework.NewNodeResource()

	framework.RunResourceResetExtenders(nr, node, message)

	return r.updateNodeResource(node, nr)
}

func (r *NodeResourceReconciler) calculateNodeResource(node *corev1.Node,
	nodeMetric *slov1alpha1.NodeMetric, podList *corev1.PodList) *framework.NodeResource {
	nr := framework.NewNodeResource()
	resourceMetrics := &framework.ResourceMetrics{
		NodeMetric: nodeMetric,
	}

	strategy := sloconfig.GetNodeColocationStrategy(r.cfgCache.GetCfgCopy(), node)
	framework.RunResourceCalculateExtenders(nr, strategy, node, podList, resourceMetrics)

	return nr
}

func (r *NodeResourceReconciler) updateNodeResource(node *corev1.Node, nr *framework.NodeResource) error {
	nodeCopy := node.DeepCopy() // avoid overwriting the cache
	strategy := sloconfig.GetNodeColocationStrategy(r.cfgCache.GetCfgCopy(), node)

	r.prepareNodeResource(strategy, nodeCopy, nr)
	needSyncStatus, needSyncMeta := r.isNodeResourceSyncNeeded(strategy, node, nodeCopy)
	if !needSyncStatus && !needSyncMeta {
		klog.V(5).InfoS("skip update node resource for node", "node", node.Name)
		return nil
	}

	var errList []error
	if needSyncStatus {
		if err := util.RetryOnConflictOrTooManyRequests(func() error {
			return r.updateNodeStatus(node, strategy, nr)
		}); err != nil {
			klog.ErrorS(err, "failed to update node status for node", "node", node.Name)
			errList = append(errList, err)
		}
	}

	if needSyncMeta {
		if err := util.RetryOnConflictOrTooManyRequests(func() error {
			return r.updateNodeMeta(node, strategy, nr)
		}); err != nil {
			klog.ErrorS(err, "failed to update node meta for node", "node", node.Name)
			errList = append(errList, err)
		}
	}

	if len(errList) <= 0 {
		return nil
	}

	return utilerrors.NewAggregate(errList)
}

func (r *NodeResourceReconciler) updateNodeStatus(node *corev1.Node, strategy *configuration.ColocationStrategy, nr *framework.NodeResource) error {
	nodeCopy := &corev1.Node{}
	if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: node.Name}, nodeCopy); err != nil {
		if errors.IsNotFound(err) {
			metrics.RecordNodeResourceReconcileCount(false, "updateNodeNotFound")
			klog.V(4).InfoS("aborted to update node", "node", nodeCopy.Name, "err", err)
			return nil
		}
		metrics.RecordNodeResourceReconcileCount(false, "updateNodeGetError")
		klog.ErrorS(err, "failed to get node", "node", node.Name)
		return err
	}

	nodeCopy = nodeCopy.DeepCopy() // avoid overwriting the cache
	r.prepareNodeResource(strategy, nodeCopy, nr)

	if err := r.Client.Status().Update(context.TODO(), nodeCopy); err != nil {
		metrics.RecordNodeResourceReconcileCount(false, "updateNodeStatus")
		klog.ErrorS(err, "failed to update node status", "node", nodeCopy.Name)
		return err
	}
	r.NodeSyncContext.Store(util.GenerateNodeKey(&node.ObjectMeta), r.Clock.Now())
	metrics.RecordNodeResourceReconcileCount(true, "updateNodeStatus")
	klog.V(5).InfoS("update node successfully", "node", nodeCopy.Name, "detail", nodeCopy)
	return nil
}

func (r *NodeResourceReconciler) updateNodeMeta(node *corev1.Node, strategy *configuration.ColocationStrategy, nr *framework.NodeResource) error {
	nodeCopy := &corev1.Node{}
	if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: node.Name}, nodeCopy); err != nil {
		if errors.IsNotFound(err) {
			metrics.RecordNodeResourceReconcileCount(false, "updateNodeMetaNotFound")
			klog.V(4).InfoS("aborted to update node", "node", nodeCopy.Name, "err", err)
			return nil
		}
		metrics.RecordNodeResourceReconcileCount(false, "updateNodeMetaGetError")
		klog.ErrorS(err, "failed to get node", "node", node.Name)
		return err
	}

	newNode := nodeCopy.DeepCopy() // avoid overwriting the cache
	r.prepareNodeResource(strategy, newNode, nr)

	patch := client.MergeFrom(nodeCopy)
	if err := r.Client.Patch(context.Background(), newNode, patch); err != nil {
		metrics.RecordNodeResourceReconcileCount(false, "patchNodeMeta")
		klog.V(4).InfoS("failed to patch node meta for node resource",
			"node", newNode.Name, "err", err)
		return err
	}

	metrics.RecordNodeResourceReconcileCount(true, "patchNodeMeta")
	klog.V(5).InfoS("successfully patched node meta for node resource",
		"node", newNode.Name, "patch", patch)
	return nil
}

// updateNodeExtensions is an extension point for updating node other than node metric resources.
func (r *NodeResourceReconciler) updateNodeExtensions(node *corev1.Node, nodeMetric *slov1alpha1.NodeMetric, podList *corev1.PodList) error {
	// update device resources
	if err := r.updateDeviceResources(node); err != nil {
		metrics.RecordNodeResourceReconcileCount(false, "updateDeviceResources")
		klog.V(4).InfoS("failed to update device resources for node", "node", node.Name,
			"err", err)
		return err
	}
	metrics.RecordNodeResourceReconcileCount(true, "updateDeviceResources")

	return nil
}

func (r *NodeResourceReconciler) isNodeResourceSyncNeeded(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, bool) {
	if newNode == nil || newNode.Status.Allocatable == nil || newNode.Status.Capacity == nil {
		klog.ErrorS(fmt.Errorf("invalid node status"), "invalid input, node should be non-nil")
		return false, false
	}

	needSyncStatus, needSyncMeta := false, false
	if r.isCommonNodeNeedSync(strategy, oldNode, newNode) {
		klog.V(6).InfoS("need sync for node", "node", newNode.Name)
		needSyncStatus = true
	}

	isNodeNeedSync := framework.RunNodeSyncExtenders(strategy, oldNode, newNode)
	if isNodeNeedSync {
		needSyncStatus = isNodeNeedSync
		klog.V(6).InfoS("need sync for node by extender", "node", newNode.Name)
	}
	isNodeMetaNeedSync := framework.RunNodeMetaSyncExtenders(strategy, oldNode, newNode)
	if isNodeMetaNeedSync {
		needSyncMeta = isNodeMetaNeedSync
		klog.V(6).InfoS("need sync for node meta by extender", "node", newNode.Name)
	}

	if !isNodeNeedSync && !isNodeMetaNeedSync {
		klog.V(4).InfoS("all good, no need to sync for node", "node", newNode.Name)
	}

	return needSyncStatus, needSyncMeta
}

func (r *NodeResourceReconciler) isCommonNodeNeedSync(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) bool {
	// update time gap is bigger than UpdateTimeThresholdSeconds
	lastUpdatedTime, ok := r.NodeSyncContext.Load(util.GenerateNodeKey(&newNode.ObjectMeta))
	if !ok || r.Clock.Since(lastUpdatedTime) > time.Duration(*strategy.UpdateTimeThresholdSeconds)*time.Second {
		klog.V(4).InfoS("node resource expired, need sync", "node", newNode.Name)
		return true
	}

	return false
}

func (r *NodeResourceReconciler) prepareNodeResource(strategy *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) {
	framework.RunNodePrepareExtenders(strategy, node, nr)
}

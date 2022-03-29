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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

const (
	disableInConfig          string = "DisableInConfig"
	degradeByKoordController string = "DegradeByKoordController"
)

type NodeResourceReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	config      Config
	SyncContext SyncContext
}

func (r *NodeResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx, "node-resource-reconciler", req.NamespacedName)

	if !r.isColocationCfgAvailable() {
		klog.Warningf("colocation config is not available")
		return ctrl.Result{Requeue: false}, nil
	}

	node := &corev1.Node{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			klog.Warningf("error: %v", err)
			return ctrl.Result{Requeue: false}, err
		} else {
			klog.Errorf("failed to get node %v, error: %v", req.Name, err)
			return ctrl.Result{Requeue: true}, err
		}
	}

	if r.isColocationCfgDisabled(node) {
		klog.Warningf("colocation for node %v is disabled, reset BE resource", req.Name)
		if err := r.resetNodeBEResource(node, disableInConfig, "node colocation is disabled in config"); err != nil {
			return ctrl.Result{Requeue: true}, err
		} else {
			return ctrl.Result{Requeue: false}, nil
		}
	}

	nodeMetric := &slov1alpha1.NodeMetric{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, nodeMetric); err != nil {
		if errors.IsNotFound(err) {
			klog.Warningf("error: %v", err)
			return ctrl.Result{Requeue: false}, err
		} else {
			klog.Errorf("failed to get NodeMetric %v, error: %v", req.Name, err)
			return ctrl.Result{Requeue: true}, err
		}
	}

	if r.isDegradeNeeded(nodeMetric, node) {
		klog.Warningf("node %v need degradation, reset BE resource", req.Name)
		if err := r.resetNodeBEResource(node, degradeByKoordController, "degrade node resource because of abnormal NodeMetric"); err != nil {
			return ctrl.Result{Requeue: true}, err
		} else {
			return ctrl.Result{Requeue: false}, nil
		}
	}

	podList := &corev1.PodList{}
	if err := r.Client.List(context.TODO(), podList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", node.Name),
	}); err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	beResource := r.calculateBEResource(node, podList, nodeMetric)

	if err := r.updateNodeBEResource(node, beResource); err != nil {
		klog.Errorf("failed to update node %v BE resource, error: %v", node.Name, err)
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{Requeue: false}, nil
}

func (r *NodeResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Watches(&source.Kind{Type: &slov1alpha1.NodeMetric{}}, &EnqueueRequestForNodeMetric{syncContext: &r.SyncContext}).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, &EnqueueRequestForConfigMap{Client: r.Client, config: &r.config}).
		Complete(r)
}

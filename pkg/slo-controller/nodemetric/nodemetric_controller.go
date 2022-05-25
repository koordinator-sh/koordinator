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

package nodemetric

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource"
)

// NodeMetricReconciler reconciles a NodeMetric object
type NodeMetricReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// TODO: Separate Config from noderesource package
	config noderesource.Config
}

// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=nodemetrics,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=nodemetrics/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=nodemetrics/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NodeMetricReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx, "node-metric-reconciler", req.NamespacedName)

	node, nodeMetric := &corev1.Node{}, &slov1alpha1.NodeMetric{}
	nodeExist, nodeMetricExist := true, true
	nodeName, nodeMetricName := req.Name, req.Name

	if err := r.Client.Get(context.TODO(), req.NamespacedName, node); err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("failed to find node %v, error: %v", nodeName, err)
			return ctrl.Result{Requeue: true}, err
		}
		nodeExist = false
	}

	if err := r.Client.Get(context.TODO(), req.NamespacedName, nodeMetric); err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("failed to find nodeMetric %v, error: %v", nodeMetricName, err)
			return ctrl.Result{Requeue: true}, err
		}
		nodeMetricExist = false
	}

	if !nodeExist && !nodeMetricExist {
		// return if neither Node nor NodeMetric exists.
		return ctrl.Result{}, nil
	} else if !nodeExist {
		// if !nodeExist && nodeMetricExist, delete NodeMetric.
		if err := r.Client.Delete(context.TODO(), nodeMetric); err != nil {
			klog.Errorf("failed to delete nodeMetric %v, error: %v", nodeMetricName, err)
			if errors.IsNotFound(err) {
				return ctrl.Result{Requeue: false}, err
			}
			return ctrl.Result{Requeue: true}, err
		}
		return ctrl.Result{}, nil
	} else if !nodeMetricExist {
		// if nodeExist && !nodeMetricExist, create an empty NodeMetric CR.
		if err := r.initNodeMetric(node, nodeMetric); err != nil {
			klog.Errorf("failed to init nodeMetric %v, error: %v", nodeMetricName, err)
			return ctrl.Result{Requeue: true}, err
		}
		if err := r.Client.Create(context.TODO(), nodeMetric); err != nil {
			klog.Errorf("failed to create nodeMetric %v, error: %v", nodeMetricName, err)
			return ctrl.Result{Requeue: true}, err
		}
	} else {
		// update nodeMetric spec if both exists
		nodeMetricSpec, err := r.getNodeMetricSpec(node, &nodeMetric.Spec)
		if err != nil {
			klog.Errorf("syncNodeMetric failed to get nodeMetric spec: %v", err)
			return reconcile.Result{Requeue: true}, err
		}
		if !reflect.DeepEqual(nodeMetricSpec, &nodeMetric.Spec) {
			nodeMetric.Spec = *nodeMetricSpec
			err = r.Client.Update(context.TODO(), nodeMetric)
			if err != nil {
				klog.Errorf("syncNodeMetric failed to update nodeMetric %v, error: %v", nodeMetricName, err)
				return reconcile.Result{Requeue: true}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *NodeMetricReconciler) getNodeMetricSpec(node *corev1.Node, oldSpec *slov1alpha1.NodeMetricSpec) (*slov1alpha1.NodeMetricSpec, error) {
	// get cr's spec from the configmap
	// if the configmap does not exist, use the default
	if r.Client == nil {
		klog.Errorf("getNodeMetricSpec failed to load configmap %s/%s",
			config.ConfigNameSpace, config.SLOCtrlConfigMap)
		return nil, fmt.Errorf("no available client")
	}

	var nodeMetricSpec *slov1alpha1.NodeMetricSpec
	if oldSpec != nil {
		nodeMetricSpec = oldSpec.DeepCopy()
	} else {
		defaultColocationCfg := config.NewDefaultColocationCfg()
		nodeMetricSpec = &slov1alpha1.NodeMetricSpec{
			CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
				AggregateDurationSeconds: defaultColocationCfg.MetricAggregateDurationSeconds,
				ReportIntervalSeconds:    defaultColocationCfg.MetricReportIntervalSeconds,
			},
		}
	}

	// TODO: record an event about the failure reason on configmap/crd when failed to load the config
	configMap := &corev1.ConfigMap{}
	keyTypes := types.NamespacedName{Namespace: config.ConfigNameSpace, Name: config.SLOCtrlConfigMap}
	if err := r.Client.Get(context.TODO(), keyTypes, configMap); err != nil {
		// default when the configmap does not exist
		if errors.IsNotFound(err) {
			klog.Infof("getNodeMetricSpec(): config map %s/%s not exist, err:%s", config.ConfigNameSpace,
				config.SLOCtrlConfigMap, err)
			return nodeMetricSpec, nil
		}
		// abort spec update if cannot get configmap
		klog.Errorf("getNodeMetricSpec(): failed to load config map %s/%s, err:%s", config.ConfigNameSpace,
			config.SLOCtrlConfigMap, err)
		return nil, err
	}

	nodeMetricCollectPolicy, err := getNodeMetricCollectPolicy(node, &r.config)
	if err != nil {
		klog.Warningf("getNodeMetricSpec(): failed to get nodeMetricCollectPolicy for node %s, set the default error: %v", node.Name, err)
	} else {
		nodeMetricSpec.CollectPolicy = nodeMetricCollectPolicy
	}

	return nodeMetricSpec, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeMetricReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&slov1alpha1.NodeMetric{}).
		Watches(&source.Kind{Type: &corev1.Node{}}, &EnqueueRequestForNode{}).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, &noderesource.EnqueueRequestForConfigMap{
			Config: &r.config,
			Client: r.Client,
		}).
		Complete(r)
}

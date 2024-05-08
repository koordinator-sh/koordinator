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
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

const Name = "nodemetric"

// NodeMetricReconciler reconciles a NodeMetric object
type NodeMetricReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	cfgCache config.ColocationCfgCache
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=nodemetrics,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=nodemetrics/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NodeMetricReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx, "node-metric-reconciler", req.NamespacedName)

	// if cache unavailable, requeue the req
	if !r.cfgCache.IsCfgAvailable() {
		// all nodes would be enqueued once the config is available, so here we just drop the req
		klog.Warningf("colocation config is not available, drop the req %v until a valid config is set",
			req.NamespacedName)
		return ctrl.Result{}, nil
	}

	node, nodeMetric := &corev1.Node{}, &slov1alpha1.NodeMetric{}
	nodeExist, nodeMetricExist := true, true
	nodeName, nodeMetricName := req.Name, req.Name

	if err := r.Client.Get(context.TODO(), req.NamespacedName, node); err != nil {
		if !errors.IsNotFound(err) {
			metrics.RecordNodeMetricReconcileCount(false, "reconcileNodeGetError")
			klog.Errorf("failed to find node %v, error: %v", nodeName, err)
			return ctrl.Result{Requeue: true}, err
		}
		nodeExist = false
	}

	if err := r.Client.Get(context.TODO(), req.NamespacedName, nodeMetric); err != nil {
		if !errors.IsNotFound(err) {
			metrics.RecordNodeMetricReconcileCount(false, "reconcileNodeMetricGetError")
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
				return ctrl.Result{}, nil
			}
			metrics.RecordNodeMetricReconcileCount(false, "deleteNodeMetric")
			return ctrl.Result{Requeue: true}, err
		}
		metrics.RecordNodeMetricReconcileCount(true, "deleteNodeMetric")
		return ctrl.Result{}, nil
	} else if !nodeMetricExist {
		// if nodeExist && !nodeMetricExist, create an empty NodeMetric CR.
		if err := r.initNodeMetric(node, nodeMetric); err != nil {
			klog.Errorf("failed to init nodeMetric %v, error: %v", nodeMetricName, err)
			return ctrl.Result{Requeue: true}, err
		}
		if err := r.Client.Create(context.TODO(), nodeMetric); err != nil {
			metrics.RecordNodeMetricReconcileCount(false, "createNodeMetric")
			klog.Errorf("failed to create nodeMetric %v, error: %v", nodeMetricName, err)
			return ctrl.Result{Requeue: true}, err
		}
		metrics.RecordNodeMetricReconcileCount(true, "createNodeMetric")
	} else {
		// update nodeMetric spec if both exists
		nodeMetricSpec, err := r.getNodeMetricSpec(node, &nodeMetric.Spec)
		if err != nil {
			klog.Errorf("failed to get nodeMetric spec of %v: %v", nodeMetricName, err)
			return ctrl.Result{Requeue: true}, err
		}
		if !reflect.DeepEqual(nodeMetricSpec, &nodeMetric.Spec) {
			nodeMetric.Spec = *nodeMetricSpec
			err = r.Client.Update(context.TODO(), nodeMetric)
			if err != nil {
				metrics.RecordNodeMetricReconcileCount(false, "updateNodeMetric")
				klog.Errorf("failed to update nodeMetric %v, error: %v", nodeMetricName, err)
				return ctrl.Result{Requeue: true}, err
			}
			metrics.RecordNodeMetricReconcileCount(true, "updateNodeMetric")
		}
	}

	klog.V(6).Infof("nodemetric-controller succeeded to update nodeMetric %v", nodeMetricName)
	return ctrl.Result{}, nil
}

func (r *NodeMetricReconciler) getNodeMetricSpec(node *corev1.Node, oldSpec *slov1alpha1.NodeMetricSpec) (*slov1alpha1.NodeMetricSpec, error) {
	if node == nil {
		klog.Errorf("getNodeMetricSpec failed to get spec for nil node")
		return nil, fmt.Errorf("invalid node input")
	}

	// if cfg cache error status(like unmarshall error),then use oldSpec
	if oldSpec != nil && r.cfgCache.IsErrorStatus() {
		return oldSpec, nil
	}

	nodeMetricSpec := getDefaultSpec()

	cfg := r.cfgCache.GetCfgCopy()
	mergedStrategy := sloconfig.GetNodeColocationStrategy(cfg, node)

	nodeMetricCollectPolicy, err := getNodeMetricCollectPolicy(mergedStrategy)
	if err != nil {
		metrics.RecordNodeMetricSpecParseCount(false, "getNodeMetricCollectPolicy")
		klog.Warningf("getNodeMetricSpec(): failed to get nodeMetricCollectPolicy for node %s, set the default error: %v", node.Name, err)
	} else {
		metrics.RecordNodeMetricSpecParseCount(true, "getNodeMetricCollectPolicy")
		nodeMetricSpec.CollectPolicy = nodeMetricCollectPolicy
	}

	return nodeMetricSpec, nil
}

func getDefaultSpec() *slov1alpha1.NodeMetricSpec {
	defaultColocationCfg := sloconfig.NewDefaultColocationCfg()
	return &slov1alpha1.NodeMetricSpec{
		CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
			AggregateDurationSeconds: defaultColocationCfg.MetricAggregateDurationSeconds,
			ReportIntervalSeconds:    defaultColocationCfg.MetricReportIntervalSeconds,
			NodeAggregatePolicy:      defaultColocationCfg.MetricAggregatePolicy,
			NodeMemoryCollectPolicy:  defaultColocationCfg.MetricMemoryCollectPolicy,
		},
	}
}

func Add(mgr ctrl.Manager) error {
	reconciler := &NodeMetricReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("nodemetric-controller"),
	}
	return reconciler.SetupWithManager(mgr)
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeMetricReconciler) SetupWithManager(mgr ctrl.Manager) error {
	handler := config.NewColocationHandlerForConfigMapEvent(r.Client, *sloconfig.NewDefaultColocationCfg(), r.Recorder)
	r.cfgCache = handler
	return ctrl.NewControllerManagedBy(mgr).
		For(&slov1alpha1.NodeMetric{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Node{}, &EnqueueRequestForNode{}).
		Watches(&corev1.ConfigMap{}, handler).
		Named(Name).
		Complete(r)
}

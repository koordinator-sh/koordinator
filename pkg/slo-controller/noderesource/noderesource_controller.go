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
	"flag"
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/source"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

const (
	Name = "noderesource"

	disableInConfig string = "DisableInConfig"
)

var (
	NodeResourcePlugins []string
)

type NodeResourceReconciler struct {
	Client          client.Client
	Recorder        record.EventRecorder
	Scheme          *runtime.Scheme
	Clock           clock.Clock
	NodeSyncContext *framework.SyncContext
	GPUSyncContext  *framework.SyncContext
	cfgCache        config.ColocationCfgCache
}

// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=scheduling.koordinator.sh,resources=devices,verbs=get;list;watch
// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=nodemetrics,verbs=get;list;watch

func (r *NodeResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.cfgCache.IsCfgAvailable() {
		klog.InfoS("colocation config is not available")
		return ctrl.Result{}, nil
	}

	node := &corev1.Node{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			// skip non-existing node and return no error to forget the request
			klog.V(3).InfoS("skip for node not found", "node", req.Name)
			return ctrl.Result{}, nil
		}
		klog.ErrorS(err, "failed to get node", "node", req.Name)
		return ctrl.Result{Requeue: true}, err
	}

	if r.isColocationCfgDisabled(node) { // disable all resources
		klog.InfoS("node colocation is disabled, reset node resources", "node", req.Name)
		if err := r.resetNodeResource(node, "node colocation is disabled in Config, reason: "+disableInConfig); err != nil {
			return ctrl.Result{Requeue: true}, err
		}
		return ctrl.Result{}, nil
	}

	nodeMetric := &slov1alpha1.NodeMetric{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, nodeMetric); err != nil {
		if !errors.IsNotFound(err) {
			klog.ErrorS(err, "failed to get nodeMetric", "node", req.Name)
			return ctrl.Result{Requeue: true}, err
		}
		// the node metric might be not exist or abnormal, resource calculation should handle this case
		klog.V(4).InfoS("calculate node resource while nodeMetric is not found", "node", req.Name)
	}

	podList := &corev1.PodList{}
	if err := r.Client.List(context.TODO(), podList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", node.Name),
	}); err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	// calculate node resources
	nr := r.calculateNodeResource(node, nodeMetric, podList)

	// update node status
	if err := r.updateNodeResource(node, nr); err != nil {
		klog.ErrorS(err, "failed to update node resource for node", "node", node.Name)
		return ctrl.Result{Requeue: true}, err
	}

	// do other node updates. e.g. update device resources
	if err := r.updateNodeExtensions(node, nodeMetric, podList); err != nil {
		klog.ErrorS(err, "failed to update node extensions for node", "node", node.Name)
		return ctrl.Result{Requeue: true}, err
	}

	klog.V(6).InfoS("noderesource-controller update node successfully", "node", node.Name)
	return ctrl.Result{}, nil
}

func InitFlags(fs *flag.FlagSet) {
	pflag.StringSliceVar(&NodeResourcePlugins, "noderesourceplugins", NodeResourcePlugins, fmt.Sprintf("A list of noderesource plugins to enable. "+
		"'-noderesourceplugins=*' enables all plugins. "+
		"'-noderesourceplugins=BatchResource' means only the 'BatchResource' plugin is enabled. "+
		"'-noderesourceplugins=*,-BatchResource' means all plugins except the 'BatchResource' plugin are enabled.\n"+
		"All plugins: %s", strings.Join(NodeResourcePlugins, ", ")))
}

func isPluginEnabled(pluginName string) bool {
	hasStar := false
	for _, p := range NodeResourcePlugins {
		if p == Name {
			return true
		}
		if p == "-"+Name {
			return false
		}
		if p == "*" {
			hasStar = true
		}
	}
	return hasStar
}

func Add(mgr ctrl.Manager) error {
	// init plugins for NodeResource
	addPlugins(isPluginEnabled)

	reconciler := &NodeResourceReconciler{
		Recorder:        mgr.GetEventRecorderFor("noderesource-controller"),
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		NodeSyncContext: framework.NewSyncContext(),
		GPUSyncContext:  framework.NewSyncContext(),
		Clock:           clock.RealClock{},
	}
	return reconciler.SetupWithManager(mgr)
}

func (r *NodeResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	handler := config.NewColocationHandlerForConfigMapEvent(r.Client, *sloconfig.NewDefaultColocationCfg(), r.Recorder)
	r.cfgCache = handler
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Watches(&source.Kind{Type: &slov1alpha1.NodeMetric{}}, &EnqueueRequestForNodeMetric{syncContext: r.NodeSyncContext}).
		Watches(&source.Kind{Type: &schedulingv1alpha1.Device{}}, &EnqueueRequestForDevice{syncContext: r.GPUSyncContext}).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, handler).
		Named(Name). // avoid conflict with others reconciling `Node`
		Complete(r)
}

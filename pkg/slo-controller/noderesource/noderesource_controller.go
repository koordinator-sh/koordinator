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
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

const (
	Name = "noderesource"

	disableInConfig string = "DisableInConfig"
)

var (
	NodeResourcePlugins []string
	AllPlugins          []string
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
			metrics.RecordNodeResourceReconcileCount(false, "reconcileNodeNotFound")
			klog.V(3).InfoS("skip for node not found", "node", req.Name)
			return ctrl.Result{}, nil
		}
		metrics.RecordNodeResourceReconcileCount(false, "reconcileNodeGetError")
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

	// do other node updates.
	if err := r.updateNodeExtensions(node, nodeMetric, podList); err != nil {
		klog.ErrorS(err, "failed to update node extensions for node", "node", node.Name)
		return ctrl.Result{Requeue: true}, err
	}

	klog.V(6).InfoS("noderesource-controller update node successfully", "node", node.Name)
	return ctrl.Result{}, nil
}

func InitFlags(fs *flag.FlagSet) {
	pflag.StringSliceVar(&NodeResourcePlugins, "noderesource-plugins", NodeResourcePlugins, fmt.Sprintf("A list of noderesource plugins to enable. "+
		"'-noderesource-plugins=*' enables all plugins. "+
		"'-noderesource-plugins=BatchResource' means only the 'BatchResource' plugin is enabled. "+
		"'-noderesource-plugins=*,-BatchResource' means all plugins except the 'BatchResource' plugin are enabled.\n"+
		"All plugins: %s", strings.Join(AllPlugins, ", ")))
}

func addPluginOption(plugin framework.Plugin, enabled bool) {
	AllPlugins = append(AllPlugins, plugin.Name())
	if enabled {
		NodeResourcePlugins = append(NodeResourcePlugins, plugin.Name())
	}
}

func isPluginEnabled(pluginName string) bool {
	hasStar := false
	for _, p := range NodeResourcePlugins {
		if p == pluginName {
			return true
		}
		if p == "-"+pluginName {
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
	cfgHandler := config.NewColocationHandlerForConfigMapEvent(r.Client, *sloconfig.NewDefaultColocationCfg(), r.Recorder)
	r.cfgCache = cfgHandler

	builder := ctrl.NewControllerManagedBy(mgr).
		Named(Name). // avoid conflict with others reconciling `Node`
		For(&corev1.Node{}).
		Watches(&slov1alpha1.NodeMetric{}, &EnqueueRequestForNodeMetric{syncContext: r.NodeSyncContext}).
		Watches(&corev1.ConfigMap{}, cfgHandler)

	// setup plugins
	// allow plugins to mutate controller via the builder
	opt := framework.NewOption().WithManager(mgr).WithControllerBuilder(builder)
	framework.RunSetupExtenders(opt)

	return opt.CompleteController(r)
}

/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-04  @SONG
 **/
package nodewatermark

import (
	"context"
	"encoding/json"
	"time"

	apimachinerytypes "k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	gocache "github.com/patrickmn/go-cache"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const Name = "nodewatermark"

var (
	ReconcileByDefault     = false
	ReconcileInterval      = 30 * time.Second
	ForceUpdatePodDuration = 5 * time.Minute
	ExpirePodCacheDuration = 10 * time.Minute
	MaxUpdatePodQPS        = 10.0
	MaxUpdatePodQPSBurst   = 100
)

type Reconciler struct {
	client.Client
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme

	rateLimiter    *rate.Limiter
	podUpdateCache gocache.Cache
}

func newReconciler(mgr ctrl.Manager) *Reconciler {
	return &Reconciler{
		Client:         mgr.GetClient(),
		Recorder:       mgr.GetEventRecorderFor(Name),
		Scheme:         mgr.GetScheme(),
		rateLimiter:    rate.NewLimiter(rate.Limit(MaxUpdatePodQPS), MaxUpdatePodQPSBurst),
		podUpdateCache: *gocache.New(ForceUpdatePodDuration, ExpirePodCacheDuration),
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	nwm := &schedulingv1alpha1.NodeWatermark{}
	if err := r.Client.Get(ctx, req.NamespacedName, nwm); err != nil {
		if !errors.IsNotFound(err) {
			klog.ErrorS(err, "failed to get node water mark", "node-water-mark", req.Name)
			return ctrl.Result{Requeue: true}, err
		}
		return ctrl.Result{}, nil
	}

	// 		newTaints := newNode.Spec.Taints
	// 	newNodeClone := oldNode.DeepCopy()
	// 	newNodeClone.Spec.Taints = newTaints
	// 	newData, err := json.Marshal(newNodeClone)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to marshal new node %#v for node %q: %v", newNodeClone, nodeName, err)
	// 	}

	// 	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, v1.Node{})
	// 	if err != nil {
	// 		return fmt.Errorf("failed to create patch for node %q: %v", nodeName, err)
	// 	}

	// 	_, err = c.CoreV1().Nodes().Patch(context.TODO(), nodeName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
	// 	return err
	// }

	// TODO: get node resource
	node := &corev1.Node{}
	if err := r.Client.Get(ctx, req.NamespacedName, node); err != nil {
		klog.ErrorS(err, "failed to get node", "node-water-mark", req.Name)
		return ctrl.Result{Requeue: true}, err
	}
	if nwm.DeletionTimestamp != nil {

		//TODO: remove label and annotation from node
		return ctrl.Result{}, nil
	}
	oldData, err := json.Marshal(node)
	if err != nil {
		klog.Errorf("failed to marshal old node %#v for node %q: %v", oldData, node.Name, err)
		return ctrl.Result{Requeue: true}, nil
	}

	newNode := node.DeepCopy()
	newNode.Labels[extension.LabelNodeType] = nwm.Spec.Type
	newData, err := json.Marshal(newNode)
	if err != nil {
		klog.Errorf("failed to marshal new node %#v for node %q: %v", newNode, node.Name, err)
		return ctrl.Result{Requeue: true}, nil
	}
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, corev1.Node{})
	if err != nil {
		klog.Errorf("failed to create patch for node %q: %v", node.Name, err)
		return ctrl.Result{Requeue: true}, nil
	}

	patch := client.RawPatch(apimachinerytypes.MergePatchType, patchBytes)
	if err := r.Client.Patch(ctx, node, patch); err != nil {
		klog.ErrorS(err, "failed to patch node label", "node-water-mark", req.Name)
		return ctrl.Result{Requeue: true}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&schedulingv1alpha1.NodeWatermark{}).
		Named(Name).
		Complete(r)

}
func Add(mgr ctrl.Manager) error {
	klog.InfoS("Node water mark is enabled, add the controller")
	reconciler := newReconciler(mgr)
	return reconciler.SetupWithManager(mgr)
}

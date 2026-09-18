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

package containercgroup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

const Name = "containercgroup"

// Reconciler aggregates ContainerCgroupOverride CRs into NodeSLO.Spec.Extensions.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=containercgroupoverrides,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=containercgroupoverrides/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=slo.koordinator.sh,resources=nodeslos,verbs=get;list;watch;patch;update
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

func Add(mgr ctrl.Manager) error {
	r := &Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named(Name).
		For(&slov1alpha1.ContainerCgroupOverride{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.mapPodToOverrides)).
		Watches(&slov1alpha1.NodeSLO{}, handler.EnqueueRequestsFromMapFunc(r.mapNodeSLOToOverrides)).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	cr := &slov1alpha1.ContainerCgroupOverride{}
	err := r.Get(ctx, req.NamespacedName, cr)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.syncAllNodes(ctx)
		}
		return reconcile.Result{}, err
	}

	if cr.Spec.Resources.Empty() {
		cr.Status.Phase = extension.CgroupOverridePhaseFailed
		cr.Status.Message = "spec.resources is empty"
		_ = r.Status().Update(ctx, cr)
		return reconcile.Result{}, nil
	}

	if cr.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(cr, extension.FinalizerContainerCgroupOverrideWriteback) {
			controllerutil.AddFinalizer(cr, extension.FinalizerContainerCgroupOverrideWriteback)
			if err := r.Update(ctx, cr); err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
	} else {
		if cr.Status.Phase == extension.CgroupOverridePhaseReleased || !writebackEnabled(cr) {
			controllerutil.RemoveFinalizer(cr, extension.FinalizerContainerCgroupOverrideWriteback)
			if err := r.Update(ctx, cr); err != nil {
				return reconcile.Result{}, err
			}
			return r.syncAllNodes(ctx)
		}
	}

	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Spec.Target.PodName}
	if err := r.Get(ctx, podKey, pod); err != nil {
		if errors.IsNotFound(err) {
			cr.Status.Phase = extension.CgroupOverridePhaseFailed
			cr.Status.Message = "target pod not found"
			_ = r.Status().Update(ctx, cr)
			return r.syncAllNodes(ctx)
		}
		return reconcile.Result{}, err
	}
	if cr.Spec.Target.PodUID != "" && string(pod.UID) != cr.Spec.Target.PodUID {
		cr.Status.Phase = extension.CgroupOverridePhaseFailed
		cr.Status.Message = "pod UID mismatch"
		_ = r.Status().Update(ctx, cr)
		return r.syncAllNodes(ctx)
	}

	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		cr.Status.Phase = extension.CgroupOverridePhasePending
		cr.Status.Message = "pod not scheduled"
		_ = r.Status().Update(ctx, cr)
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if cr.Status.NodeName != nodeName || cr.Status.Phase == "" || cr.Status.Phase == extension.CgroupOverridePhasePending {
		cr.Status.NodeName = nodeName
		if cr.Status.Phase == "" || cr.Status.Phase == extension.CgroupOverridePhasePending {
			if cr.DeletionTimestamp.IsZero() {
				cr.Status.Phase = extension.CgroupOverridePhasePending
				cr.Status.Message = "waiting for koordlet apply"
			}
		}
		_ = r.Status().Update(ctx, cr)
	}

	return r.syncAllNodes(ctx)
}

func (r *Reconciler) syncAllNodes(ctx context.Context) (reconcile.Result, error) {
	list := &slov1alpha1.ContainerCgroupOverrideList{}
	if err := r.List(ctx, list); err != nil {
		return reconcile.Result{}, err
	}

	byNode := map[string][]extension.NodeCgroupOverrideItem{}
	missingNodeSLO := false
	for i := range list.Items {
		cr := &list.Items[i]
		item, nodeName, ok := r.toItem(ctx, cr)
		if !ok || nodeName == "" {
			continue
		}
		byNode[nodeName] = append(byNode[nodeName], item)
	}

	nodeSLOList := &slov1alpha1.NodeSLOList{}
	if err := r.List(ctx, nodeSLOList); err != nil {
		return reconcile.Result{}, err
	}
	for i := range nodeSLOList.Items {
		nodeSLO := &nodeSLOList.Items[i]
		items := byNode[nodeSLO.Name]
		if err := r.patchNodeSLO(ctx, nodeSLO, items); err != nil {
			klog.ErrorS(err, "patch NodeSLO extensions failed", "node", nodeSLO.Name)
			return reconcile.Result{}, err
		}
		delete(byNode, nodeSLO.Name)
	}
	for node := range byNode {
		missingNodeSLO = true
		klog.V(4).InfoS("NodeSLO missing for overrides; wait for nodeslo controller", "node", node)
	}
	if missingNodeSLO {
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return reconcile.Result{}, nil
}

func (r *Reconciler) toItem(ctx context.Context, cr *slov1alpha1.ContainerCgroupOverride) (extension.NodeCgroupOverrideItem, string, bool) {
	if cr.Spec.Resources.Empty() {
		return extension.NodeCgroupOverrideItem{}, "", false
	}
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Spec.Target.PodName}, pod); err != nil {
		return extension.NodeCgroupOverrideItem{}, "", false
	}
	if cr.Spec.Target.PodUID != "" && string(pod.UID) != cr.Spec.Target.PodUID {
		return extension.NodeCgroupOverrideItem{}, "", false
	}
	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		return extension.NodeCgroupOverrideItem{}, "", false
	}
	item := extension.NodeCgroupOverrideItem{
		Namespace:         cr.Namespace,
		Name:              cr.Name,
		PodNamespace:      cr.Namespace,
		PodName:           cr.Spec.Target.PodName,
		PodUID:            string(pod.UID),
		ContainerName:     cr.Spec.Target.ContainerName,
		Resources:         *cr.Spec.Resources.DeepCopy(),
		WritebackOnDelete: writebackEnabled(cr),
		PendingDelete:     !cr.DeletionTimestamp.IsZero(),
		Baseline:          cr.Status.Baseline.DeepCopy(),
		DesiredHash:       desiredHash(cr),
	}
	return item, nodeName, true
}

func (r *Reconciler) patchNodeSLO(ctx context.Context, nodeSLO *slov1alpha1.NodeSLO, items []extension.NodeCgroupOverrideItem) error {
	if nodeSLO.Spec.Extensions == nil {
		nodeSLO.Spec.Extensions = &slov1alpha1.ExtensionsMap{Object: map[string]interface{}{}}
	}
	if nodeSLO.Spec.Extensions.Object == nil {
		nodeSLO.Spec.Extensions.Object = map[string]interface{}{}
	}

	cur, _ := extension.ParseNodeCgroupOverrides(nodeSLO.Spec.Extensions.Object[extension.ExtensionContainerCgroupOverrides])
	same := len(cur.Items) == len(items)
	if same {
		m := map[string]extension.NodeCgroupOverrideItem{}
		for _, it := range cur.Items {
			m[it.Namespace+"/"+it.Name] = it
		}
		for _, it := range items {
			old, ok := m[it.Namespace+"/"+it.Name]
			if !ok || old.DesiredHash != it.DesiredHash || old.PendingDelete != it.PendingDelete ||
				!extension.ResourcesEqual(old.Baseline, it.Baseline) {
				same = false
				break
			}
		}
	}
	if same {
		return nil
	}

	base := nodeSLO.DeepCopy()
	if len(items) == 0 {
		delete(nodeSLO.Spec.Extensions.Object, extension.ExtensionContainerCgroupOverrides)
	} else {
		nodeSLO.Spec.Extensions.Object[extension.ExtensionContainerCgroupOverrides] = &extension.NodeCgroupOverrides{Items: items}
	}
	return r.Patch(ctx, nodeSLO, client.MergeFrom(base))
}

func (r *Reconciler) mapPodToOverrides(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	list := &slov1alpha1.ContainerCgroupOverrideList{}
	if err := r.List(ctx, list, client.InNamespace(pod.Namespace)); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		if list.Items[i].Spec.Target.PodName == pod.Name {
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: list.Items[i].Namespace,
					Name:      list.Items[i].Name,
				},
			})
		}
	}
	return reqs
}

func (r *Reconciler) mapNodeSLOToOverrides(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &slov1alpha1.ContainerCgroupOverrideList{}
	if err := r.List(ctx, list); err != nil || len(list.Items) == 0 {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: list.Items[0].Namespace,
			Name:      list.Items[0].Name,
		},
	}}
}

func writebackEnabled(cr *slov1alpha1.ContainerCgroupOverride) bool {
	if cr.Spec.WritebackOnDelete == nil {
		return true
	}
	return *cr.Spec.WritebackOnDelete
}

func desiredHash(cr *slov1alpha1.ContainerCgroupOverride) string {
	b, _ := json.Marshal(cr.Spec.Resources)
	s := fmt.Sprintf("%s|%s|%s|%s|%v",
		cr.Spec.Target.PodName, cr.Spec.Target.ContainerName, cr.Spec.Target.PodUID, string(b), writebackEnabled(cr))
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-02 @author yangwanjin
 */

package controller

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// workloadPatcher abstracts annotation patching for one workload kind.
// To add a new kind: implement this interface, add one entry to NewWorkloadPatcherRegistry.
type workloadPatcher interface {
	Patch(ctx context.Context, namespace, name string, annotations map[string]string) error
}

// WorkloadPatcherRegistry maps workload Kind → its patcher.
type WorkloadPatcherRegistry struct {
	patchers map[string]workloadPatcher
	client   kubernetes.Interface
}

// NewWorkloadPatcherRegistry returns the default registry for all supported kinds.
func NewWorkloadPatcherRegistry(client kubernetes.Interface) WorkloadPatcherRegistry {
	return WorkloadPatcherRegistry{
		client: client,
		patchers: map[string]workloadPatcher{
			"Deployment":  &deploymentPatcher{client},
			"StatefulSet": &statefulSetPatcher{client},
			"DaemonSet":   &daemonSetPatcher{client},
			"ReplicaSet":  &replicaSetPatcher{client},
			"Job":         &jobPatcher{client},
			"CronJob":     &cronJobPatcher{client},
		},
	}
}

// Get returns the patcher for the given workload kind, or (nil, false).
func (r WorkloadPatcherRegistry) Get(kind string) (workloadPatcher, bool) {
	p, ok := r.patchers[kind]
	return p, ok
}

// Client returns the shared kubernetes client.
func (r WorkloadPatcherRegistry) Client() kubernetes.Interface { return r.client }

// annotationPatch is the minimal JSON structure sent as a MergePatch body.
type annotationPatch struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

// mergePatch is the single shared implementation used by every patcher.
// patchFn should call the appropriate typed client Patch method.
func mergePatch(
	ctx context.Context,
	kind, ns, name string,
	annotations map[string]string,
	patchFn func(ctx context.Context, ns, name string, data []byte) error,
) error {
	var p annotationPatch
	p.Metadata.Annotations = annotations
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("build merge patch for %s %s/%s: %w", kind, ns, name, err)
	}
	if err := patchFn(ctx, ns, name, data); err != nil {
		return fmt.Errorf("patch %s %s/%s: %w", kind, ns, name, err)
	}
	klog.V(5).InfoS("Annotation patch applied", "kind", kind, "namespace", ns, "name", name)
	return nil
}

// ---------------------------------------------------------------------------
// Deployment
// ---------------------------------------------------------------------------

type deploymentPatcher struct{ client kubernetes.Interface }

func (p *deploymentPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	return mergePatch(ctx, "Deployment", ns, name, ann,
		func(ctx context.Context, ns, name string, data []byte) error {
			_, err := p.client.AppsV1().Deployments(ns).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
			return err
		})
}

// ---------------------------------------------------------------------------
// StatefulSet
// ---------------------------------------------------------------------------

type statefulSetPatcher struct{ client kubernetes.Interface }

func (p *statefulSetPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	return mergePatch(ctx, "StatefulSet", ns, name, ann,
		func(ctx context.Context, ns, name string, data []byte) error {
			_, err := p.client.AppsV1().StatefulSets(ns).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
			return err
		})
}

// ---------------------------------------------------------------------------
// DaemonSet
// ---------------------------------------------------------------------------

type daemonSetPatcher struct{ client kubernetes.Interface }

func (p *daemonSetPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	return mergePatch(ctx, "DaemonSet", ns, name, ann,
		func(ctx context.Context, ns, name string, data []byte) error {
			_, err := p.client.AppsV1().DaemonSets(ns).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
			return err
		})
}

// ---------------------------------------------------------------------------
// ReplicaSet
// ---------------------------------------------------------------------------

type replicaSetPatcher struct{ client kubernetes.Interface }

func (p *replicaSetPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	return mergePatch(ctx, "ReplicaSet", ns, name, ann,
		func(ctx context.Context, ns, name string, data []byte) error {
			_, err := p.client.AppsV1().ReplicaSets(ns).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
			return err
		})
}

// ---------------------------------------------------------------------------
// Job
// ---------------------------------------------------------------------------

type jobPatcher struct{ client kubernetes.Interface }

func (p *jobPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	return mergePatch(ctx, "Job", ns, name, ann,
		func(ctx context.Context, ns, name string, data []byte) error {
			_, err := p.client.BatchV1().Jobs(ns).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
			return err
		})
}

// ---------------------------------------------------------------------------
// CronJob
// ---------------------------------------------------------------------------

type cronJobPatcher struct{ client kubernetes.Interface }

func (p *cronJobPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	return mergePatch(ctx, "CronJob", ns, name, ann,
		func(ctx context.Context, ns, name string, data []byte) error {
			_, err := p.client.BatchV1().CronJobs(ns).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
			return err
		})
}

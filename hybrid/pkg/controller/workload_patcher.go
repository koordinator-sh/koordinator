/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-02 @author yangwanjin
 */

package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"hybrid/pkg/predictor"
)

// workloadPatcher abstracts the Get + merge + Update cycle for one workload kind.
// To add support for a new kind: implement this interface and add one entry to
// NewWorkloadPatcherRegistry. No other files change.
type workloadPatcher interface {
	Patch(ctx context.Context, namespace, name string, annotations map[string]string) error
}

// WorkloadPatcherRegistry maps workload Kind → its patcher.
// It also holds the kubernetes client so controllers can call Client()
// when they need the client for other purposes (e.g. pod owner resolution).
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

// needsAnnotationUpdate returns true when any non-timestamp annotation key differs.
func needsAnnotationUpdate(existing, desired map[string]string) bool {
	if existing == nil {
		return true
	}
	for k, v := range desired {
		if k == predictor.AnnotationTimestamp {
			continue // timestamp always changes; don't treat it as a diff signal
		}
		if existing[k] != v {
			return true
		}
	}
	return false
}

func mergeAnnotations(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

func patchErr(kind, ns, name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("patch %s %s/%s: %w", kind, ns, name, err)
}

// ---------------------------------------------------------------------------
// Deployment
// ---------------------------------------------------------------------------

type deploymentPatcher struct{ client kubernetes.Interface }

func (p *deploymentPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	obj, err := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !needsAnnotationUpdate(obj.Annotations, ann) {
		klog.V(5).InfoS("Deployment annotations up-to-date", "namespace", ns, "name", name)
		return nil
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	mergeAnnotations(obj.Annotations, ann)
	_, err = p.client.AppsV1().Deployments(ns).Update(ctx, obj, metav1.UpdateOptions{})
	return patchErr("Deployment", ns, name, err)
}

// ---------------------------------------------------------------------------
// StatefulSet
// ---------------------------------------------------------------------------

type statefulSetPatcher struct{ client kubernetes.Interface }

func (p *statefulSetPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	obj, err := p.client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !needsAnnotationUpdate(obj.Annotations, ann) {
		return nil
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	mergeAnnotations(obj.Annotations, ann)
	_, err = p.client.AppsV1().StatefulSets(ns).Update(ctx, obj, metav1.UpdateOptions{})
	return patchErr("StatefulSet", ns, name, err)
}

// ---------------------------------------------------------------------------
// DaemonSet
// ---------------------------------------------------------------------------

type daemonSetPatcher struct{ client kubernetes.Interface }

func (p *daemonSetPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	obj, err := p.client.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !needsAnnotationUpdate(obj.Annotations, ann) {
		return nil
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	mergeAnnotations(obj.Annotations, ann)
	_, err = p.client.AppsV1().DaemonSets(ns).Update(ctx, obj, metav1.UpdateOptions{})
	return patchErr("DaemonSet", ns, name, err)
}

// ---------------------------------------------------------------------------
// ReplicaSet
// ---------------------------------------------------------------------------

type replicaSetPatcher struct{ client kubernetes.Interface }

func (p *replicaSetPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	obj, err := p.client.AppsV1().ReplicaSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !needsAnnotationUpdate(obj.Annotations, ann) {
		return nil
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	mergeAnnotations(obj.Annotations, ann)
	_, err = p.client.AppsV1().ReplicaSets(ns).Update(ctx, obj, metav1.UpdateOptions{})
	return patchErr("ReplicaSet", ns, name, err)
}

// ---------------------------------------------------------------------------
// Job
// ---------------------------------------------------------------------------

type jobPatcher struct{ client kubernetes.Interface }

func (p *jobPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	obj, err := p.client.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !needsAnnotationUpdate(obj.Annotations, ann) {
		return nil
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	mergeAnnotations(obj.Annotations, ann)
	_, err = p.client.BatchV1().Jobs(ns).Update(ctx, obj, metav1.UpdateOptions{})
	return patchErr("Job", ns, name, err)
}

// ---------------------------------------------------------------------------
// CronJob
// ---------------------------------------------------------------------------

type cronJobPatcher struct{ client kubernetes.Interface }

func (p *cronJobPatcher) Patch(ctx context.Context, ns, name string, ann map[string]string) error {
	obj, err := p.client.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !needsAnnotationUpdate(obj.Annotations, ann) {
		return nil
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	mergeAnnotations(obj.Annotations, ann)
	_, err = p.client.BatchV1().CronJobs(ns).Update(ctx, obj, metav1.UpdateOptions{})
	return patchErr("CronJob", ns, name, err)
}

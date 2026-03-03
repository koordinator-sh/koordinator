/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-04 @author yangwanjin
 */

// Package utils provides shared Kubernetes utility helpers.
package utils

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// ControllerReference holds identity information for the workload that owns a Pod.
type ControllerReference struct {
	Name       string
	Kind       string
	APIVersion string
	UID        string
}

// GetControllerInfoForPod returns the top-level workload controller owning the named Pod.
// Strategy:
//  1. Follow OwnerReference chain (Pod → ReplicaSet → Deployment, etc.).
//  2. If no owner references exists, fall back to name-pattern heuristics.
func GetControllerInfoForPod(ctx context.Context, client kubernetes.Interface, namespace, podName string) (*ControllerReference, error) {
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s/%s: %w", namespace, podName, err)
	}

	if len(pod.OwnerReferences) > 0 {
		ref, err := resolveTopLevelOwner(ctx, client, namespace, pod.OwnerReferences)
		if err == nil && ref != nil {
			klog.V(5).InfoS("Resolved controller via owner references", "pod", podName, "kind", ref.Kind, "name", ref.Name)
			return ref, nil
		}
		klog.V(4).InfoS("Owner reference resolution failed, trying name inference", "pod", podName, "err", err)
	}

	klog.V(4).InfoS("No owner reference found, attempting name-based inference", "pod", podName)
	ref, err := inferControllerFromPodName(ctx, client, namespace, podName)
	if err == nil && ref != nil {
		klog.V(4).InfoS("Inferred controller from pod name", "pod", podName, "kind", ref.Kind, "name", ref.Name)
		return ref, nil
	}

	return nil, fmt.Errorf("could not determine controller for pod %s/%s", namespace, podName)
}

// resolveTopLevelOwner walks the OwnerReference chain and returns the root controller.
// Example: Pod → ReplicaSet → Deployment  returns Deployment.
func resolveTopLevelOwner(ctx context.Context, client kubernetes.Interface, namespace string, refs []metav1.OwnerReference) (*ControllerReference, error) {
	var owner *metav1.OwnerReference
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			owner = &refs[i]
			break
		}
	}
	if owner == nil {
		return nil, fmt.Errorf("no controlling OwnerReference found")
	}

	switch owner.Kind {
	case "ReplicaSet":
		rs, err := client.AppsV1().ReplicaSets(namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err == nil && len(rs.OwnerReferences) > 0 {
			if parent, err := resolveTopLevelOwner(ctx, client, namespace, rs.OwnerReferences); err == nil {
				return parent, nil
			}
		}
		return ownerRefToControllerRef(owner), nil

	case "Job":
		job, err := client.BatchV1().Jobs(namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err == nil && len(job.OwnerReferences) > 0 {
			if parent, err := resolveTopLevelOwner(ctx, client, namespace, job.OwnerReferences); err == nil {
				return parent, nil
			}
		}
		return ownerRefToControllerRef(owner), nil

	default:
		// Deployment, StatefulSet, DaemonSet, CronJob — already at the top.
		return ownerRefToControllerRef(owner), nil
	}
}

func ownerRefToControllerRef(ref *metav1.OwnerReference) *ControllerReference {
	if ref == nil {
		return nil
	}
	return &ControllerReference{
		Name:       ref.Name,
		Kind:       ref.Kind,
		APIVersion: ref.APIVersion,
		UID:        string(ref.UID),
	}
}

// ---------------------------------------------------------------------------
// Name-based heuristics (fallback when no OwnerReferences are present)
// ---------------------------------------------------------------------------

func inferControllerFromPodName(ctx context.Context, client kubernetes.Interface, namespace, podName string) (*ControllerReference, error) {
	if ref := tryStatefulSetPattern(ctx, client, namespace, podName); ref != nil {
		return ref, nil
	}
	if ref := tryDeploymentPattern(ctx, client, namespace, podName); ref != nil {
		return ref, nil
	}
	if ref := trySingleSuffixPattern(ctx, client, namespace, podName); ref != nil {
		return ref, nil
	}
	return nil, fmt.Errorf("could not infer controller from pod name %q", podName)
}

func tryStatefulSetPattern(ctx context.Context, client kubernetes.Interface, namespace, podName string) *ControllerReference {
	re := regexp.MustCompile(`^(.+)-(\d+)$`)
	m := re.FindStringSubmatch(podName)
	if len(m) != 3 {
		return nil
	}
	name := m[1]
	if _, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
		return nil
	}
	return &ControllerReference{Name: name, Kind: "StatefulSet"}
}

func tryDeploymentPattern(ctx context.Context, client kubernetes.Interface, namespace, podName string) *ControllerReference {
	parts := strings.Split(podName, "-")
	if len(parts) < 3 {
		return nil
	}
	last, secondLast := parts[len(parts)-1], parts[len(parts)-2]
	if !isLikelyHash(last) || !isLikelyHash(secondLast) {
		return nil
	}
	name := strings.Join(parts[:len(parts)-2], "-")
	if _, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
		return nil
	}
	return &ControllerReference{Name: name, Kind: "Deployment"}
}

func trySingleSuffixPattern(ctx context.Context, client kubernetes.Interface, namespace, podName string) *ControllerReference {
	parts := strings.Split(podName, "-")
	if len(parts) < 2 {
		return nil
	}
	if !isLikelyHash(parts[len(parts)-1]) {
		return nil
	}
	name := strings.Join(parts[:len(parts)-1], "-")

	checks := []struct {
		kind string
		fn   func(context.Context, kubernetes.Interface, string, string) error
	}{
		{"DaemonSet", checkDaemonSet},
		{"Job", checkJob},
		{"CronJob", checkCronJob},
	}
	for _, c := range checks {
		if c.fn(ctx, client, namespace, name) == nil {
			return &ControllerReference{Name: name, Kind: c.kind}
		}
	}
	return nil
}

func isLikelyHash(s string) bool {
	if len(s) < 5 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func checkDaemonSet(ctx context.Context, c kubernetes.Interface, ns, name string) error {
	_, err := c.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	return err
}
func checkJob(ctx context.Context, c kubernetes.Interface, ns, name string) error {
	_, err := c.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
	return err
}
func checkCronJob(ctx context.Context, c kubernetes.Interface, ns, name string) error {
	_, err := c.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	return err
}

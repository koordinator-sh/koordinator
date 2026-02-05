/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-04 @author yangwanjin
 */

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

// ControllerReference save ref msg
type ControllerReference struct {
	Name       string
	Kind       string
	APIVersion string
	UID        string
}

// GetControllerInfoForPod get pod controller msg
func GetControllerInfoForPod(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) (*ControllerReference, error) {
	// 1. get pod info
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod %s/%s: %w", namespace, podName, err)
	}

	// 2. check  owner references info
	if len(pod.OwnerReferences) > 0 {
		controller, err := getTopLevelController(ctx, clientset, namespace, pod.OwnerReferences)
		if err == nil && controller != nil {
			klog.V(4).Infof("Found controller via ownerReference for pod %s: %s/%s", podName, controller.Kind, controller.Name)
			return controller, nil
		}
		klog.V(4).Infof("Failed to resolve owner references for pod %s: %v", podName, err)
	}

	// 3. if  owner reference not exist, try parse by name
	klog.Infof("No owner reference found for pod %s, attempting name-based inference...", podName)
	controller, err := inferControllerFromPodName(ctx, clientset, namespace, podName)
	if err == nil && controller != nil {
		klog.Infof("Inferred controller for pod %s: %s/%s", podName, controller.Kind, controller.Name)
		return controller, nil
	}

	return nil, fmt.Errorf("could not determine controller for pod %s/%s", namespace, podName)
}

// getTopLevelController
// check flow is: Pod -> ReplicaSet -> Deployment, return Deployment
func getTopLevelController(ctx context.Context, clientset *kubernetes.Clientset, namespace string, ownerRefs []metav1.OwnerReference) (*ControllerReference, error) {
	var controllerRef *metav1.OwnerReference
	for i := range ownerRefs {
		if ownerRefs[i].Controller != nil && *ownerRefs[i].Controller {
			controllerRef = &ownerRefs[i]
			break
		}
	}

	if controllerRef == nil {
		return nil, fmt.Errorf("no controller owner found")
	}

	switch controllerRef.Kind {
	case "ReplicaSet":
		rs, err := clientset.AppsV1().ReplicaSets(namespace).Get(ctx, controllerRef.Name, metav1.GetOptions{})
		if err != nil {
			return convertOwnerRefToController(controllerRef), nil
		}

		// if ReplicaSet has owner maybe top controller is Deployment
		if len(rs.OwnerReferences) > 0 {
			parentController, err := getTopLevelController(ctx, clientset, namespace, rs.OwnerReferences)
			if err == nil {
				return parentController, nil // return top controller: Deployment
			}
		}

		// if top controller is none, return ReplicaSet
		return convertOwnerRefToController(controllerRef), nil

	case "Job":
		// Job maybe belong CronJob, find top controller
		job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, controllerRef.Name, metav1.GetOptions{})
		if err != nil {
			return convertOwnerRefToController(controllerRef), nil
		}

		if len(job.OwnerReferences) > 0 {
			parentController, err := getTopLevelController(ctx, clientset, namespace, job.OwnerReferences)
			if err == nil {
				return parentController, nil // return  CronJob
			}
		}

		return convertOwnerRefToController(controllerRef), nil
	case "Deployment", "StatefulSet", "DaemonSet", "CronJob":
		return convertOwnerRefToController(controllerRef), nil
	default:
		klog.V(4).Infof("Unknown controller kind: %s", controllerRef.Kind)
		return convertOwnerRefToController(controllerRef), nil
	}
}

// convertOwnerRefToController convert OwnerReference to ControllerReference
func convertOwnerRefToController(ownerRef *metav1.OwnerReference) *ControllerReference {
	if ownerRef == nil {
		return nil
	}
	return &ControllerReference{
		Name:       ownerRef.Name,
		Kind:       ownerRef.Kind,
		APIVersion: ownerRef.APIVersion,
		UID:        string(ownerRef.UID),
	}
}

// inferControllerFromPodName infer the controller through the Pod name
func inferControllerFromPodName(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) (*ControllerReference, error) {
	// Pod name format:
	// Deployment: <name>-<replicaset-hash>-<pod-hash>   (e.g.: nginx-7c8f9d5b6-xyz12)
	// StatefulSet: <name>-<ordinal>                     (e.g.: mysql-0)
	// DaemonSet: <name>-<node-hash>                     (e.g.: node-exporter-abc123)
	// Job: <name>-<random>                              (e.g.: backup-job-28394)

	// 1: try StatefulSet
	if controller := tryStatefulSetPattern(ctx, clientset, namespace, podName); controller != nil {
		return controller, nil
	}

	// 2: try Deployment
	if controller := tryDeploymentPattern(ctx, clientset, namespace, podName); controller != nil {
		return controller, nil
	}

	// 3: try DaemonSet/Job
	if controller := trySingleSuffixPattern(ctx, clientset, namespace, podName); controller != nil {
		return controller, nil
	}

	return nil, fmt.Errorf("could not infer controller from pod name: %s", podName)
}

func tryStatefulSetPattern(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) *ControllerReference {
	// regexp: end with a number
	re := regexp.MustCompile(`^(.+)-(\d+)$`)
	matches := re.FindStringSubmatch(podName)

	if len(matches) == 3 {
		controllerName := matches[1]

		// check StatefulSet
		_, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, controllerName, metav1.GetOptions{})
		if err == nil {
			klog.V(4).Infof("Matched StatefulSet pattern: %s -> %s", podName, controllerName)
			return &ControllerReference{
				Name: controllerName,
				Kind: "StatefulSet",
			}
		}
	}

	return nil
}

func tryDeploymentPattern(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) *ControllerReference {
	// name format e.g.: nginx-deployment-7c8f9d5b6-xyz12
	parts := strings.Split(podName, "-")

	if len(parts) >= 3 {
		controllerName := strings.Join(parts[:len(parts)-2], "-")

		lastPart := parts[len(parts)-1]
		secondLastPart := parts[len(parts)-2]

		if isLikelyHash(lastPart, 5, 10) && isLikelyHash(secondLastPart, 5, 10) {
			// check deployment exist
			_, err := clientset.AppsV1().Deployments(namespace).Get(ctx, controllerName, metav1.GetOptions{})
			if err == nil {
				klog.V(4).Infof("Matched Deployment pattern: %s -> %s", podName, controllerName)
				return &ControllerReference{
					Name: controllerName,
					Kind: "Deployment",
				}
			}
		}
	}

	return nil
}

// trySingleSuffixPattern handle name format : <name>-<suffix>
// e.g. DaemonSet, Job
func trySingleSuffixPattern(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) *ControllerReference {
	parts := strings.Split(podName, "-")
	if len(parts) >= 2 {
		lastPart := parts[len(parts)-1]

		if isLikelyHash(lastPart, 5, 10) {
			controllerName := strings.Join(parts[:len(parts)-1], "-")

			// try different controller types according to priority
			controllerTypes := []struct {
				kind      string
				checkFunc func(context.Context, *kubernetes.Clientset, string, string) error
			}{
				{"DaemonSet", checkDaemonSet},
				{"Job", checkJob},
				{"CronJob", checkCronJob},
			}

			for _, ct := range controllerTypes {
				if err := ct.checkFunc(ctx, clientset, namespace, controllerName); err == nil {
					klog.V(4).Infof("Matched %s pattern: %s -> %s", ct.kind, podName, controllerName)
					return &ControllerReference{
						Name: controllerName,
						Kind: ct.kind,
					}
				}
			}
		}
	}

	return nil
}

// isLikelyHash check pod name is likely hash
func isLikelyHash(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}

	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}

	return true
}

func checkDaemonSet(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) error {
	_, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	return err
}

func checkJob(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) error {
	_, err := clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	return err
}

func checkCronJob(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) error {
	_, err := clientset.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	return err
}

// GetControllerName only return controller name
func GetControllerName(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) (string, error) {
	controller, err := GetControllerInfoForPod(ctx, clientset, namespace, podName)
	if err != nil {
		return "", err
	}
	return controller.Name, nil
}

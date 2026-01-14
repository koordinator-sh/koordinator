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

package defragmentation

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
)

// SafetyChecker checks if pods can be safely migrated
type SafetyChecker struct {
	client kubernetes.Interface
	config *config.DefragmentationSafetyConfig
}

// NewSafetyChecker creates a new SafetyChecker
func NewSafetyChecker(
	client kubernetes.Interface,
	config *config.DefragmentationSafetyConfig,
) *SafetyChecker {
	return &SafetyChecker{
		client: client,
		config: config,
	}
}

// CanMigratePod checks if a pod can be safely migrated
func (s *SafetyChecker) CanMigratePod(ctx context.Context, pod *corev1.Pod) (bool, string) {
	// 1. Check namespace whitelist
	if !s.checkNamespaceWhitelist(pod) {
		return false, "namespace not in whitelist"
	}

	// 2. Check blacklist labels
	if s.hasBlacklistLabels(pod) {
		return false, "pod has blacklist labels"
	}

	// 3. Check blacklist annotations
	if s.hasBlacklistAnnotations(pod) {
		return false, "pod has blacklist annotations"
	}

	// 4. Check PodDisruptionBudget
	if s.config.RespectPDB {
		if canDisrupt, reason := s.checkPDB(ctx, pod); !canDisrupt {
			return false, fmt.Sprintf("PDB violation: %s", reason)
		}
	}

	// 5. Check graceful shutdown configuration
	if s.config.RequireGracefulShutdown {
		if !s.hasGracefulShutdown(pod) {
			return false, "no graceful shutdown configured"
		}
	}

	// 6. Check pod status
	if pod.Status.Phase != corev1.PodRunning {
		return false, fmt.Sprintf("pod not running: %s", pod.Status.Phase)
	}

	// 7. Check if DaemonSet pod
	if s.isDaemonSetPod(pod) {
		return false, "daemonset pods cannot be migrated"
	}

	// 8. Check if static pod
	if s.isStaticPod(pod) {
		return false, "static pods cannot be migrated"
	}

	// 9. Check for local storage
	if s.hasLocalStorage(pod) {
		klog.V(5).InfoS("Pod has local storage, migration may cause data loss",
			"pod", klog.KObj(pod))
		// Don't block, but log warning
	}

	return true, ""
}

// checkNamespaceWhitelist checks if pod namespace is in whitelist
func (s *SafetyChecker) checkNamespaceWhitelist(pod *corev1.Pod) bool {
	if len(s.config.WhitelistNamespaces) == 0 {
		return true // No whitelist configured, allow all namespaces
	}

	for _, ns := range s.config.WhitelistNamespaces {
		if pod.Namespace == ns {
			return true
		}
	}

	return false
}

// hasBlacklistLabels checks if pod has blacklist labels
func (s *SafetyChecker) hasBlacklistLabels(pod *corev1.Pod) bool {
	if len(s.config.BlacklistLabels) == 0 {
		return false
	}

	for key, value := range s.config.BlacklistLabels {
		if podValue, exists := pod.Labels[key]; exists {
			if value == "*" || podValue == value {
				return true
			}
		}
	}

	return false
}

// hasBlacklistAnnotations checks if pod has blacklist annotations
func (s *SafetyChecker) hasBlacklistAnnotations(pod *corev1.Pod) bool {
	if len(s.config.BlacklistAnnotations) == 0 {
		return false
	}

	for key, value := range s.config.BlacklistAnnotations {
		if podValue, exists := pod.Annotations[key]; exists {
			if value == "*" || podValue == value {
				return true
			}
		}
	}

	return false
}

// checkPDB checks PodDisruptionBudget
func (s *SafetyChecker) checkPDB(ctx context.Context, pod *corev1.Pod) (bool, string) {
	// Get all PDBs in the namespace
	var pdbList *policyv1.PodDisruptionBudgetList
	var err error
	pdbList, err = s.client.PolicyV1().PodDisruptionBudgets(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.ErrorS(err, "Failed to list PDBs", "namespace", pod.Namespace)
		return false, "failed to check PDB"
	}

	// Check if pod is protected by any PDB
	for i := range pdbList.Items {
		pdb := &pdbList.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			continue
		}

		if selector.Matches(labels.Set(pod.Labels)) {
			// Pod is protected by this PDB, check if disruption is allowed
			if pdb.Status.DisruptionsAllowed <= 0 {
				return false, fmt.Sprintf("PDB %s does not allow disruptions", pdb.Name)
			}

			klog.V(5).InfoS("Pod protected by PDB, but disruptions allowed",
				"pod", klog.KObj(pod),
				"pdb", pdb.Name,
				"disruptionsAllowed", pdb.Status.DisruptionsAllowed,
			)
		}
	}

	return true, ""
}

// hasGracefulShutdown checks if pod has graceful shutdown configured
func (s *SafetyChecker) hasGracefulShutdown(pod *corev1.Pod) bool {
	if pod.Spec.TerminationGracePeriodSeconds == nil {
		return false
	}

	gracePeriod := *pod.Spec.TerminationGracePeriodSeconds
	minGracePeriod := s.config.MinGracefulShutdownSeconds

	if minGracePeriod > 0 && gracePeriod < minGracePeriod {
		klog.V(5).InfoS("Pod grace period too short",
			"pod", klog.KObj(pod),
			"gracePeriod", gracePeriod,
			"minRequired", minGracePeriod,
		)
		return false
	}

	return gracePeriod > 0
}

// isDaemonSetPod checks if pod is a DaemonSet pod
func (s *SafetyChecker) isDaemonSetPod(pod *corev1.Pod) bool {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// isStaticPod checks if pod is a static pod
func (s *SafetyChecker) isStaticPod(pod *corev1.Pod) bool {
	_, isStatic := pod.Annotations[corev1.MirrorPodAnnotationKey]
	return isStatic
}

// hasLocalStorage checks if pod has local storage
func (s *SafetyChecker) hasLocalStorage(pod *corev1.Pod) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.HostPath != nil || volume.EmptyDir != nil {
			return true
		}
	}
	return false
}

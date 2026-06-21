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

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type FragmentationAwareArgs struct {
	metav1.TypeMeta `json:",inline"`

	// Paused indicates whether the FragmentationAware plugin is enabled.
	// Default is false.
	Paused *bool `json:"paused,omitempty"`

	// DryRun executes the descheduling logic without evicting Pods.
	// Default is false.
	DryRun *bool `json:"dryRun,omitempty"`

	// NodeSelector selects the nodes that match the labelSelector.
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`

	// EvictableNamespaces limits the namespaces of pods that can be evicted.
	EvictableNamespaces *Namespaces `json:"evictableNamespaces,omitempty"`

	// PodSelectors selects the pods that match the labelSelector.
	PodSelectors []FragmentationAwarePodSelector `json:"podSelectors,omitempty"`

	// NodeFit enables checking whether a candidate Pod can fit on at least one
	// other node before eviction, considering scheduling constraints such as node
	// affinity, taints/tolerations, and available resources.
	// Default is true.
	NodeFit *bool `json:"nodeFit,omitempty"`

	// Resources indicates the resource types used to calculate node fragmentation.
	// The default resources are CPU and memory.
	Resources []corev1.ResourceName `json:"resources,omitempty"`

	// ImbalanceThreshold specifies the minimum node resource imbalance score required
	// to consider a node for pod migration.
	// Default is 0.15.
	ImbalanceThreshold *float64 `json:"imbalanceThreshold,omitempty"`

	// MinImprovementThreshold specifies the minimum reduction in the node's imbalance
	// score (std_before - std_after) required to evict a Pod.
	// Default is 0.02.
	MinImprovementThreshold *float64 `json:"minImprovementThreshold,omitempty"`
}

type FragmentationAwarePodSelector struct {
	// Selector is a label query over pods for migration.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

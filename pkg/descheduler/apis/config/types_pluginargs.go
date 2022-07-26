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

package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DefaultEvictorArgs holds arguments used to configure the DefaultEvictor plugin.
type DefaultEvictorArgs struct {
	metav1.TypeMeta

	DryRun bool
	// MaxNoOfPodsToEvictPerNode restricts maximum of pods to be evicted per node.
	MaxNoOfPodsToEvictPerNode *int
	// MaxNoOfPodsToEvictPerNamespace restricts maximum of pods to be evicted per namespace.
	MaxNoOfPodsToEvictPerNamespace *int

	// EvictFailedBarePods allows pods without ownerReferences and in failed phase to be evicted.
	EvictFailedBarePods bool

	// EvictLocalStoragePods allows pods using local storage to be evicted.
	EvictLocalStoragePods bool

	// EvictSystemCriticalPods allows eviction of pods of any priority (including Kubernetes system pods)
	EvictSystemCriticalPods bool

	// IgnorePVCPods prevents pods with PVCs from being evicted.
	IgnorePvcPods bool

	// NodeFit sets whether to consider taints, node selectors,
	// and pod affinity when evicting. A pod whose tolerations, node selectors,
	// and affinity match a node other than the one it is currently running on
	// is evictable.
	NodeFit bool
	// PriorityThreshold represents a threshold for pod's priority class.
	// Any pod whose priority class is lower is evictable.
	PriorityThreshold *PriorityThreshold
	// LabelSelector sets whether to apply label filtering when evicting.
	// Any pod matching the label selector is considered evictable.
	LabelSelector *metav1.LabelSelector
}

type PriorityThreshold struct {
	Value *int32
	Name  string
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RemovePodsViolatingNodeAffinityArgs holds arguments used to configure the RemovePodsViolatingNodeAffinity plugin.
type RemovePodsViolatingNodeAffinityArgs struct {
	metav1.TypeMeta

	Namespaces       *Namespaces
	LabelSelector    *metav1.LabelSelector
	NodeAffinityType []string
}

// Namespaces carries a list of included/excluded namespaces
// for which a given strategy is applicable
type Namespaces struct {
	Include []string
	Exclude []string
}

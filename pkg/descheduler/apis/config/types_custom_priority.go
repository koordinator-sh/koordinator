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

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CustomPriorityArgs holds arguments used to configure the CustomPriority descheduling plugin.
type CustomPriorityArgs struct {
	metav1.TypeMeta

	// Paused indicates whether the CustomPriority should work or not.
	// Default is false
	Paused bool

	// DryRun means only execute the entire deschedule logic but don't migrate Pod
	// Default is false
	DryRun bool

	// EvictableNamespaces carries a list of included/excluded namespaces
	// for which the strategy is applicable
	EvictableNamespaces *Namespaces

	// NodeSelector selects the nodes that matched labelSelector
	NodeSelector *metav1.LabelSelector

	// PodSelectors selects the pods that matched labelSelector
	PodSelectors []CustomPriorityPodSelector

	// NodeFit if enabled, it will check whether the candidate Pods have suitable nodes,
	// including NodeAffinity, TaintTolerance, and whether resources are sufficient.
	// by default, NodeFit is set to true.
	NodeFit bool

	// EvictionOrder specifies the order of eviction priority for different resource types.
	// The order is from high priority (expensive) to low priority (cheap).
	// Pods on higher priority resources will be evicted to lower priority resources when possible.
	EvictionOrder []ResourcePriority

	// Mode controls the working mode of CustomPriority. Supported values:
	// - "BestEffort": evict any pod that can be rescheduled onto higher-priority nodes (default)
	// - "DrainNode": prefer draining whole nodes; only evict when all candidate pods on a source node can be placed
	Mode string

	// AutoCordon controls whether to cordon the source node automatically when using DrainNode mode.
	// Default is false.
	AutoCordon bool
}

const (
	CustomPriorityEvictModeBestEffort = "BestEffort"
	CustomPriorityEvictModeDrainNode  = "DrainNode"
)

// ResourcePriority defines the priority of a resource type for eviction.
type ResourcePriority struct {
	// Name represents the name of the resource priority level
	Name string

	// NodeSelector selects the nodes that belong to this priority level
	NodeSelector *metav1.LabelSelector

	// KeepThreshold specifies the minimum resource usage threshold to keep pods on this resource type.
	// If the resource usage is below this threshold, pods may be evicted to lower priority resources.
	KeepThreshold *ResourceThresholds

	// RejectThreshold specifies the maximum resource usage threshold to reject new pods on this resource type.
	// If the resource usage is above this threshold, pods may be evicted to lower priority resources.
	RejectThreshold *ResourceThresholds
}

// CustomPriorityPodSelector defines how to select pods for eviction
type CustomPriorityPodSelector struct {
	// Name represents the name of the pod selector
	Name string

	// Selector label query over pods for eviction
	Selector *metav1.LabelSelector
}

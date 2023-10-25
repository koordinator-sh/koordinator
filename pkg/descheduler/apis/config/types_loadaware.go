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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type LowNodeLoadArgs struct {
	metav1.TypeMeta

	// Paused indicates whether the LoadHotspot should to work or not.
	// Default is false
	Paused bool

	// DryRun means only execute the entire deschedule logic but don't migrate Pod
	// Default is false
	DryRun bool

	// NumberOfNodes can be configured to activate the strategy only when the number of under utilized nodes are above the configured value.
	// This could be helpful in large clusters where a few nodes could go under utilized frequently or for a short period of time.
	// By default, NumberOfNodes is set to zero.
	NumberOfNodes int32

	// NodeMetricExpirationSeconds indicates the NodeMetric expiration in seconds.
	// When NodeMetrics expired, the node is considered abnormal, and should not be considered by deschedule plugin.
	// Default is 180 seconds.
	NodeMetricExpirationSeconds *int64

	// Naming this one differently since namespaces are still
	// considered while considering resoures used by pods
	// but then filtered out before eviction
	EvictableNamespaces *Namespaces

	// NodeSelector selects the nodes that matched labelSelector
	NodeSelector *metav1.LabelSelector

	// PodSelectors selects the pods that matched labelSelector
	PodSelectors []LowNodeLoadPodSelector

	// NodeFit if enabled, it will check whether the candidate Pods have suitable nodes, including NodeAffinity, TaintTolerance, and whether resources are sufficient.
	// by default, NodeFit is set to true.
	NodeFit bool

	// If UseDeviationThresholds is set to `true`, the thresholds are considered as percentage deviations from mean resource usage.
	// `LowThresholds` will be deducted from the mean among all nodes and `HighThresholds` will be added to the mean.
	// A resource consumption above (resp. below) this window is considered as overutilization (resp. underutilization).
	UseDeviationThresholds bool

	// HighThresholds defines the target usage threshold of resources
	HighThresholds ResourceThresholds

	// LowThresholds defines the low usage threshold of resources
	LowThresholds ResourceThresholds

	// ResourceWeights indicates the weights of resources.
	// The weights of resources are both 1 by default.
	ResourceWeights map[corev1.ResourceName]int64

	// AnomalyCondition indicates the node load anomaly thresholds,
	// the default is 5 consecutive times exceeding HighThresholds,
	// it is determined that the node is abnormal, and the Pods need to be migrated to reduce the load.
	AnomalyCondition *LoadAnomalyCondition

	// NodePools supports multiple different types of batch nodes to configure different strategies
	NodePools []LowNodeLoadNodePool
}

type LowNodeLoadNodePool struct {
	// Name represents the name of pool
	Name string
	// NodeSelector selects the nodes that matched labelSelector
	NodeSelector *metav1.LabelSelector
	// If UseDeviationThresholds is set to `true`, the thresholds are considered as percentage deviations from mean resource usage.
	// `LowThresholds` will be deducted from the mean among all nodes and `HighThresholds` will be added to the mean.
	// A resource consumption above (resp. below) this window is considered as overutilization (resp. underutilization).
	UseDeviationThresholds bool

	// HighThresholds defines the target usage threshold of resources
	HighThresholds ResourceThresholds

	// LowThresholds defines the low usage threshold of resources
	LowThresholds ResourceThresholds

	// ResourceWeights indicates the weights of resources.
	// The weights of resources are both 1 by default.
	ResourceWeights map[corev1.ResourceName]int64

	// AnomalyCondition indicates the node load anomaly thresholds,
	// the default is 5 consecutive times exceeding HighThresholds,
	// it is determined that the node is abnormal, and the Pods need to be migrated to reduce the load.
	AnomalyCondition *LoadAnomalyCondition
}

type LowNodeLoadPodSelector struct {
	Name string

	// Selector label query over pods for migrated
	Selector *metav1.LabelSelector
}

type LoadAnomalyCondition struct {
	// Timeout indicates the expiration time of the abnormal state, the default is 1 minute
	Timeout metav1.Duration
	// ConsecutiveAbnormalities indicates the number of consecutive abnormalities
	ConsecutiveAbnormalities uint32
	// ConsecutiveNormalities indicates the number of consecutive normalities
	ConsecutiveNormalities uint32
}

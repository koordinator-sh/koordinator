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

package v1beta2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LoadAwareSchedulingArgs holds arguments used to configure the LoadAwareScheduling plugin.
type LoadAwareSchedulingArgs struct {
	metav1.TypeMeta `json:",inline"`

	// FilterExpiredNodeMetrics indicates whether to filter nodes where koordlet fails to update NodeMetric.
	FilterExpiredNodeMetrics *bool `json:"filterExpiredNodeMetrics,omitempty"`
	// NodeMetricExpirationSeconds indicates the NodeMetric expiration in seconds.
	// When NodeMetrics expired, the node is considered abnormal.
	// Default is 180 seconds.
	NodeMetricExpirationSeconds *int64 `json:"nodeMetricExpirationSeconds,omitempty"`
	// ResourceWeights indicates the weights of resources.
	// The weights of CPU and Memory are both 1 by default.
	ResourceWeights map[corev1.ResourceName]int64 `json:"resourceWeights,omitempty"`
	// UsageThresholds indicates the resource utilization threshold.
	// The default for CPU is 65%, and the default for memory is 95%.
	UsageThresholds map[corev1.ResourceName]int64 `json:"usageThresholds,omitempty"`
	// EstimatedScalingFactors indicates the factor when estimating resource usage.
	// The default value of CPU is 85%, and the default value of Memory is 70%.
	EstimatedScalingFactors map[corev1.ResourceName]int64 `json:"estimatedScalingFactors,omitempty"`
}

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

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LoadAwareSchedulingArgs holds arguments used to configure the LoadAwareScheduling plugin.
type LoadAwareSchedulingArgs struct {
	metav1.TypeMeta

	FilterUnhealthyNodeMetrics       bool                          `json:"filterUnhealthyNodeMetrics,omitempty"`
	NodeMetricUpdateMaxWindowSeconds int64                         `json:"nodeMetricUpdateMaxWindowSeconds,omitempty"`
	ResourceWeights                  map[corev1.ResourceName]int64 `json:"resourceWeights,omitempty"`
	UsageThresholds                  map[corev1.ResourceName]int64 `json:"usageThresholds,omitempty"`
	EstimatedScalingFactors          map[corev1.ResourceName]int64 `json:"estimatedScalingFactors,omitempty"`
}

func DefaultLoadAwareSchedulingArgs() *LoadAwareSchedulingArgs {
	return &LoadAwareSchedulingArgs{
		FilterUnhealthyNodeMetrics:       true,
		NodeMetricUpdateMaxWindowSeconds: 180, // 3 minutes
		ResourceWeights: map[corev1.ResourceName]int64{
			corev1.ResourceCPU:    1,
			corev1.ResourceMemory: 1,
		},
		UsageThresholds: map[corev1.ResourceName]int64{
			corev1.ResourceCPU:    65, // 65%
			corev1.ResourceMemory: 95, // 95%
		},
		EstimatedScalingFactors: map[corev1.ResourceName]int64{
			corev1.ResourceCPU:    85, // 85%
			corev1.ResourceMemory: 70, // 70%
		},
	}
}

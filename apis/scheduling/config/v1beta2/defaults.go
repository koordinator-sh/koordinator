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
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/utils/pointer"
)

var (
	defaultNodeMetricExpirationSeconds int64 = 180

	defaultResourceWeights = map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    1,
		corev1.ResourceMemory: 1,
	}

	defaultUsageThresholds = map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    65, // 65%
		corev1.ResourceMemory: 95, // 95%
	}

	defaultEstimatedScalingFactors = map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    85, // 85%
		corev1.ResourceMemory: 70, // 70%
	}

	defaultPreferredCPUBindPolicy          = CPUBindPolicyFullPCPUs
	defaultNUMAAllocateStrategy            = NUMAMostAllocated
	defaultNodeNUMAResourceScoringStrategy = &ScoringStrategy{
		Type: MostAllocated,
		Resources: []schedconfig.ResourceSpec{
			{
				Name:   string(corev1.ResourceCPU),
				Weight: 1,
			},
		},
	}
)

// SetDefaults_LoadAwareSchedulingArgs sets the default parameters for LoadAwareScheduling plugin.
func SetDefaults_LoadAwareSchedulingArgs(obj *LoadAwareSchedulingArgs) {
	if obj.FilterExpiredNodeMetrics == nil {
		obj.FilterExpiredNodeMetrics = pointer.Bool(true)
	}
	if obj.NodeMetricExpirationSeconds == nil {
		obj.NodeMetricExpirationSeconds = pointer.Int64Ptr(defaultNodeMetricExpirationSeconds)
	}
	if len(obj.ResourceWeights) == 0 {
		obj.ResourceWeights = defaultResourceWeights
	}
	if len(obj.UsageThresholds) == 0 {
		obj.UsageThresholds = defaultUsageThresholds
	}
	if len(obj.EstimatedScalingFactors) == 0 {
		obj.EstimatedScalingFactors = defaultEstimatedScalingFactors
	}
}

// SetDefaults_NodeNUMAResourceArgs sets the default parameters for NodeNUMANodeResource plugin.
func SetDefaults_NodeNUMAResourceArgs(obj *NodeNUMAResourceArgs) {
	if obj.DefaultCPUBindPolicy == "" {
		obj.DefaultCPUBindPolicy = defaultPreferredCPUBindPolicy
	}
	if obj.NUMAAllocateStrategy == "" {
		obj.NUMAAllocateStrategy = defaultNUMAAllocateStrategy
	}
	if obj.ScoringStrategy == nil {
		obj.ScoringStrategy = defaultNodeNUMAResourceScoringStrategy
	}
}

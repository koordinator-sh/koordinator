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

package extension

import (
	"encoding/json"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// AnnotationCustomUsageThresholds represents the user-defined resource utilization threshold.
	// For specific value definitions, see CustomUsageThresholds
	AnnotationCustomUsageThresholds = SchedulingDomainPrefix + "/usage-thresholds"
	// AnnotationCustomEstimatedScalingFactors represents the user-defined factor when estimating resource usage.
	AnnotationCustomEstimatedScalingFactors = SchedulingDomainPrefix + "/load-estimated-scaling-factors"
	// AnnotationCustomEstimatedSecondsAfterPodScheduled represents the user-defined
	// force estimation seconds after pod scheduled.
	AnnotationCustomEstimatedSecondsAfterPodScheduled = SchedulingDomainPrefix + "/load-estimated-seconds-after-pod-scheduled"
	// AnnotationCustomEstimatedSecondsAfterInitialized represents the user-defined
	// force estimation seconds after initialized.
	AnnotationCustomEstimatedSecondsAfterInitialized = SchedulingDomainPrefix + "/load-estimated-seconds-after-initialized"
)

// CustomUsageThresholds supports user-defined node resource utilization thresholds.
type CustomUsageThresholds struct {
	// UsageThresholds indicates the resource utilization threshold of the whole machine.
	UsageThresholds map[corev1.ResourceName]int64 `json:"usageThresholds,omitempty"`
	// ProdUsageThresholds indicates the resource utilization threshold of Prod Pods compared to the whole machine
	ProdUsageThresholds map[corev1.ResourceName]int64 `json:"prodUsageThresholds,omitempty"`
	// AggregatedUsage supports resource utilization filtering and scoring based on percentile statistics
	AggregatedUsage *CustomAggregatedUsage `json:"aggregatedUsage,omitempty"`
}

type CustomAggregatedUsage struct {
	// UsageThresholds indicates the resource utilization threshold of the machine based on percentile statistics
	UsageThresholds map[corev1.ResourceName]int64 `json:"usageThresholds,omitempty"`
	// UsageAggregationType indicates the percentile type of the machine's utilization when filtering
	UsageAggregationType AggregationType `json:"usageAggregationType,omitempty"`
	// UsageAggregatedDuration indicates the statistical period of the percentile of the machine's utilization when filtering
	UsageAggregatedDuration *metav1.Duration `json:"usageAggregatedDuration,omitempty"`
}

func GetCustomUsageThresholds(node *corev1.Node) (*CustomUsageThresholds, error) {
	usageThresholds := &CustomUsageThresholds{}
	data, ok := node.Annotations[AnnotationCustomUsageThresholds]
	if !ok {
		return usageThresholds, nil
	}
	err := json.Unmarshal([]byte(data), usageThresholds)
	if err != nil {
		return nil, err
	}
	return usageThresholds, nil
}

// also returns nil if unmarshal error
func GetCustomEstimatedScalingFactors(pod *corev1.Pod) map[corev1.ResourceName]int64 {
	if s := pod.Annotations[AnnotationCustomEstimatedScalingFactors]; s != "" {
		factors := make(map[corev1.ResourceName]int64)
		if err := json.Unmarshal([]byte(s), &factors); err == nil {
			return factors
		}
	}
	return nil
}

func GetCustomEstimatedSecondsAfterPodScheduled(pod *corev1.Pod) int64 {
	if s := pod.Annotations[AnnotationCustomEstimatedSecondsAfterPodScheduled]; s != "" {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	}
	return -1
}

func GetCustomEstimatedSecondsAfterInitialized(pod *corev1.Pod) int64 {
	if s := pod.Annotations[AnnotationCustomEstimatedSecondsAfterInitialized]; s != "" {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	}
	return -1
}

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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	// AnnotationCustomUsageThresholds represents the user-defined resource utilization threshold.
	// For specific value definitions, see CustomUsageThresholds
	AnnotationCustomUsageThresholds = SchedulingDomainPrefix + "/usage-thresholds"

	// AnnotationDeviceAllocated represents the device allocated by the pod
	AnnotationDeviceAllocated = SchedulingDomainPrefix + "/device-allocated"
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

// DeviceAllocations would be injected into Pod as form of annotation during Pre-bind stage.
/*
{
  "gpu": [
    {
      "minor": 0,
      "resources": {
        "koordinator.sh/gpu-core": 100,
        "koordinator.sh/gpu-mem-ratio": 100,
        "koordinator.sh/gpu-mem": "16Gi"
      }
    },
    {
      "minor": 1,
      "resources": {
        "koordinator.sh/gpu-core": 100,
        "koordinator.sh/gpu-mem-ratio": 100,
        "koordinator.sh/gpu-mem": "16Gi"
      }
    }
  ]
}
*/
type DeviceAllocations map[schedulingv1alpha1.DeviceType][]*DeviceAllocation

type DeviceAllocation struct {
	Minor     int32               `json:"minor"`
	Resources corev1.ResourceList `json:"resources"`
	Extension json.RawMessage     `json:"extension,omitempty"`
}

func GetDeviceAllocations(podAnnotations map[string]string) (DeviceAllocations, error) {
	deviceAllocations := DeviceAllocations{}
	data, ok := podAnnotations[AnnotationDeviceAllocated]
	if !ok {
		return nil, nil
	}
	err := json.Unmarshal([]byte(data), &deviceAllocations)
	if err != nil {
		return nil, err
	}
	return deviceAllocations, nil
}

func SetDeviceAllocations(obj metav1.Object, allocations DeviceAllocations) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	data, err := json.Marshal(allocations)
	if err != nil {
		return err
	}

	annotations[AnnotationDeviceAllocated] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

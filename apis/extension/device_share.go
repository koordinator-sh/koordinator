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
	// AnnotationDeviceAllocated represents the device allocated by the pod
	AnnotationDeviceAllocated = SchedulingDomainPrefix + "/device-allocated"
)

const (
	ResourceNvidiaGPU      corev1.ResourceName = "nvidia.com/gpu"
	ResourceHygonDCU       corev1.ResourceName = "dcu.com/gpu"
	ResourceRDMA           corev1.ResourceName = DomainPrefix + "rdma"
	ResourceFPGA           corev1.ResourceName = DomainPrefix + "fpga"
	ResourceGPU            corev1.ResourceName = DomainPrefix + "gpu"
	ResourceGPUShared      corev1.ResourceName = DomainPrefix + "gpu.shared"
	ResourceGPUCore        corev1.ResourceName = DomainPrefix + "gpu-core"
	ResourceGPUMemory      corev1.ResourceName = DomainPrefix + "gpu-memory"
	ResourceGPUMemoryRatio corev1.ResourceName = DomainPrefix + "gpu-memory-ratio"
)

const (
	LabelGPUModel         string = NodeDomainPrefix + "/gpu-model"
	LabelGPUDriverVersion string = NodeDomainPrefix + "/gpu-driver-version"
)

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

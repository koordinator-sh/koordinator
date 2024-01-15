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
	// AnnotationDeviceAllocateHint guides the scheduler in selecting and allocating specialized hardware resources
	AnnotationDeviceAllocateHint = SchedulingDomainPrefix + "/device-allocate-hint"
	// AnnotationDeviceJointAllocate guides the scheduler joint-allocates devices
	AnnotationDeviceJointAllocate = SchedulingDomainPrefix + "/device-joint-allocate"
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
	Minor     int32                      `json:"minor"`
	Resources corev1.ResourceList        `json:"resources"`
	Extension *DeviceAllocationExtension `json:"extension,omitempty"`
}

type DeviceAllocationExtension struct {
	VirtualFunctions []VirtualFunction `json:"vfs,omitempty"`
}

type VirtualFunction struct {
	Minor int    `json:"minor,omitempty"`
	BusID string `json:"busID,omitempty"`
}

type DeviceJointAllocate struct {
	// DeviceTypes indicates that the specified types of devices are grouped and allocated according to topology.
	DeviceTypes []schedulingv1alpha1.DeviceType `json:"deviceTypes,omitempty"`
	// RequiredScope specifies the allocation scope required for the joint allocation of devices.
	// It defines the granularity at which devices should be joint-allocated, e.g. in the same PCIe.
	RequiredScope DeviceJointAllocateScope `json:"requiredScope,omitempty"`
}

type DeviceJointAllocateScope string

const (
	SamePCIeDeviceJointAllocateScope DeviceJointAllocateScope = "SamePCIe"
)

type DeviceAllocateHints map[schedulingv1alpha1.DeviceType]*DeviceHint

type DeviceHint struct {
	// Selector selects devices by label selector.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// VFSelector selects VFs by label selector.
	// If specified the VFSelector, scheduler will allocate VFs from PFs which satisfy VFSelector.
	VFSelector *metav1.LabelSelector `json:"vfSelector,omitempty"`
	// AllocateStrategy controls the allocation strategy
	AllocateStrategy DeviceAllocateStrategy `json:"allocateStrategy,omitempty"`
	// ExclusivePolicy indicates the exclusive policy.
	ExclusivePolicy DeviceExclusivePolicy `json:"exclusivePolicy,omitempty"`
}

type DeviceAllocateStrategy string

const (
	ApplyForAllDeviceAllocateStrategy DeviceAllocateStrategy = "ApplyForAll"
	RequestsAsCountAllocateStrategy   DeviceAllocateStrategy = "RequestsAsCount"
)

type DeviceExclusivePolicy string

const (
	// DeviceLevelDeviceExclusivePolicy represents mutual exclusion in the device instance dimension
	DeviceLevelDeviceExclusivePolicy DeviceExclusivePolicy = "DeviceLevel"
	// PCIExpressLevelDeviceExclusivePolicy represents mutual exclusion in the PCIe dimension
	PCIExpressLevelDeviceExclusivePolicy DeviceExclusivePolicy = "PCIeLevel"
)

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

func SetDeviceAllocateHints(obj metav1.Object, hint DeviceAllocateHints) error {
	if hint == nil {
		return nil
	}

	data, err := json.Marshal(hint)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationDeviceAllocateHint] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetDeviceAllocateHints(annotations map[string]string) (DeviceAllocateHints, error) {
	var hint DeviceAllocateHints
	if val, ok := annotations[AnnotationDeviceAllocateHint]; ok {
		hint = DeviceAllocateHints{}
		err := json.Unmarshal([]byte(val), &hint)
		if err != nil {
			return nil, err
		}
	}
	return hint, nil
}

func SetDeviceJointAllocate(obj metav1.Object, jointAllocate *DeviceJointAllocate) error {
	if jointAllocate == nil {
		return nil
	}

	data, err := json.Marshal(jointAllocate)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationDeviceJointAllocate] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetDeviceJointAllocate(annotations map[string]string) (*DeviceJointAllocate, error) {
	val, ok := annotations[AnnotationDeviceJointAllocate]
	if !ok {
		return nil, nil
	}
	var jointAllocate DeviceJointAllocate
	err := json.Unmarshal([]byte(val), &jointAllocate)
	if err != nil {
		return nil, err
	}
	return &jointAllocate, nil
}

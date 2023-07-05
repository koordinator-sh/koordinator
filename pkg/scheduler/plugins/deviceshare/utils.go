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

package deviceshare

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	NvidiaGPU = 1 << iota
	HygonDCU
	KoordGPU
	GPUCore
	GPUMemory
	GPUMemoryRatio
	FPGA
	RDMA
)

var DeviceResourceNames = map[schedulingv1alpha1.DeviceType][]corev1.ResourceName{
	schedulingv1alpha1.GPU: {
		apiext.ResourceNvidiaGPU,
		apiext.ResourceHygonDCU,
		apiext.ResourceGPU,
		apiext.ResourceGPUCore,
		apiext.ResourceGPUMemory,
		apiext.ResourceGPUMemoryRatio,
	},
	schedulingv1alpha1.RDMA: {apiext.ResourceRDMA},
	schedulingv1alpha1.FPGA: {apiext.ResourceFPGA},
}

var DeviceResourceFlags = map[corev1.ResourceName]uint{
	apiext.ResourceNvidiaGPU:      NvidiaGPU,
	apiext.ResourceHygonDCU:       HygonDCU,
	apiext.ResourceGPU:            KoordGPU,
	apiext.ResourceGPUCore:        GPUCore,
	apiext.ResourceGPUMemory:      GPUMemory,
	apiext.ResourceGPUMemoryRatio: GPUMemoryRatio,
	apiext.ResourceFPGA:           FPGA,
	apiext.ResourceRDMA:           RDMA,
}

var ValidDeviceResourceCombinations = map[uint]bool{
	NvidiaGPU:                true,
	HygonDCU:                 true,
	KoordGPU:                 true,
	GPUMemory:                true,
	GPUMemoryRatio:           true,
	GPUCore | GPUMemory:      true,
	GPUCore | GPUMemoryRatio: true,
	FPGA:                     true,
	RDMA:                     true,
}

var DeviceResourceValidators = map[corev1.ResourceName]func(q resource.Quantity) bool{
	apiext.ResourceGPU:            ValidatePercentageResource,
	apiext.ResourceGPUCore:        ValidatePercentageResource,
	apiext.ResourceGPUMemoryRatio: ValidatePercentageResource,
	apiext.ResourceFPGA:           ValidatePercentageResource,
	apiext.ResourceRDMA:           ValidatePercentageResource,
}

var ResourceCombinationsMapper = map[uint]func(podRequest corev1.ResourceList) corev1.ResourceList{
	GPUMemory: func(podRequest corev1.ResourceList) corev1.ResourceList {
		return corev1.ResourceList{
			apiext.ResourceGPUMemory: podRequest[apiext.ResourceGPUMemory],
		}
	},
	GPUMemoryRatio: func(podRequest corev1.ResourceList) corev1.ResourceList {
		return corev1.ResourceList{
			apiext.ResourceGPUMemoryRatio: podRequest[apiext.ResourceGPUMemoryRatio],
		}
	},
	GPUCore | GPUMemory: func(podRequest corev1.ResourceList) corev1.ResourceList {
		return corev1.ResourceList{
			apiext.ResourceGPUCore:   podRequest[apiext.ResourceGPUCore],
			apiext.ResourceGPUMemory: podRequest[apiext.ResourceGPUMemory],
		}
	},
	GPUCore | GPUMemoryRatio: func(podRequest corev1.ResourceList) corev1.ResourceList {
		return corev1.ResourceList{
			apiext.ResourceGPUCore:        podRequest[apiext.ResourceGPUCore],
			apiext.ResourceGPUMemoryRatio: podRequest[apiext.ResourceGPUMemoryRatio],
		}
	},
	KoordGPU: func(podRequest corev1.ResourceList) corev1.ResourceList {
		return corev1.ResourceList{
			apiext.ResourceGPUCore:        podRequest[apiext.ResourceGPU],
			apiext.ResourceGPUMemoryRatio: podRequest[apiext.ResourceGPU],
		}
	},
	NvidiaGPU: func(podRequest corev1.ResourceList) corev1.ResourceList {
		nvidiaGPU := podRequest[apiext.ResourceNvidiaGPU]
		return corev1.ResourceList{
			apiext.ResourceGPUCore:        *resource.NewQuantity(nvidiaGPU.Value()*100, resource.DecimalSI),
			apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(nvidiaGPU.Value()*100, resource.DecimalSI),
		}
	},
	HygonDCU: func(podRequest corev1.ResourceList) corev1.ResourceList {
		hygonDCU := podRequest[apiext.ResourceHygonDCU]
		return corev1.ResourceList{
			apiext.ResourceGPUCore:        *resource.NewQuantity(hygonDCU.Value()*100, resource.DecimalSI),
			apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(hygonDCU.Value()*100, resource.DecimalSI),
		}
	},
	FPGA: func(podRequest corev1.ResourceList) corev1.ResourceList {
		return corev1.ResourceList{
			apiext.ResourceFPGA: podRequest[apiext.ResourceFPGA],
		}
	},
	RDMA: func(podRequest corev1.ResourceList) corev1.ResourceList {
		return corev1.ResourceList{
			apiext.ResourceRDMA: podRequest[apiext.ResourceRDMA],
		}
	},
}

func ValidatePercentageResource(q resource.Quantity) bool {
	if q.Value() > 100 && q.Value()%100 != 0 {
		return false
	}
	return true
}

func ValidateDeviceRequest(podRequest corev1.ResourceList) (uint, error) {
	var combination uint

	if podRequest == nil || len(podRequest) == 0 {
		return combination, fmt.Errorf("pod request should not be empty")
	}

	for resourceName, quantity := range podRequest {
		flag := DeviceResourceFlags[resourceName]
		combination |= flag

		validator := DeviceResourceValidators[resourceName]
		if validator != nil && !validator(quantity) {
			return combination, fmt.Errorf("invalid resource unit %v: %v", resourceName, quantity.String())
		}
	}

	if valid := ValidDeviceResourceCombinations[combination]; !valid {
		return combination, fmt.Errorf("invalid resource device requests: %v", quotav1.ResourceNames(podRequest))
	}
	return combination, nil
}

func ConvertDeviceRequest(podRequest corev1.ResourceList, combination uint) corev1.ResourceList {
	if podRequest == nil || len(podRequest) == 0 {
		klog.Warningf("pod request should not be empty")
		return nil
	}
	mapper := ResourceCombinationsMapper[combination]
	if mapper != nil {
		return mapper(podRequest)
	}
	return nil
}

func isPodRequestsMultipleDevice(podRequest corev1.ResourceList, deviceType schedulingv1alpha1.DeviceType) bool {
	if podRequest == nil || len(podRequest) == 0 {
		klog.Warningf("pod request should not be empty")
		return false
	}
	switch deviceType {
	case schedulingv1alpha1.GPU:
		memoryRatio := podRequest[apiext.ResourceGPUMemoryRatio]
		return memoryRatio.Value() > 100 && memoryRatio.Value()%100 == 0
	case schedulingv1alpha1.RDMA:
		rdma := podRequest[apiext.ResourceRDMA]
		return rdma.Value() > 100 && rdma.Value()%100 == 0
	case schedulingv1alpha1.FPGA:
		fpga := podRequest[apiext.ResourceFPGA]
		return fpga.Value() > 100 && fpga.Value()%100 == 0
	default:
		return false
	}
}

func memoryRatioToBytes(ratio, totalMemory resource.Quantity) resource.Quantity {
	return *resource.NewQuantity(ratio.Value()*totalMemory.Value()/100, resource.BinarySI)
}

func memoryBytesToRatio(bytes, totalMemory resource.Quantity) resource.Quantity {
	return *resource.NewQuantity(int64(float64(bytes.Value())/float64(totalMemory.Value())*100), resource.DecimalSI)
}

func fillGPUTotalMem(nodeDeviceTotal deviceResources, podRequest corev1.ResourceList) error {
	// nodeDeviceTotal uses the minor of GPU as key. However, under certain circumstances,
	// minor 0 might not exist. We need to iterate the cache once to find the active minor.
	var total corev1.ResourceList
	for _, resources := range nodeDeviceTotal {
		if len(resources) > 0 && !quotav1.IsZero(resources) {
			total = resources
			break
		}
	}
	if total == nil {
		return fmt.Errorf("cannot find sastisfied GPU resources")
	}

	// a node can only contain one type of GPU, so each of them has the same total memory.
	if gpuMem, ok := podRequest[apiext.ResourceGPUMemory]; ok {
		podRequest[apiext.ResourceGPUMemoryRatio] = memoryBytesToRatio(gpuMem, total[apiext.ResourceGPUMemory])
	} else {
		gpuMemRatio := podRequest[apiext.ResourceGPUMemoryRatio]
		podRequest[apiext.ResourceGPUMemory] = memoryRatioToBytes(gpuMemRatio, total[apiext.ResourceGPUMemory])
	}
	return nil
}

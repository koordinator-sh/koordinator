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
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func init() {
	deviceHandlers[schedulingv1alpha1.GPU] = &GPUHandler{}
}

var _ DeviceHandler = &GPUHandler{}

type GPUHandler struct {
}

func (h *GPUHandler) CalcDesiredRequestsAndCount(node *corev1.Node, pod *corev1.Pod, podRequests corev1.ResourceList, nodeDevice *nodeDevice, hint *apiext.DeviceHint, state *preFilterState) (corev1.ResourceList, int, *framework.Status) {
	totalDevice := nodeDevice.deviceTotal[schedulingv1alpha1.GPU]
	if len(totalDevice) == 0 {
		return nil, 0, framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("Insufficient %s devices", schedulingv1alpha1.GPU))
	}
	if state == nil || state.gpuRequirements == nil {
		// just for test
		requestsPerGPU, numberOfGPUs, _ := calcDesiredRequestsAndCountForGPU(podRequests)
		return requestsPerGPU, numberOfGPUs, nil
	}
	return state.gpuRequirements.requestsPerGPU, state.gpuRequirements.numberOfGPUs, nil
}

func calcDesiredRequestsAndCountForGPU(podRequests corev1.ResourceList) (corev1.ResourceList, int, bool) {
	desiredCount := int64(1)
	isShared := false

	gpuShare, ok := podRequests[apiext.ResourceGPUShared]
	gpuCore, coreExists := podRequests[apiext.ResourceGPUCore]
	gpuMemoryRatio, memoryRatioExists := podRequests[apiext.ResourceGPUMemoryRatio]
	// gpu share mode
	if ok && gpuShare.Value() > 0 {
		desiredCount = gpuShare.Value()
	} else {
		if memoryRatioExists && gpuMemoryRatio.Value() > 100 && gpuMemoryRatio.Value()%100 == 0 {
			desiredCount = gpuMemoryRatio.Value() / 100
		}
	}

	requests := corev1.ResourceList{}
	if coreExists {
		requests[apiext.ResourceGPUCore] = *resource.NewQuantity(gpuCore.Value()/desiredCount, resource.DecimalSI)
	}
	if memoryRatioExists {
		gpuMemoryRatioPerGPU := gpuMemoryRatio.Value() / desiredCount
		if gpuMemoryRatioPerGPU < 100 {
			isShared = true
		}
		requests[apiext.ResourceGPUMemoryRatio] = *resource.NewQuantity(gpuMemoryRatioPerGPU, resource.DecimalSI)
	} else if gpuMem, memExists := podRequests[apiext.ResourceGPUMemory]; memExists {
		isShared = true
		requests[apiext.ResourceGPUMemory] = *resource.NewQuantity(gpuMem.Value()/desiredCount, resource.BinarySI)
	}
	return requests, int(desiredCount), isShared
}

func fillGPUTotalMem(allocations apiext.DeviceAllocations, nodeDeviceInfo *nodeDevice) error {
	gpuAllocations, ok := allocations[schedulingv1alpha1.GPU]
	if !ok {
		return nil
	}
	gpuTotalDevices, ok := nodeDeviceInfo.deviceTotal[schedulingv1alpha1.GPU]
	if !ok {
		return nil
	}

	for i, allocation := range gpuAllocations {
		gpuDevice, ok := gpuTotalDevices[int(allocation.Minor)]
		if !ok || gpuDevice == nil || quotav1.IsZero(gpuDevice) {
			return fmt.Errorf("no healthy gpu device with minor %d of allocation", allocation.Minor)
		}
		gpuAllocations[i].Resources = gpuAllocations[i].Resources.DeepCopy()
		if gpuMem, ok := allocation.Resources[apiext.ResourceGPUMemory]; ok {
			gpuAllocations[i].Resources[apiext.ResourceGPUMemoryRatio] = memoryBytesToRatio(gpuMem, gpuDevice[apiext.ResourceGPUMemory])
		} else {
			gpuMemRatio := allocation.Resources[apiext.ResourceGPUMemoryRatio]
			gpuAllocations[i].Resources[apiext.ResourceGPUMemory] = memoryRatioToBytes(gpuMemRatio, gpuDevice[apiext.ResourceGPUMemory])
		}
	}
	return nil
}

func memoryRatioToBytes(ratio, totalMemory resource.Quantity) resource.Quantity {
	return *resource.NewQuantity(ratio.Value()*totalMemory.Value()/100, resource.BinarySI)
}

func memoryBytesToRatio(bytes, totalMemory resource.Quantity) resource.Quantity {
	return *resource.NewQuantity(int64(float64(bytes.Value())/float64(totalMemory.Value())*100), resource.DecimalSI)
}

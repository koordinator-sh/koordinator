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

func (h *GPUHandler) CalcDesiredRequestsAndCount(node *corev1.Node, pod *corev1.Pod, podRequests corev1.ResourceList, nodeDevice *nodeDevice, hint *apiext.DeviceHint) (corev1.ResourceList, int, *framework.Status) {
	totalDevice := nodeDevice.deviceTotal[schedulingv1alpha1.GPU]
	if len(totalDevice) == 0 {
		return nil, 0, framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("Insufficient %s devices", schedulingv1alpha1.GPU))
	}

	podRequests = podRequests.DeepCopy()
	if err := fillGPUTotalMem(totalDevice, podRequests); err != nil {
		return nil, 0, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}

	requests := podRequests
	desiredCount := int64(1)

	memoryRatio := podRequests[apiext.ResourceGPUMemoryRatio]
	multiDevices := memoryRatio.Value() > 100 && memoryRatio.Value()%100 == 0
	if multiDevices {
		gpuCore, gpuMem, gpuMemoryRatio := podRequests[apiext.ResourceGPUCore], podRequests[apiext.ResourceGPUMemory], podRequests[apiext.ResourceGPUMemoryRatio]
		desiredCount = gpuMemoryRatio.Value() / 100
		requests = corev1.ResourceList{
			apiext.ResourceGPUCore:        *resource.NewQuantity(gpuCore.Value()/desiredCount, resource.DecimalSI),
			apiext.ResourceGPUMemory:      *resource.NewQuantity(gpuMem.Value()/desiredCount, resource.BinarySI),
			apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(gpuMemoryRatio.Value()/desiredCount, resource.DecimalSI),
		}
	}
	return requests, int(desiredCount), nil
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
		return fmt.Errorf("no healthy GPU Devices")
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

func memoryRatioToBytes(ratio, totalMemory resource.Quantity) resource.Quantity {
	return *resource.NewQuantity(ratio.Value()*totalMemory.Value()/100, resource.BinarySI)
}

func memoryBytesToRatio(bytes, totalMemory resource.Quantity) resource.Quantity {
	return *resource.NewQuantity(int64(float64(bytes.Value())/float64(totalMemory.Value())*100), resource.DecimalSI)
}

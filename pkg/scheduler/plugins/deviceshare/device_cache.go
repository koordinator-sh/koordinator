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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

type nodeDeviceCache struct {
	lock sync.Mutex
	// nodeDeviceInfos stores nodeDevice for each node
	// and uses node name as map key.
	nodeDeviceInfos map[string]*nodeDevice
}

type nodeDevice struct {
	lock        sync.RWMutex
	deviceTotal map[schedulingv1alpha1.DeviceType]deviceResources
	deviceFree  map[schedulingv1alpha1.DeviceType]deviceResources
	deviceUsed  map[schedulingv1alpha1.DeviceType]deviceResources
	allocateSet map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]struct{}
}

// deviceResources is used to present resources per device.
// we use the minor of device as key
// "0": {koordinator.sh/gpu-core:100, koordinator.sh/gpu-memory-ratio:100, koordinator.sh/gpu-memory: 16GB}
// "1": {koordinator.sh/gpu-core:100, koordinator.sh/gpu-memory-ratio:100, koordinator.sh/gpu-memory: 16GB}
type deviceResources map[int]corev1.ResourceList

func newNodeDeviceCache() *nodeDeviceCache {
	return &nodeDeviceCache{
		nodeDeviceInfos: make(map[string]*nodeDevice),
	}
}

func newNodeDevice() *nodeDevice {
	return &nodeDevice{
		deviceTotal: make(map[schedulingv1alpha1.DeviceType]deviceResources),
		deviceFree:  make(map[schedulingv1alpha1.DeviceType]deviceResources),
		deviceUsed:  make(map[schedulingv1alpha1.DeviceType]deviceResources),
		allocateSet: make(map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]struct{}),
	}
}

func (in deviceResources) DeepCopy() deviceResources {
	if in == nil {
		return nil
	}
	out := deviceResources{}
	for k, v := range in {
		out[k] = v.DeepCopy()
	}
	return out
}

func (n *nodeDeviceCache) getNodeDevice(nodeName string) *nodeDevice {
	n.lock.Lock()
	defer n.lock.Unlock()
	return n.nodeDeviceInfos[nodeName]
}

func (n *nodeDeviceCache) createNodeDevice(nodeName string) *nodeDevice {
	n.lock.Lock()
	defer n.lock.Unlock()
	n.nodeDeviceInfos[nodeName] = newNodeDevice()
	return n.nodeDeviceInfos[nodeName]
}

func (n *nodeDevice) resetDeviceTotal(resources map[schedulingv1alpha1.DeviceType]deviceResources) {
	for deviceType := range n.deviceTotal {
		if _, ok := resources[deviceType]; !ok {
			resources[deviceType] = make(deviceResources)
		}
	}
	n.deviceTotal = resources
	for deviceType := range resources {
		n.resetDeviceFree(deviceType)
	}
}

// updateCacheUsed is used to update deviceUsed when there is a new pod created/deleted
func (n *nodeDevice) updateCacheUsed(deviceAllocations apiext.DeviceAllocations, pod *corev1.Pod, add bool) {
	podNamespacedName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
	if len(deviceAllocations) > 0 {
		for deviceType, allocations := range deviceAllocations {
			allocateSet := n.allocateSet[deviceType]
			if allocateSet == nil {
				allocateSet = make(map[types.NamespacedName]struct{})
			}
			n.allocateSet[deviceType] = allocateSet

			if add {
				if _, ok := allocateSet[podNamespacedName]; ok {
					// for non-failover scenario, pod might already exist in cache after Reserve step.
					continue
				}
			} else {
				if _, ok := allocateSet[podNamespacedName]; !ok {
					continue
				}
			}
			n.updateDeviceUsed(deviceType, allocations, add)
			n.resetDeviceFree(deviceType)
		}
		n.updateAllocateSet(deviceAllocations, pod, add)
	}
}

func (n *nodeDevice) resetDeviceFree(deviceType schedulingv1alpha1.DeviceType) {
	if n.deviceFree[deviceType] == nil {
		n.deviceFree[deviceType] = make(deviceResources)
	}
	if n.deviceTotal[deviceType] == nil {
		n.deviceTotal[deviceType] = make(deviceResources)
	}
	n.deviceFree[deviceType] = n.deviceTotal[deviceType].DeepCopy()
	for minor, usedResource := range n.deviceUsed[deviceType] {
		if n.deviceFree[deviceType][minor] == nil {
			n.deviceFree[deviceType][minor] = make(corev1.ResourceList)
		}
		if n.deviceTotal[deviceType][minor] == nil {
			n.deviceTotal[deviceType][minor] = make(corev1.ResourceList)
		}
		n.deviceFree[deviceType][minor] = quotav1.SubtractWithNonNegativeResult(
			n.deviceTotal[deviceType][minor],
			usedResource)
	}
}

func (n *nodeDevice) updateDeviceUsed(deviceType schedulingv1alpha1.DeviceType, allocations []*apiext.DeviceAllocation, add bool) {
	if n.deviceUsed[deviceType] == nil {
		n.deviceUsed[deviceType] = make(deviceResources)
	}
	for _, allocation := range allocations {
		if n.deviceUsed[deviceType][int(allocation.Minor)] == nil {
			n.deviceUsed[deviceType][int(allocation.Minor)] = make(corev1.ResourceList)
		}
		if add {
			n.deviceUsed[deviceType][int(allocation.Minor)] = quotav1.Add(
				n.deviceUsed[deviceType][int(allocation.Minor)],
				allocation.Resources)
		} else {
			n.deviceUsed[deviceType][int(allocation.Minor)] = quotav1.SubtractWithNonNegativeResult(
				n.deviceUsed[deviceType][int(allocation.Minor)],
				allocation.Resources)
		}
	}
}

func (n *nodeDevice) updateAllocateSet(allocations apiext.DeviceAllocations, pod *corev1.Pod, add bool) {
	podNamespacedName := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}
	for deviceType, v := range allocations {
		if v == nil || len(v) == 0 {
			break
		}
		if n.allocateSet[deviceType] == nil {
			n.allocateSet[deviceType] = make(map[types.NamespacedName]struct{})
		}
		if add {
			n.allocateSet[deviceType][podNamespacedName] = struct{}{}
		} else {
			delete(n.allocateSet[deviceType], podNamespacedName)
		}
	}
}

func (n *nodeDevice) tryAllocateDevice(podRequest corev1.ResourceList) (apiext.DeviceAllocations, error) {
	allocateResult := make(apiext.DeviceAllocations)

	for deviceType := range deviceResourceNames {
		switch deviceType {
		case schedulingv1alpha1.RDMA, schedulingv1alpha1.FPGA:
			if !hasDeviceResource(podRequest, deviceType) {
				break
			}
			if err := n.tryAllocateCommonDevice(podRequest, deviceType, allocateResult); err != nil {
				return nil, err
			}
		case schedulingv1alpha1.GPU:
			if !hasDeviceResource(podRequest, deviceType) {
				break
			}
			if err := n.tryAllocateGPU(podRequest, allocateResult); err != nil {
				return nil, err
			}
		default:
			klog.Warningf("device type %v is not supported yet", deviceType)
		}
	}

	return allocateResult, nil
}

func (n *nodeDevice) tryAllocateCommonDevice(podRequest corev1.ResourceList, deviceType schedulingv1alpha1.DeviceType, allocateResult apiext.DeviceAllocations) error {
	podRequest = quotav1.Mask(podRequest, deviceResourceNames[deviceType])
	nodeDeviceTotal := n.deviceTotal[deviceType]
	if len(nodeDeviceTotal) <= 0 {
		return fmt.Errorf("node does not have enough %v", deviceType)
	}

	var deviceAllocations []*apiext.DeviceAllocation

	if isMultipleCommonDevicePod(podRequest, deviceType) {
		var commonDeviceWanted int64
		var podRequestPerCard corev1.ResourceList
		switch deviceType {
		case schedulingv1alpha1.RDMA:
			commonDevice := podRequest[apiext.KoordRDMA]
			commonDeviceWanted = commonDevice.Value() / 100
			podRequestPerCard = corev1.ResourceList{
				apiext.KoordRDMA: *resource.NewQuantity(commonDevice.Value()/commonDeviceWanted, resource.DecimalSI),
			}
		case schedulingv1alpha1.FPGA:
			commonDevice := podRequest[apiext.KoordFPGA]
			commonDeviceWanted = commonDevice.Value() / 100
			podRequestPerCard = corev1.ResourceList{
				apiext.KoordFPGA: *resource.NewQuantity(commonDevice.Value()/commonDeviceWanted, resource.DecimalSI),
			}
		}
		satisfiedDeviceCount := 0
		for minor, resources := range n.deviceFree[deviceType] {
			if satisfied, _ := quotav1.LessThanOrEqual(podRequestPerCard, resources); satisfied {
				satisfiedDeviceCount++
				deviceAllocations = append(deviceAllocations, &apiext.DeviceAllocation{
					Minor:     int32(minor),
					Resources: podRequestPerCard,
				})
			}
			if satisfiedDeviceCount == int(commonDeviceWanted) {
				allocateResult[deviceType] = deviceAllocations
				return nil
			}
		}
		klog.V(5).Infof("node resource does not satisfy pod's multiple %v request, expect %v, got %v", deviceType, commonDeviceWanted, satisfiedDeviceCount)
		return fmt.Errorf("node does not have enough %v", deviceType)
	}

	for minor, resources := range n.deviceFree[deviceType] {
		if satisfied, _ := quotav1.LessThanOrEqual(podRequest, resources); satisfied {
			deviceAllocations = append(deviceAllocations, &apiext.DeviceAllocation{
				Minor:     int32(minor),
				Resources: podRequest,
			})
			allocateResult[deviceType] = deviceAllocations
			return nil
		}
	}
	klog.V(5).Infof("node resource does not satisfy pod's %v request", deviceType)
	return fmt.Errorf("node does not have enough %v", deviceType)
}

func (n *nodeDevice) tryAllocateGPU(podRequest corev1.ResourceList, allocateResult apiext.DeviceAllocations) error {
	podRequest = quotav1.Mask(podRequest, deviceResourceNames[schedulingv1alpha1.GPU])
	nodeDeviceTotal := n.deviceTotal[schedulingv1alpha1.GPU]
	if len(nodeDeviceTotal) <= 0 {
		return fmt.Errorf("node does not have enough GPU")
	}

	fillGPUTotalMem(nodeDeviceTotal, podRequest)

	var deviceAllocations []*apiext.DeviceAllocation
	if isMultipleGPUPod(podRequest) {
		gpuCore, gpuMem, gpuMemRatio := podRequest[apiext.GPUCore], podRequest[apiext.GPUMemory], podRequest[apiext.GPUMemoryRatio]
		gpuWanted := gpuCore.Value() / 100
		podRequestPerCard := corev1.ResourceList{
			apiext.GPUCore:        *resource.NewQuantity(gpuCore.Value()/gpuWanted, resource.DecimalSI),
			apiext.GPUMemory:      *resource.NewQuantity(gpuMem.Value()/gpuWanted, resource.BinarySI),
			apiext.GPUMemoryRatio: *resource.NewQuantity(gpuMemRatio.Value()/gpuWanted, resource.DecimalSI),
		}
		satisfiedDeviceCount := 0
		for minor, resources := range n.deviceFree[schedulingv1alpha1.GPU] {
			if satisfied, _ := quotav1.LessThanOrEqual(podRequestPerCard, resources); satisfied {
				satisfiedDeviceCount++
				deviceAllocations = append(deviceAllocations, &apiext.DeviceAllocation{
					Minor:     int32(minor),
					Resources: podRequestPerCard,
				})
			}
			if satisfiedDeviceCount == int(gpuWanted) {
				allocateResult[schedulingv1alpha1.GPU] = deviceAllocations
				return nil
			}
		}
		klog.V(5).Infof("node GPU resource does not satisfy pod's multiple GPU request, expect %v, got %v", gpuWanted, satisfiedDeviceCount)
		return fmt.Errorf("node does not have enough GPU")
	}
	for minor, resources := range n.deviceFree[schedulingv1alpha1.GPU] {
		if satisfied, _ := quotav1.LessThanOrEqual(podRequest, resources); satisfied {
			deviceAllocations = append(deviceAllocations, &apiext.DeviceAllocation{
				Minor:     int32(minor),
				Resources: podRequest,
			})
			allocateResult[schedulingv1alpha1.GPU] = deviceAllocations
			return nil
		}
	}
	klog.V(5).Infof("node GPU resource does not satisfy pod's request")
	return fmt.Errorf("node does not have enough GPU")
}

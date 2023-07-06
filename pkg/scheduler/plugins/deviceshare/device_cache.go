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
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

type nodeDevice struct {
	lock        sync.RWMutex
	deviceTotal map[schedulingv1alpha1.DeviceType]deviceResources
	deviceFree  map[schedulingv1alpha1.DeviceType]deviceResources
	deviceUsed  map[schedulingv1alpha1.DeviceType]deviceResources
	allocateSet map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources
}

func newNodeDevice() *nodeDevice {
	return &nodeDevice{
		deviceTotal: make(map[schedulingv1alpha1.DeviceType]deviceResources),
		deviceFree:  make(map[schedulingv1alpha1.DeviceType]deviceResources),
		deviceUsed:  make(map[schedulingv1alpha1.DeviceType]deviceResources),
		allocateSet: make(map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources),
	}
}

func (n *nodeDevice) getNodeDeviceSummary() *NodeDeviceSummary {
	n.lock.RLock()
	defer n.lock.RUnlock()

	nodeDeviceSummary := NewNodeDeviceSummary()
	calFunc := func(localDeviceRes map[schedulingv1alpha1.DeviceType]deviceResources,
		deviceResSummary map[corev1.ResourceName]*resource.Quantity,
		deviceResDetailSummary map[schedulingv1alpha1.DeviceType]deviceResources) {

		for deviceType, resourceMap := range localDeviceRes {
			deviceResDetailSummary[deviceType] = make(deviceResources)
			for minor, deviceResource := range resourceMap {
				deviceResDetailSummary[deviceType][minor] = deviceResource.DeepCopy()
				for key, value := range deviceResource {
					if _, exist := deviceResSummary[key]; !exist {
						deviceResSummary[key] = &resource.Quantity{}
						*deviceResSummary[key] = value.DeepCopy()
					} else {
						deviceResSummary[key].Add(value)
					}
				}
			}
		}
	}

	calFunc(n.deviceTotal, nodeDeviceSummary.DeviceTotal, nodeDeviceSummary.DeviceTotalDetail)
	calFunc(n.deviceFree, nodeDeviceSummary.DeviceFree, nodeDeviceSummary.DeviceFreeDetail)
	calFunc(n.deviceUsed, nodeDeviceSummary.DeviceUsed, nodeDeviceSummary.DeviceUsedDetail)

	for deviceType, allocateSet := range n.allocateSet {
		nodeDeviceSummary.AllocateSet[deviceType] = make(map[string]map[int]corev1.ResourceList)
		for podNamespacedName, allocations := range allocateSet {
			nodeDeviceSummary.AllocateSet[deviceType][podNamespacedName.String()] = make(map[int]corev1.ResourceList)
			for minor, resource := range allocations {
				nodeDeviceSummary.AllocateSet[deviceType][podNamespacedName.String()][minor] = resource.DeepCopy()
			}
		}
	}

	return nodeDeviceSummary
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
	if len(deviceAllocations) > 0 {
		for deviceType, allocations := range deviceAllocations {
			if !n.isValid(deviceType, pod.Namespace, pod.Name, add) {
				continue
			}
			n.updateDeviceUsed(deviceType, allocations, add)
			n.resetDeviceFree(deviceType)
			n.updateAllocateSet(deviceType, allocations, pod, add)
		}
	}
}

func (n *nodeDevice) getUsed(namespace, name string) map[schedulingv1alpha1.DeviceType]deviceResources {
	podNamespacedName := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	allocations := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for deviceType, podAllocated := range n.allocateSet {
		resources := podAllocated[podNamespacedName]
		if len(resources) == 0 {
			continue
		}
		resourcesCopy := make(map[int]corev1.ResourceList, len(resources))
		for minor, res := range resources {
			resourcesCopy[minor] = res.DeepCopy()
		}
		allocations[deviceType] = resourcesCopy
	}
	return allocations
}

func (n *nodeDevice) replaceWith(freeDevices map[schedulingv1alpha1.DeviceType]deviceResources) *nodeDevice {
	nn := newNodeDevice()
	usedDevices := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for deviceType, total := range n.deviceTotal {
		resources, ok := freeDevices[deviceType]
		if !ok {
			nn.deviceTotal[deviceType] = total.DeepCopy()
			continue
		}

		deviceTotalResources := deviceResources{}
		deviceUsedResources := deviceResources{}
		for minor, free := range resources {
			deviceTotalResources[minor] = total[minor].DeepCopy()
			used := quotav1.SubtractWithNonNegativeResult(total[minor], free)
			deviceUsedResources[minor] = used
		}
		nn.deviceTotal[deviceType] = deviceTotalResources
		usedDevices[deviceType] = deviceUsedResources
	}

	for deviceType, used := range n.deviceUsed {
		resources, ok := usedDevices[deviceType]
		if !ok {
			nn.deviceUsed[deviceType] = used.DeepCopy()
			continue
		}
		nn.deviceUsed[deviceType] = resources
	}

	for deviceType := range nn.deviceTotal {
		nn.resetDeviceFree(deviceType)
	}
	return nn
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
		n.deviceFree[deviceType][minor] = quotav1.SubtractWithNonNegativeResult(n.deviceTotal[deviceType][minor], usedResource)
	}
}

func (n *nodeDevice) updateDeviceUsed(deviceType schedulingv1alpha1.DeviceType, allocations []*apiext.DeviceAllocation, add bool) {
	deviceUsed := n.deviceUsed[deviceType]
	if deviceUsed == nil {
		deviceUsed = make(deviceResources)
		n.deviceUsed[deviceType] = deviceUsed
	}
	for _, allocation := range allocations {
		if deviceUsed[int(allocation.Minor)] == nil {
			deviceUsed[int(allocation.Minor)] = make(corev1.ResourceList)
		}
		if add {
			deviceUsed[int(allocation.Minor)] = quotav1.Add(deviceUsed[int(allocation.Minor)], allocation.Resources)
		} else {
			used := quotav1.SubtractWithNonNegativeResult(deviceUsed[int(allocation.Minor)], allocation.Resources)
			if quotav1.IsZero(used) {
				delete(deviceUsed, int(allocation.Minor))
			} else {
				deviceUsed[int(allocation.Minor)] = used
			}
		}
	}
	if !add && len(deviceUsed) == 0 {
		delete(n.deviceUsed, deviceType)
	}
}

func (n *nodeDevice) isValid(deviceType schedulingv1alpha1.DeviceType, namespace string, name string, add bool) bool {
	allocateSet := n.allocateSet[deviceType]
	if allocateSet == nil {
		allocateSet = make(map[types.NamespacedName]deviceResources)
	}
	n.allocateSet[deviceType] = allocateSet

	podNamespacedName := types.NamespacedName{Namespace: namespace, Name: name}
	if add {
		if _, ok := allocateSet[podNamespacedName]; ok {
			// for non-failover scenario, pod might already exist in cache after Reserve step.
			return false
		}
	} else {
		if _, ok := allocateSet[podNamespacedName]; !ok {
			return false
		}
	}

	return true
}

func (n *nodeDevice) updateAllocateSet(deviceType schedulingv1alpha1.DeviceType, allocations []*apiext.DeviceAllocation, pod *corev1.Pod, add bool) {
	podNamespacedName := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}

	if n.allocateSet[deviceType] == nil {
		n.allocateSet[deviceType] = make(map[types.NamespacedName]deviceResources)
	}
	if add {
		resources := make(deviceResources)
		for _, allocation := range allocations {
			resources[int(allocation.Minor)] = allocation.Resources.DeepCopy()
		}
		n.allocateSet[deviceType][podNamespacedName] = resources
	} else {
		delete(n.allocateSet[deviceType], podNamespacedName)
	}
}

func (n *nodeDevice) tryAllocateDevice(podRequest corev1.ResourceList, required, preferred map[schedulingv1alpha1.DeviceType]sets.Int, requiredDeviceResources, preemptibleDeviceResources map[schedulingv1alpha1.DeviceType]deviceResources) (apiext.DeviceAllocations, error) {
	allocateResult := make(apiext.DeviceAllocations)

	for deviceType, supportedResourceNames := range DeviceResourceNames {
		deviceRequest := quotav1.Mask(podRequest, supportedResourceNames)
		if quotav1.IsZero(deviceRequest) {
			continue
		}
		err := n.tryAllocateDeviceByType(
			deviceRequest,
			deviceType,
			required[deviceType],
			preferred[deviceType],
			allocateResult,
			requiredDeviceResources[deviceType],
			preemptibleDeviceResources[deviceType],
		)
		if err != nil {
			return nil, err
		}
	}

	return allocateResult, nil
}

func (n *nodeDevice) tryAllocateDeviceByType(
	podRequest corev1.ResourceList,
	deviceType schedulingv1alpha1.DeviceType,
	required sets.Int,
	preferred sets.Int,
	allocateResult apiext.DeviceAllocations,
	requiredDeviceResources deviceResources,
	preemptibleDeviceResources deviceResources,
) error {
	nodeDeviceTotal := n.deviceTotal[deviceType]
	if len(nodeDeviceTotal) == 0 {
		return fmt.Errorf("node does not have enough %v", deviceType)
	}

	var freeDevices deviceResources
	if len(requiredDeviceResources) > 0 {
		freeDevices = requiredDeviceResources
	} else {
		freeDevices = n.calcFreeWithPreemptible(deviceType, preemptibleDeviceResources)
	}

	if deviceType == schedulingv1alpha1.GPU {
		if err := fillGPUTotalMem(nodeDeviceTotal, podRequest); err != nil {
			return err
		}
	}

	var deviceAllocations []*apiext.DeviceAllocation
	deviceWanted := int64(1)
	podRequestPerCard := podRequest
	if isPodRequestsMultipleDevice(podRequest, deviceType) {
		switch deviceType {
		case schedulingv1alpha1.GPU:
			gpuCore, gpuMem, gpuMemoryRatio := podRequest[apiext.ResourceGPUCore], podRequest[apiext.ResourceGPUMemory], podRequest[apiext.ResourceGPUMemoryRatio]
			deviceWanted = gpuMemoryRatio.Value() / 100
			podRequestPerCard = corev1.ResourceList{
				apiext.ResourceGPUCore:        *resource.NewQuantity(gpuCore.Value()/deviceWanted, resource.DecimalSI),
				apiext.ResourceGPUMemory:      *resource.NewQuantity(gpuMem.Value()/deviceWanted, resource.BinarySI),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(gpuMemoryRatio.Value()/deviceWanted, resource.DecimalSI),
			}
		case schedulingv1alpha1.RDMA:
			commonDevice := podRequest[apiext.ResourceRDMA]
			deviceWanted = commonDevice.Value() / 100
			podRequestPerCard = corev1.ResourceList{
				apiext.ResourceRDMA: *resource.NewQuantity(commonDevice.Value()/deviceWanted, resource.DecimalSI),
			}
		case schedulingv1alpha1.FPGA:
			commonDevice := podRequest[apiext.ResourceFPGA]
			deviceWanted = commonDevice.Value() / 100
			podRequestPerCard = corev1.ResourceList{
				apiext.ResourceFPGA: *resource.NewQuantity(commonDevice.Value()/deviceWanted, resource.DecimalSI),
			}
		}
	}

	satisfiedDeviceCount := 0
	orderedDeviceResources := sortDeviceResourcesByMinor(freeDevices, preferred)
	for _, deviceResource := range orderedDeviceResources {
		if required.Len() > 0 && !required.Has(deviceResource.minor) {
			continue
		}
		// Skip unhealthy Device instances with zero resources
		if quotav1.IsZero(deviceResource.resources) {
			continue
		}
		if satisfied, _ := quotav1.LessThanOrEqual(podRequestPerCard, deviceResource.resources); satisfied {
			satisfiedDeviceCount++
			deviceAllocations = append(deviceAllocations, &apiext.DeviceAllocation{
				Minor:     int32(deviceResource.minor),
				Resources: podRequestPerCard,
			})
		}
		if satisfiedDeviceCount == int(deviceWanted) {
			allocateResult[deviceType] = deviceAllocations
			return nil
		}
	}
	klog.V(5).Infof("node resource does not satisfy pod's multiple %v request, expect %v, got %v", deviceType, deviceWanted, satisfiedDeviceCount)
	return fmt.Errorf("node does not have enough %v", deviceType)
}

func (n *nodeDevice) calcFreeWithPreemptible(deviceType schedulingv1alpha1.DeviceType, preemptible deviceResources) deviceResources {
	deviceFree := n.deviceFree[deviceType]
	deviceUsed := n.deviceUsed[deviceType]
	deviceTotal := n.deviceTotal[deviceType]
	var mergedFreeDevices deviceResources
	if len(preemptible) > 0 {
		mergedFreeDevices = make(deviceResources)
		for minor, v := range preemptible {
			used := quotav1.SubtractWithNonNegativeResult(deviceUsed[minor], v)
			remaining := quotav1.SubtractWithNonNegativeResult(deviceTotal[minor], used)
			if !quotav1.IsZero(remaining) {
				mergedFreeDevices[minor] = remaining
			}
		}
	}

	// The merging logic is executed only when there is a device that can be preempted,
	// and the remaining idle devices are merged together to participate in the allocation
	if len(mergedFreeDevices) > 0 {
		for minor, v := range deviceFree {
			res := mergedFreeDevices[minor]
			if res == nil {
				mergedFreeDevices[minor] = v.DeepCopy()
			}
		}
		deviceFree = mergedFreeDevices
	}
	return deviceFree
}

type nodeDeviceCache struct {
	lock sync.Mutex
	// nodeDeviceInfos stores nodeDevice for each node
	// and uses node name as map key.
	nodeDeviceInfos map[string]*nodeDevice
}

func newNodeDeviceCache() *nodeDeviceCache {
	return &nodeDeviceCache{
		nodeDeviceInfos: make(map[string]*nodeDevice),
	}
}

func (n *nodeDeviceCache) getNodeDevice(nodeName string, needInit bool) *nodeDevice {
	n.lock.Lock()
	defer n.lock.Unlock()

	// getNodeDevice will create new `nodeDevice` if needInit is true and nodeDeviceInfos[nodeName] is nil
	if n.nodeDeviceInfos[nodeName] == nil && needInit {
		klog.V(5).Infof("node device cache not found, nodeName: %v, createNodeDevice", nodeName)
		n.nodeDeviceInfos[nodeName] = newNodeDevice()
	}

	return n.nodeDeviceInfos[nodeName]
}

func (n *nodeDeviceCache) removeNodeDevice(nodeName string) {
	if nodeName == "" {
		return
	}
	n.lock.Lock()
	defer n.lock.Unlock()
	delete(n.nodeDeviceInfos, nodeName)
}

func (n *nodeDeviceCache) updateNodeDevice(nodeName string, device *schedulingv1alpha1.Device) {
	if nodeName == "" || device == nil {
		return
	}

	info := n.getNodeDevice(nodeName, true)

	info.lock.Lock()
	defer info.lock.Unlock()

	nodeDeviceResource := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for _, deviceInfo := range device.Spec.Devices {
		if nodeDeviceResource[deviceInfo.Type] == nil {
			nodeDeviceResource[deviceInfo.Type] = make(deviceResources)
		}
		if !deviceInfo.Health {
			nodeDeviceResource[deviceInfo.Type][int(*deviceInfo.Minor)] = make(corev1.ResourceList)
			klog.Errorf("Find device unhealthy, nodeName:%v, deviceType:%v, minor:%v",
				nodeName, deviceInfo.Type, deviceInfo.Minor)
		} else {
			resources := deviceInfo.Resources
			nodeDeviceResource[deviceInfo.Type][int(*deviceInfo.Minor)] = resources
			klog.V(5).Infof("Find device resource update, nodeName:%v, deviceType:%v, minor:%v, res:%v",
				nodeName, deviceInfo.Type, deviceInfo.Minor, resources)
		}
	}

	info.resetDeviceTotal(nodeDeviceResource)
}

func (n *nodeDeviceCache) getNodeDeviceSummary(nodeName string) (*NodeDeviceSummary, bool) {
	n.lock.Lock()
	defer n.lock.Unlock()

	if _, exist := n.nodeDeviceInfos[nodeName]; !exist {
		return nil, false
	}

	nodeDeviceSummary := n.nodeDeviceInfos[nodeName].getNodeDeviceSummary()
	return nodeDeviceSummary, true
}

func (n *nodeDeviceCache) getAllNodeDeviceSummary() map[string]*NodeDeviceSummary {
	n.lock.Lock()
	defer n.lock.Unlock()

	nodeDeviceSummaries := make(map[string]*NodeDeviceSummary)
	for nodeName, nodeDeviceInfo := range n.nodeDeviceInfos {
		nodeDeviceSummaries[nodeName] = nodeDeviceInfo.getNodeDeviceSummary()
	}
	return nodeDeviceSummaries
}

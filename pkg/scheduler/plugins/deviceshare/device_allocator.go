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
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/schedulingphase"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

var deviceHandlers = map[schedulingv1alpha1.DeviceType]DeviceHandler{}
var deviceAllocators = map[schedulingv1alpha1.DeviceType]DeviceAllocator{}

type DeviceHandler interface {
	CalcDesiredRequestsAndCount(node *corev1.Node, pod *corev1.Pod, podRequests corev1.ResourceList, nodeDevice *nodeDevice, hint *apiext.DeviceHint, state *preFilterState) (corev1.ResourceList, int, *framework.Status)
}

type DeviceAllocator interface {
	Allocate(requestCtx *requestContext, nodeDevice *nodeDevice, desiredCount int, maxDesiredCount int, preferredPCIEs sets.String) ([]*apiext.DeviceAllocation, *framework.Status)
}

type requestContext struct {
	pod                       *corev1.Pod
	node                      *corev1.Node
	requestsPerInstance       map[schedulingv1alpha1.DeviceType]corev1.ResourceList
	desiredCountPerDeviceType map[schedulingv1alpha1.DeviceType]int
	gpuRequirements           *GPURequirements
	hints                     apiext.DeviceAllocateHints
	hintSelectors             map[schedulingv1alpha1.DeviceType][2]labels.Selector
	required                  map[schedulingv1alpha1.DeviceType]sets.Int
	preferred                 map[schedulingv1alpha1.DeviceType]sets.Int
	allocationScorer          *resourceAllocationScorer
	nodeDevice                *nodeDevice
}

type AutopilotAllocator struct {
	state                     *preFilterState
	phaseBeingExecuted        string
	nodeDevice                *nodeDevice
	node                      *corev1.Node
	pod                       *corev1.Pod
	scorer                    *resourceAllocationScorer
	numaNodes                 bitmask.BitMask
	requestsPerInstance       map[schedulingv1alpha1.DeviceType]corev1.ResourceList
	desiredCountPerDeviceType map[schedulingv1alpha1.DeviceType]int
}

func (a *AutopilotAllocator) Prepare() *framework.Status {
	if a.requestsPerInstance != nil {
		return nil
	}
	state := a.state
	nodeDevice := a.nodeDevice
	requestsPerInstance, desiredCountPerDeviceType, status := a.calcRequestsAndCountByDeviceType(state.podRequests, nodeDevice, state.hints, state.primaryDeviceType)
	if !status.IsSuccess() {
		return status
	}

	for deviceType := range requestsPerInstance {
		if mustAllocateVF(a.state.hints[deviceType]) && !hasVirtualFunctions(nodeDevice, deviceType) {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("Insufficient %s VirtualFunctions", deviceType))
		}
	}

	a.requestsPerInstance = requestsPerInstance
	a.desiredCountPerDeviceType = desiredCountPerDeviceType
	return nil
}

func (a *AutopilotAllocator) Allocate(
	required, preferred map[schedulingv1alpha1.DeviceType]sets.Int,
	requiredDeviceResources, preemptibleDeviceResources map[schedulingv1alpha1.DeviceType]deviceResources,
) (apiext.DeviceAllocations, *framework.Status) {
	if status := a.Prepare(); !status.IsSuccess() {
		return nil, status
	}

	nodeDevice := a.filterNodeDevice(requiredDeviceResources, preemptibleDeviceResources)
	requestCtx := &requestContext{
		pod:                       a.pod,
		node:                      a.node,
		gpuRequirements:           a.state.gpuRequirements,
		hints:                     a.state.hints,
		hintSelectors:             a.state.hintSelectors,
		requestsPerInstance:       a.requestsPerInstance,
		desiredCountPerDeviceType: a.desiredCountPerDeviceType,
		allocationScorer:          a.scorer,
		required:                  required,
		preferred:                 preferred,
		nodeDevice:                a.nodeDevice,
	}
	var deviceAllocations apiext.DeviceAllocations
	var status *framework.Status
	if len(a.requestsPerInstance) > 1 {
		deviceAllocations, status = a.tryJointAllocate(requestCtx, a.state.jointAllocate, nodeDevice)
		if !status.IsSuccess() {
			return nil, status
		}
	}
	deviceAllocations, status = a.allocateDevices(requestCtx, nodeDevice, deviceAllocations)
	if !status.IsSuccess() {
		return nil, status
	}
	if len(deviceAllocations) == 0 {
		var reasons []string
		for deviceType := range a.requestsPerInstance {
			reasons = append(reasons, fmt.Sprintf("Insufficient %s devices", deviceType))
		}
		return nil, framework.NewStatus(framework.Unschedulable, reasons...)
	}
	return deviceAllocations, nil
}

func (a *AutopilotAllocator) filterNodeDevice(
	requiredDeviceResources, preemptibleDeviceResources map[schedulingv1alpha1.DeviceType]deviceResources,
) *nodeDevice {
	devices := map[schedulingv1alpha1.DeviceType][]int{}
	for deviceType := range a.requestsPerInstance {
		deviceInfos := a.nodeDevice.deviceInfos[deviceType]
		minors := sets.NewInt()
		selector := a.state.hintSelectors[deviceType][0]
		for _, deviceInfo := range deviceInfos {
			// TODO if a.numaNodes == nil && selector == nil return all device of this deviceType
			if a.numaNodes != nil {
				if deviceInfo.Topology == nil || (deviceInfo.Topology.NodeID != -1 && !a.numaNodes.IsSet(int(deviceInfo.Topology.NodeID))) {
					continue
				}
			}
			if selector == nil || selector.Matches(labels.Set(deviceInfo.Labels)) {
				minors.Insert(int(pointer.Int32Deref(deviceInfo.Minor, 0)))
			}
		}
		if minors.Len() > 0 {
			devices[deviceType] = minors.UnsortedList()
		}
	}
	nodeDevice := a.nodeDevice.filter(devices, a.state.hints, requiredDeviceResources, preemptibleDeviceResources)
	return nodeDevice
}

func (a *AutopilotAllocator) calcRequestsAndCountByDeviceType(
	podRequests map[schedulingv1alpha1.DeviceType]corev1.ResourceList,
	nodeDevice *nodeDevice, hints apiext.DeviceAllocateHints, primaryDeviceType schedulingv1alpha1.DeviceType,
) (map[schedulingv1alpha1.DeviceType]corev1.ResourceList, map[schedulingv1alpha1.DeviceType]int, *framework.Status) {
	requestPerInstance := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{}
	desiredCountPerDeviceType := map[schedulingv1alpha1.DeviceType]int{}
	for deviceType, requests := range podRequests {
		if quotav1.IsZero(requests) {
			continue
		}

		handler := deviceHandlers[deviceType]
		if handler == nil {
			continue
		}

		if primaryDeviceType != "" && deviceType != primaryDeviceType && nodeDevice.secondaryDeviceWellPlanned && a.phaseBeingExecuted != schedulingphase.Reserve {
			continue
		}

		requests, desiredCount, status := handler.CalcDesiredRequestsAndCount(a.node, a.pod, requests, nodeDevice, hints[deviceType], a.state)
		if !status.IsSuccess() {
			if status.Code() == framework.Skip {
				continue
			}
			return nil, nil, status
		}
		requestPerInstance[deviceType] = requests
		desiredCountPerDeviceType[deviceType] = desiredCount
	}
	return requestPerInstance, desiredCountPerDeviceType, nil
}

func (a *AutopilotAllocator) tryJointAllocate(requestCtx *requestContext, jointAllocate *apiext.DeviceJointAllocate, nodeDevice *nodeDevice) (apiext.DeviceAllocations, *framework.Status) {
	if jointAllocate == nil || len(jointAllocate.DeviceTypes) == 0 {
		return nil, nil
	}
	primaryDeviceType := jointAllocate.DeviceTypes[0]
	allocations, status := a.jointAllocate(nodeDevice, requestCtx, jointAllocate, primaryDeviceType, jointAllocate.DeviceTypes[1:])
	if !status.IsSuccess() {
		return nil, status
	}
	if jointAllocate.RequiredScope == apiext.SamePCIeDeviceJointAllocateScope {
		status = a.validateJointAllocation(jointAllocate, nodeDevice, allocations)
		if !status.IsSuccess() {
			return nil, status
		}
	}
	return allocations, nil
}

func (a *AutopilotAllocator) validateJointAllocation(jointAllocate *apiext.DeviceJointAllocate, nodeDevice *nodeDevice, deviceAllocations apiext.DeviceAllocations) *framework.Status {
	if jointAllocate == nil || len(jointAllocate.DeviceTypes) == 1 || jointAllocate.RequiredScope != apiext.SamePCIeDeviceJointAllocateScope {
		return nil
	}

	pcieGetterFn := func(deviceType schedulingv1alpha1.DeviceType) sets.String {
		pcies := sets.NewString()
		deviceInfos := nodeDevice.deviceInfos[deviceType]
		for _, allocation := range deviceAllocations[deviceType] {
			for _, v := range deviceInfos {
				if pointer.Int32Deref(v.Minor, 0) == allocation.Minor && v.Topology != nil {
					pcies.Insert(v.Topology.PCIEID)
					break
				}
			}
		}
		return pcies
	}

	primaryDeviceType := jointAllocate.DeviceTypes[0]
	primaryPCIes := pcieGetterFn(primaryDeviceType)

	for _, deviceType := range jointAllocate.DeviceTypes[1:] {
		secondaryPCIes := pcieGetterFn(deviceType)
		if !secondaryPCIes.Equal(primaryPCIes) {
			return framework.NewStatus(framework.Unschedulable, "node(s) Device Joint-Allocate rules violation")
		}
	}
	return nil
}

func (a *AutopilotAllocator) jointAllocate(nodeDevice *nodeDevice, requestCtx *requestContext, jointAllocate *apiext.DeviceJointAllocate, primaryDeviceType schedulingv1alpha1.DeviceType, secondaryDeviceTypes []schedulingv1alpha1.DeviceType) (apiext.DeviceAllocations, *framework.Status) {
	primaryAllocations, status := allocateDevices(
		requestCtx,
		nodeDevice,
		primaryDeviceType,
		requestCtx.requestsPerInstance[primaryDeviceType],
		requestCtx.desiredCountPerDeviceType[primaryDeviceType], nil)
	if !status.IsSuccess() {
		return nil, status
	}
	if len(primaryAllocations) == 0 {
		return nil, framework.NewStatus(framework.Unschedulable, "node(s) Insufficient primary device")
	}

	var secondaryDeviceAllocations apiext.DeviceAllocations
	if len(secondaryDeviceTypes) > 0 {
		pcieIDs := newPreferredPCIes(nodeDevice, primaryDeviceType, primaryAllocations)
		secondaryDeviceAllocations = apiext.DeviceAllocations{}
		for _, deviceType := range secondaryDeviceTypes {
			desiredCount := a.desiredCountPerDeviceType[deviceType]
			if jointAllocate != nil && jointAllocate.RequiredScope == apiext.SamePCIeDeviceJointAllocateScope && desiredCount < pcieIDs.Len() {
				desiredCount = pcieIDs.Len()
			}
			allocations, status := allocateDevices(
				requestCtx,
				nodeDevice,
				deviceType,
				requestCtx.requestsPerInstance[deviceType],
				desiredCount,
				pcieIDs)
			if !status.IsSuccess() {
				return nil, status
			}
			if len(allocations) != 0 {
				secondaryDeviceAllocations[deviceType] = allocations
			}
		}
	}

	result := apiext.DeviceAllocations{}
	result[primaryDeviceType] = append(result[primaryDeviceType], primaryAllocations...)
	for deviceType, allocations := range secondaryDeviceAllocations {
		result[deviceType] = append(result[deviceType], allocations...)
	}
	return result, nil
}

func (a *AutopilotAllocator) allocateDevices(requestCtx *requestContext, nodeDevice *nodeDevice, deviceAllocations apiext.DeviceAllocations) (apiext.DeviceAllocations, *framework.Status) {
	if deviceAllocations == nil {
		deviceAllocations = apiext.DeviceAllocations{}
	}
	for deviceType := range requestCtx.requestsPerInstance {
		if deviceAllocations[deviceType] != nil {
			continue
		}
		allocations, status := allocateDevices(requestCtx, nodeDevice, deviceType, requestCtx.requestsPerInstance[deviceType], requestCtx.desiredCountPerDeviceType[deviceType], nil)
		if !status.IsSuccess() {
			return nil, status
		}
		if len(allocations) != 0 {
			deviceAllocations[deviceType] = allocations
		}
	}
	return deviceAllocations, nil
}

func allocateDevices(requestCtx *requestContext, nodeDevice *nodeDevice, deviceType schedulingv1alpha1.DeviceType, requestPerInstance corev1.ResourceList, desiredCount int, preferredPCIEs sets.String) (allocations []*apiext.DeviceAllocation, status *framework.Status) {
	maxDesiredCount := desiredCount
	if len(preferredPCIEs) > maxDesiredCount {
		maxDesiredCount = len(preferredPCIEs)
	}
	if desiredCount == 0 {
		desiredCount = 1
	}
	if maxDesiredCount < desiredCount {
		maxDesiredCount = desiredCount
	}

	allocator := deviceAllocators[deviceType]
	if allocator != nil {
		return allocator.Allocate(requestCtx, nodeDevice, desiredCount, maxDesiredCount, nil)
	}

	allocations, status = defaultAllocateDevices(
		nodeDevice,
		requestCtx,
		requestPerInstance,
		desiredCount,
		maxDesiredCount,
		deviceType,
		preferredPCIEs,
	)
	if !status.IsSuccess() {
		return nil, status
	}
	return allocations, nil
}

func defaultAllocateDevices(
	nodeDevice *nodeDevice,
	requestCtx *requestContext,
	podRequestPerInstance corev1.ResourceList,
	desiredCount int,
	maxDesiredCount int,
	deviceType schedulingv1alpha1.DeviceType,
	preferredPCIEs sets.String,
) ([]*apiext.DeviceAllocation, *framework.Status) {
	freeDevices := nodeDevice.deviceFree[deviceType]
	nodeDeviceTotal := nodeDevice.deviceTotal[deviceType]
	vfAllocation := nodeDevice.vfAllocations[deviceType]
	required := requestCtx.required[deviceType]
	hint := requestCtx.hints[deviceType]
	vfSelector := requestCtx.hintSelectors[deviceType][1]

	deviceInfos := map[int]*schedulingv1alpha1.DeviceInfo{}
	for _, v := range nodeDevice.deviceInfos[deviceType] {
		minor := pointer.Int32Deref(v.Minor, 0)
		deviceInfos[int(minor)] = v
	}

	var allocations []*apiext.DeviceAllocation
	resourceMinorPairs := scoreDevices(podRequestPerInstance, nodeDeviceTotal, freeDevices, requestCtx.allocationScorer)
	resourceMinorPairs = sortDeviceResourcesByPreferredPCIe(resourceMinorPairs, preferredPCIEs, deviceInfos)
	// TODO 这里多执行了一次排序算法
	resourceMinorPairs = sortDeviceResourcesByMinor(resourceMinorPairs, requestCtx.preferred[deviceType])
	for _, resourceMinorPair := range resourceMinorPairs {
		if required.Len() > 0 && !required.Has(resourceMinorPair.minor) {
			continue
		}
		// Skip unhealthy Device instances with zero resources
		if quotav1.IsZero(resourceMinorPair.resources) {
			continue
		}
		satisfied, _ := quotav1.LessThanOrEqual(podRequestPerInstance, resourceMinorPair.resources)
		if !satisfied {
			continue
		}

		r := &apiext.DeviceAllocation{
			Minor:     int32(resourceMinorPair.minor),
			Resources: podRequestPerInstance,
		}
		if mustAllocateVF(hint) {
			vf := allocateVF(vfAllocation, deviceInfos, resourceMinorPair.minor, vfSelector)
			if vf == nil {
				continue
			}

			r.Extension = &apiext.DeviceAllocationExtension{
				VirtualFunctions: []apiext.VirtualFunction{
					{
						BusID: vf.BusID,
						Minor: int(vf.Minor),
					},
				},
			}
		}

		allocations = append(allocations, r)
		if len(allocations) == maxDesiredCount {
			break
		}
	}

	if len(allocations) < desiredCount {
		klog.V(5).Infof("node resource does not satisfy pod's multiple %v request, expect %v, got %v", deviceType, desiredCount, len(allocations))
		return nil, framework.NewStatus(framework.Unschedulable, fmt.Sprintf("Insufficient %s devices", deviceType))
	}
	return allocations, nil
}

func allocateVF(vfAllocation *VFAllocation, deviceInfos map[int]*schedulingv1alpha1.DeviceInfo, minor int, vfSelector labels.Selector) *schedulingv1alpha1.VirtualFunction {
	deviceInfo := deviceInfos[minor]
	if deviceInfo == nil {
		return nil
	}

	var allocated sets.String
	if vfAllocation != nil {
		allocated = vfAllocation.allocatedVFs[minor]
	}
	var remainingVFs []schedulingv1alpha1.VirtualFunction
	for _, vfGroup := range deviceInfo.VFGroups {
		if vfSelector == nil || vfSelector.Matches(labels.Set(vfGroup.Labels)) {
			for _, vf := range vfGroup.VFs {
				if !allocated.Has(vf.BusID) {
					remainingVFs = append(remainingVFs, vf)
				}
			}
		}
	}
	if len(remainingVFs) == 0 {
		return nil
	}
	sort.Slice(remainingVFs, func(i, j int) bool {
		return remainingVFs[i].BusID < remainingVFs[j].BusID
	})
	vf := &remainingVFs[0]
	return vf
}

func newPreferredPCIes(nodeDevice *nodeDevice, deviceType schedulingv1alpha1.DeviceType, allocations []*apiext.DeviceAllocation) sets.String {
	pcies := sets.NewString()
	for _, v := range allocations {
		for _, deviceInfo := range nodeDevice.deviceInfos[deviceType] {
			if pointer.Int32Deref(deviceInfo.Minor, 0) == v.Minor && deviceInfo.Topology != nil {
				pcies.Insert(deviceInfo.Topology.PCIEID)
				break
			}
		}
	}
	return pcies
}

func (a *AutopilotAllocator) score(
	requiredDeviceResources, preemptibleDeviceResources map[schedulingv1alpha1.DeviceType]deviceResources,
) (int64, *framework.Status) {
	if status := a.Prepare(); !status.IsSuccess() {
		return 0, status
	}

	nodeDevice := a.filterNodeDevice(requiredDeviceResources, preemptibleDeviceResources)

	var finalScore int64
	for deviceType, requests := range a.requestsPerInstance {
		if quotav1.IsZero(requests) {
			continue
		}
		deviceTotal := nodeDevice.deviceTotal[deviceType]
		if len(deviceTotal) > 0 {
			score := a.scorer.scoreNode(requests, deviceTotal, nodeDevice.deviceFree[deviceType])
			// TODO(joseph): Maybe different device types have different weights, but that's not currently supported.
			finalScore += score
		}
	}

	return finalScore, nil
}

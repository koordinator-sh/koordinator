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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
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

func hasVirtualFunctions(nodeDevice *nodeDevice, deviceType schedulingv1alpha1.DeviceType) bool {
	deviceInfos := nodeDevice.deviceInfos[deviceType]
	for _, v := range deviceInfos {
		if len(v.VFGroups) > 0 {
			return true
		}
	}
	return false
}

func mustAllocateVF(hint *apiext.DeviceHint) bool {
	return hint != nil && hint.VFSelector != nil
}

func preparePod(pod *corev1.Pod) (state *preFilterState, status *framework.Status) {
	state = &preFilterState{
		skip:               true,
		preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
		preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
	}

	requests, err := GetPodDeviceRequests(pod)
	if err != nil {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}

	state.podRequests = requests
	state.skip = len(requests) == 0
	if !state.skip {
		err = parsePodDeviceShareExtensions(pod, requests, state)
		if err != nil {
			return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
		}
		reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(pod)
		if err != nil {
			return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
		}
		state.hasReservationAffinity = reservationAffinity != nil
	}

	return
}

func GetPodDeviceRequests(pod *corev1.Pod) (map[schedulingv1alpha1.DeviceType]corev1.ResourceList, error) {
	podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	podRequests = quotav1.RemoveZeros(podRequests)

	var requests map[schedulingv1alpha1.DeviceType]corev1.ResourceList
	for deviceType, supportedResourceNames := range DeviceResourceNames {
		deviceRequest := quotav1.Mask(podRequests, supportedResourceNames)
		if quotav1.IsZero(deviceRequest) {
			continue
		}
		combination, err := ValidateDeviceRequest(deviceRequest)
		if err != nil {
			return nil, err
		}
		if requests == nil {
			requests = map[schedulingv1alpha1.DeviceType]corev1.ResourceList{}
		}
		requests[deviceType] = ConvertDeviceRequest(deviceRequest, combination)
	}
	return requests, nil
}

func parsePodDeviceShareExtensions(pod *corev1.Pod, podRequests map[schedulingv1alpha1.DeviceType]corev1.ResourceList, state *preFilterState) error {
	hints, err := apiext.GetDeviceAllocateHints(pod.Annotations)
	if err != nil {
		return fmt.Errorf("invalid DeviceAllocateHint annotation, err: %s", err.Error())
	}

	hintSelectors, err := newHintSelectors(hints)
	if err != nil {
		return err
	}

	jointAllocate, err := apiext.GetDeviceJointAllocate(pod.Annotations)
	if err != nil {
		return fmt.Errorf("invalid DeviceJointAllocate annotation, err: %s", err.Error())
	}

	if jointAllocate != nil {
		var deviceTypes []schedulingv1alpha1.DeviceType
		for _, deviceType := range jointAllocate.DeviceTypes {
			if h := hints[deviceType]; h != nil && h.AllocateStrategy == apiext.ApplyForAllDeviceAllocateStrategy {
				continue
			}
			requests := podRequests[deviceType]
			if !quotav1.IsZero(requests) {
				deviceTypes = append(deviceTypes, deviceType)
			}
		}
		jointAllocate.DeviceTypes = deviceTypes
	}

	state.hints = hints
	state.hintSelectors = hintSelectors
	state.jointAllocate = jointAllocate
	return nil
}

func newHintSelectors(hints apiext.DeviceAllocateHints) (map[schedulingv1alpha1.DeviceType][2]labels.Selector, error) {
	var hintSelectors map[schedulingv1alpha1.DeviceType][2]labels.Selector
	for deviceType, v := range hints {
		var selector labels.Selector
		var vfSelector labels.Selector
		if v.Selector != nil {
			var err error
			selector, err = util.GetFastLabelSelector(v.Selector)
			if err != nil {
				return nil, fmt.Errorf("invalid Selector of DeviceHint, deviceType: %s, err: %s", deviceType, err.Error())
			}
		}
		if v.VFSelector != nil {
			var err error
			vfSelector, err = util.GetFastLabelSelector(v.VFSelector)
			if err != nil {
				return nil, fmt.Errorf("invalid VFSelector of DeviceHint, deviceType: %s, err: %s", deviceType, err.Error())
			}
		}
		if hintSelectors == nil {
			hintSelectors = map[schedulingv1alpha1.DeviceType][2]labels.Selector{}
		}
		hintSelectors[deviceType] = [2]labels.Selector{selector, vfSelector}
	}
	return hintSelectors, nil
}

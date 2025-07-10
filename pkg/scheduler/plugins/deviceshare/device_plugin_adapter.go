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
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	// AnnotationBindTimestamp represents the bind time (unix nano) of the pod which can be used by
	// custom device plugins which are adapted for koord-scheduler to determine pod.
	//
	// Background: Device plugins cannot get pod manifest (including annotations) from kubelet so they have to
	// use the bind time to determine the target pod when multiple pods are assigned to the same node at the same time.
	AnnotationBindTimestamp = apiext.SchedulingDomainPrefix + "/bind-timestamp"

	// AnnotationGPUMinors represents a comma separated minor list of GPU(s) allocated for the pod.
	// A typical use case is used as env field ref to override the built-in NVIDIA_VISIBLE_DEVICES=all env of CUDA images.
	//
	// Background: The envs injected by device plugin would be overridden by the ones of pod image, so sometimes users
	// need to explicitly declare the envs in pod template to override the ones of pod image.
	AnnotationGPUMinors = apiext.SchedulingDomainPrefix + "/gpu-minors"

	// AnnotationPredicateTime represents the bind time (unix nano) of the pod which is used by
	// Huawei NPU device plugins to determine pod.
	AnnotationPredicateTime = "predicate-time"
	// AnnotationHuaweiNPUCore represents the NPU/vNPU allocation result for the pod which is used by
	// Huawei NPU device plugins to allocate NPU(s)/vNPU for pod.
	AnnotationHuaweiNPUCore = "huawei.com/npu-core"
)

// DevicePluginAdapter adapt koord-scheduler's device allocation result to the format that third party vendor's
// device plugin recognizes, so that user can directly use them as allocators with koord-scheduler without modification.
// This is especially useful when third party device plugin natively supports fine-grained device allocation or virtualization.
type DevicePluginAdapter interface {
	Adapt(object metav1.Object, allocation []*apiext.DeviceAllocation) error
}

var (
	defaultDevicePluginAdapter = &generalDevicePluginAdapter{
		clock: clock.RealClock{},
	}
	defaultGPUDevicePluginAdapter = &generalGPUDevicePluginAdapter{}
	gpuDevicePluginAdapterMap     = map[string]DevicePluginAdapter{
		apiext.GPUVendorHuawei: &huaweiGPUDevicePluginAdapter{
			clock: clock.RealClock{},
		},
	}
)

func (p *Plugin) adaptForDevicePlugin(object metav1.Object, allocationResult apiext.DeviceAllocations, nodeName string) error {
	if err := defaultDevicePluginAdapter.Adapt(object, nil); err != nil {
		return err
	}

	if gpuAllocation, ok := allocationResult[schedulingv1alpha1.GPU]; ok {
		if err := defaultGPUDevicePluginAdapter.Adapt(object, gpuAllocation); err != nil {
			return err
		}

		extendedHandle, ok := p.handle.(frameworkext.ExtendedHandle)
		if !ok {
			return fmt.Errorf("expect handle to be type frameworkext.ExtendedHandle, got %T", p.handle)
		}
		deviceLister := extendedHandle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Devices().Lister()
		device, err := deviceLister.Get(nodeName)
		if err != nil {
			klog.ErrorS(err, "Failed to get Device", "node", nodeName)
			return err
		}

		vendor := device.Labels[apiext.LabelGPUVendor]
		if adapter, ok := gpuDevicePluginAdapterMap[vendor]; ok {
			if err := adapter.Adapt(object, gpuAllocation); err != nil {
				return fmt.Errorf("failed to adapt for GPU device plugin of vendor %q: %w", vendor, err)
			}
		}
	}

	return nil
}

// generalDevicePluginAdapter annotates the bind timestamp to pod which enables users to write their own device plugins
// that can be used with koord-scheduler.
type generalDevicePluginAdapter struct {
	clock clock.Clock
}

func (a *generalDevicePluginAdapter) Adapt(object metav1.Object, _ []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationBindTimestamp] = strconv.FormatInt(a.clock.Now().UnixNano(), 10)
	return nil
}

// generalGPUDevicePluginAdapter annotates necessary information to pod which resolves problems when using device plugin
// with koord-scheduler.
type generalGPUDevicePluginAdapter struct {
}

func (a *generalGPUDevicePluginAdapter) Adapt(object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationGPUMinors] = buildGPUMinorsStr(allocation)
	return nil
}

type huaweiGPUDevicePluginAdapter struct {
	clock clock.Clock
}

func (a *huaweiGPUDevicePluginAdapter) Adapt(object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationPredicateTime] = strconv.FormatInt(a.clock.Now().UnixNano(), 10)
	if allocation[0].Extension != nil && allocation[0].Extension.GPUSharedResourceTemplate != "" {
		// vNPU
		object.GetAnnotations()[AnnotationHuaweiNPUCore] = fmt.Sprintf("%d-%s", allocation[0].Minor, allocation[0].Extension.GPUSharedResourceTemplate)
	} else {
		// full NPU
		object.GetAnnotations()[AnnotationHuaweiNPUCore] = buildGPUMinorsStr(allocation)
	}
	return nil
}

func buildGPUMinorsStr(allocation []*apiext.DeviceAllocation) string {
	minors := make([]string, 0, len(allocation))
	for _, alloc := range allocation {
		minors = append(minors, strconv.Itoa(int(alloc.Minor)))
	}
	return strings.Join(minors, ",")
}

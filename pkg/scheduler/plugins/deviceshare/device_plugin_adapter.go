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
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util"
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

	// AnnotationCambriconDsmluAssigned is used by Cambricon dynamic sMLU device plugins to determine pod.
	AnnotationCambriconDsmluAssigned = "CAMBRICON_DSMLU_ASSIGHED"
	// AnnotationCambriconDsmluProfile represents the dynamic sMLU allocation result for the pod which is used by
	// Cambricon dynamic sMLU device plugins to allocate sMLU for pod.
	AnnotationCambriconDsmluProfile = "CAMBRICON_DSMLU_PROFILE"
	// AnnotationCambriconDsmluLock is annotated to node by scheduler to prevent scheduling multiple pods
	// with dynamic sMLU to the same node which would make Cambricon device plugin unable to determine pod.
	// Cambricon dynamic sMLU device plugin will automatically remove it after allocation of a pod.
	AnnotationCambriconDsmluLock = "cambricon.com/dsmlu.lock"
	// cambriconVMemoryUnit is the minial virtual memory unit of Cambricon dynamic sMLU currently supported.
	// This should always match the min-dsmlu-unit arg of device plugin.
	cambriconVMemoryUnit = 256 * 1024 * 1024
)

const (
	nodeLockTimeout = 5 * time.Minute
)

// DevicePluginAdapter adapt koord-scheduler's device allocation result to the format that third party vendor's
// device plugin recognizes, so that user can directly use them as allocators with koord-scheduler without modification.
// This is especially useful when third party device plugin natively supports fine-grained device allocation or virtualization.
type DevicePluginAdapter interface {
	Adapt(ctx *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error
}

type DevicePluginAdaptContext struct {
	context.Context
	node *corev1.Node
}

var (
	defaultDevicePluginAdapter DevicePluginAdapter = &generalDevicePluginAdapter{
		clock: clock.RealClock{},
	}
	defaultDevicePluginAdapterMap = map[schedulingv1alpha1.DeviceType]DevicePluginAdapter{
		schedulingv1alpha1.GPU: &generalGPUDevicePluginAdapter{},
	}
	gpuDevicePluginAdapterMap = map[string]DevicePluginAdapter{
		apiext.GPUVendorHuawei: &huaweiGPUDevicePluginAdapter{
			clock: clock.RealClock{},
		},
		apiext.GPUVendorCambricon: &cambriconGPUDevicePluginAdapter{
			clock: clock.RealClock{},
		},
	}
)

func (p *Plugin) adaptForDevicePlugin(ctx context.Context, object metav1.Object, allocationResult apiext.DeviceAllocations, nodeName string) error {
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		klog.ErrorS(err, "Failed to get NodeInfo", "node", nodeName)
		return err
	}
	adaptCtx := &DevicePluginAdaptContext{
		Context: ctx,
		node:    nodeInfo.Node().DeepCopy(),
	}

	if err := defaultDevicePluginAdapter.Adapt(adaptCtx, object, nil); err != nil {
		return err
	}

	for deviceType, allocation := range allocationResult {
		if adapter, ok := defaultDevicePluginAdapterMap[deviceType]; ok {
			if err := adapter.Adapt(adaptCtx, object, allocation); err != nil {
				return err
			}
		}

		if deviceType == schedulingv1alpha1.GPU {
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
				if err := adapter.Adapt(adaptCtx, object, allocation); err != nil {
					return fmt.Errorf("failed to adapt for GPU device plugin of vendor %q: %w", vendor, err)
				}
			}
		}
	}

	if _, err := util.PatchNode(ctx, p.handle.ClientSet(), nodeInfo.Node(), adaptCtx.node); err != nil {
		return fmt.Errorf("failed to patch node %q: %v", adaptCtx.node.Name, err)
	}

	return nil
}

// generalDevicePluginAdapter annotates the bind timestamp to pod which enables users to write their own device plugins
// that can be used with koord-scheduler.
type generalDevicePluginAdapter struct {
	clock clock.Clock
}

func (a *generalDevicePluginAdapter) Adapt(_ *DevicePluginAdaptContext, object metav1.Object, _ []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationBindTimestamp] = strconv.FormatInt(a.clock.Now().UnixNano(), 10)
	return nil
}

// generalGPUDevicePluginAdapter annotates necessary information to pod which resolves problems when using device plugin
// with koord-scheduler.
type generalGPUDevicePluginAdapter struct {
}

func (a *generalGPUDevicePluginAdapter) Adapt(ctx *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationGPUMinors] = buildGPUMinorsStr(allocation)
	if object.GetLabels()[apiext.LabelGPUIsolationProvider] == string(apiext.GPUIsolationProviderHAMICore) {
		object.GetLabels()[apiext.LabelHAMIVGPUNodeName] = ctx.node.Name
	}
	return nil
}

type huaweiGPUDevicePluginAdapter struct {
	clock clock.Clock
}

func (a *huaweiGPUDevicePluginAdapter) Adapt(_ *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
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

type cambriconGPUDevicePluginAdapter struct {
	clock clock.Clock
}

func (a *cambriconGPUDevicePluginAdapter) Adapt(ctx *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	if len(allocation) > 1 {
		return fmt.Errorf("multiple gpu share is not supported on device side")
	}

	core, ok := allocation[0].Resources[apiext.ResourceGPUCore]
	if !ok {
		return fmt.Errorf("gpu core resource is required")
	}
	vcore := core.Value()

	memory := allocation[0].Resources[apiext.ResourceGPUMemory]
	if memory.Value() < cambriconVMemoryUnit {
		return fmt.Errorf("gpu memory must not be less than %d bytes", cambriconVMemoryUnit)
	}
	vmemory := memory.Value() / cambriconVMemoryUnit

	if err := lockNode(ctx.node, object, AnnotationCambriconDsmluLock, a.clock.Now()); err != nil {
		return fmt.Errorf("failed to lock node: %v", err)
	}

	object.GetAnnotations()[AnnotationCambriconDsmluAssigned] = "false"
	object.GetAnnotations()[AnnotationCambriconDsmluProfile] = fmt.Sprintf("%d_%d_%d", allocation[0].Minor, vcore, vmemory)
	return nil
}

func lockNode(node *corev1.Node, object metav1.Object, lockKey string, lockTime time.Time) error {
	if val, ok := node.Annotations[lockKey]; ok {
		lockTime, err := time.Parse(time.RFC3339, strings.Split(val, ",")[0])
		if err != nil {
			return err
		}
		if time.Since(lockTime) > nodeLockTimeout {
			// this should never happen in normal cases, and we don't want to lock the node forever
			unlockNode(node, lockKey)
		} else {
			return fmt.Errorf("node %q has been locked", node.Name)
		}
	}
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[lockKey] = fmt.Sprintf("%s,%s,%s", lockTime.Format(time.RFC3339), object.GetNamespace(), object.GetName())
	return nil
}

func unlockNode(node *corev1.Node, lockKey string) {
	if _, ok := node.Annotations[lockKey]; !ok {
		return
	}
	delete(node.Annotations, lockKey)
}

func buildGPUMinorsStr(allocation []*apiext.DeviceAllocation) string {
	minors := make([]string, 0, len(allocation))
	for _, alloc := range allocation {
		minors = append(minors, strconv.Itoa(int(alloc.Minor)))
	}
	return strings.Join(minors, ",")
}

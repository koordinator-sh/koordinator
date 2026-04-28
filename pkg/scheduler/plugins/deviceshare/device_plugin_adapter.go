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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corelister "k8s.io/client-go/listers/core/v1"
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

	// AnnotationHAMiLock is annotated to node by scheduler to prevent scheduling multiple pods
	// with devices of the same vendor to the same node which would make the vendor's device plugin unable to determine pod.
	// The vendor's device plugin will automatically remove it after allocation of a pod.
	// This annotation key was originally defined in HAMi and then be adopted by some vendors.
	// Vendors that currently need this lock: metax
	AnnotationHAMiLock = "hami.io/mutex.lock"

	// AnnotationPredicateTime represents the bind time (unix nano) of the pod which is used by
	// Huawei NPU device plugins to determine pod.
	AnnotationPredicateTime = "predicate-time"
	// AnnotationHuaweiNPUCore represents the NPU/vNPU allocation result for the pod which is used by
	// Huawei NPU device plugins to allocate NPU(s)/vNPU for pod.
	AnnotationHuaweiNPUCore = "huawei.com/npu-core"
	// AnnotationHuaweiAscend310P represents the NPU allocation result for the pod which is used by
	// Huawei Ascend 310P device plugins to allocate NPU(s) for pod.
	AnnotationHuaweiAscend310P = "huawei.com/Ascend310P"

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

	// AnnotationMetaXGPUDevicesAllocated represents the GPU/sGPU allocation result for the pod which is used by
	// MetaX GPU device plugins to allocate GPU(s)/sGPU(s) for pod.
	AnnotationMetaXGPUDevicesAllocated = "metax-tech.com/gpu-devices-allocated"
	// metaxVRamUnit is the minial virtual memory unit of MetaX sGPU currently supported.
	metaxVRamUnit = 1 * 1024 * 1024

	// AnnotationHygonDCUDevicesAllocated represents the DCU allocation result for the pod which is used by
	// Hygon DCU device plugins to allocate DCU for pod.
	// The format is: {device-id},{device-type},{memory},{cores}:{container-index};
	AnnotationHygonDCUDevicesAllocated = "hami.io/dcu-devices-allocated"
	// AnnotationHygonDCUDevicesToAllocate represents the DCU devices to be allocated.
	AnnotationHygonDCUDevicesToAllocate = "hami.io/dcu-devices-to-allocate"
	// AnnotationHygonBindPhase represents the bind phase of the pod.
	AnnotationHygonBindPhase = "hami.io/bind-phase"
	// AnnotationHygonBindTime represents the bind time of the pod.
	AnnotationHygonBindTime = "hami.io/bind-time"
	// AnnotationHygonVgpuTime represents the vgpu time of the pod.
	AnnotationHygonVgpuTime = "hami.io/vgpu-time"
	// AnnotationHygonContainerIndex represents the container index for DCU allocation.
	AnnotationHygonContainerIndex = "hygon.com/container-index"

	// hygonContainerResourceAnnotationSuffix is the per-container annotation key suffix
	// used by HAMi DCU device plugin to read the container's DCU resource summary.
	// The full annotation key is "<container-name>-hygon.com/<resource>" (see HAMi
	// pkg/device/hygon/device.go).
	hygonContainerResourceAnnotationSuffix = "-hygon.com"
	// hygonContainerResourceCores / Mem / Num are the resource names used in the
	// per-container resource summary annotations.
	hygonContainerResourceCores = "dcucores"
	hygonContainerResourceMem   = "dcumem"
	hygonContainerResourceNum   = "dcunum"

	// hygonBindPhaseAllocating indicates the device plugin has not yet processed
	// this pod. The device plugin will flip this annotation to "success" after
	// Allocate() completes.
	hygonBindPhaseAllocating = "allocating"

	// HAMi annotation encoding separators (see HAMi pkg/device/devices.go).
	hygonDeviceSepSymbol    = ":" // OneContainerMultiDeviceSplitSymbol
	hygonContainerSepSymbol = ";" // OnePodMultiContainerSplitSymbol
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

// DevicePluginAdapterWithNodeLock is DevicePluginAdapter which utilize node lock to prevent device plugin
// from unable to determine pod.
type DevicePluginAdapterWithNodeLock interface {
	DevicePluginAdapter
	NodeLockKey() string
}

type DevicePluginAdaptContext struct {
	context.Context
	nodeLister   corelister.NodeLister
	podLister    corelister.PodLister
	node         *corev1.Node
	modifiedNode *corev1.Node
	nodeLock     *sync.Mutex
	gpuModel     string
}

var (
	defaultDevicePluginAdapter    DevicePluginAdapter = &generalDevicePluginAdapter{}
	defaultDevicePluginAdapterMap                     = map[schedulingv1alpha1.DeviceType]DevicePluginAdapter{
		schedulingv1alpha1.GPU: &generalGPUDevicePluginAdapter{},
	}
	gpuDevicePluginAdapterMap = map[string]DevicePluginAdapter{
		apiext.GPUVendorHuawei:    &huaweiGPUDevicePluginAdapter{},
		apiext.GPUVendorCambricon: &cambriconGPUDevicePluginAdapter{},
		apiext.GPUVendorMetaX:     &metaxDevicePluginAdapter{},
		apiext.GPUVendorHygon:     &hygonDCUDevicePluginAdapter{},
	}
)

var (
	dpAdapterClock clock.Clock = clock.RealClock{} // for testing

	nodeLockMap     = make(map[string]*sync.Mutex)
	nodeLockMapLock sync.Mutex
)

func (p *Plugin) adaptForDevicePlugin(ctx context.Context, object metav1.Object, allocationResult apiext.DeviceAllocations, nodeName string) error {
	adaptCtx := &DevicePluginAdaptContext{
		Context:    ctx,
		nodeLister: p.handle.SharedInformerFactory().Core().V1().Nodes().Lister(),
		podLister:  p.handle.SharedInformerFactory().Core().V1().Pods().Lister(),
	}
	var err error
	adaptCtx.node, err = adaptCtx.nodeLister.Get(nodeName)
	if err != nil {
		klog.ErrorS(err, "Failed to get Node", "node", nodeName)
		return err
	}

	defer func() {
		if adaptCtx.nodeLock != nil {
			adaptCtx.nodeLock.Unlock()
		}
	}()

	if err := adapt(adaptCtx, defaultDevicePluginAdapter, object, nil, nodeName); err != nil {
		return err
	}

	for deviceType, allocation := range allocationResult {
		if adapter, ok := defaultDevicePluginAdapterMap[deviceType]; ok {
			if err := adapt(adaptCtx, adapter, object, allocation, nodeName); err != nil {
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

			vendor, model := device.Labels[apiext.LabelGPUVendor], device.Labels[apiext.LabelGPUModel]
			adaptCtx.gpuModel = model
			if adapter, ok := gpuDevicePluginAdapterMap[vendor]; ok {
				if err := adapt(adaptCtx, adapter, object, allocation, nodeName); err != nil {
					return fmt.Errorf("failed to adapt for GPU device plugin of vendor %q: %w", vendor, err)
				}
			}
		}
	}

	if adaptCtx.modifiedNode != nil {
		if _, err := util.PatchNode(ctx, p.handle.ClientSet(), adaptCtx.node, adaptCtx.modifiedNode); err != nil {
			return fmt.Errorf("failed to patch node %q: %v", nodeName, err)
		}
	}

	return nil
}

func adapt(ctx *DevicePluginAdaptContext, adapter DevicePluginAdapter, object metav1.Object, allocation []*apiext.DeviceAllocation, nodeName string) error {
	if err := adapter.Adapt(ctx, object, allocation); err != nil {
		return err
	}
	if adapterWithNodeLock, ok := adapter.(DevicePluginAdapterWithNodeLock); ok {
		if ctx.nodeLock == nil {
			ctx.nodeLock = getNodeLock(nodeName)
			ctx.nodeLock.Lock()
			var err error
			// always get latest node here to avoid lock conflict
			ctx.node, err = ctx.nodeLister.Get(nodeName)
			if err != nil {
				return fmt.Errorf("failed to get node %q: %v", nodeName, err)
			}
			ctx.modifiedNode = ctx.node.DeepCopy()
		}
		if err := lockNode(ctx, object, adapterWithNodeLock.NodeLockKey(), dpAdapterClock.Now()); err != nil {
			return fmt.Errorf("failed to lock node: %v", err)
		}
	}
	return nil
}

func getNodeLock(nodeName string) *sync.Mutex {
	nodeLockMapLock.Lock()
	defer nodeLockMapLock.Unlock()
	if _, ok := nodeLockMap[nodeName]; !ok {
		nodeLockMap[nodeName] = &sync.Mutex{}
	}
	return nodeLockMap[nodeName]
}

func deleteNodeLock(nodeName string) {
	nodeLockMapLock.Lock()
	defer nodeLockMapLock.Unlock()
	delete(nodeLockMap, nodeName)
}

func lockNode(ctx *DevicePluginAdaptContext, object metav1.Object, lockKey string, lockTime time.Time) error {
	node := ctx.modifiedNode
	if val, ok := node.Annotations[lockKey]; ok {
		parts := strings.Split(val, ",")
		if len(parts) != 3 {
			return fmt.Errorf("invalid node lock value %q", val)
		}
		lockTimeStr, lockPodNs, lockPodName := parts[0], parts[1], parts[2]
		lockTime, err := time.Parse(time.RFC3339, lockTimeStr)
		if err != nil {
			return err
		}
		if time.Since(lockTime) > nodeLockTimeout {
			// this should never happen in normal cases, and we don't want to lock the node forever
			unlockNode(node, lockKey)
		} else if _, err := ctx.podLister.Pods(lockPodNs).Get(lockPodName); err != nil && apierrors.IsNotFound(err) {
			// original locker pod has been deleted, safe to unlock
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

// generalDevicePluginAdapter annotates the bind timestamp to pod which enables users to write their own device plugins
// that can be used with koord-scheduler.
type generalDevicePluginAdapter struct{}

func (a *generalDevicePluginAdapter) Adapt(_ *DevicePluginAdaptContext, object metav1.Object, _ []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationBindTimestamp] = strconv.FormatInt(dpAdapterClock.Now().UnixNano(), 10)
	return nil
}

// generalGPUDevicePluginAdapter annotates necessary information to pod which resolves problems when using device plugin
// with koord-scheduler.
type generalGPUDevicePluginAdapter struct {
}

func (a *generalGPUDevicePluginAdapter) Adapt(ctx *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationGPUMinors] = buildGPUMinorsStr(allocation, "")
	if object.GetLabels()[apiext.LabelGPUIsolationProvider] == string(apiext.GPUIsolationProviderHAMICore) {
		object.GetLabels()[apiext.LabelHAMIVGPUNodeName] = ctx.node.Name
	}
	return nil
}

type huaweiGPUDevicePluginAdapter struct{}

func (a *huaweiGPUDevicePluginAdapter) Adapt(ctx *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	object.GetAnnotations()[AnnotationPredicateTime] = strconv.FormatInt(dpAdapterClock.Now().UnixNano(), 10)
	switch ctx.gpuModel {
	case "Ascend-310P3-300I-DUO":
		object.GetAnnotations()[AnnotationHuaweiAscend310P] = buildGPUMinorsStr(allocation, "Ascend310P-")
	default:
		if allocation[0].Extension != nil && allocation[0].Extension.GPUSharedResourceTemplate != "" {
			// vNPU
			object.GetAnnotations()[AnnotationHuaweiNPUCore] = fmt.Sprintf("%d-%s", allocation[0].Minor, allocation[0].Extension.GPUSharedResourceTemplate)
		} else {
			// full NPU
			object.GetAnnotations()[AnnotationHuaweiNPUCore] = buildGPUMinorsStr(allocation, "")
		}
	}
	return nil
}

type cambriconGPUDevicePluginAdapter struct{}

func (a *cambriconGPUDevicePluginAdapter) Adapt(_ *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
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

	object.GetAnnotations()[AnnotationCambriconDsmluAssigned] = "false"
	object.GetAnnotations()[AnnotationCambriconDsmluProfile] = fmt.Sprintf("%d_%d_%d", allocation[0].Minor, vcore, vmemory)
	return nil
}

func (a *cambriconGPUDevicePluginAdapter) NodeLockKey() string {
	return AnnotationCambriconDsmluLock
}

type metaxDevicePluginAdapter struct{}

type metaxContainerDeviceRequest struct {
	UUID    string `json:"uuid"`
	Compute int32  `json:"compute"`
	VRam    int32  `json:"vRam"`
}

func (a *metaxDevicePluginAdapter) Adapt(_ *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	deviceRequests := make([]metaxContainerDeviceRequest, 0, len(allocation))
	for _, alloc := range allocation {
		core, ok := alloc.Resources[apiext.ResourceGPUCore]
		if !ok {
			return fmt.Errorf("gpu core resource is required")
		}
		compute := core.Value()

		memory := alloc.Resources[apiext.ResourceGPUMemory]
		if memory.Value() < metaxVRamUnit {
			return fmt.Errorf("gpu memory must not be less than %d bytes", metaxVRamUnit)
		}
		vram := memory.Value() / metaxVRamUnit

		deviceRequests = append(deviceRequests, metaxContainerDeviceRequest{
			UUID:    alloc.ID,
			Compute: int32(compute),
			VRam:    int32(vram),
		})
	}
	allocatedData, err := json.Marshal([][]metaxContainerDeviceRequest{deviceRequests})
	if err != nil {
		return err
	}

	object.GetAnnotations()[AnnotationMetaXGPUDevicesAllocated] = string(allocatedData)
	return nil
}

func (a *metaxDevicePluginAdapter) NodeLockKey() string {
	return AnnotationHAMiLock
}

// hygonDCUDevicePluginAdapter adapts koord-scheduler's device allocation result to the format that
// Hygon DCU device plugin recognizes. Hygon DCU uses HAMi scheduler plugin format.
//
// HAMi format reference: pkg/device/devices.go in Project-HAMi/HAMi
//   - EncodeContainerDevices: "UUID,Type,Memory,Cores:" for each device, joined together
//   - EncodePodSingleDevice:  appends ";" after each container's segment
//
// So for a single container with one DCU, the final string looks like:
//
//	"DCU-T6V51625061001,DCU,31744,50:;"
//
// For multi container case, each container gets its own segment separated by ";",
// and a container that does not request DCU gets an empty segment so that the
// index between annotation segments and pod containers stays aligned.
type hygonDCUDevicePluginAdapter struct{}

// hygonDCUDeviceType is the device type string used in HAMi DCU device plugin's
// per-container device encoding (e.g. "UUID,DCU,Memory,Cores:"). It must match
// the constant `HygonDCUDevice` defined in HAMi project (pkg/device/hygon/device.go).
const hygonDCUDeviceType = "DCU"

// hygonDCUDeviceIDPrefix is the mandatory prefix for Hygon DCU device UUID in
// HAMi annotations. HAMi DCU device plugin registers each card on the node as
// "DCU-<serial>" (see HAMi pkg/device/hygon/device.go) and matches the pod
// allocation against that registered UUID. If the prefix is missing the device
// plugin will reject the pod with "device request not found".
const hygonDCUDeviceIDPrefix = "DCU-"

func (a *hygonDCUDevicePluginAdapter) Adapt(ctx *DevicePluginAdaptContext, object metav1.Object, allocation []*apiext.DeviceAllocation) error {
	if len(allocation) == 0 {
		return nil
	}

	pod, ok := object.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("object is not a pod")
	}
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	deviceSegment, err := buildHygonDeviceSegment(pod, allocation)
	if err != nil {
		return err
	}

	dcuContainerIndex := findHygonDCUContainerIndex(pod)
	allocationStr := composeHygonAllocationString(pod, dcuContainerIndex, deviceSegment)

	setHygonAllocationAnnotations(annotations, allocationStr)
	setHygonContainerResourceAnnotations(annotations, pod, dcuContainerIndex, allocation)

	// Set vGPU node label so that HAMi-aware components can discover the node.
	object.GetLabels()[apiext.LabelHAMIVGPUNodeName] = ctx.node.Name

	return nil
}

func (a *hygonDCUDevicePluginAdapter) NodeLockKey() string {
	return AnnotationHAMiLock
}

// buildHygonDeviceSegment builds the HAMi-format device segment for the
// DCU-requesting container, e.g. "DCU-xxxx,DCU,31744,50:DCU-yyyy,DCU,31744,50:".
func buildHygonDeviceSegment(pod *corev1.Pod, allocation []*apiext.DeviceAllocation) (string, error) {
	var b strings.Builder
	for _, alloc := range allocation {
		core, ok := alloc.Resources[apiext.ResourceGPUCore]
		if !ok {
			return "", fmt.Errorf("gpu core resource is required for DCU allocation")
		}
		memory := alloc.Resources[apiext.ResourceGPUMemory]
		if memory.IsZero() {
			return "", fmt.Errorf("gpu memory resource must be greater than zero for DCU allocation")
		}
		memoryMB := memory.Value() / (1024 * 1024)

		deviceID := normalizeHygonDeviceID(alloc.ID, alloc.Minor)

		// Format: UUID,Type,Memory,Cores: (":" is the device separator, not a container index)
		fmt.Fprintf(&b, "%s,%s,%d,%d%s", deviceID, hygonDCUDeviceType, memoryMB, core.Value(), hygonDeviceSepSymbol)
	}
	return b.String(), nil
}

// normalizeHygonDeviceID returns the device UUID that HAMi DCU device plugin
// expects in the allocation annotation. It falls back to the device minor when
// the upstream allocation did not provide an ID, and always ensures the "DCU-"
// prefix is present.
func normalizeHygonDeviceID(rawID string, minor int32) string {
	id := rawID
	if id == "" {
		id = strconv.Itoa(int(minor))
	}
	if !strings.HasPrefix(id, hygonDCUDeviceIDPrefix) {
		id = hygonDCUDeviceIDPrefix + id
	}
	return id
}

// findHygonDCUContainerIndex returns the index of the container that requests
// hygon DCU resources. It returns 0 as a fallback so that single-container pods
// that only declare koordinator.sh/gpu-* resources still work.
func findHygonDCUContainerIndex(pod *corev1.Pod) int {
	for i, container := range pod.Spec.Containers {
		if _, ok := container.Resources.Limits[apiext.ResourceHygonDCUNum]; ok {
			return i
		}
		if _, ok := container.Resources.Requests[apiext.ResourceHygonDCUNum]; ok {
			return i
		}
	}
	return 0
}

// composeHygonAllocationString places deviceSegment at the position of the
// DCU-requesting container, leaving the other containers' segments empty so
// that the device plugin's PodDevices index stays aligned with the container
// index. The result always ends with a trailing container separator.
func composeHygonAllocationString(pod *corev1.Pod, dcuContainerIndex int, deviceSegment string) string {
	segments := make([]string, len(pod.Spec.Containers))
	if dcuContainerIndex >= 0 && dcuContainerIndex < len(segments) {
		segments[dcuContainerIndex] = deviceSegment
	}
	return strings.Join(segments, hygonContainerSepSymbol) + hygonContainerSepSymbol
}

// setHygonAllocationAnnotations writes the allocation result and the bind-phase
// metadata expected by HAMi DCU device plugin.
//
// Per HAMi PatchAnnotations behavior (see HAMi pkg/device/hygon/device.go), the
// scheduler initially sets both "to-allocate" and "allocated" annotations to
// the same content. The DCU device plugin will then:
//   - read the request from "to-allocate" during Allocate()
//   - clear "to-allocate" once allocation is done
//   - keep "allocated" as the final allocation record
//
// Setting only "to-allocate" (or setting "to-allocate" to empty up front)
// causes the device plugin to fail with "device request not found".
func setHygonAllocationAnnotations(annotations map[string]string, allocationStr string) {
	now := strconv.FormatInt(dpAdapterClock.Now().Unix(), 10)
	annotations[AnnotationHygonDCUDevicesAllocated] = allocationStr
	annotations[AnnotationHygonDCUDevicesToAllocate] = allocationStr
	annotations[AnnotationHygonBindPhase] = hygonBindPhaseAllocating
	annotations[AnnotationHygonBindTime] = now
	annotations[AnnotationHygonVgpuTime] = now
}

// setHygonContainerResourceAnnotations writes the per-container DCU resource
// summary annotations (dcucores / dcumem / dcunum) expected by HAMi DCU device
// plugin. Only the container that actually requests DCU gets the summary so
// that other containers do not mislead the device plugin.
func setHygonContainerResourceAnnotations(
	annotations map[string]string,
	pod *corev1.Pod,
	dcuContainerIndex int,
	allocation []*apiext.DeviceAllocation,
) {
	var totalCores, totalMemoryMB int64
	for _, alloc := range allocation {
		if core, ok := alloc.Resources[apiext.ResourceGPUCore]; ok {
			totalCores += core.Value()
		}
		if memory, ok := alloc.Resources[apiext.ResourceGPUMemory]; ok {
			totalMemoryMB += memory.Value() / (1024 * 1024)
		}
	}
	containerName := pod.Spec.Containers[dcuContainerIndex].Name
	keyPrefix := containerName + hygonContainerResourceAnnotationSuffix + "/"
	annotations[keyPrefix+hygonContainerResourceCores] = strconv.FormatInt(totalCores, 10)
	annotations[keyPrefix+hygonContainerResourceMem] = strconv.FormatInt(totalMemoryMB, 10)
	annotations[keyPrefix+hygonContainerResourceNum] = strconv.FormatInt(int64(len(allocation)), 10)
	annotations[AnnotationHygonContainerIndex] = fmt.Sprintf("%d,", dcuContainerIndex)
}

func buildGPUMinorsStr(allocation []*apiext.DeviceAllocation, prefix string) string {
	minors := make([]string, 0, len(allocation))
	for _, alloc := range allocation {
		minors = append(minors, prefix+strconv.Itoa(int(alloc.Minor)))
	}
	return strings.Join(minors, ",")
}

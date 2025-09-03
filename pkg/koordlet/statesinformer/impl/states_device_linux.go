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

package impl

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func (s *statesInformer) reportDevice() {
	node := s.GetNode()
	if node == nil {
		klog.Errorf("node is nil")
		return
	}
	device := s.buildBasicDevice(node)
	skipBuildGPU := false
	func() {
		xpuRawDevices := getXPUDevices(s.metricsCache)
		xpuDevices := s.buildXPUDevice(xpuRawDevices)
		annotations := s.buildXPUDeviceAnnotations(xpuRawDevices)
		labels := s.buildXPUDeviceLabels(xpuRawDevices)
		klog.V(4).Infof("Device: annotations: %s, labels %s", annotations, labels)
		if len(xpuDevices) != 0 {
			klog.V(2).Infof("report xpu devices from device infos directory")
			device.Spec.Devices = append(device.Spec.Devices, xpuDevices...)
			device.ObjectMeta.Annotations = annotations
			device.ObjectMeta.Labels = labels
			skipBuildGPU = true
		}
	}()
	func() {
		// if xpu devices exist, report xpu devices; others, fallback to report gpu devices from nvml
		if s.config.XPUEnforceCollectFromDeviceInfos || skipBuildGPU {
			return
		}
		gpuDevices := s.buildGPUDevice()
		if len(gpuDevices) == 0 {
			return
		}
		klog.V(2).Infof("report gpu devices from nvml")
		gpuModel, gpuDriverVer := s.getGPUDriverAndModelFunc()
		s.fillGPUDevice(device, gpuDevices, gpuModel, gpuDriverVer)
	}()
	func() {
		rdmaDevices := s.buildRDMADevice()
		if len(rdmaDevices) != 0 {
			device.Spec.Devices = append(device.Spec.Devices, rdmaDevices...)
		}
	}()

	err := s.updateDevice(device)
	if err == nil {
		klog.V(4).Infof("successfully update Device %s", node.Name)
		return
	}
	if !errors.IsNotFound(err) {
		klog.Errorf("Failed to updateDevice %s, err: %v", node.Name, err)
		return
	}

	err = s.createDevice(device)
	if err == nil {
		klog.V(4).Infof("successfully create Device %s", node.Name)
	} else {
		klog.Errorf("Failed to create Device %s, err: %v", node.Name, err)
	}
}

func (s *statesInformer) buildBasicDevice(node *corev1.Node) *schedulingv1alpha1.Device {
	blocker := true
	device := &schedulingv1alpha1.Device{
		ObjectMeta: metav1.ObjectMeta{
			Name: node.Name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					Kind:               "Node",
					Name:               node.Name,
					UID:                node.UID,
					Controller:         &blocker,
					BlockOwnerDeletion: &blocker,
				},
			},
		},
	}

	return device
}

func (s *statesInformer) fillGPUDevice(device *schedulingv1alpha1.Device,
	gpuDevices []schedulingv1alpha1.DeviceInfo, gpuModel string, gpuDriverVer string) {

	device.Spec.Devices = append(device.Spec.Devices, gpuDevices...)
	if device.Labels == nil {
		device.Labels = make(map[string]string)
	}
	// currently the built-in informer only supports NVIDIA GPUs
	device.Labels[extension.LabelGPUVendor] = extension.GPUVendorNVIDIA
	if gpuModel != "" {
		device.Labels[extension.LabelGPUModel] = gpuModel
	}
	if gpuDriverVer != "" {
		device.Labels[extension.LabelGPUDriverVersion] = gpuDriverVer
	}
}

func (s *statesInformer) createDevice(device *schedulingv1alpha1.Device) error {
	_, err := s.deviceClient.Create(context.TODO(), device, metav1.CreateOptions{})
	return err
}

func (s *statesInformer) updateDevice(device *schedulingv1alpha1.Device) error {
	sorter := func(devices []schedulingv1alpha1.DeviceInfo) {
		sort.Slice(devices, func(i, j int) bool {
			if devices[i].Type != devices[j].Type {
				return devices[i].Type < devices[j].Type
			}
			return *(devices[i].Minor) < *(devices[j].Minor)
		})
	}
	sorter(device.Spec.Devices)

	conditionSorter := func(conditions []metav1.Condition) {
		sort.Slice(conditions, func(i, j int) bool {
			return conditions[i].Type < conditions[j].Type
		})
	}
	for i := range device.Spec.Devices {
		conditionSorter(device.Spec.Devices[i].Conditions)
	}

	return util.RetryOnConflictOrTooManyRequests(func() error {
		latestDevice, err := s.deviceClient.Get(context.TODO(), device.Name, metav1.GetOptions{ResourceVersion: "0"})
		if err != nil {
			return err
		}
		sorter(latestDevice.Spec.Devices)

		if apiequality.Semantic.DeepEqual(device.Spec.Devices, latestDevice.Spec.Devices) &&
			apiequality.Semantic.DeepEqual(device.Labels, latestDevice.Labels) &&
			apiequality.Semantic.DeepEqual(device.Annotations, latestDevice.Annotations) {
			klog.V(4).Infof("Device %s has not changed and does not need to be updated", device.Name)
			return nil
		}

		// update the device info conditions
		oldDevConditionsMap := make(map[string]map[string]metav1.Condition, len(latestDevice.Spec.Devices))
		for _, oldDev := range latestDevice.Spec.Devices {
			conditionTypeMap := make(map[string]metav1.Condition, len(oldDev.Conditions))
			for _, condition := range oldDev.Conditions {
				conditionTypeMap[condition.Type] = condition
			}
			oldDevConditionsMap[oldDev.UUID] = conditionTypeMap
		}

		for i, newDev := range device.Spec.Devices {
			if conditionTypeMap, ok := oldDevConditionsMap[newDev.UUID]; ok {
				for j := range newDev.Conditions {
					if oldCondition, ok := conditionTypeMap[newDev.Conditions[j].Type]; ok {
						if oldCondition.Status != newDev.Conditions[j].Status {
							newDev.Conditions[j].LastTransitionTime = metav1.Now()
						} else {
							newDev.Conditions[j].LastTransitionTime = oldCondition.LastTransitionTime
						}
					}
				}
				device.Spec.Devices[i] = newDev
			}
		}

		latestDevice.Spec.Devices = device.Spec.Devices
		latestDevice.Labels = device.Labels
		latestDevice.Annotations = device.Annotations

		_, err = s.deviceClient.Update(context.TODO(), latestDevice, metav1.UpdateOptions{})
		return err
	})
}

func (s *statesInformer) buildGPUDevice() []schedulingv1alpha1.DeviceInfo {
	//queryParam := generateQueryParam()
	gpuDeviceInfo, exist := s.metricsCache.Get(koordletuti.GPUDeviceType)
	if !exist {
		klog.V(4).Infof("gpu device not exist")
		return nil
	}
	gpus, ok := gpuDeviceInfo.(koordletuti.GPUDevices)
	if !ok {
		klog.Errorf("value type error, expect: %T, got %T", koordletuti.GPUDevices{}, gpuDeviceInfo)
		return nil
	}

	var deviceInfos []schedulingv1alpha1.DeviceInfo
	for idx := range gpus {
		gpu := gpus[idx]
		health := true
		s.gpuMutex.RLock()
		if _, ok := s.unhealthyGPU[gpu.UUID]; ok {
			health = false
		}
		s.gpuMutex.RUnlock()

		var topology *schedulingv1alpha1.DeviceTopology
		if gpu.NodeID >= 0 && gpu.PCIE != "" && gpu.BusID != "" {
			topology = &schedulingv1alpha1.DeviceTopology{
				SocketID: -1,
				NodeID:   gpu.NodeID,
				PCIEID:   gpu.PCIE,
				BusID:    gpu.BusID,
			}
		}

		deviceInfos = append(deviceInfos, schedulingv1alpha1.DeviceInfo{
			UUID:   gpu.UUID,
			Minor:  &gpu.Minor,
			Type:   schedulingv1alpha1.GPU,
			Health: health,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceGPUMemory:      *resource.NewQuantity(int64(gpu.MemoryTotal), resource.BinarySI),
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
			},
			Topology: topology,
		})
	}
	return deviceInfos
}

func (s *statesInformer) buildRDMADevice() []schedulingv1alpha1.DeviceInfo {
	rawRDMADevices, exist := s.metricsCache.Get(koordletuti.RDMADeviceType)
	if !exist {
		klog.V(4).Infof("rdma device not exist")
		return nil
	}
	rdmaDevices := rawRDMADevices.(koordletuti.RDMADevices)
	var deviceInfos []schedulingv1alpha1.DeviceInfo
	for idx := range rdmaDevices {
		rdma := rdmaDevices[idx]
		deviceInfo := schedulingv1alpha1.DeviceInfo{
			UUID:   rdma.ID,
			Minor:  pointer.Int32(0),
			Type:   schedulingv1alpha1.RDMA,
			Health: true,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
			},
			Topology: &schedulingv1alpha1.DeviceTopology{
				SocketID: -1,
				NodeID:   rdma.NodeID,
				PCIEID:   rdma.PCIE,
				BusID:    rdma.BusID,
			},
		}
		if rdma.VFEnabled {
			var vfs []schedulingv1alpha1.VirtualFunction
			for _, vf := range rdma.VFMap {
				vfs = append(vfs, schedulingv1alpha1.VirtualFunction{
					Minor: -1,
					BusID: vf.ID,
				})
			}
			sort.Slice(vfs, func(i, j int) bool {
				return vfs[i].BusID < vfs[j].BusID
			})
			deviceInfo.VFGroups = append(deviceInfo.VFGroups, schedulingv1alpha1.VirtualFunctionGroup{
				Labels: nil,
				VFs:    vfs,
			})
		}
		deviceInfos = append(deviceInfos, deviceInfo)
	}

	sort.Slice(deviceInfos, func(i, j int) bool {
		return deviceInfos[i].UUID < deviceInfos[j].UUID
	})
	for i := range deviceInfos {
		deviceInfos[i].Minor = pointer.Int32(int32(i))
	}
	return deviceInfos
}

func (s *statesInformer) buildXPUDevice(xpuDevices koordletuti.XPUDevices) []schedulingv1alpha1.DeviceInfo {
	var deviceInfos []schedulingv1alpha1.DeviceInfo
	for idx := range xpuDevices {
		xpu := xpuDevices[idx]

		minor, err := strconv.ParseInt(xpu.Minor, 10, 32)
		if err != nil {
			klog.Errorf("failed to parse xpu minor %s, err: %v", xpu.Minor, err)
		}

		resources := make(map[corev1.ResourceName]resource.Quantity)
		resources[extension.ResourceGPUMemoryRatio] = *resource.NewQuantity(100, resource.DecimalSI)
		for resourceName, resourceQuantity := range xpu.Resources {
			quantity, err := resource.ParseQuantity(resourceQuantity)
			if err != nil {
				klog.Errorf("failed to parse xpu resource %s %s, err: %v", resourceName, resourceQuantity, err)
				continue
			}
			// check resource name equal "gpu-core" or "gpu-memory" but not starts with "koordinator.sh/"
			if resourceName == "gpu-core" || resourceName == "gpu-memory" {
				resourceName = extension.DomainPrefix + resourceName
			}

			resources[corev1.ResourceName(resourceName)] = quantity
		}

		deviceHealthy := true
		if xpu.Status != nil {
			deviceHealthy = xpu.Status.Healthy
		}

		topo := getXPUDeviceTopology(&xpu)
		conditions := getXPUDviceConditions(&xpu)

		deviceInfo := schedulingv1alpha1.DeviceInfo{
			UUID:       xpu.UUID,
			Minor:      pointer.Int32(int32(minor)),
			Type:       schedulingv1alpha1.GPU,
			Health:     deviceHealthy,
			Resources:  resources,
			Topology:   topo,
			Conditions: conditions,
		}

		deviceInfos = append(deviceInfos, deviceInfo)
	}

	sort.Slice(deviceInfos, func(i, j int) bool {
		return deviceInfos[i].UUID < deviceInfos[j].UUID
	})
	return deviceInfos
}

func (s *statesInformer) initGPU() bool {
	if ret := nvml.Init(); ret != nvml.SUCCESS {
		if ret == nvml.ERROR_LIBRARY_NOT_FOUND {
			klog.Warning("nvml init failed, library not found")
			return false
		}
		klog.Warningf("nvml init failed, return %s", nvml.ErrorString(ret))
		return false
	}
	return true
}

func (s *statesInformer) getGPUDriverAndModel() (string, string) {
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		klog.Errorf("unable to get device count: %v", nvml.ErrorString(ret))
		return "", ""
	}

	if count == 0 {
		klog.Errorf("no gpu device found")
		return "", ""
	}

	var modelList []string
	for deviceIndex := 0; deviceIndex < count; deviceIndex++ {
		gpuDevice, ret := nvml.DeviceGetHandleByIndex(deviceIndex)
		if ret != nvml.SUCCESS {
			klog.Errorf("unable to get device model: %v", nvml.ErrorString(ret))
			continue
		}
		deviceModel, _ := gpuDevice.GetName()
		modelList = append(modelList, deviceModel)
	}

	model := ""
	for i, v := range modelList {
		if v == "" {
			klog.Errorf("device model invalid: %v", modelList)
			return "", ""
		} else if i == 0 {
			model = v
		} else if model != v {
			klog.Errorf("device model invalid: %v", modelList)
			return "", ""
		}
	}

	// NVIDIA Driver 470 report GPU Model with "NVIDIA " Prefix
	model = strings.TrimPrefix(model, "NVIDIA ")

	// A100 SXM4 80GB -> A100-SXM4-80GB
	// Tesla P100-PCIE-16GB -> Tesla-P100-PCIE-16GB
	// Tesla V100-SXM2-16GB -> Tesla-V100-SXM2-16GB
	// Tesla T4 -> Tesla-T4
	// Tesla P40 -> Tesla-P40
	// Tesla M40 -> Tesla-M40
	// GeForce RTX 2080 Ti -> GeForce-RTX-2080-Ti
	// GeForce GTX 1080 Ti -> GeForce-GTX-1080-Ti
	transModel := strings.ReplaceAll(model, " ", "-")

	driverVersion, ret := nvml.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		klog.Errorf("unable to get device driver version: %v", nvml.ErrorString(ret))
		return "", ""
	}

	return transModel, driverVersion
}

func (s *statesInformer) gpuHealCheck(stopCh <-chan struct{}) {
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		klog.Errorf("unable to get device count: %v", nvml.ErrorString(ret))
		return
	}
	if count == 0 {
		klog.Errorf("no gpu device found")
		return
	}
	devices := []string{}
	for deviceIndex := 0; deviceIndex < count; deviceIndex++ {
		gpudevice, ret := nvml.DeviceGetHandleByIndex(deviceIndex)
		if ret != nvml.SUCCESS {
			klog.Errorf("unable to get device at index %d: %v", deviceIndex, nvml.ErrorString(ret))
			continue
		}
		uuid, ret := gpudevice.GetUUID()
		if ret != nvml.SUCCESS {
			klog.Errorf("failed to get device uuid at index %d, err: %v", deviceIndex, nvml.ErrorString(ret))
		}
		devices = append(devices, uuid)
	}
	unhealthyChan := make(chan string)
	go checkHealth(stopCh, devices, unhealthyChan)
	klog.Info("start to do gpu health check")
	for d := range unhealthyChan {
		// FIXME: there is no way to recover from the Unhealthy state.
		s.gpuMutex.Lock()
		s.unhealthyGPU[d] = struct{}{}
		s.gpuMutex.Unlock()
		klog.Infof("get a unhealthy gpu %s", d)
	}
}

// check status of gpus, and send unhealthy devices to the unhealthyDeviceChan channel
func checkHealth(stopCh <-chan struct{}, devs []string, xids chan<- string) {
	eventSet, ret := nvml.EventSetCreate()
	if ret != nvml.SUCCESS {
		klog.Errorf("failed to create event set, err: %v", nvml.ErrorString(ret))
		os.Exit(1)
	}
	defer eventSet.Free()

	for _, d := range devs {
		device, ret := nvml.DeviceGetHandleByUUID(d)
		if ret != nvml.SUCCESS {
			klog.Errorf("failed to get device %s, err: %v", d, nvml.ErrorString(ret))
			continue
		}
		ret = nvml.DeviceRegisterEvents(device, nvml.EventTypeXidCriticalError, eventSet)
		if ret == nvml.ERROR_NOT_SUPPORTED {
			klog.Infof("Warning: %s is too old to support healthchecking: %v. Marking it unhealthy.", d, nvml.ErrorString(ret))
			xids <- d
			continue
		}

		if ret != nvml.SUCCESS {
			klog.Infof("failed to register event for device %s, err: %v", d, nvml.ErrorString(ret))
			continue
		}
	}

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		e, ret := eventSet.Wait(5000)
		if ret != nvml.SUCCESS && e.EventType != nvml.EventTypeXidCriticalError {
			continue
		}

		// http://docs.nvidia.com/deploy/xid-errors/index.html#topic_4
		// Application errors: the GPU should still be healthy
		if e.EventData == 13 || e.EventData == 31 || e.EventData == 43 || e.EventData == 45 || e.EventData == 68 {
			continue
		}

		uuid, ret := e.Device.GetUUID()
		if ret != nvml.SUCCESS {
			klog.Errorf("failed to get uuid of device %s, err: %v", e.ComputeInstanceId, nvml.ErrorString(ret))
			continue
		}

		if len(uuid) == 0 {
			// All devices are unhealthy
			for _, d := range devs {
				xids <- d
			}
			continue
		}

		for _, d := range devs {
			if d == uuid {
				xids <- d
			}
		}
	}
}

func (s *statesInformer) buildXPUDeviceLabels(xpuDevices koordletuti.XPUDevices) map[string]string {
	if len(xpuDevices) == 0 {
		return nil
	}

	// use the first device's vendor and model as the label value
	vendor := xpuDevices[0].Vendor
	model := xpuDevices[0].Model

	label := map[string]string{
		extension.LabelGPUModel:  model,
		extension.LabelGPUVendor: vendor,
	}

	if xpuDevices[0].Topology != nil && xpuDevices[0].Topology.P2PLinks != nil {
		// If the device has P2P links, set the partition policy
		label[extension.LabelGPUPartitionPolicy] = string(extension.GPUPartitionPolicyPrefer)
		if xpuDevices[0].Topology.MustHonorPartition {
			label[extension.LabelGPUPartitionPolicy] = string(extension.GPUPartitionPolicyHonor)
		}
	}

	return label
}

func (s *statesInformer) buildXPUDeviceAnnotations(xpuDevices koordletuti.XPUDevices) map[string]string {
	partitionTable := getPartitionTableFromXPUDevices(xpuDevices)
	if partitionTable == nil {
		return map[string]string{}
	}
	rawBytes, err := json.Marshal(partitionTable)
	if err != nil {
		klog.Errorf("failed to marshal gpu partition table, err: %v", err)
		return nil
	}

	return map[string]string{
		extension.AnnotationGPUPartitions: string(rawBytes),
	}
}

func getPartitionTableFromXPUDevices(xpus koordletuti.XPUDevices) extension.GPUPartitionTable {
	partitions := make(extension.GPUPartitionTable)
	for _, xpu := range xpus {
		if xpu.Topology == nil || xpu.Topology.P2PLinks == nil {
			return nil
		}

		for _, p2pLink := range xpu.Topology.P2PLinks {
			if p2pLink.PeerMinor == "" || string(p2pLink.Type) == "" {
				klog.Errorf("xpu %s-%s-%s has invalid p2p link %v", xpu.Vendor, xpu.Model, xpu.Minor, p2pLink)
				continue
			}

			var minorsINt []int
			minorsStr := strings.Split(p2pLink.PeerMinor, ",")
			minorsStr = append(minorsStr, xpu.Minor)
			for _, minor := range minorsStr {
				minorInt, err := strconv.Atoi(minor)
				if err != nil {
					klog.Errorf("failed to parse peer minor %s, err: %v", p2pLink.PeerMinor, err)
				}
				minorsINt = append(minorsINt, minorInt)
			}

			sort.Ints(minorsINt)

			partition := extension.GPUPartition{
				Minors:          minorsINt,
				GPULinkType:     extension.GPULinkType(p2pLink.Type),
				AllocationScore: 1,
			}

			partitions[len(minorsINt)] = append(partitions[len(minorsINt)], partition)
		}
	}

	return partitions
}

func getXPUDevices(metricsCache metriccache.MetricCache) koordletuti.XPUDevices {
	rawXPUDevices, exist := metricsCache.Get(koordletuti.XPUDeviceType)
	if !exist {
		klog.V(4).Infof("xpu device not exist")
		return nil
	}
	xpuDevices := rawXPUDevices.(koordletuti.XPUDevices)

	return xpuDevices
}

func getXPUDeviceTopology(xpu *koordletuti.XPUDeviceInfo) *schedulingv1alpha1.DeviceTopology {
	if xpu == nil || xpu.Topology == nil {
		return nil
	}

	var nodeID int32
	if xpu.Topology.NodeID != "" {
		nodeIDRaw, err := strconv.ParseInt(xpu.Topology.NodeID, 10, 32)
		if err != nil {
			klog.Errorf("failed to parse nodeID %s, err: %v", xpu.Topology.NodeID, err)
		}
		nodeID = int32(nodeIDRaw)
	} else {
		nodeID = -1
	}

	return &schedulingv1alpha1.DeviceTopology{
		SocketID: -1,
		NodeID:   nodeID,
		PCIEID:   xpu.Topology.PCIEID,
		BusID:    xpu.Topology.BusID,
	}
}

func getXPUDviceConditions(xpu *koordletuti.XPUDeviceInfo) []metav1.Condition {
	if xpu == nil || xpu.Status == nil {
		return nil
	}

	conditions := []metav1.Condition{
		{
			Type:               string(schedulingv1alpha1.DeviceConditionHealthy),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
	}

	if !xpu.Status.Healthy {
		conditions[0].Status = metav1.ConditionFalse
		conditions[0].Reason = xpu.Status.ErrCode
		conditions[0].Message = xpu.Status.ErrMessage
	}

	return conditions
}

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

package statesinformer

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func generateQueryParam() *metriccache.QueryParam {
	end := time.Now()
	start := end.Add(-time.Duration(60) * time.Second)
	return &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeLast,
		Start:     &start,
		End:       &end,
	}
}

func (s *statesInformer) reportDevice() {
	node := s.GetNode()
	gpuDevices := s.buildGPUDevice()
	gpuModel, gpuDriverVer := s.getGPUDriverAndModel()

	if len(gpuDevices) == 0 {
		return
	}

	device := s.buildBasicDevice(node)
	s.fillGpuDevice(device, gpuDevices, gpuModel, gpuDriverVer)

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

func (s *statesInformer) fillGpuDevice(device *schedulingv1alpha1.Device,
	gpuDevices []schedulingv1alpha1.DeviceInfo, gpuModel string, gpuDriverVer string) {

	device.Spec.Devices = append(device.Spec.Devices, gpuDevices...)
	if device.Labels == nil {
		device.Labels = make(map[string]string)
	}
	if gpuModel != "" {
		device.Labels[extension.GPUModel] = gpuModel
	}
	if gpuDriverVer != "" {
		device.Labels[extension.GPUDriver] = gpuDriverVer
	}
}

func (s *statesInformer) createDevice(device *schedulingv1alpha1.Device) error {
	_, err := s.deviceClient.Create(context.TODO(), device, metav1.CreateOptions{})
	return err
}

func (s *statesInformer) updateDevice(deviceNew *schedulingv1alpha1.Device) error {
	sorter := func(devices []schedulingv1alpha1.DeviceInfo) {
		sort.Slice(devices, func(i, j int) bool {
			return *(devices[i].Minor) < *(devices[j].Minor)
		})
	}
	sorter(deviceNew.Spec.Devices)

	return util.RetryOnConflictOrTooManyRequests(func() error {
		deviceOld, err := s.deviceClient.Get(context.TODO(), deviceNew.Name, metav1.GetOptions{ResourceVersion: "0"})
		if err != nil {
			return err
		}
		sorter(deviceOld.Spec.Devices)

		if apiequality.Semantic.DeepEqual(deviceNew.Spec.Devices, deviceOld.Spec.Devices) &&
			apiequality.Semantic.DeepEqual(deviceNew.Labels, deviceOld.Labels) {
			klog.V(4).Infof("Device %s has not changed and does not need to be updated", deviceNew.Name)
			return nil
		}

		_, err = s.deviceClient.Update(context.TODO(), deviceNew, metav1.UpdateOptions{})
		return err
	})
}

func (s *statesInformer) buildGPUDevice() []schedulingv1alpha1.DeviceInfo {
	queryParam := generateQueryParam()
	nodeResource := s.metricsCache.GetNodeResourceMetric(queryParam)
	if nodeResource.Error != nil {
		klog.Errorf("failed to get node resource metric, err: %v", nodeResource.Error)
		return nil
	}
	if len(nodeResource.Metric.GPUs) == 0 {
		klog.V(5).Info("no gpu device found")
		return nil
	}
	var deviceInfos []schedulingv1alpha1.DeviceInfo
	for i := range nodeResource.Metric.GPUs {
		gpu := nodeResource.Metric.GPUs[i]
		health := true
		s.gpuMutex.RLock()
		if _, ok := s.unhealthyGPU[gpu.DeviceUUID]; ok {
			health = false
		}
		s.gpuMutex.RUnlock()
		deviceInfos = append(deviceInfos, schedulingv1alpha1.DeviceInfo{
			UUID:   gpu.DeviceUUID,
			Minor:  &gpu.Minor,
			Type:   schedulingv1alpha1.GPU,
			Health: health,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.GPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
				extension.GPUMemory:      gpu.MemoryTotal,
				extension.GPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
			},
		})
	}
	return deviceInfos
}

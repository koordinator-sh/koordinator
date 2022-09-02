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
	"os"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
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
	copyNode := s.GetNode()
	gpuDevices := s.buildGPUDevice()
	blocker := true
	device := &schedulingv1alpha1.Device{
		ObjectMeta: metav1.ObjectMeta{
			Name: copyNode.Name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					Kind:               "Node",
					Name:               copyNode.Name,
					UID:                copyNode.UID,
					Controller:         &blocker,
					BlockOwnerDeletion: &blocker,
				},
			},
		},
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: gpuDevices,
		},
	}
	// TODO: compare diff before update
	_, err := s.deviceClient.Update(context.TODO(), device, metav1.UpdateOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("failed to update device %s, err: %v", copyNode.Name, err)
			return
		}
		_, err = s.deviceClient.Create(context.TODO(), device, metav1.CreateOptions{})
		if err != nil {
			klog.Errorf("failed to create device %s, err: %v", copyNode.Name, err)
			return
		}
	}
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
	for _, gpu := range nodeResource.Metric.GPUs {
		health := true
		s.gpuMutex.RLock()
		if _, ok := s.unhealthyGPU[gpu.DeviceUUID]; ok {
			health = false
		}
		s.gpuMutex.RUnlock()
		deviceInfos = append(deviceInfos, schedulingv1alpha1.DeviceInfo{
			UUID:   gpu.DeviceUUID,
			Minor:  gpu.Minor,
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

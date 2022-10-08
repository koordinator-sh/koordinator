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
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"k8s.io/klog/v2"
	"os"
	"strings"
)

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

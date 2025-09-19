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

package resource

import (
	"fmt"
	"strconv"

	"github.com/jaypipes/ghw"
	"k8s.io/klog/v2"

	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

type xpuManager struct{}

func NewXPUManager() Manager {
	return &xpuManager{}
}

func (manager *xpuManager) GetDevices() ([]Device, error) {
	klog.Info("getXPUDevice(): Getting XPU devices")
	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("getXPUDevice(): new PCI instance error, %v", err)
	}
	devices := pci.ListDevices()
	if len(devices) == 0 {
		klog.Info("getXPUDevice(): no pci devices")
		return nil, nil
	}
	xpuExists := false
	for _, device := range devices {
		if IsXPUDevice(device) {
			xpuExists = true
			break
		}
	}
	if !xpuExists {
		return []Device{}, nil
	}
	var xpuDevices []Device

	count, err := GetXPUCount(XPU)
	if err != nil {
		return nil, fmt.Errorf("getXPUDevice(): get GPU count, %v", err)
	}

	productName, err := GetXPUName(XPU)
	if err != nil {
		return nil, fmt.Errorf("getXPUDevice(): get product name error, %v", err)
	}

	mem, err := GetXPUMemory(XPU)
	if err != nil {
		return nil, fmt.Errorf("getXPUDevice(): get memory error, %v", err)
	}

	for i := 0; i < count; i++ {
		topo, status, uuid, err := GetDeviceInfo(XPU, fmt.Sprintf("%d", i))
		if err != nil {
			klog.Errorf("getXPUDevice(): get device info error, %v", err)
			continue
		}
		deviceInfo := koordletuti.XPUDeviceInfo{
			Vendor: "kunlun",
			Model:  productName,
			UUID:   uuid,
			Minor:  strconv.Itoa(i),
			Resources: map[string]string{
				"koordinator.sh/gpu-memory": mem,
				"koordinator.sh/gpu-core":   "100",
			},
			Topology: &topo,
			Status:   &status,
		}
		xpuDevice := xpuDevice{
			DeviceInfo: deviceInfo,
		}
		xpuDevices = append(xpuDevices, xpuDevice)
	}

	klog.Infof("gpu devices: %+v", xpuDevices)
	return xpuDevices, nil
}

func (manager *xpuManager) GetDriverVersion() (string, error) {
	return "", nil
}

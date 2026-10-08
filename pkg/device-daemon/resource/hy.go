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

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

type hyDevice struct {
	DeviceInfo koordletuti.XPUDeviceInfo
}

func (device hyDevice) GetDeviceInfo() (koordletuti.XPUDeviceInfo, error) {
	return device.DeviceInfo, nil
}

type hyManager struct{}

func NewHYManager() Manager {
	return &hyManager{}
}

func (manager *hyManager) GetDeviceVendor() string {
	return extension.GPUVendorHygon
}

func (manager *hyManager) GetDevices() ([]Device, error) {
	klog.Info("getHYDevice(): Getting HY devices")
	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("getHYDevice(): new PCI instance error, %v", err)
	}
	devices := pci.Devices
	if len(devices) == 0 {
		klog.Info("getHYDevice(): no pci devices")
		return nil, nil
	}
	hyExists := false
	for _, device := range devices {
		if IsHYDevice(device) {
			hyExists = true
			break
		}
	}
	if !hyExists {
		return []Device{}, nil
	}
	var hyDevices []Device

	count, err := GetXPUCount(HY)
	if err != nil {
		return nil, fmt.Errorf("getHYDevice(): get HY count, %v", err)
	}

	productName, err := GetXPUName(HY)
	if err != nil {
		return nil, fmt.Errorf("getHYDevice(): get product name error, %v", err)
	}

	mem, err := GetXPUMemory(HY)
	if err != nil {
		return nil, fmt.Errorf("getHYDevice(): get memory error, %v", err)
	}

	for i := 0; i < count; i++ {
		topo, status, uuid, err := GetDeviceInfo(HY, fmt.Sprintf("%d", i))
		if err != nil {
			klog.Errorf("getHYDevice(): get device info error, %v", err)
			continue
		}
		deviceInfo := koordletuti.XPUDeviceInfo{
			Vendor: extension.GPUVendorHygon,
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
		hyDevice := hyDevice{
			DeviceInfo: deviceInfo,
		}
		hyDevices = append(hyDevices, hyDevice)
	}

	klog.Infof("HY devices: %+v", hyDevices)
	return hyDevices, nil
}

func (manager *hyManager) GetDriverVersion() (string, error) {
	return "", nil
}

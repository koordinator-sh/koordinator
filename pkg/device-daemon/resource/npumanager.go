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

type npuManager struct{}

func NewNPUManager() Manager {
	return &npuManager{}
}

func (manager *npuManager) GetDevices() ([]Device, error) {
	klog.Info("getNPUDevice(): Getting NPU devices")
	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("getXPUDevice(): new PCI instance error, %v", err)
	}
	devices := pci.ListDevices()
	if len(devices) == 0 {
		klog.Info("getNPUDevice(): no pci devices")
		return nil, nil
	}
	npuExists := false
	for _, device := range devices {
		if IsNPUDevice(device) {
			npuExists = true
			break
		}
	}
	if !npuExists {
		return []Device{}, nil
	}

	productName, err := GetXPUName(NPU)
	if err != nil {
		return nil, fmt.Errorf("getNPUDevice(): get product name error, %v", err)
	}

	mem, err := GetXPUMemory(NPU)
	if err != nil {
		return nil, fmt.Errorf("getNPUDevice(): get memory error, %v", err)
	}

	cardIDs, err := GetXPUCards(NPU)
	if err != nil {
		return nil, fmt.Errorf("getNPUDevice(): get cards error, %v", err)
	}

	minors, err := GetXPUMinors(NPU)
	if err != nil {
		return nil, fmt.Errorf("getNPUDevice(): get minors error, %v", err)
	}
	//not support for 310P3
	var aiCore int
	switch productName {
	case "Ascend-310P3":
		aiCore = 8
	default:
		aiCore, err = GetAiCore()
		if err != nil {
			return nil, fmt.Errorf("getNPUDevice(): get ai core error, %v", err)
		}
	}

	//not support for 910B
	var aiCpu int
	switch productName {
	case "Ascend-910B":
		aiCpu = 0
	case "Ascend-910B2":
		aiCpu = 6
	case "Ascend-910B3":
		aiCpu = 7
	case "Ascend-910B4":
		aiCpu = 7
	default:
		aiCpu, err = GetAiCpu(cardIDs[0])
		if err != nil {
			return nil, fmt.Errorf("getNPUDevice(): get ai cpu error, %v", err)
		}
	}

	var npuDevices []Device
	for index, npuId := range cardIDs {
		topo, status, uuid, err := GetDeviceInfo(NPU, npuId)
		if err != nil {
			klog.Errorf("getNPUDevice(): get device info topo error, %v", err)
			continue
		}
		klog.V(4).Infof("getNPUDevice(): get device info topo, %v", topo)
		klog.V(4).Infof("getNPUDevice(): get device info status, %v", status)
		klog.V(4).Infof("getNPUDevice(): get device info uuid, %s", uuid)
		info := koordletuti.XPUDeviceInfo{
			Vendor: "huawei",
			Model:  productName,
			UUID:   uuid + "-" + minors[index],
			Minor:  minors[index],
			Resources: map[string]string{
				"koordinator.sh/gpu-memory": mem,
				"huawei.com/npu-cpu":        strconv.Itoa(aiCpu),
				"huawei.com/npu-core":       strconv.Itoa(aiCore),
				"huawei.com/npu-dvpp":       strconv.Itoa(100),
			},
			Topology: &topo,
			Status:   &status,
		}
		npuDevice := npuDevice{
			DeviceInfo: info,
		}
		npuDevices = append(npuDevices, npuDevice)
	}

	klog.Infof("npu devices: %+v", npuDevices)
	return npuDevices, nil
}

func (manager *npuManager) GetDriverVersion() (string, error) {
	//TODO implement me
	panic("implement me")
}

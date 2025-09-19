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
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jaypipes/ghw"
	"k8s.io/klog/v2"

	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

type mluManager struct{}

func NewMLUManager() Manager {
	return &mluManager{}

}

func (manager *mluManager) GetDevices() ([]Device, error) {
	klog.Info("getMLUDevice(): Getting MLU devices")
	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("getMLUDevice(): new PCI instance error, %v", err)
	}
	devices := pci.ListDevices()
	if len(devices) == 0 {
		klog.Info("getMLUDevice(): no pci devices")
		return nil, nil
	}
	b, _ := json.Marshal(devices)
	klog.V(4).Infof("getMLUDevice(): found devices, %s", string(b))

	mluExists := false
	for _, device := range devices {
		if IsMLUDevice(device) {
			mluExists = true
			break
		}
	}
	if !mluExists {
		klog.Warningf("getMLUDevice(): MLU device not found")
		return []Device{}, nil
	}
	var mluDevices []Device
	count, err := GetXPUCount(MLU)
	if err != nil {
		return nil, fmt.Errorf("getMLUDevice(): get MLU count error, %v", err)
	}
	klog.V(4).Infof("getMLUDevice(): get mlu count, %d", count)

	productName, err := GetXPUName(MLU)
	if err != nil {
		return nil, fmt.Errorf("getMLUDevice(): get product name error, %v", err)
	}
	klog.V(4).Infof("getMLUDevice(): get mlu product name, %s", productName)

	mem, err := GetXPUMemory(MLU)
	if err != nil {
		return nil, fmt.Errorf("getMLUDevice(): get memory error, %v", err)
	}
	klog.V(4).Infof("getMLUDevice(): get mlu memory, %s", mem)

	for i := 0; i < count; i++ {
		topo, status, uuid, err := GetDeviceInfo(MLU, strconv.Itoa(i))
		if err != nil {
			klog.Errorf("getMLUDevice(): get device info topo error, %v", err)
			continue
		}
		klog.V(4).Infof("getMLUDevice(): get device info topo, %v", topo)
		klog.V(4).Infof("getMLUDevice(): get device info status, %v", status)
		klog.V(4).Infof("getMLUDevice(): get device info uuid, %s", uuid)
		info := koordletuti.XPUDeviceInfo{
			Vendor: "cambricon",
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

		mluDevice := mluDevice{
			DeviceInfo: info,
		}
		mluDevices = append(mluDevices, mluDevice)
	}

	mluBytes, _ := json.Marshal(mluDevices)
	klog.V(4).Infof("mlu devices: %s", string(mluBytes))
	return mluDevices, nil
}

func (manager *mluManager) GetDriverVersion() (string, error) {
	//TODO implement me
	panic("implement me")
}

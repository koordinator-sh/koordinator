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

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

type mxDevice struct {
	DeviceInfo koordletuti.XPUDeviceInfo
}

func (device mxDevice) GetDeviceInfo() (koordletuti.XPUDeviceInfo, error) {
	return device.DeviceInfo, nil
}

type mxManager struct{}

func NewMXManager() Manager {
	return &mxManager{}

}

func (manager *mxManager) GetDeviceVendor() string {
	return extension.GPUVendorMetaX
}

func (manager *mxManager) GetDevices() ([]Device, error) {
	klog.Info("getmxDevice(): Getting mx devices")
	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("getmxDevice(): new PCI instance error, %v", err)
	}
	devices := pci.Devices
	if len(devices) == 0 {
		klog.Info("getmxDevice(): no pci devices")
		return nil, nil
	}
	b, _ := json.Marshal(devices)
	klog.V(4).Infof("getmxDevice(): found devices, %s", string(b))

	mxExists := false
	for _, device := range devices {
		if IsMXDevice(device) {
			mxExists = true
			break
		}
	}
	if !mxExists {
		klog.Warningf("getmxDevice(): mx device not found")
		return []Device{}, nil
	}
	var mxDevices []Device
	count, err := GetXPUCount(MX)
	if err != nil {
		return nil, fmt.Errorf("getmxDevice(): get mx count error, %v", err)
	}
	klog.V(4).Infof("getmxDevice(): get mx count, %d", count)

	productName, err := GetXPUName(MX)
	if err != nil {
		return nil, fmt.Errorf("getmxDevice(): get product name error, %v", err)
	}
	klog.V(4).Infof("getmxDevice(): get mx product name, %s", productName)

	mem, err := GetXPUMemory(MX)
	if err != nil {
		return nil, fmt.Errorf("getmxDevice(): get memory error, %v", err)
	}
	klog.V(4).Infof("getmxDevice(): get mx memory, %s", mem)

	for i := 0; i < count; i++ {
		topo, status, uuid, err := GetDeviceInfo(MX, strconv.Itoa(i))
		if err != nil {
			klog.Errorf("getmxDevice(): get device info topo error, %v", err)
			continue
		}
		klog.V(4).Infof("getmxDevice(): get device info topo, %v", topo)
		klog.V(4).Infof("getmxDevice(): get device info status, %v", status)
		klog.V(4).Infof("getmxDevice(): get device info uuid, %s", uuid)
		deviceInfo := koordletuti.XPUDeviceInfo{
			Vendor: "metax",
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

		mxDevice := mxDevice{
			DeviceInfo: deviceInfo,
		}
		mxDevices = append(mxDevices, mxDevice)
	}

	mxBytes, _ := json.Marshal(mxDevices)
	klog.V(4).Infof("mx devices: %s", string(mxBytes))
	return mxDevices, nil
}

func (manager *mxManager) GetDriverVersion() (string, error) { return "", nil }

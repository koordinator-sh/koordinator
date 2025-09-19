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

package mamanger

import (
	"fmt"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/device-daemon/resource"
)

func newXPUPrinter(manager resource.Manager) (Printer, error) {
	klog.Info("Creating XPU printer")
	xpuDevices, err := manager.GetDevices()
	if err != nil {
		return nil, fmt.Errorf("error getting XPU devices: %v", err)
	}

	xpuCount := len(xpuDevices)
	if xpuCount == 0 {
		return DevicePrinters{}, nil
	}

	xpuPrinters := DevicePrinters{}
	for _, xpu := range xpuDevices {
		deviceInfo, err := xpu.GetDeviceInfo()
		if err != nil {
			return nil, fmt.Errorf("error getting XPU device info: %v", err)
		}
		xpuPrinters = append(xpuPrinters, deviceInfo)
	}

	return xpuPrinters, nil
}

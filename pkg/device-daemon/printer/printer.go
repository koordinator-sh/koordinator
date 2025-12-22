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
	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

// Printer defines an interface for generating XPUDevices
type Printer interface {
	Prints() (koordletuti.XPUDevices, error)
}

type devicePrinters koordletuti.XPUDevices

func (printer devicePrinters) Prints() (koordletuti.XPUDevices, error) {
	return koordletuti.XPUDevices(printer), nil
}

func newPrinter(manager resource.Manager) (Printer, error) {
	vendor := manager.GetDeviceVendor()
	klog.Infof("Creating %s printer", vendor)
	devices, err := manager.GetDevices()
	if err != nil {
		return nil, fmt.Errorf("error getting %s devices: %v", vendor, err)
	}

	deviceCount := len(devices)
	if deviceCount == 0 {
		return devicePrinters{}, nil
	}

	printers := devicePrinters{}
	for _, device := range devices {
		deviceInfo, err := device.GetDeviceInfo()
		if err != nil {
			return nil, fmt.Errorf("error getting %s device info: %v", vendor, err)
		}
		printers = append(printers, deviceInfo)
	}

	return printers, nil
}

type printerList []Printer

func (printerList printerList) Prints() (koordletuti.XPUDevices, error) {
	allDevices := koordletuti.XPUDevices{}
	for _, printer := range printerList {
		prints, err := printer.Prints()
		if err != nil {
			return nil, fmt.Errorf("error generating prints: %v", err)
		}
		for _, p := range prints {
			allDevices = append(allDevices, p)
		}
	}

	return allDevices, nil
}

func NewPrinters(manager map[string]resource.Manager) (Printer, error) {
	var printers printerList
	for _, manager := range manager {
		printer, err := newPrinter(manager)
		if err != nil {
			return nil, fmt.Errorf("error creating printer: %v", err)
		}
		printers = append(printers, printer)
	}
	return printers, nil
}

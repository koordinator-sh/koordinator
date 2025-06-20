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

package xpu

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

const (
	DeviceInfosDir = "/etc/kubernetes/koordlet/device-infos/" // Directory where device info files are stored
)

func GetXPUDevice() (metriccache.Devices, error) {
	//	read device infos from dirctory /etc/kubernetes/koordlet/device-infos/
	entries, err := os.ReadDir(DeviceInfosDir)
	if err != nil {
		return nil, err
	}

	var devices util.XPUDevices
	for _, entry := range entries {
		if entry.IsDir() {
			continue // Skip directories
		}

		filePath := filepath.Join(DeviceInfosDir, entry.Name())
		deviceInfos, err := readDeviceInfosFromFile(filePath)
		if err != nil {
			return nil, err
		}

		if deviceInfos == nil {
			continue // Skip empty device info
		}

		// Add the device info to the metric cache
		devices = append(devices, deviceInfos...)
	}

}

func readDeviceInfosFromFile(filePath string) ([]util.XPUDeviceInfo, error) {
	// Read the device info from the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var deviceInfos []util.XPUDeviceInfo
	if err := json.NewDecoder(file).Decode(deviceInfos); err != nil {
		return nil, fmt.Errorf("failed to decode device info from file %s: %v", filePath, err)
	}

	if len(deviceInfos) == 0 {
		return nil, nil
	}

	return deviceInfos, nil
}

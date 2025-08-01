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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	DeviceCollectorName = "XPU"
)

type xpuCollector struct {
	enabled        bool
	deviceInfosDir string
}

func New(opt *framework.Options) framework.DeviceCollector {
	return &xpuCollector{
		enabled:        features.DefaultKoordletFeatureGate.Enabled(features.Accelerators),
		deviceInfosDir: system.Conf.XPUDeviceInfosDir,
	}
}

func (x xpuCollector) Enabled() bool {
	return x.enabled
}

func (x xpuCollector) Setup(s *framework.Context) {}

func (x xpuCollector) Run(stopCh <-chan struct{}) {}

func (x xpuCollector) Started() bool {
	return true
}

func (x xpuCollector) Shutdown() {}

func (x xpuCollector) Infos() metriccache.Devices {
	xpuDevices, err := GetXPUDevice(x.deviceInfosDir)
	if err != nil {
		klog.Errorf("failed to get XPU devices: %v", err)
	}
	return xpuDevices
}

func (x xpuCollector) GetNodeMetric() ([]metriccache.MetricSample, error) {
	return nil, nil
}

func (x xpuCollector) GetPodMetric(uid, podParentDir string, cs []corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return nil, nil
}

func (x xpuCollector) GetContainerMetric(containerID, podParentDir string, c *corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return nil, nil
}

func GetXPUDevice(deviceInfosDir string) (metriccache.Devices, error) {
	// default deviceInfosDir: "/var/run/koordlet/device-infos/"
	entries, err := os.ReadDir(deviceInfosDir)
	if err != nil {
		return nil, err
	}

	var devices util.XPUDevices
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(deviceInfosDir, entry.Name())
		deviceInfos, err := readDeviceInfosFromFile(filePath)
		if err != nil {
			return nil, err
		}

		if deviceInfos == nil {
			continue
		}

		devices = append(devices, deviceInfos...)
	}
	return devices, nil
}

func readDeviceInfosFromFile(filePath string) (util.XPUDevices, error) {
	// read the device info from the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var deviceInfos util.XPUDevices
	if err := json.NewDecoder(file).Decode(&deviceInfos); err != nil {
		return nil, fmt.Errorf("failed to decode device info from file %s: %v", filePath, err)
	}

	if len(deviceInfos) == 0 {
		return nil, nil
	}

	return deviceInfos, nil
}

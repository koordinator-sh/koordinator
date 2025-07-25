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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
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
		deviceInfosDir: opt.Config.XPUDeviceInfosDir,
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

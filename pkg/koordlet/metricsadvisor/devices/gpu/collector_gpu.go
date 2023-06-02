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

package gpu

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
)

const (
	DeviceCollectorName = "GPU"
)

type gpuCollector struct {
	enabled          bool
	collectInterval  time.Duration
	gpuDeviceManager GPUDeviceManager
}

func New(opt *framework.Options) framework.DeviceCollector {
	return &gpuCollector{
		enabled:         features.DefaultKoordletFeatureGate.Enabled(features.Accelerators),
		collectInterval: opt.Config.CollectResUsedInterval,
	}
}

func (g *gpuCollector) Shutdown() {
	if err := g.gpuDeviceManager.shutdown(); err != nil {
		klog.Warningf("gpu collector shutdown failed, error %v", err)
	}
}

func (g *gpuCollector) Enabled() bool {
	return g.enabled
}

func (g *gpuCollector) Setup(fra *framework.Context) {
	g.gpuDeviceManager = initGPUDeviceManager()
}

func (g *gpuCollector) Run(stopCh <-chan struct{}) {
	go wait.Until(g.gpuDeviceManager.collectGPUUsage, g.collectInterval, stopCh)
}

func (g *gpuCollector) Started() bool {
	return g.gpuDeviceManager.started()
}

func (g *gpuCollector) Infos() metriccache.Devices {
	return g.gpuDeviceManager.deviceInfos()
}

func (g *gpuCollector) GetNodeMetric() ([]metriccache.MetricSample, error) {
	return g.gpuDeviceManager.getNodeGPUUsage(), nil
}

func (g *gpuCollector) GetPodMetric(uid, podParentDir string, cs []corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return g.gpuDeviceManager.getPodGPUUsage(uid, podParentDir, cs)
}

func (g *gpuCollector) GetContainerMetric(ContainerID, podParentDir string, c *corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return g.gpuDeviceManager.getContainerGPUUsage(ContainerID, podParentDir, c)
}

type GPUDeviceManager interface {
	started() bool
	deviceInfos() metriccache.Devices
	collectGPUUsage()
	getNodeGPUUsage() []metriccache.MetricSample
	getPodGPUUsage(uid, podParentDir string, cs []corev1.ContainerStatus) ([]metriccache.MetricSample, error)
	getContainerGPUUsage(containerID, podParentDir string, c *corev1.ContainerStatus) ([]metriccache.MetricSample, error)
	shutdown() error
}

type dummyDeviceManager struct{}

func (d *dummyDeviceManager) started() bool {
	return true
}

func (d *dummyDeviceManager) deviceInfos() metriccache.Devices {
	return nil
}

func (d *dummyDeviceManager) collectGPUUsage() {}

func (d *dummyDeviceManager) getNodeGPUUsage() []metriccache.MetricSample {
	return nil
}

func (d *dummyDeviceManager) getPodGPUUsage(uid, podParentDir string, cs []corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return nil, nil
}

func (d *dummyDeviceManager) getContainerGPUUsage(containerID, podParentDir string, c *corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return nil, nil
}

func (d *dummyDeviceManager) shutdown() error {
	return nil
}

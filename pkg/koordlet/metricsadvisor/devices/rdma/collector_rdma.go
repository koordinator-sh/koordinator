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

package rdma

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
)

const (
	DeviceCollectorName = "RDMA"
)

type rdmaCollector struct {
	enabled bool
}

func New(opt *framework.Options) framework.DeviceCollector {
	return &rdmaCollector{
		enabled: features.DefaultKoordletFeatureGate.Enabled(features.RDMADevices),
	}
}

func (g *rdmaCollector) Shutdown() {
}

func (g *rdmaCollector) Enabled() bool {
	return g.enabled
}

func (g *rdmaCollector) Setup(fra *framework.Context) {
}

func (g *rdmaCollector) Run(stopCh <-chan struct{}) {
}

func (g *rdmaCollector) Started() bool {
	return true
}

func (g *rdmaCollector) Infos() metriccache.Devices {
	netDevices, err := GetNetDevice()
	if err != nil {
		klog.Errorf("failed to get net device: %v", err)
	}
	return netDevices
}

func (g *rdmaCollector) GetNodeMetric() ([]metriccache.MetricSample, error) {
	return nil, nil
}

func (g *rdmaCollector) GetPodMetric(uid, podParentDir string, cs []corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return nil, nil
}

func (g *rdmaCollector) GetContainerMetric(containerID, podParentDir string, c *corev1.ContainerStatus) ([]metriccache.MetricSample, error) {
	return nil, nil
}

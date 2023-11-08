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

package framework

import (
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
)

type Context struct {
	DeviceCollectors map[string]DeviceCollector
	Collectors       map[string]Collector
	State            *SharedState
}

func DeviceCollectorsStarted(devices map[string]DeviceCollector) bool {
	for name, device := range devices {
		if device.Enabled() && !device.Started() {
			klog.V(6).Infof("device collector %v is enabled but has not started yet", name)
			return false
		}
	}
	return true
}

func CollectorsHasStarted(collectors map[string]Collector) bool {
	for name, collector := range collectors {
		if collector.Enabled() && !collector.Started() {
			klog.V(6).Infof("collector %v is enabled but has not started yet", name)
			return false
		}
	}
	return true
}

type CPUStat struct {
	// TODO check CPUTick or CPUUsage can be abandoned
	CPUTick   uint64
	CPUUsage  uint64
	Timestamp time.Time
}

// SharedState is for sharing infos across collectors, for example the system resource collector use the result of
// pod and node resource collector for calculating system usage
type SharedState struct {
	LatestMetric
}

func NewSharedState() *SharedState {
	return &SharedState{
		LatestMetric: LatestMetric{
			podsCPUByCollector:    make(map[string]metriccache.Point),
			podsMemoryByCollector: make(map[string]metriccache.Point),
		},
	}
}

type LatestMetric struct {
	nodeMutex  sync.RWMutex
	nodeCPU    *metriccache.Point
	nodeMemory *metriccache.Point

	podMutex              sync.RWMutex
	podsCPUByCollector    map[string]metriccache.Point
	podsMemoryByCollector map[string]metriccache.Point

	hostAppMutex  sync.RWMutex
	hostAppCPU    *metriccache.Point
	hostAppMemory *metriccache.Point
}

func (r *SharedState) UpdateNodeUsage(cpu, memory metriccache.Point) {
	r.nodeMutex.Lock()
	defer r.nodeMutex.Unlock()
	r.nodeCPU = &cpu
	r.nodeMemory = &memory
}

func (r *SharedState) UpdatePodUsage(collectorName string, cpu, memory metriccache.Point) {
	r.podMutex.Lock()
	defer r.podMutex.Unlock()
	r.podsCPUByCollector[collectorName] = cpu
	r.podsMemoryByCollector[collectorName] = memory
}

func (r *SharedState) UpdateHostAppUsage(cpu, memory metriccache.Point) {
	r.hostAppMutex.Lock()
	defer r.hostAppMutex.Unlock()
	r.hostAppCPU = &cpu
	r.hostAppMemory = &memory
}

func (r *SharedState) GetNodeUsage() (cpu, memory *metriccache.Point) {
	r.nodeMutex.RLock()
	defer r.nodeMutex.RUnlock()
	return r.nodeCPU, r.nodeMemory
}

func (r *SharedState) GetHostAppUsage() (cpu, memory *metriccache.Point) {
	r.hostAppMutex.RLock()
	defer r.hostAppMutex.RUnlock()
	return r.hostAppCPU, r.hostAppMemory
}

func (r *SharedState) GetPodsUsageByCollector() (cpu, memory map[string]metriccache.Point) {
	r.podMutex.RLock()
	defer r.podMutex.RUnlock()
	podsCPU := map[string]metriccache.Point{}
	podsMemory := map[string]metriccache.Point{}
	for collector, val := range r.podsCPUByCollector {
		podsCPU[collector] = val
	}
	for collector, val := range r.podsMemoryByCollector {
		podsMemory[collector] = val
	}
	return podsCPU, podsMemory
}

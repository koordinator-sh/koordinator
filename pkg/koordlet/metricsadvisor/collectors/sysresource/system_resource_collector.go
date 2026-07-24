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

package sysresource

import (
	"fmt"
	"strconv"
	"time"

	"go.uber.org/atomic"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	CollectorName = "SystemResourceCollector"
)

var (
	timeNow = time.Now
)

type systemResourceCollector struct {
	collectInterval  time.Duration
	outdatedInterval time.Duration
	started          *atomic.Bool
	appendableDB     metriccache.Appendable
	sharedState      *framework.SharedState
}

func New(opt *framework.Options) framework.Collector {
	return &systemResourceCollector{
		collectInterval:  opt.Config.CollectResUsedInterval,
		outdatedInterval: opt.Config.CollectSysMetricOutdatedInterval,
		started:          atomic.NewBool(false),
		appendableDB:     opt.MetricCache,
	}
}

func (s *systemResourceCollector) Enabled() bool {
	return true
}

func (s *systemResourceCollector) Setup(c *framework.Context) {
	s.sharedState = c.State
}

func (s *systemResourceCollector) Run(stopCh <-chan struct{}) {
	dependencyStarted := func() bool {
		if cpu, memory := s.sharedState.GetNodeUsage(); cpu == nil || memory == nil {
			return false
		}
		if cpu, memory := s.sharedState.GetPodsUsageByCollector(); len(cpu) == 0 || len(memory) == 0 {
			return false
		}
		return true
	}
	if !cache.WaitForCacheSync(stopCh, dependencyStarted) {
		klog.Fatal("time out waiting for other collector started")
	}
	go wait.Until(s.collectSysResUsed, s.collectInterval, stopCh)
}

func (s *systemResourceCollector) Started() bool {
	return s.started.Load()
}

func (s *systemResourceCollector) collectSysResUsed() {
	klog.V(6).Info("collectSysResUsed start")

	// get node resource usage
	validTime := timeNow().Add(-s.outdatedInterval)
	nodeCPU, nodeMemory := s.sharedState.GetNodeUsage()
	if nodeCPU == nil || nodeMemory == nil {
		klog.Warningf("node resource cpu %v or memory %v is empty during collect system usage", nodeCPU, nodeMemory)
		return
	}
	if nodeCPU.Timestamp.Before(validTime) || nodeMemory.Timestamp.Before(validTime) {
		klog.Warningf("node resource metric is timeout, valid time %v, metric time is %v and %v",
			validTime.String(), nodeCPU.Timestamp.String(), nodeMemory.Timestamp.String())
		return
	}

	// get all pod resource usage
	podsCPUUsage, podsMemoryUsage, err := s.getAllPodsResourceUsage()
	if err != nil {
		klog.Warningf("get all pods resource usage failed, error %v", err)
		return
	}

	// get all host application resource usage
	hostAppCPU, hostAppMemory := s.sharedState.GetHostAppUsage()
	if hostAppCPU == nil || hostAppMemory == nil {
		klog.Warningf("host application resource cpu %v or memory %v is empty during collect system usage",
			hostAppCPU, hostAppMemory)
		return
	}
	if hostAppCPU.Timestamp.Before(validTime) || hostAppMemory.Timestamp.Before(validTime) {
		klog.Warningf("host application metric is timeout, valid time %v, metric time is %v and %v",
			validTime.String(), hostAppCPU.Timestamp.String(), hostAppMemory.Timestamp.String())
		return
	}

	// calculate system resource usage
	collectTime := timeNow()
	systemCPUUsage := util.MaxFloat64(nodeCPU.Value-podsCPUUsage-hostAppCPU.Value, 0)
	systemMemoryUsage := util.MaxFloat64(nodeMemory.Value-podsMemoryUsage-hostAppMemory.Value, 0)
	systemCPUMetric, err := metriccache.SystemCPUUsageMetric.GenerateSample(nil, collectTime, systemCPUUsage)
	if err != nil {
		klog.Warningf("generate system cpu metric failed, err %v", err)
		return
	}
	systemMemoryMetric, err := metriccache.SystemMemoryUsageMetric.GenerateSample(nil, collectTime, systemMemoryUsage)
	if err != nil {
		klog.Warningf("generate system memory metric failed, err %v", err)
		return
	}

	// commit metric sample
	appender := s.appendableDB.Appender()
	if err := appender.Append([]metriccache.MetricSample{systemCPUMetric, systemMemoryMetric}); err != nil {
		klog.ErrorS(err, "append system metrics error")
		return
	}
	if err := appender.Commit(); err != nil {
		klog.ErrorS(err, "commit system metrics error")
		return
	}

	// collect the per-NUMA system usage; failures only skip the NUMA part
	s.collectSysNUMAResUsed(collectTime, systemCPUUsage)

	klog.V(4).Infof("collect system resource usage finished, cpu %v, memory %v", systemCPUUsage, systemMemoryUsage)
	s.started.Store(true)
}

// collectSysNUMAResUsed calculates and commits the per-NUMA system usage.
// The memory usage is calculated by `node NUMA usage - sum(pod NUMA usage)` precisely; since there is no
// pod-level per-NUMA CPU accounting interface (especially on cgroups-v2), the cpu usage is apportioned from
// the node-level system cpu usage by the NUMA cpu usage ratio.
// NOTE: the host application usage is not excluded from the NUMA memory usage since its per-NUMA usage is
// not collected, which is an approximation different from the node-level system usage.
func (s *systemResourceCollector) collectSysNUMAResUsed(collectTime time.Time, systemCPUUsage float64) {
	validTime := timeNow().Add(-s.outdatedInterval)
	nodeNUMACPU, nodeNUMAMemory := s.sharedState.GetNodeNUMAUsage()
	if len(nodeNUMACPU) == 0 && len(nodeNUMAMemory) == 0 {
		klog.V(5).Infof("skip collecting system NUMA usage, node NUMA usage is empty")
		return
	}

	numaMetrics := make([]metriccache.MetricSample, 0)

	// memory: nodeNUMAMem - sum(pods NUMA memory), clamped to be non-negative
	podsNUMAMemoryByCollector := s.sharedState.GetPodsNUMAMemoryUsage()
	podsNUMAMemory := map[int32]float64{}
	podsNUMAMemoryValid := true
	for collector, numaUsage := range podsNUMAMemoryByCollector {
		for numaID, point := range numaUsage {
			if point.Timestamp.Before(validTime) {
				klog.V(4).Infof("pod collector %v NUMA memory metric is timeout, valid time %v, metric time is %v",
					collector, validTime.String(), point.Timestamp.String())
				podsNUMAMemoryValid = false
				break
			}
			podsNUMAMemory[numaID] += point.Value
		}
	}
	if podsNUMAMemoryValid && len(podsNUMAMemoryByCollector) > 0 {
		for numaID, nodeMem := range nodeNUMAMemory {
			if nodeMem.Timestamp.Before(validTime) {
				klog.V(4).Infof("node NUMA %d memory metric is timeout, valid time %v, metric time is %v",
					numaID, validTime.String(), nodeMem.Timestamp.String())
				continue
			}
			systemNUMAMemory := util.MaxFloat64(nodeMem.Value-podsNUMAMemory[numaID], 0)
			memMetric, err := metriccache.SystemNUMAMemoryUsageMetric.GenerateSample(
				metriccache.MetricPropertiesFunc.NUMA(strconv.FormatInt(int64(numaID), 10)), collectTime, systemNUMAMemory)
			if err != nil {
				klog.Warningf("generate system NUMA %d memory metric failed, err %v", numaID, err)
				continue
			}
			numaMetrics = append(numaMetrics, memMetric)
		}
	}

	// cpu: apportion the node-level system cpu usage by the NUMA cpu usage ratio;
	// if any node NUMA cpu metric is outdated, only the cpu part is skipped
	totalNUMACPU := float64(0)
	nodeNUMACPUValid := true
	for numaID, nodeCPU := range nodeNUMACPU {
		if nodeCPU.Timestamp.Before(validTime) {
			klog.V(4).Infof("node NUMA %d cpu metric is timeout, valid time %v, metric time is %v",
				numaID, validTime.String(), nodeCPU.Timestamp.String())
			nodeNUMACPUValid = false
			break
		}
		totalNUMACPU += nodeCPU.Value
	}
	if nodeNUMACPUValid {
		for numaID, nodeCPU := range nodeNUMACPU {
			systemNUMACPU := float64(0)
			if totalNUMACPU > 0 {
				systemNUMACPU = systemCPUUsage * nodeCPU.Value / totalNUMACPU
			}
			cpuMetric, err := metriccache.SystemNUMACPUUsageMetric.GenerateSample(
				metriccache.MetricPropertiesFunc.NUMA(strconv.FormatInt(int64(numaID), 10)), collectTime, systemNUMACPU)
			if err != nil {
				klog.Warningf("generate system NUMA %d cpu metric failed, err %v", numaID, err)
				continue
			}
			numaMetrics = append(numaMetrics, cpuMetric)
		}
	}

	if len(numaMetrics) <= 0 {
		return
	}
	appender := s.appendableDB.Appender()
	if err := appender.Append(numaMetrics); err != nil {
		klog.ErrorS(err, "append system NUMA metrics error")
		return
	}
	if err := appender.Commit(); err != nil {
		klog.ErrorS(err, "commit system NUMA metrics error")
		return
	}
	klog.V(6).Infof("collect system NUMA resource usage finished, metrics num %v", len(numaMetrics))
}

func (s *systemResourceCollector) getAllPodsResourceUsage() (cpuCore float64, memory float64, err error) {
	validTime := timeNow().Add(-s.outdatedInterval)
	podCPUByCollector, podMemoryByCollector := s.sharedState.GetPodsUsageByCollector()
	if len(podCPUByCollector) == 0 || len(podMemoryByCollector) == 0 {
		err = fmt.Errorf("pod resource cpu %v or memory %v is empty during collect system usage", podCPUByCollector, podMemoryByCollector)
		return
	}
	for collector, podCPU := range podCPUByCollector {
		if podCPU.Timestamp.Before(validTime) {
			err = fmt.Errorf("pod collector %v cpu resource metric is timeout, valid time %v, metric time is %v",
				collector, validTime.String(), podCPU.Timestamp.String())
			return
		}
		cpuCore += podCPU.Value
	}
	for collector, podMemory := range podMemoryByCollector {
		if podMemory.Timestamp.Before(validTime) {
			err = fmt.Errorf("pod collector %v memory resource metric is timeout, valid time %v, metric time is %v",
				collector, validTime.String(), podMemory.Timestamp.String())
			return
		}
		memory += podMemory.Value
	}
	return
}

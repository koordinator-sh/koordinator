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

	metricOutdatedIntervalRatio = 3
)

var (
	timeNow = time.Now
)

type systemResourceCollector struct {
	collectInterval time.Duration
	started         *atomic.Bool
	appendableDB    metriccache.Appendable
	sharedState     *framework.SharedState
}

func New(opt *framework.Options) framework.Collector {
	return &systemResourceCollector{
		collectInterval: opt.Config.CollectResUsedInterval,
		started:         atomic.NewBool(false),
		appendableDB:    opt.MetricCache,
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
	validTime := timeNow().Add(-s.collectInterval * metricOutdatedIntervalRatio)
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

	// calculate system resource usage
	systemCPUUsage := util.MaxFloat64(nodeCPU.Value-podsCPUUsage, 0)
	systemMemoryUsage := util.MaxFloat64(nodeMemory.Value-podsMemoryUsage, 0)
	collectTime := nodeCPU.Timestamp
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

	klog.V(4).Infof("collect system resource usage finished, cpu %v, memory %v", systemCPUUsage, systemMemoryUsage)
	s.started.Store(true)
}

func (s *systemResourceCollector) getAllPodsResourceUsage() (cpuCore float64, memory float64, err error) {
	validTime := timeNow().Add(-s.collectInterval * metricOutdatedIntervalRatio)
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

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

package hostapplication

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/atomic"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

const (
	CollectorName = "HostApplicationCollector"
)

var (
	timeNow = time.Now
)

type hostAppCollector struct {
	collectInterval time.Duration
	started         *atomic.Bool
	appendableDB    metriccache.Appendable
	statesInformer  statesinformer.StatesInformer
	cgroupReader    resourceexecutor.CgroupReader
	lastAppCPUStat  *gocache.Cache
	sharedState     *framework.SharedState
}

func New(opt *framework.Options) framework.Collector {
	collectInterval := opt.Config.CollectResUsedInterval
	return &hostAppCollector{
		collectInterval: collectInterval,
		started:         atomic.NewBool(false),
		appendableDB:    opt.MetricCache,
		statesInformer:  opt.StatesInformer,
		cgroupReader:    opt.CgroupReader,
		lastAppCPUStat:  gocache.New(collectInterval*framework.ContextExpiredRatio, framework.CleanupInterval),
	}
}

var _ framework.Collector = &hostAppCollector{}

func (h *hostAppCollector) Enabled() bool {
	return true
}

func (h *hostAppCollector) Setup(c *framework.Context) {
	h.sharedState = c.State
}

func (h *hostAppCollector) Run(stopCh <-chan struct{}) {
	if !cache.WaitForCacheSync(stopCh, h.statesInformer.HasSynced) {
		// Koordlet exit because of statesInformer sync failed.
		klog.Fatalf("timed out waiting for states informer caches to sync")
	}
	go wait.Until(h.collectHostAppResUsed, h.collectInterval, stopCh)
}

func (h *hostAppCollector) Started() bool {
	return h.started.Load()
}

func (h *hostAppCollector) collectHostAppResUsed() {
	klog.V(6).Info("start collectHostAppResUsed")
	nodeSLO := h.statesInformer.GetNodeSLO()
	if nodeSLO == nil {
		klog.Warningf("get nil node slo during collect host application resource usage")
		return
	}
	count := 0
	metrics := make([]metriccache.MetricSample, 0)
	allCPUUsageCores := metriccache.Point{Timestamp: timeNow(), Value: 0}
	allMemoryUsage := metriccache.Point{Timestamp: timeNow(), Value: 0}
	for _, hostApp := range nodeSLO.Spec.HostApplications {
		collectTime := timeNow()
		cgroupDir := util.GetHostAppCgroupRelativePath(&hostApp)
		currentCPUUsage, errCPU := h.cgroupReader.ReadCPUAcctUsage(cgroupDir)
		memStat, errMem := h.cgroupReader.ReadMemoryStat(cgroupDir)
		if errCPU != nil || errMem != nil {
			klog.V(4).Infof("cannot collect host application resource usage, cpu reason %v, memory reason %v",
				errCPU, errMem)
			continue
		}
		if memStat == nil {
			klog.V(4).Infof("get nil memory status during collect host application resource usage")
			continue
		}

		lastCPUStatValue, ok := h.lastAppCPUStat.Get(hostApp.Name)
		h.lastAppCPUStat.Set(hostApp.Name, framework.CPUStat{
			CPUUsage:  currentCPUUsage,
			Timestamp: collectTime,
		}, gocache.DefaultExpiration)
		klog.V(6).Infof("last host application cpu stat size in collector cache %v", h.lastAppCPUStat.ItemCount())
		if !ok {
			klog.V(4).Infof("ignore the first cpu stat collection for host app %s", hostApp.Name)
			continue
		}
		lastCPUStat := lastCPUStatValue.(framework.CPUStat)

		cpuUsageValue := float64(currentCPUUsage-lastCPUStat.CPUUsage) / float64(collectTime.Sub(lastCPUStat.Timestamp))
		cpuUsageMetric, err := metriccache.HostAppCPUUsageMetric.GenerateSample(
			metriccache.MetricPropertiesFunc.HostApplication(hostApp.Name),
			collectTime, cpuUsageValue)
		if err != nil {
			klog.V(4).Infof("failed to generate pod mem metrics for host application %s , err %v", hostApp.Name, err)
			return
		}
		memoryUsageValue := memStat.Usage()
		memUsageMetric, err := metriccache.HostAppMemoryUsageMetric.GenerateSample(
			metriccache.MetricPropertiesFunc.HostApplication(hostApp.Name),
			collectTime, float64(memoryUsageValue))
		if err != nil {
			klog.V(4).Infof("failed to generate memory metrics for host application %s , err %v", hostApp.Name, err)
			return
		}

		metrics = append(metrics, cpuUsageMetric, memUsageMetric)
		klog.V(6).Infof("collect host application %v finished, metric %+v", hostApp.Name, metrics)
		count++
		allCPUUsageCores.Value += cpuUsageValue
		allMemoryUsage.Value += float64(memoryUsageValue)
	}

	appender := h.appendableDB.Appender()
	if err := appender.Append(metrics); err != nil {
		klog.Warningf("Append host application metrics error: %v", err)
		return
	}

	if err := appender.Commit(); err != nil {
		klog.Warningf("Commit host application metrics failed, error: %v", err)
		return
	}

	h.sharedState.UpdateHostAppUsage(allCPUUsageCores, allMemoryUsage)

	h.started.Store(true)
	klog.V(4).Infof("collectHostAppResUsed finished, host application num %d, collected %d",
		len(nodeSLO.Spec.HostApplications), count)
}

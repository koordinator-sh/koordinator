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

package beresource

import (
	"time"

	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	CollectorName = "BEResourceCollector"
)

type beResourceCollector struct {
	collectInterval time.Duration
	started         *atomic.Bool
	metricCache     metriccache.MetricCache
	statesInformer  statesinformer.StatesInformer
	cgroupReader    resourceexecutor.CgroupReader

	lastBECPUStat *framework.CPUStat
}

func New(opt *framework.Options) framework.Collector {
	return &beResourceCollector{
		collectInterval: opt.Config.CollectResUsedInterval,
		started:         atomic.NewBool(false),
		metricCache:     opt.MetricCache,
		statesInformer:  opt.StatesInformer,
		cgroupReader:    opt.CgroupReader,
	}
}

func (b *beResourceCollector) Enabled() bool {
	return true
}

func (b *beResourceCollector) Setup(s *framework.Context) {
	return
}

func (b *beResourceCollector) Run(stopCh <-chan struct{}) {
	if !cache.WaitForCacheSync(stopCh, b.statesInformer.HasSynced) {
		// Koordlet exit because of statesInformer sync failed.
		klog.Fatalf("timed out waiting for states informer caches to sync")
	}
	go wait.Until(b.collectBECPUResourceMetric, b.collectInterval, stopCh)
}

func (b *beResourceCollector) Started() bool {
	return b.started.Load()
}

func (b *beResourceCollector) collectBECPUResourceMetric() {
	klog.V(6).Info("collectBECPUResourceMetric start")

	realMilliLimit, err := b.getBECPURealMilliLimit()
	if err != nil {
		klog.Errorf("getBECPURealMilliLimit failed, error: %v", err)
		return
	}

	beCPUMilliRequest := b.getBECPURequestMilliCores()

	beCPUUsageMilliCores, err := b.getBECPUUsageMilliCores()
	if err != nil {
		klog.Errorf("getBECPUUsageCores failed, error: %v", err)
		return
	}

	collectTime := time.Now()
	beLimit, err01 := metriccache.NodeBEMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationRealLimit)), collectTime, float64(realMilliLimit))
	beRequest, err02 := metriccache.NodeBEMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationRequest)), collectTime, float64(beCPUMilliRequest))
	beUsage, err03 := metriccache.NodeBEMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationUsage)), collectTime, float64(beCPUUsageMilliCores))

	if err01 != nil || err02 != nil || err03 != nil {
		klog.Errorf("failed to collect node BECPU, beLimitGenerateSampleErr: %v, beRequestGenerateSampleErr: %v, beUsageGenerateSampleErr: %v", err01, err02, err03)
		return
	}
	metrics.RecordBESuppressBEUsedCPU(float64(beCPUUsageMilliCores) / 1000)
	beMetrics := make([]metriccache.MetricSample, 0)
	beMetrics = append(beMetrics, beLimit, beRequest, beUsage)

	appender := b.metricCache.Appender()
	if err := appender.Append(beMetrics); err != nil {
		klog.ErrorS(err, "Append node BECPUResource metrics error")
		return
	}

	if err := appender.Commit(); err != nil {
		klog.ErrorS(err, "Commit node BECPUResouce metrics failed")
		return
	}

	b.started.Store(true)
	klog.V(6).Info("collectBECPUResourceMetric finished")
}

func (b *beResourceCollector) getBECPURealMilliLimit() (int, error) {
	limit := 0

	cpuSet, err := koordletutil.GetBECgroupCurCPUSet()
	if err != nil {
		return 0, err
	}
	limit = len(cpuSet) * 1000

	BECgroupParentDir := koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	cfsQuota, err := b.cgroupReader.ReadCPUQuota(BECgroupParentDir)
	if err != nil {
		return 0, err
	}

	// -1 means not suppress by cfs_quota
	if cfsQuota == -1 {
		return limit, nil
	}

	cfsPeriod, err := b.cgroupReader.ReadCPUPeriod(BECgroupParentDir)
	if err != nil {
		return 0, err
	}

	limitByCfsQuota := int(cfsQuota * 1000 / cfsPeriod)

	if limitByCfsQuota < limit {
		limit = limitByCfsQuota
	}

	return limit, nil
}

func (b *beResourceCollector) getBECPURequestMilliCores() int64 {
	requestSum := int64(0)
	for _, podMeta := range b.statesInformer.GetAllPods() {
		pod := podMeta.Pod
		if apiext.GetPodQoSClassRaw(pod) == apiext.QoSBE {
			podCPUReq := util.GetPodBEMilliCPURequest(pod)
			if podCPUReq > 0 {
				requestSum += podCPUReq
			}
		}
	}
	return requestSum
}

func (b *beResourceCollector) getBECPUUsageMilliCores() (int64, error) {
	klog.V(6).Info("getBECPUUsageCores start")

	cpuUsageMilliCores := int64(0)
	collectTime := time.Now()
	BECgroupParentDir := koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	currentCPUUsage, err := b.cgroupReader.ReadCPUAcctUsage(BECgroupParentDir)
	if err != nil {
		klog.Warningf("failed to collect be cgroup usage, error: %v", err)
		return cpuUsageMilliCores, err
	}

	lastCPUStat := b.lastBECPUStat
	b.lastBECPUStat = &framework.CPUStat{
		CPUUsage:  currentCPUUsage,
		Timestamp: collectTime,
	}

	if lastCPUStat == nil {
		klog.V(6).Infof("ignore the first cpu stat collection")
		return cpuUsageMilliCores, nil
	}

	// NOTE:
	// 1. do subtraction and division first to avoid overflow.
	// 2. To solve the problem of insufficient precision of nanosecond floating-point numbers, convert to Milliseconds
	cpuUsageValue := float64((currentCPUUsage-lastCPUStat.CPUUsage)/1000000) / float64(collectTime.Sub(lastCPUStat.Timestamp).Milliseconds())
	cpuUsageMilliCores = int64(cpuUsageValue * 1000)
	// 1.0 CPU = 1000 Milli-CPU
	// cpuUsageCores := resource.NewMilliQuantity(int64(cpuUsageValue*1000), resource.DecimalSI)
	klog.V(6).Infof("collectBECPUUsageCores finished %.2f", cpuUsageValue)
	return cpuUsageMilliCores, nil
}

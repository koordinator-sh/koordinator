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

package noderesource

import (
	"strconv"
	"time"

	"go.uber.org/atomic"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	CollectorName = "NodeResourceCollector"
)

var (
	timeNow = time.Now
)

// perCPUStat records the per-CPU usage ticks of the last collection.
type perCPUStat struct {
	cpuTicks  map[int32]uint64
	timestamp time.Time
}

// TODO more ut is needed for this plugin
type nodeResourceCollector struct {
	collectInterval time.Duration
	started         *atomic.Bool
	appendableDB    metriccache.Appendable
	metricDB        metriccache.MetricCache

	lastNodeCPUStat    *framework.CPUStat
	lastNodePerCPUStat *perCPUStat

	sharedState      *framework.SharedState
	deviceCollectors map[string]framework.DeviceCollector
}

func New(opt *framework.Options) framework.Collector {
	return &nodeResourceCollector{
		collectInterval: opt.Config.CollectResUsedInterval,
		started:         atomic.NewBool(false),
		appendableDB:    opt.MetricCache,
		metricDB:        opt.MetricCache,
	}
}

func (n *nodeResourceCollector) Enabled() bool {
	return true
}

func (n *nodeResourceCollector) Setup(c *framework.Context) {
	n.deviceCollectors = c.DeviceCollectors
	n.sharedState = c.State
}

func (n *nodeResourceCollector) Run(stopCh <-chan struct{}) {
	devicesSynced := func() bool {
		return framework.DeviceCollectorsStarted(n.deviceCollectors)
	}
	if !cache.WaitForCacheSync(stopCh, devicesSynced) {
		// Koordlet exit because of statesInformer sync failed.
		klog.Fatalf("timed out waiting for devices to sync")
	}
	go wait.Until(n.collectNodeResUsed, n.collectInterval, stopCh)
}

func (n *nodeResourceCollector) Started() bool {
	return n.started.Load()
}

func (n *nodeResourceCollector) collectNodeResUsed() {
	klog.V(6).Info("collectNodeResUsed start")
	nodeMetrics := make([]metriccache.MetricSample, 0)
	collectTime := timeNow()

	// get the accumulated cpu ticks
	currentCPUTick, err0 := koordletutil.GetCPUStatUsageTicks()
	// NOTE: The collected memory usage is in kilobytes not bytes.
	memInfo, err1 := koordletutil.GetMemInfo()
	if err0 != nil || err1 != nil {
		klog.Warningf("failed to collect node usage, CPU err: %s, Memory err: %s", err0, err1)
		return
	}

	memUsageValue := float64(memInfo.MemUsageBytes())
	memUsageMetrics, err := metriccache.NodeMemoryUsageMetric.GenerateSample(nil, collectTime, memUsageValue)
	if err != nil {
		klog.Warningf("generate node cpu metrics failed, err %v", err)
		return
	}
	nodeMetrics = append(nodeMetrics, memUsageMetrics)

	lastCPUStat := n.lastNodeCPUStat
	n.lastNodeCPUStat = &framework.CPUStat{
		CPUTick:   currentCPUTick,
		Timestamp: collectTime,
	}
	if lastCPUStat == nil {
		klog.V(6).Infof("ignore the first cpu stat collection")
		return
	}
	// 1 jiffy can be 10ms by default.
	// NOTE: do subtraction and division first to avoid overflow
	cpuUsageValue := float64(currentCPUTick-lastCPUStat.CPUTick) / system.GetPeriodTicks(lastCPUStat.Timestamp, collectTime)
	cpuUsageMetrics, err := metriccache.NodeCPUUsageMetric.GenerateSample(nil, collectTime, cpuUsageValue)
	if err != nil {
		klog.Warningf("generate node cpu metrics failed, err %v", err)
		return
	}
	nodeMetrics = append(nodeMetrics, cpuUsageMetrics)

	// collect the per-NUMA node usage; failures only skip the NUMA part without blocking the node-level collection
	numaUsageMetrics, numaCPUUsage, numaMemUsage := n.collectNodeNUMAResUsed(collectTime)
	nodeMetrics = append(nodeMetrics, numaUsageMetrics...)

	for name, deviceCollector := range n.deviceCollectors {
		if !deviceCollector.Enabled() {
			klog.V(6).Infof("skip node metrics from the disabled device collector %s", name)
			continue
		}

		if metric, err := deviceCollector.GetNodeMetric(); err != nil {
			klog.Warningf("get node metrics from the device collector %s failed, err: %s", name, err)
		} else {
			nodeMetrics = append(nodeMetrics, metric...)
		}
		if info := deviceCollector.Infos(); info != nil {
			n.metricDB.Set(info.Type(), info)
		}
	}

	appender := n.appendableDB.Appender()
	if err := appender.Append(nodeMetrics); err != nil {
		klog.ErrorS(err, "Append node metrics error")
		return
	}

	if err := appender.Commit(); err != nil {
		klog.Warningf("Commit node metrics failed, reason: %v", err)
		return
	}

	n.sharedState.UpdateNodeUsage(metriccache.Point{Timestamp: collectTime, Value: cpuUsageValue},
		metriccache.Point{Timestamp: collectTime, Value: memUsageValue})
	if len(numaCPUUsage) > 0 || len(numaMemUsage) > 0 {
		n.sharedState.UpdateNodeNUMAUsage(numaCPUUsage, numaMemUsage)
	}

	// update collect time
	n.started.Store(true)
	metrics.RecordNodeUsedCPU(cpuUsageValue) // in cpu cores
	metrics.RecordNodeUsedMemory(memUsageValue)

	klog.V(4).Infof("collectNodeResUsed finished, count %v, cpu[%v], mem[%v]",
		len(nodeMetrics), cpuUsageValue, memUsageValue)
}

// collectNodeNUMAResUsed collects the per-NUMA node cpu and memory usage.
// The cpu usage is aggregated from the per-CPU usage ticks by the CPU-to-NUMA topology, and the memory
// usage is estimated from the per-NUMA meminfo.
func (n *nodeResourceCollector) collectNodeNUMAResUsed(collectTime time.Time) ([]metriccache.MetricSample, map[int32]metriccache.Point, map[int32]metriccache.Point) {
	numaMetrics := make([]metriccache.MetricSample, 0)

	// per-NUMA memory usage
	numaMemUsage := map[int32]metriccache.Point{}
	nodeNUMAInfo, err := koordletutil.GetNodeNUMAInfo()
	if err != nil {
		klog.Warningf("failed to get node NUMA info for NUMA memory usage, err: %s", err)
	} else {
		for numaID, memInfo := range nodeNUMAInfo.MemInfoMap {
			if memInfo == nil {
				continue
			}
			memUsageValue := float64(memInfo.NUMAMemUsageBytes())
			memUsageMetric, err := metriccache.NodeNUMAMemoryUsageMetric.GenerateSample(
				metriccache.MetricPropertiesFunc.NUMA(strconv.FormatInt(int64(numaID), 10)), collectTime, memUsageValue)
			if err != nil {
				klog.Warningf("generate NUMA %d memory usage metric failed, err %v", numaID, err)
				continue
			}
			numaMetrics = append(numaMetrics, memUsageMetric)
			numaMemUsage[numaID] = metriccache.Point{Timestamp: collectTime, Value: memUsageValue}
		}
	}

	// per-NUMA cpu usage
	numaCPUUsage := map[int32]metriccache.Point{}
	currentPerCPUTicks, err := koordletutil.GetPerCPUStatUsageTicks()
	if err != nil {
		klog.Warningf("failed to get per-CPU stat usage ticks, err: %s", err)
		return numaMetrics, numaCPUUsage, numaMemUsage
	}
	lastPerCPUStat := n.lastNodePerCPUStat
	n.lastNodePerCPUStat = &perCPUStat{
		cpuTicks:  currentPerCPUTicks,
		timestamp: collectTime,
	}
	if lastPerCPUStat == nil {
		klog.V(6).Infof("ignore the first per-CPU stat collection")
		return numaMetrics, numaCPUUsage, numaMemUsage
	}
	cpuToNUMA := n.getCPUToNUMAMapping()
	if len(cpuToNUMA) <= 0 {
		klog.V(4).Infof("skip NUMA cpu usage collection, CPU-to-NUMA topology is not ready")
		return numaMetrics, numaCPUUsage, numaMemUsage
	}

	perNUMADeltaTicks := map[int32]uint64{}
	for cpuID, ticks := range currentPerCPUTicks {
		numaID, ok := cpuToNUMA[cpuID]
		if !ok {
			klog.V(6).Infof("skip cpu %d whose NUMA node is unknown", cpuID)
			continue
		}
		lastTicks, ok := lastPerCPUStat.cpuTicks[cpuID]
		if !ok || ticks < lastTicks {
			continue
		}
		perNUMADeltaTicks[numaID] += ticks - lastTicks
	}
	periodTicks := system.GetPeriodTicks(lastPerCPUStat.timestamp, collectTime)
	if periodTicks <= 0 {
		// e.g. the collect timestamps are identical or the clock went backwards
		klog.V(4).Infof("skip NUMA cpu usage collection, invalid period ticks %v from %v to %v",
			periodTicks, lastPerCPUStat.timestamp, collectTime)
		return numaMetrics, numaCPUUsage, numaMemUsage
	}
	for numaID, deltaTicks := range perNUMADeltaTicks {
		cpuUsageValue := float64(deltaTicks) / periodTicks
		cpuUsageMetric, err := metriccache.NodeNUMACPUUsageMetric.GenerateSample(
			metriccache.MetricPropertiesFunc.NUMA(strconv.FormatInt(int64(numaID), 10)), collectTime, cpuUsageValue)
		if err != nil {
			klog.Warningf("generate NUMA %d cpu usage metric failed, err %v", numaID, err)
			continue
		}
		numaMetrics = append(numaMetrics, cpuUsageMetric)
		numaCPUUsage[numaID] = metriccache.Point{Timestamp: collectTime, Value: cpuUsageValue}
	}

	klog.V(6).Infof("collect NUMA node usage finished, cpu %+v, memory %+v", numaCPUUsage, numaMemUsage)
	return numaMetrics, numaCPUUsage, numaMemUsage
}

// getCPUToNUMAMapping returns the mapping from the logical CPU ID to the NUMA node ID, based on the
// node CPU info collected by the nodeinfo collector. It returns nil if the CPU info is not ready.
func (n *nodeResourceCollector) getCPUToNUMAMapping() map[int32]int32 {
	value, ok := n.metricDB.Get(metriccache.NodeCPUInfoKey)
	if !ok {
		return nil
	}
	cpuInfo, ok := value.(*metriccache.NodeCPUInfo)
	if !ok || cpuInfo == nil {
		return nil
	}
	cpuToNUMA := make(map[int32]int32, len(cpuInfo.ProcessorInfos))
	for _, p := range cpuInfo.ProcessorInfos {
		cpuToNUMA[p.CPUID] = p.NodeID
	}
	return cpuToNUMA
}

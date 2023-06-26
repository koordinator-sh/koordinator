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

// TODO more ut is needed for this plugin
type nodeResourceCollector struct {
	collectInterval time.Duration
	started         *atomic.Bool
	appendableDB    metriccache.Appendable
	metricDB        metriccache.MetricCache

	lastNodeCPUStat *framework.CPUStat

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
	collectTime := time.Now()

	// get the accumulated cpu ticks
	currentCPUTick, err0 := koordletutil.GetCPUStatUsageTicks()
	// NOTE: The collected memory usage is in kilobytes not bytes.
	memUsageKB, err1 := koordletutil.GetMemInfoUsageKB()
	if err0 != nil || err1 != nil {
		klog.Warningf("failed to collect node usage, CPU err: %s, Memory err: %s", err0, err1)
		return
	}

	memUsageValue := 1024 * float64(memUsageKB)
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

	for _, deviceCollector := range n.deviceCollectors {
		if metric, _ := deviceCollector.GetNodeMetric(); metric != nil {
			nodeMetrics = append(nodeMetrics, metric...)
		}
		if info := deviceCollector.Infos(); info != nil {
			n.metricDB.Set(koordletutil.GPUDeviceType, info)
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

	// update collect time
	n.started.Store(true)
	metrics.RecordNodeUsedCPU(cpuUsageValue) // in cpu cores

	klog.V(4).Infof("collectNodeResUsed finished, count %v, cpu[%v], mem[%v]",
		len(nodeMetrics), cpuUsageValue, memUsageValue)
}

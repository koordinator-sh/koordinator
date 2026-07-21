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

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
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
	metricCache      metriccache.MetricCache
	sharedState      *framework.SharedState
	statesInformer   statesinformer.StatesInformer
}

func New(opt *framework.Options) framework.Collector {
	return &systemResourceCollector{
		collectInterval:  opt.Config.CollectResUsedInterval,
		outdatedInterval: opt.Config.CollectSysMetricOutdatedInterval,
		started:          atomic.NewBool(false),
		metricCache:      opt.MetricCache,
		statesInformer:   opt.StatesInformer,
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

	// adjust node/pod memory values based on NodeMemoryCollectPolicy
	collectTime := timeNow()
	nodeMemoryValue := nodeMemory.Value
	podsMemoryValue := podsMemoryUsage
	hostAppMemoryValue := hostAppMemory.Value
	if s.statesInformer != nil {
		nodeMetricSpec := s.statesInformer.GetNodeMetricSpec()
		if nodeMetricSpec != nil && nodeMetricSpec.CollectPolicy != nil && nodeMetricSpec.CollectPolicy.NodeMemoryCollectPolicy != nil {
			policy := *nodeMetricSpec.CollectPolicy.NodeMemoryCollectPolicy
			if policy == slov1alpha1.UsageWithPageCache || policy == slov1alpha1.UsageWithHotPageCache {
				nodeMemoryValue, podsMemoryValue, hostAppMemoryValue = s.queryMemoryWithPolicy(policy, collectTime, nodeMemory, podsMemoryUsage, hostAppMemory)
			}
		}
	}

	// calculate system resource usage
	systemCPUUsage := util.MaxFloat64(nodeCPU.Value-podsCPUUsage-hostAppCPU.Value, 0)
	systemMemoryUsage := util.MaxFloat64(nodeMemoryValue-podsMemoryValue-hostAppMemoryValue, 0)
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
	appender := s.metricCache.Appender()
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

func (s *systemResourceCollector) queryMemoryWithPolicy(policy slov1alpha1.NodeMemoryCollectPolicy, collectTime time.Time,
	nodeMemory *metriccache.Point, podsMemory float64, hostAppMemory *metriccache.Point) (float64, float64, float64) {
	// Query the correct node memory metric from the metric cache based on the policy.
	// The sharedState only stores the default (UsageWithoutPageCache) memory value.
	// For pods and host apps, the sharedState values are already the correct policy-aware values
	// (podresource and hostapplication collectors handle the policy).
	// We only need to adjust the node memory to match the same policy.
	querier, err := s.metricCache.Querier(collectTime.Add(-s.outdatedInterval), collectTime)
	if err != nil {
		klog.Warningf("create querier for policy-aware memory query failed, err %v", err)
		return nodeMemory.Value, podsMemory, hostAppMemory.Value
	}
	defer querier.Close()

	var nodeMemoryMetric metriccache.MetricMeta
	var metaErr error
	switch policy {
	case slov1alpha1.UsageWithPageCache:
		nodeMemoryMetric, metaErr = metriccache.NodeMemoryUsageWithPageCacheMetric.BuildQueryMeta(nil)
	case slov1alpha1.UsageWithHotPageCache:
		nodeMemoryMetric, metaErr = metriccache.NodeMemoryWithHotPageUsageMetric.BuildQueryMeta(nil)
	}
	if metaErr != nil {
		klog.Warningf("build query meta for policy-aware node memory metric failed, err %v", metaErr)
		return nodeMemory.Value, podsMemory, hostAppMemory.Value
	}

	var nodeMemResult metriccache.AggregateResult = metriccache.DefaultAggregateResultFactory.New(nodeMemoryMetric)
	if err := querier.Query(nodeMemoryMetric, nil, nodeMemResult); err != nil {
		klog.Warningf("query policy-aware node memory metric failed, err %v", err)
		return nodeMemory.Value, podsMemory, hostAppMemory.Value
	}

	nodeMemVal, err := nodeMemResult.Value(metriccache.AggregationTypeLast)
	if err != nil {
		klog.Warningf("get policy-aware node memory value failed, err %v", err)
		return nodeMemory.Value, podsMemory, hostAppMemory.Value
	}

	return nodeMemVal, podsMemory, hostAppMemory.Value
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

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

package cpuevict

import (
	"fmt"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	CPUEvictName = "CPUEvict"

	beCPUSatisfactionLowPercentMax   = 60
	beCPUSatisfactionUpperPercentMax = 100
	beCPUUsageThresholdPercent       = 90

	defaultMinAllocatableBatchMilliCPU = 1
)

var _ framework.QOSStrategy = &cpuEvictor{}

type cpuEvictor struct {
	evictInterval         time.Duration
	evictCoolingInterval  time.Duration
	metricCollectInterval time.Duration
	statesInformer        statesinformer.StatesInformer
	metricCache           metriccache.MetricCache
	evictor               *framework.Evictor
	lastEvictTime         time.Time
}

func New(opt *framework.Options) framework.QOSStrategy {
	return &cpuEvictor{
		evictInterval:         time.Duration(opt.Config.CPUEvictIntervalSeconds) * time.Second,
		evictCoolingInterval:  time.Duration(opt.Config.CPUEvictCoolTimeSeconds) * time.Second,
		metricCollectInterval: opt.MetricAdvisorConfig.CollectResUsedInterval,
		statesInformer:        opt.StatesInformer,
		metricCache:           opt.MetricCache,
		lastEvictTime:         time.Now(),
	}
}

func (c *cpuEvictor) Enabled() bool {
	return features.DefaultKoordletFeatureGate.Enabled(features.BECPUEvict) && c.evictInterval > 0
}

func (c *cpuEvictor) Setup(ctx *framework.Context) {
	c.evictor = ctx.Evictor
}

func (c *cpuEvictor) Run(stopCh <-chan struct{}) {
	go wait.Until(c.cpuEvict, c.evictInterval, stopCh)
}

type podEvictCPUInfo struct {
	milliRequest   int64
	milliUsedCores int64
	cpuUsage       float64 // cpuUsage = milliUsedCores / milliRequest
	pod            *corev1.Pod
}

func (c *cpuEvictor) cpuEvict() {
	klog.V(5).Infof("cpu evict process start")

	nodeSLO := c.statesInformer.GetNodeSLO()
	if disabled, err := features.IsFeatureDisabled(nodeSLO, features.BECPUEvict); err != nil {
		klog.Warningf("cpuEvict failed, cannot check the feature gate, err: %s", err)
		return
	} else if disabled {
		klog.V(4).Infof("cpuEvict skipped, nodeSLO disable the feature gate")
		return
	}

	if time.Since(c.lastEvictTime) < c.evictCoolingInterval {
		klog.V(4).Infof("skip CPU evict process, still in evict cool time")
		return
	}

	thresholdConfig := nodeSLO.Spec.ResourceUsedThresholdWithBE
	windowSeconds := int64(c.metricCollectInterval.Seconds() * 2)
	if thresholdConfig.CPUEvictTimeWindowSeconds != nil && *thresholdConfig.CPUEvictTimeWindowSeconds > int64(c.metricCollectInterval.Seconds()) {
		windowSeconds = *thresholdConfig.CPUEvictTimeWindowSeconds
	}

	node := c.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("cpuEvict failed, got nil node")
		return
	}

	cpuCapacity := node.Status.Capacity.Cpu().Value()
	if cpuCapacity <= 0 {
		klog.Warningf("cpuEvict failed, node cpuCapacity not valid,value: %d", cpuCapacity)
		return
	}

	c.evictByResourceSatisfaction(node, thresholdConfig, windowSeconds)
	klog.V(5).Info("cpu evict process finished.")
}

func (c *cpuEvictor) calculateMilliRelease(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, windowSeconds int64) int64 {
	// Step1: Calculate release resource by BECPUResourceMetric in window
	queryparam := helpers.GenerateQueryParamsAvg(windowSeconds)
	querier, err := c.metricCache.Querier(*queryparam.Start, *queryparam.End)
	if err != nil {
		klog.Warningf("get query failed, error %v", err)
		return 0
	}
	// BECPUUsage
	avgBECPUMilliUsage, count01 := getBECPUMetric(metriccache.BEResouceAllocationUsage, querier, queryparam.Aggregate)
	// BECPURequest
	avgBECPUMilliRequest, count02 := getBECPUMetric(metriccache.BEResouceAllocationRequest, querier, queryparam.Aggregate)
	// BECPULimit
	avgBECPUMilliRealLimit, count03 := getBECPUMetric(metriccache.BEResouceAllocationRealLimit, querier, queryparam.Aggregate)

	// CPU Satisfaction considers the allocatable when policy=evictByAllocatable.
	avgBECPUMilliLimit := avgBECPUMilliRealLimit
	beCPUMilliAllocatable := c.getBEMilliAllocatable()
	if thresholdConfig.CPUEvictPolicy == slov1alpha1.EvictByAllocatablePolicy {
		avgBECPUMilliLimit = beCPUMilliAllocatable
	}

	// get min count
	count := minInt64(count01, count02, count03)

	if !isAvgQueryResultValid(windowSeconds, int64(c.metricCollectInterval.Seconds()), count) {
		return 0
	}

	if !isBECPUUsageHighEnough(avgBECPUMilliUsage, avgBECPUMilliLimit, thresholdConfig.CPUEvictBEUsageThresholdPercent) {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped, avg usage not enough, "+
			"BEUsage:%v, BERequest:%v, BELimit:%v, BERealLimit:%v, BEAllocatable:%v",
			avgBECPUMilliUsage, avgBECPUMilliRequest, avgBECPUMilliLimit, avgBECPUMilliRealLimit, beCPUMilliAllocatable)
		return 0
	}

	milliRelease := calculateResourceMilliToRelease(avgBECPUMilliRequest, avgBECPUMilliLimit, thresholdConfig)
	if milliRelease <= 0 {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped, releaseByAvg: %v", milliRelease)
		return 0
	}

	// Step2: Calculate release resource current
	queryparam = helpers.GenerateQueryParamsLast(c.metricCollectInterval * 2)
	querier, err = c.metricCache.Querier(*queryparam.Start, *queryparam.End)
	if err != nil {
		klog.Warningf("get query failed, error %v", err)
		return 0
	}
	// BECPUUsage
	currentBECPUMilliUsage, _ := getBECPUMetric(metriccache.BEResouceAllocationUsage, querier, queryparam.Aggregate)
	// BECPURequest
	currentBECPUMilliRequest, _ := getBECPUMetric(metriccache.BEResouceAllocationRequest, querier, queryparam.Aggregate)
	// BECPULimit
	currentBECPUMilliRealLimit, _ := getBECPUMetric(metriccache.BEResouceAllocationRealLimit, querier, queryparam.Aggregate)

	// CPU Satisfaction considers the allocatable when policy=evictByAllocatable.
	currentBECPUMilliLimit := currentBECPUMilliRealLimit
	if thresholdConfig.CPUEvictPolicy == slov1alpha1.EvictByAllocatablePolicy {
		currentBECPUMilliLimit = beCPUMilliAllocatable
	}

	if !isBECPUUsageHighEnough(currentBECPUMilliUsage, currentBECPUMilliLimit, thresholdConfig.CPUEvictBEUsageThresholdPercent) {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped, current usage not enough, "+
			"BEUsage:%v, BERequest:%v, BELimit:%v, BERealLimit:%v, BEAllocatable:%v",
			currentBECPUMilliUsage, currentBECPUMilliRequest, currentBECPUMilliLimit, currentBECPUMilliRealLimit,
			beCPUMilliAllocatable)
		return 0
	}

	// Requests and limits do not change frequently.
	// If the current request and limit are equal to the average request and limit within the window period, there is no need to recalculate.
	if currentBECPUMilliRequest == avgBECPUMilliRequest && currentBECPUMilliLimit == avgBECPUMilliLimit {
		return milliRelease
	}
	milliReleaseByCurrent := calculateResourceMilliToRelease(currentBECPUMilliRequest, currentBECPUMilliLimit, thresholdConfig)
	if milliReleaseByCurrent <= 0 {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped, releaseByCurrent: %v", milliReleaseByCurrent)
		return 0
	}

	// Step3：release = min(releaseByAvg,releaseByCurrent)
	if milliReleaseByCurrent < milliRelease {
		milliRelease = milliReleaseByCurrent
	}

	if milliRelease > 0 {
		klog.V(4).Infof("cpuEvict by ResourceSatisfaction start to evict, milliRelease: %v,"+
			"current status (BEUsage:%v, BERequest:%v, BELimit:%v, BERealLimit:%v, BEAllocatable:%v)",
			currentBECPUMilliUsage, currentBECPUMilliRequest, currentBECPUMilliLimit, currentBECPUMilliRealLimit,
			beCPUMilliAllocatable)
	}
	return milliRelease
}

func isAvgQueryResultValid(windowSeconds, collectIntervalSeconds, count int64) bool {
	if count*collectIntervalSeconds < windowSeconds/3 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped, metricsCount(%d) not enough!windowSize: %v, collectInterval: %v", count, windowSeconds, collectIntervalSeconds)
		return false
	}
	return true
}

func isBECPUUsageHighEnough(beCPUMilliUsage, beCPUMilliRealLimit float64, thresholdPercent *int64) bool {
	if beCPUMilliRealLimit <= 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped! CPURealLimit %v is no larger than zero!",
			beCPUMilliRealLimit)
		return false
	}
	if beCPUMilliUsage < 1000 {
		return true
	}
	cpuUsage := beCPUMilliUsage / beCPUMilliRealLimit
	if thresholdPercent == nil {
		thresholdPercent = pointer.Int64(beCPUUsageThresholdPercent)
	}
	if cpuUsage < float64(*thresholdPercent)/100 {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped! cpuUsage(%.2f) and thresholdPercent %d!", cpuUsage, *thresholdPercent)
		return false
	}

	klog.V(4).Infof("cpuEvict by ResourceSatisfaction: cpuUsage(%.2f) >= thresholdPercent %d!", cpuUsage, *thresholdPercent)
	return true
}

func calculateResourceMilliToRelease(beCPUMilliRequest, beCPUMilliRealLimit float64, thresholdConfig *slov1alpha1.ResourceThresholdStrategy) int64 {
	if beCPUMilliRequest <= 0 {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped! be pods requests is zero!")
		return 0
	}

	satisfactionRate := beCPUMilliRealLimit / beCPUMilliRequest
	if satisfactionRate > float64(*thresholdConfig.CPUEvictBESatisfactionLowerPercent)/100 {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped! satisfactionRate(%.2f) and lowPercent(%f)", satisfactionRate, float64(*thresholdConfig.CPUEvictBESatisfactionLowerPercent))
		return 0
	}

	rateGap := float64(*thresholdConfig.CPUEvictBESatisfactionUpperPercent)/100 - satisfactionRate
	if rateGap <= 0 {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped! satisfactionRate(%.2f) > upperPercent(%f)", satisfactionRate, float64(*thresholdConfig.CPUEvictBESatisfactionUpperPercent))
		return 0
	}

	milliRelease := beCPUMilliRequest * rateGap
	return int64(milliRelease)
}

func (c *cpuEvictor) evictByResourceSatisfaction(node *corev1.Node, thresholdConfig *slov1alpha1.ResourceThresholdStrategy, windowSeconds int64) {
	if !isSatisfactionConfigValid(thresholdConfig) {
		return
	}
	milliRelease := c.calculateMilliRelease(thresholdConfig, windowSeconds)
	if milliRelease > 0 {
		bePodInfos := c.getPodEvictInfoAndSort()
		c.killAndEvictBEPodsRelease(node, bePodInfos, milliRelease)
	}
}

func (c *cpuEvictor) killAndEvictBEPodsRelease(node *corev1.Node, bePodInfos []*podEvictCPUInfo, cpuNeedMilliRelease int64) {
	message := fmt.Sprintf("killAndEvictBEPodsRelease for node(%s), need release milli CPU: %v",
		node.Name, cpuNeedMilliRelease)

	cpuMilliReleased := int64(0)
	var killedPods []*corev1.Pod
	for _, bePod := range bePodInfos {
		if cpuMilliReleased >= cpuNeedMilliRelease {
			break
		}

		podKillMsg := fmt.Sprintf("%s, kill pod: %s", message, util.GetPodKey(bePod.pod))
		helpers.KillContainers(bePod.pod, podKillMsg)

		killedPods = append(killedPods, bePod.pod)
		cpuMilliReleased = cpuMilliReleased + bePod.milliRequest

		klog.V(5).Infof("cpuEvict pick pod %s/%s to evict", util.GetPodKey(bePod.pod))
	}

	c.evictor.EvictPodsIfNotEvicted(killedPods, node, resourceexecutor.EvictPodByBECPUSatisfaction, message)

	if len(killedPods) > 0 {
		c.lastEvictTime = time.Now()
	}
	klog.V(5).Infof("killAndEvictBEPodsRelease finished! cpuNeedMilliRelease(%d) cpuMilliReleased(%d)",
		cpuNeedMilliRelease, cpuMilliReleased)
}

func (c *cpuEvictor) getPodEvictInfoAndSort() []*podEvictCPUInfo {
	var bePodInfos []*podEvictCPUInfo

	for _, podMeta := range c.statesInformer.GetAllPods() {
		pod := podMeta.Pod
		if apiext.GetPodQoSClassRaw(pod) == apiext.QoSBE {

			bePodInfo := &podEvictCPUInfo{pod: podMeta.Pod}
			queryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(pod.UID)))
			if err == nil {
				result, err := helpers.CollectPodMetricLast(c.metricCache, queryMeta, c.metricCollectInterval)
				if err == nil {
					bePodInfo.milliUsedCores = int64(result * 1000)
				}
			}

			milliRequestSum := int64(0)
			for _, container := range pod.Spec.Containers {
				containerCPUReq := util.GetContainerBatchMilliCPURequest(&container)
				if containerCPUReq > 0 {
					milliRequestSum = milliRequestSum + containerCPUReq
				}
			}

			bePodInfo.milliRequest = milliRequestSum
			if bePodInfo.milliRequest > 0 {
				bePodInfo.cpuUsage = float64(bePodInfo.milliUsedCores) / float64(bePodInfo.milliRequest)
			}

			bePodInfos = append(bePodInfos, bePodInfo)
		}
	}

	sort.Slice(bePodInfos, func(i, j int) bool {
		if bePodInfos[i].pod.Spec.Priority == nil || bePodInfos[j].pod.Spec.Priority == nil ||
			*bePodInfos[i].pod.Spec.Priority == *bePodInfos[j].pod.Spec.Priority {
			return bePodInfos[i].cpuUsage > bePodInfos[j].cpuUsage
		}
		return *bePodInfos[i].pod.Spec.Priority < *bePodInfos[j].pod.Spec.Priority
	})
	return bePodInfos
}

func (c *cpuEvictor) getBEMilliAllocatable() float64 {
	node := c.statesInformer.GetNode()
	if node == nil || node.Status.Allocatable == nil {
		return -1
	}

	batchCPUQuant, ok := node.Status.Allocatable[apiext.BatchCPU]
	if !ok || batchCPUQuant.Value() < 0 {
		return -1
	}
	// The batch allocatable value can be set to zero when high-priority util is high, where we still need to calculate
	// the satisfaction rate. Here we use a small allocatable for the BE utilization check.
	if batchCPUQuant.IsZero() {
		return defaultMinAllocatableBatchMilliCPU
	}

	return float64(batchCPUQuant.Value())
}

func isSatisfactionConfigValid(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) bool {
	lowPercent := thresholdConfig.CPUEvictBESatisfactionLowerPercent
	upperPercent := thresholdConfig.CPUEvictBESatisfactionUpperPercent
	if lowPercent == nil || upperPercent == nil {
		klog.V(4).Infof("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionLowerPercent or CPUEvictBESatisfactionUpperPercent not config")
		return false
	}
	if *lowPercent > beCPUSatisfactionLowPercentMax || *lowPercent <= 0 {
		klog.V(4).Infof("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionLowerPercent(%d) is not valid! must (0,%d]", *lowPercent, beCPUSatisfactionLowPercentMax)
		return false
	}
	if *upperPercent >= beCPUSatisfactionUpperPercentMax || *upperPercent <= 0 {
		klog.V(4).Infof("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionUpperPercent(%d) is not valid,must (0,%d)!", *upperPercent, beCPUSatisfactionUpperPercentMax)
		return false
	} else if *upperPercent < *lowPercent {
		klog.V(4).Infof("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionUpperPercent(%d) < CPUEvictBESatisfactionLowerPercent(%d)", *upperPercent, *lowPercent)
		return false
	}
	return true
}

func getBECPUMetric(resouceAllocation metriccache.MetricPropertyValue, querier metriccache.Querier, aggregateType metriccache.AggregationType) (float64, int64) {
	var properties map[metriccache.MetricProperty]string

	switch resouceAllocation {
	case metriccache.BEResouceAllocationUsage:
		properties = metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResouceAllocationUsage))
	case metriccache.BEResouceAllocationRequest:
		properties = metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResouceAllocationRequest))
	case metriccache.BEResouceAllocationRealLimit:
		properties = metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResouceAllocationRealLimit))
	default:
		properties = map[metriccache.MetricProperty]string{}
	}

	result, err := helpers.Query(querier, metriccache.NodeBEMetric, properties)
	if err != nil {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped, %s queryResult error: %v", resouceAllocation, err)
		return 0.0, 0
	}
	value, err := result.Value(aggregateType)
	if err != nil {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped, queryResult %s error: %v", aggregateType, err)
		return 0.0, 0
	}
	count := result.Count()

	return value, int64(count)

}

func minInt64(num ...int64) int64 {
	min := int64(math.MaxInt64)

	for _, n := range num {
		if n < min {
			min = n
		}
	}

	return min
}

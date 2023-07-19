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

package resmanager

import (
	"fmt"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	beCPUSatisfactionLowPercentMax   = 60
	beCPUSatisfactionUpperPercentMax = 100
	beCPUUsageThresholdPercent       = 90
)

type CPUEvictor struct {
	resmanager    *resmanager
	lastEvictTime time.Time
}

func NewCPUEvictor(resmanager *resmanager) *CPUEvictor {
	return &CPUEvictor{
		resmanager:    resmanager,
		lastEvictTime: time.Now(),
	}
}

type podEvictCPUInfo struct {
	milliRequest   int64
	milliUsedCores int64
	cpuUsage       float64 // cpuUsage = milliUsedCores / milliRequest
	pod            *corev1.Pod
}

func (c *CPUEvictor) cpuEvict() {
	klog.V(5).Infof("cpu evict process start")

	nodeSLO := c.resmanager.getNodeSLOCopy()
	if disabled, err := isFeatureDisabled(nodeSLO, features.BECPUEvict); err != nil {
		klog.Warningf("cpuEvict failed, cannot check the feature gate, err: %s", err)
		return
	} else if disabled {
		klog.V(4).Infof("cpuEvict skipped, nodeSLO disable the feature gate")
		return
	}

	if time.Since(c.lastEvictTime) < time.Duration(c.resmanager.config.CPUEvictCoolTimeSeconds)*time.Second {
		klog.V(4).Infof("skip CPU evict process, still in evict cool time")
		return
	}

	thresholdConfig := nodeSLO.Spec.ResourceUsedThresholdWithBE
	windowSeconds := c.resmanager.collectResUsedIntervalSeconds * 2
	if thresholdConfig.CPUEvictTimeWindowSeconds != nil && *thresholdConfig.CPUEvictTimeWindowSeconds > c.resmanager.collectResUsedIntervalSeconds {
		windowSeconds = *thresholdConfig.CPUEvictTimeWindowSeconds
	}

	node := c.resmanager.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("cpuEvict failed, got nil node %s", c.resmanager.nodeName)
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

func (c *CPUEvictor) calculateMilliRelease(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, windowSeconds int64) int64 {
	// Step1: Calculate release resource by BECPUResourceMetric in window
	queryparam := generateQueryParamsAvg(windowSeconds)
	querier, err := c.resmanager.metricCache.Querier(*queryparam.Start, *queryparam.End)
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

	// get min count
	count := minInt64(count01, count02, count03)

	if !isAvgQueryResultValid(windowSeconds, c.resmanager.collectResUsedIntervalSeconds, count) {
		return 0
	}

	if !isBECPUUsageHighEnough(avgBECPUMilliUsage, avgBECPUMilliRequest, avgBECPUMilliRealLimit, thresholdConfig.CPUEvictBEUsageThresholdPercent) {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped,current cpuUsage not Enough! metric(BECPUUsage, BECPURequest, BECPULimit): %s, %s, %s", avgBECPUMilliUsage, avgBECPUMilliRequest, avgBECPUMilliRealLimit)
		return 0
	}

	milliRelease := calculateResourceMilliToRelease(avgBECPUMilliRequest, avgBECPUMilliRealLimit, thresholdConfig)
	if milliRelease <= 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped,releaseByAvg: %d", milliRelease)
		return 0
	}

	// Step2: Calculate release resource current
	queryparam = generateQueryParamsLast(c.resmanager.collectResUsedIntervalSeconds * 2)
	querier, err = c.resmanager.metricCache.Querier(*queryparam.Start, *queryparam.End)
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

	if !isBECPUUsageHighEnough(currentBECPUMilliUsage, currentBECPUMilliRequest, currentBECPUMilliRealLimit, thresholdConfig.CPUEvictBEUsageThresholdPercent) {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped,current cpuUsage not Enough! metric(BECPUUsage, BECPURequest, BECPULimit): %s, %s, %s", currentBECPUMilliUsage, currentBECPUMilliRequest, currentBECPUMilliRealLimit)
		return 0
	}

	// Requests and limits do not change frequently.
	// If the current request and limit are equal to the average request and limit within the window period, there is no need to recalculate.
	if currentBECPUMilliUsage == avgBECPUMilliRequest && currentBECPUMilliRealLimit == avgBECPUMilliRealLimit {
		return milliRelease
	}
	milliReleaseByCurrent := calculateResourceMilliToRelease(currentBECPUMilliRequest, currentBECPUMilliRealLimit, thresholdConfig)
	if milliReleaseByCurrent <= 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped,releaseByCurrent: %d", milliReleaseByCurrent)
		return 0
	}

	// Step3：release = min(releaseByAvg,releaseByCurrent)
	if milliReleaseByCurrent < milliRelease {
		milliRelease = milliReleaseByCurrent
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

func isBECPUUsageHighEnough(beCPUMilliUsage, beCPUMilliRequest, beCPUMilliRealLimit float64, thresholdPercent *int64) bool {
	if beCPUMilliRealLimit == 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped! CPURealLimit is zero!")
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
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped! cpuUsage(%.2f) and thresholdPercent %d!", cpuUsage, *thresholdPercent)
		return false
	}
	return true
}

func calculateResourceMilliToRelease(beCPUMilliRequest, beCPUMilliRealLimit float64, thresholdConfig *slov1alpha1.ResourceThresholdStrategy) int64 {
	if beCPUMilliRequest == 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped! be pods requests is zero!")
		return 0
	}

	satisfactionRate := beCPUMilliRealLimit / beCPUMilliRequest
	if satisfactionRate > float64(*thresholdConfig.CPUEvictBESatisfactionLowerPercent)/100 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped! satisfactionRate(%.2f) and lowPercent(%f)", satisfactionRate, float64(*thresholdConfig.CPUEvictBESatisfactionLowerPercent))
		return 0
	}

	rateGap := float64(*thresholdConfig.CPUEvictBESatisfactionUpperPercent)/100 - satisfactionRate
	if rateGap <= 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped! satisfactionRate(%.2f) > upperPercent(%f)", satisfactionRate, float64(*thresholdConfig.CPUEvictBESatisfactionUpperPercent))
		return 0
	}

	milliRelease := beCPUMilliRequest * rateGap
	return int64(milliRelease)
}

func (c *CPUEvictor) evictByResourceSatisfaction(node *corev1.Node, thresholdConfig *slov1alpha1.ResourceThresholdStrategy, windowSeconds int64) {
	if !isSatisfactionConfigValid(thresholdConfig) {
		return
	}
	milliRelease := c.calculateMilliRelease(thresholdConfig, windowSeconds)
	if milliRelease > 0 {
		bePodInfos := c.getPodEvictInfoAndSort()
		c.killAndEvictBEPodsRelease(node, bePodInfos, milliRelease)
	}
}

func (c *CPUEvictor) killAndEvictBEPodsRelease(node *corev1.Node, bePodInfos []*podEvictCPUInfo, cpuNeedMilliRelease int64) {
	message := fmt.Sprintf("killAndEvictBEPodsRelease for node(%s), need realase CPU : %d", c.resmanager.nodeName, cpuNeedMilliRelease)

	cpuMilliReleased := int64(0)
	var killedPods []*corev1.Pod
	for _, bePod := range bePodInfos {
		if cpuMilliReleased >= cpuNeedMilliRelease {
			break
		}

		podKillMsg := fmt.Sprintf("%s, kill pod : %s", message, bePod.pod.Name)
		killContainers(bePod.pod, podKillMsg)

		killedPods = append(killedPods, bePod.pod)
		cpuMilliReleased = cpuMilliReleased + bePod.milliRequest
	}

	c.resmanager.evictPodsIfNotEvicted(killedPods, node, resourceexecutor.EvictPodByBECPUSatisfaction, message)

	if len(killedPods) > 0 {
		c.lastEvictTime = time.Now()
	}
	klog.V(5).Infof("killAndEvictBEPodsRelease finished!cpuNeedMilliRelease(%d) cpuMilliReleased(%d)", cpuNeedMilliRelease, cpuMilliReleased)
}

func (c *CPUEvictor) getPodEvictInfoAndSort() []*podEvictCPUInfo {
	var bePodInfos []*podEvictCPUInfo

	for _, podMeta := range c.resmanager.statesInformer.GetAllPods() {
		pod := podMeta.Pod
		if apiext.GetPodQoSClass(pod) == apiext.QoSBE {

			bePodInfo := &podEvictCPUInfo{pod: podMeta.Pod}
			queryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(pod.UID)))
			if err == nil {
				result, err := c.resmanager.collectPodMetricLast(queryMeta)
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

func isSatisfactionConfigValid(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) bool {
	lowPercent := thresholdConfig.CPUEvictBESatisfactionLowerPercent
	upperPercent := thresholdConfig.CPUEvictBESatisfactionUpperPercent
	if lowPercent == nil || upperPercent == nil {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionLowerPercent or  CPUEvictBESatisfactionUpperPercent not config!")
		return false
	}
	if *lowPercent > beCPUSatisfactionLowPercentMax || *lowPercent <= 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionLowerPercent(%d) is not valid! must (0,%d]", *lowPercent, beCPUSatisfactionLowPercentMax)
		return false
	}
	if *upperPercent >= beCPUSatisfactionUpperPercentMax || *upperPercent <= 0 {
		klog.Warningf("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionUpperPercent(%d) is not valid,must (0,%d)!", *upperPercent, beCPUSatisfactionUpperPercentMax)
		return false
	} else if *upperPercent < *lowPercent {
		klog.Infof("cpuEvict by ResourceSatisfaction skipped, CPUEvictBESatisfactionUpperPercent(%d) < CPUEvictBESatisfactionLowerPercent(%d)!", *upperPercent, *lowPercent)
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

	result, err := doQuery(querier, metriccache.NodeBEMetric, properties)
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

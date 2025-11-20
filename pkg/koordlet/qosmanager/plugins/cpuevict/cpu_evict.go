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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	qosmanagerUtil "github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/util"
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
	cpuReleaseBufferPercent            = 2
)

var _ framework.QOSStrategy = &cpuEvictor{}

type cpuEvictor struct {
	evictInterval         time.Duration
	evictCoolingInterval  time.Duration
	metricCollectInterval time.Duration
	statesInformer        statesinformer.StatesInformer
	metricCache           metriccache.MetricCache
	lastEvictTime         time.Time
	evictExecutor         qosmanagerUtil.EvictionExecutor
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
	return (features.DefaultKoordletFeatureGate.Enabled(features.BECPUEvict) || features.DefaultKoordletFeatureGate.Enabled(features.CPUEvict)) &&
		c.evictInterval > 0
}

func (c *cpuEvictor) Setup(ctx *framework.Context) {
	c.evictExecutor = qosmanagerUtil.InitializeEvictionExecutor(ctx.Evictor, ctx.OnlyEvictByAPI)
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

// cpu evict triggered by configured mechanism:
func (c *cpuEvictor) cpuEvict() {
	klog.V(5).Infof("cpu evict process start")
	defer klog.V(5).Info("cpu evict process finished.")

	if time.Since(c.lastEvictTime) < c.evictCoolingInterval {
		klog.V(4).Infof("skip CPU evict process, still in evict cool time")
		return
	}
	nodeSLO := c.statesInformer.GetNodeSLO()
	// runtime check
	node := c.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("cpuEvict failed, got nil node")
		return
	}
	nodeMilliCPUCapacity := node.Status.Capacity.Cpu().MilliValue()
	if nodeMilliCPUCapacity <= 0 {
		klog.Warningf("skip CPU evict, node nodeMilliCPUCapacity not valid, value: %d", nodeMilliCPUCapacity)
		return
	}

	// build cpu evict tasks
	var evictTasks []*qosmanagerUtil.EvictTaskInfo
	// When both features.BECPUEvict and features.CPUEvict are enabled:
	// - Both eviction mechanisms will be activated.
	// - BECPUEvict runs first, followed by CPUEvict.
	// - Resource release effects are cumulatively considered; the total reclaimed resources
	//   from both phases are accounted for in scheduling and capacity planning.
	triggerFeatures := []featuregate.Feature{features.BECPUEvict, features.CPUEvict}
	for _, feature := range triggerFeatures {
		if !features.DefaultKoordletFeatureGate.Enabled(feature) {
			continue
		}
		if disabled, err := features.IsFeatureDisabled(nodeSLO, feature); err != nil {
			klog.Warningf("feature %s failed, cannot check the feature gate, err: %v", feature, err)
			continue
		} else if disabled {
			klog.V(4).Infof("feature %s skipped, nodeSLO disable the feature gate", feature)
			continue
		}
		task, err := c.buildEvictTask(feature, nodeSLO, nodeMilliCPUCapacity)
		if err != nil {
			klog.Warningf("failed to build cpuEvict task trigger by feature %v, err: %v", feature, err)
			continue
		}
		if task == nil {
			continue
		}
		evictTasks = append(evictTasks, task)
	}
	if len(evictTasks) == 0 {
		klog.V(4).Infof("cpuEvict skipped, no task to evict")
		return
	}
	released, hasReleased := qosmanagerUtil.KillAndEvictPods(c.evictExecutor, node, evictTasks)
	if hasReleased {
		c.lastEvictTime = time.Now()
	}
	// report and renew time
	for _, task := range evictTasks {
		succeed, failedToRelease := qosmanagerUtil.EvictTaskCheck(task, released)
		if succeed {
			klog.V(4).Infof("evict task %v succeed, released resourceTarget[%v]: %v", task.Reason, task.ReleaseTarget, task.ToReleaseResource)
		} else {
			klog.Warningf("evict task %v failed, failed to release resourceTarget[%v]: to release %v,  failed to release %v ", task.Reason, task.ReleaseTarget, task.ToReleaseResource, failedToRelease)
		}
	}
}

func (c *cpuEvictor) calculateMilliReleaseBySatisfaction(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, nodeMilliCPUCapacity int64) int64 {
	windowSeconds := int64(c.metricCollectInterval.Seconds())
	if thresholdConfig.CPUEvictTimeWindowSeconds != nil && *thresholdConfig.CPUEvictTimeWindowSeconds > windowSeconds {
		windowSeconds = *thresholdConfig.CPUEvictTimeWindowSeconds
	}
	// Step1: Calculate release resource by BECPUResourceMetric in window
	queryParam := helpers.GenerateQueryParamsAvg(time.Duration(windowSeconds) * time.Second)
	querier, err := c.metricCache.Querier(*queryParam.Start, *queryParam.End)
	if err != nil {
		klog.Warningf("get query failed, error %v", err)
		return 0
	}
	defer querier.Close()
	// BECPUUsage
	avgBECPUMilliUsage, count01 := getBECPUMetric(metriccache.BEResourceAllocationUsage, querier, queryParam.Aggregate)
	// BECPURequest
	avgBECPUMilliRequest, count02 := getBECPUMetric(metriccache.BEResourceAllocationRequest, querier, queryParam.Aggregate)
	// BECPULimit
	avgBECPUMilliRealLimit, count03 := getBECPUMetric(metriccache.BEResourceAllocationRealLimit, querier, queryParam.Aggregate)

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

	milliRelease := calculateResourceMilliToReleaseBySatisfaction(avgBECPUMilliRequest, avgBECPUMilliLimit, thresholdConfig)
	if milliRelease <= 0 {
		klog.V(5).Infof("cpuEvict by ResourceSatisfaction skipped, releaseByAvg: %v", milliRelease)
		return 0
	}

	// Step2: Calculate release resource current
	queryParam = helpers.GenerateQueryParamsLast(c.metricCollectInterval * 2)
	querier, err = c.metricCache.Querier(*queryParam.Start, *queryParam.End)
	if err != nil {
		klog.Warningf("get query failed, error %v", err)
		return 0
	}
	defer querier.Close()
	// BECPUUsage
	currentBECPUMilliUsage, _ := getBECPUMetric(metriccache.BEResourceAllocationUsage, querier, queryParam.Aggregate)
	// BECPURequest
	currentBECPUMilliRequest, _ := getBECPUMetric(metriccache.BEResourceAllocationRequest, querier, queryParam.Aggregate)
	// BECPULimit
	currentBECPUMilliRealLimit, _ := getBECPUMetric(metriccache.BEResourceAllocationRealLimit, querier, queryParam.Aggregate)

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
	milliReleaseByCurrent := calculateResourceMilliToReleaseBySatisfaction(currentBECPUMilliRequest, currentBECPUMilliLimit, thresholdConfig)
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
			milliRelease, currentBECPUMilliUsage, currentBECPUMilliRequest, currentBECPUMilliLimit, currentBECPUMilliRealLimit,
			beCPUMilliAllocatable)
	}
	return milliRelease
}

func (c *cpuEvictor) calculateMilliReleaseByUsedThresholdPercent(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, nodeMilliCPUCapacity int64) int64 {
	windowSeconds := int64(c.metricCollectInterval.Seconds())
	if thresholdConfig.CPUEvictTimeWindowSeconds != nil && *thresholdConfig.CPUEvictTimeWindowSeconds > windowSeconds {
		windowSeconds = *thresholdConfig.CPUEvictTimeWindowSeconds
	}
	queryParam := helpers.GenerateQueryParamsAvg(time.Duration(windowSeconds) * time.Second)
	queryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
	if err != nil {
		klog.Warningf("get query failed, error %v", err)
		return 0
	}
	// cpu peak shaving
	queryResult, err := helpers.CollectNodeMetrics(c.metricCache, *queryParam.Start, *queryParam.End, queryMeta)
	if err != nil {
		klog.Warningf("cpuEvict by usedTresholdPercent skippped, get node metrics error: %v", err)
		return 0
	}
	nodeCPUUsed, err := queryResult.Value(queryParam.Aggregate)
	if err != nil {
		klog.Warningf("get node cpu metric failed, error: %v", err)
		return 0
	}
	nodeCPUUsage := int64(nodeCPUUsed*1000) * 100 / nodeMilliCPUCapacity
	thresholdPercent := thresholdConfig.CPUEvictThresholdPercent
	if nodeCPUUsage < *thresholdPercent {
		klog.V(5).Infof("cpuEvict by usedTresholdPercent skippped, node cpu usage(%v) is below threshold(%v)", nodeCPUUsage, *thresholdPercent)
		return 0
	}
	lowerPercent := int64(0)
	if thresholdConfig.CPUEvictLowerPercent != nil {
		lowerPercent = *thresholdConfig.CPUEvictLowerPercent
	} else {
		lowerPercent = *thresholdPercent - cpuReleaseBufferPercent
	}
	milliCPUNeedRelease := nodeMilliCPUCapacity * (nodeCPUUsage - lowerPercent) / 100
	klog.Infof("cpuEvict by usedTresholdPercent start to evict %v, node cpuUsage(%v): %.2f, evictThresholdUsage: %.2f, evictLowerUsage: %.2f",
		milliCPUNeedRelease,
		nodeCPUUsed,
		float64(nodeCPUUsage)/100,
		float64(*thresholdPercent)/100,
		float64(lowerPercent)/100,
	)
	return milliCPUNeedRelease
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
	if beCPUMilliRealLimit < 1000 {
		klog.Warningf("cpuEvict by ResourceSatisfaction: CPURealLimit %v is less than 1 core", beCPUMilliRealLimit)
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

func calculateResourceMilliToReleaseBySatisfaction(beCPUMilliRequest, beCPUMilliRealLimit float64, thresholdConfig *slov1alpha1.ResourceThresholdStrategy) int64 {
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

func (c *cpuEvictor) buildEvictTask(feature featuregate.Feature, nodeSLO *slov1alpha1.NodeSLO, nodeMilliCPUCapacity int64) (*qosmanagerUtil.EvictTaskInfo, error) {
	thresholdConfig := nodeSLO.Spec.ResourceUsedThresholdWithBE
	var evictReason string
	var releaseTarget qosmanagerUtil.ReleaseTargetType
	var configCheckFunc func(*slov1alpha1.ResourceThresholdStrategy) error
	var getPodEvictInfoAndSortFunc func(*slov1alpha1.ResourceThresholdStrategy) []*qosmanagerUtil.PodEvictInfo
	var calculateReleaseResource func(*slov1alpha1.ResourceThresholdStrategy, int64) int64
	// check config
	switch feature {
	case features.BECPUEvict:
		evictReason = "trigger by koordlet feature " + resourceexecutor.EvictBEPodByBECPUSatisfaction
		configCheckFunc = isSatisfactionConfigValid
		calculateReleaseResource = c.calculateMilliReleaseBySatisfaction
		getPodEvictInfoAndSortFunc = c.getBEPodEvictInfoAndSort
		releaseTarget = qosmanagerUtil.ReleaseTargetTypeBatchResourceRequest
	case features.CPUEvict:
		evictReason = "trigger by koordlet feature " + resourceexecutor.EvictPodByCPUUsedThresholdPercent
		configCheckFunc = isUsedThresholdConfigValid
		calculateReleaseResource = c.calculateMilliReleaseByUsedThresholdPercent
		getPodEvictInfoAndSortFunc = c.getPodEvictInfoAndSortByPriority
		releaseTarget = qosmanagerUtil.ReleaseTargetTypeResourceUsed
	default:
		return nil, fmt.Errorf("unknown feature %v", feature)
	}
	if err := configCheckFunc(thresholdConfig); err != nil {
		return nil, fmt.Errorf("skip cpu evict feature %v, invalid config, err=%v", feature, err)
	}
	milliRelease := calculateReleaseResource(thresholdConfig, nodeMilliCPUCapacity)
	if milliRelease <= 0 {
		return nil, nil
	}
	sortedPodInfos := getPodEvictInfoAndSortFunc(thresholdConfig)
	return &qosmanagerUtil.EvictTaskInfo{
		Reason:          evictReason,
		SortedEvictPods: sortedPodInfos,
		ToReleaseResource: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewMilliQuantity(milliRelease, resource.DecimalSI),
		},
		ReleaseTarget: releaseTarget,
	}, nil
}

func (c *cpuEvictor) getPodEvictInfoAndSortByPriority(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) []*qosmanagerUtil.PodEvictInfo {
	var podsInfos []*qosmanagerUtil.PodEvictInfo
	priorityThreshold := *thresholdConfig.EvictEnabledPriorityThreshold
	for _, podMeta := range c.statesInformer.GetAllPods() {
		pod := podMeta.Pod
		// higher priority pods are not allowed to evict
		// 1. filter inactive / priority
		podInfo := &qosmanagerUtil.PodEvictInfo{Pod: podMeta.Pod}
		if util.IsPodInactive(pod) {
			continue
		}
		if priority := apiext.GetPodPriorityValueWithDefault(pod); priority == nil || *priority > priorityThreshold {
			continue
		} else {
			podInfo.Priority = *priority
		}
		// 2. filter: exclude: koordinator.sh/evict-disabled
		if !apiext.PodEvictEnabled(pod) {
			continue
		}
		// 3. sort :  koordinator.sh/priority
		podPriority := qosmanagerUtil.GetPodPriorityLabel(pod, int64(podInfo.Priority))
		podInfo.LabelPriority = podPriority
		// 4. filter no metrics
		queryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(pod.UID)))
		if err != nil {
			klog.Warningf("get pod %v query failed, error %v", pod.UID, err)
			continue
		}
		result, err := helpers.CollectPodMetricLast(c.metricCache, queryMeta, c.metricCollectInterval)
		if err != nil {
			klog.Warningf("get pod %v metrics failed, error %v", pod.UID, err)
			continue
		}
		podInfo.MilliCPUUsed = int64(result * 1000)
		milliRequestSum := int64(0)
		for _, container := range pod.Spec.Containers {
			containerCPUReq := util.GetContainerBatchMilliCPURequest(&container)
			if containerCPUReq > 0 {
				milliRequestSum = milliRequestSum + containerCPUReq
			}
		}
		podInfo.MilliCPURequest = milliRequestSum
		podsInfos = append(podsInfos, podInfo)
	}

	sort.Slice(podsInfos, func(i, j int) bool {
		if podsInfos[i].Priority != podsInfos[j].Priority {
			return podsInfos[i].Priority < podsInfos[j].Priority
		}
		if podsInfos[i].LabelPriority != podsInfos[j].LabelPriority {
			return podsInfos[i].LabelPriority < podsInfos[j].LabelPriority
		}
		return podsInfos[i].MilliCPUUsed > podsInfos[j].MilliCPUUsed
	})
	return podsInfos
}
func (c *cpuEvictor) getBEPodEvictInfoAndSort(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) []*qosmanagerUtil.PodEvictInfo {
	var bePodInfos []*qosmanagerUtil.PodEvictInfo

	for _, podMeta := range c.statesInformer.GetAllPods() {
		pod := podMeta.Pod
		if apiext.GetPodQoSClassRaw(pod) == apiext.QoSBE {
			bePodInfo := &qosmanagerUtil.PodEvictInfo{Pod: podMeta.Pod}
			queryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(pod.UID)))
			if err == nil {
				result, err := helpers.CollectPodMetricLast(c.metricCache, queryMeta, c.metricCollectInterval)
				if err == nil {
					bePodInfo.MilliCPUUsed = int64(result * 1000)
				}
			}

			milliRequestSum := int64(0)
			for _, container := range pod.Spec.Containers {
				containerCPUReq := util.GetContainerBatchMilliCPURequest(&container)
				if containerCPUReq > 0 {
					milliRequestSum = milliRequestSum + containerCPUReq
				}
			}

			bePodInfo.MilliCPURequest = milliRequestSum
			if bePodInfo.MilliCPURequest > 0 {
				bePodInfo.CpuUsage = float64(bePodInfo.MilliCPUUsed) / float64(bePodInfo.MilliCPURequest)
			}

			bePodInfos = append(bePodInfos, bePodInfo)
		}
	}

	sort.Slice(bePodInfos, func(i, j int) bool {
		if bePodInfos[i].Pod.Spec.Priority == nil || bePodInfos[j].Pod.Spec.Priority == nil ||
			*bePodInfos[i].Pod.Spec.Priority == *bePodInfos[j].Pod.Spec.Priority {
			return bePodInfos[i].CpuUsage > bePodInfos[j].CpuUsage
		}
		return *bePodInfos[i].Pod.Spec.Priority < *bePodInfos[j].Pod.Spec.Priority
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

func isSatisfactionConfigValid(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) error {
	if thresholdConfig == nil {
		return fmt.Errorf("ResourceThresholdStrategy not config")
	}
	lowPercent := thresholdConfig.CPUEvictBESatisfactionLowerPercent
	upperPercent := thresholdConfig.CPUEvictBESatisfactionUpperPercent
	if lowPercent == nil || upperPercent == nil {
		return fmt.Errorf("CPUEvictBESatisfactionLowerPercent or CPUEvictBESatisfactionUpperPercent not config")
	}
	if *lowPercent > beCPUSatisfactionLowPercentMax || *lowPercent <= 0 {
		return fmt.Errorf("CPUEvictBESatisfactionLowerPercent(%d) is not valid! must (0,%d]", *lowPercent, beCPUSatisfactionLowPercentMax)
	}
	if *upperPercent >= beCPUSatisfactionUpperPercentMax || *upperPercent <= 0 {
		return fmt.Errorf("CPUEvictBESatisfactionUpperPercent(%d) is not valid,must (0,%d)", *upperPercent, beCPUSatisfactionUpperPercentMax)
	} else if *upperPercent < *lowPercent {
		return fmt.Errorf("CPUEvictBESatisfactionUpperPercent(%d) < CPUEvictBESatisfactionLowerPercent(%d)", *upperPercent, *lowPercent)
	}
	return nil
}

func isUsedThresholdConfigValid(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) error {
	if thresholdConfig == nil {
		return fmt.Errorf("ResourceThresholdStrategy not config")
	}
	thresholdPercent := thresholdConfig.CPUEvictThresholdPercent
	if thresholdPercent == nil {
		return fmt.Errorf("CPUEvictThresholdPercent not config")
	} else if *thresholdPercent < 0 {
		return fmt.Errorf("threshold percent(%v) should greater than 0", *thresholdPercent)
	}
	lowerPercent := int64(0)
	if thresholdConfig.CPUEvictLowerPercent != nil {
		lowerPercent = *thresholdConfig.CPUEvictLowerPercent
	} else {
		lowerPercent = *thresholdPercent - cpuReleaseBufferPercent
	}
	if lowerPercent >= *thresholdPercent {
		return fmt.Errorf("lower percent(%v) should less than threshold percent(%v)", lowerPercent, *thresholdPercent)
	}
	if thresholdConfig.EvictEnabledPriorityThreshold == nil {
		return fmt.Errorf("EvictEnabledPriorityThreshold not config")
	}
	return nil
}
func getBECPUMetric(resouceAllocation metriccache.MetricPropertyValue, querier metriccache.Querier, aggregateType metriccache.AggregationType) (float64, int64) {
	var properties map[metriccache.MetricProperty]string

	switch resouceAllocation {
	case metriccache.BEResourceAllocationUsage:
		properties = metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationUsage))
	case metriccache.BEResourceAllocationRequest:
		properties = metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationRequest))
	case metriccache.BEResourceAllocationRealLimit:
		properties = metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationRealLimit))
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

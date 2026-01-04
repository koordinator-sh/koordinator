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

package memoryevict

import (
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
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
	MemoryEvictName = "memoryEvict"

	memoryReleaseBufferPercent = 2
)

var _ framework.QOSStrategy = &memoryEvictor{}

type memoryEvictor struct {
	evictInterval         time.Duration
	evictCoolingInterval  time.Duration
	metricCollectInterval time.Duration
	statesInformer        statesinformer.StatesInformer
	metricCache           metriccache.MetricCache
	lastEvictTime         time.Time
	evictExecutor         qosmanagerUtil.EvictionExecutor
}

func New(opt *framework.Options) framework.QOSStrategy {
	return &memoryEvictor{
		evictInterval:         time.Duration(opt.Config.MemoryEvictIntervalSeconds) * time.Second,
		evictCoolingInterval:  time.Duration(opt.Config.MemoryEvictCoolTimeSeconds) * time.Second,
		metricCollectInterval: opt.MetricAdvisorConfig.CollectResUsedInterval,
		statesInformer:        opt.StatesInformer,
		metricCache:           opt.MetricCache,
	}
}

func (m *memoryEvictor) Enabled() bool {
	return (features.DefaultKoordletFeatureGate.Enabled(features.BEMemoryEvict) || features.DefaultKoordletFeatureGate.Enabled(features.MemoryEvict)) && m.evictInterval > 0
}

func (m *memoryEvictor) Setup(ctx *framework.Context) {
	m.evictExecutor = qosmanagerUtil.InitializeEvictionExecutor(ctx.Evictor, ctx.OnlyEvictByAPI)
}

func (m *memoryEvictor) Run(stopCh <-chan struct{}) {
	go wait.Until(m.memoryEvict, m.evictInterval, stopCh)
}

func (m *memoryEvictor) memoryEvict() {
	klog.V(5).Infof("starting memory evict process")
	defer klog.V(5).Infof("memory evict process completed")

	if time.Now().Before(m.lastEvictTime.Add(m.evictCoolingInterval)) {
		klog.V(5).Infof("skip memory evict process, still in evict cooling time")
		return
	}

	nodeSLO := m.statesInformer.GetNodeSLO()
	node := m.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("skip memory evict, Node is nil")
		return
	}

	// build memory evict tasks
	var evictTasks []*qosmanagerUtil.EvictTaskInfo
	memoryCapacity := node.Status.Capacity.Memory().Value()
	if memoryCapacity <= 0 {
		klog.Warningf("skip memory evict, node memoryCapacity not valid, value: %d", memoryCapacity)
		return
	}
	// triggerFeatures defines the features that can trigger pod eviction based on memory pressure.
	// - features.BEMemoryEvict and features.MemoryEvict can operate independently.
	// - Both evaluate eviction decisions using the same memory watermark (threshold).
	// - When both are enabled:
	//   * BEMemoryEvict runs first, followed by MemoryEvict.
	//   * They target different sets of pods (no duplicate victims), but the overall resource
	//     release effect is cumulative and considered together during scheduling and capacity planning.
	// - Although safe to enable concurrently, it is generally NOT RECOMMENDED to run both simultaneously,
	//   as this may lead to redundant eviction logic and increased system complexity without clear benefit.
	triggerFeatures := []featuregate.Feature{features.BEMemoryEvict, features.MemoryEvict}
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
		task, err := m.buildEvictTask(feature, nodeSLO, memoryCapacity)
		if err != nil {
			klog.Warningf("failed to build memoryEvict task trigger by feature %v, err: %v", feature, err)
			continue
		}
		if task == nil {
			continue
		}
		evictTasks = append(evictTasks, task)
	}
	if len(evictTasks) == 0 {
		klog.V(4).Infof("memoryEvict skipped, no task to evict")
		return
	}
	released, hasReleased := qosmanagerUtil.KillAndEvictPods(m.evictExecutor, node, evictTasks)
	if hasReleased {
		m.lastEvictTime = time.Now()
	}
	// report and renew time
	for _, task := range evictTasks {
		succeed, failedToRelease := qosmanagerUtil.EvictTaskCheck(task, released)
		if succeed {
			klog.V(4).Infof("evict task %v succeed, should release %v[%v], completed", task.Reason, task.ReleaseTarget, task.ToReleaseResource)
		} else {
			klog.Warningf("evict task %v failed, should release %v[%v], failed to release %v[%v]", task.Reason, task.ReleaseTarget, task.ToReleaseResource, task.ReleaseTarget, failedToRelease)
		}
	}
}

func (m *memoryEvictor) buildEvictTask(feature featuregate.Feature, nodeSLO *slov1alpha1.NodeSLO, memoryCapacity int64) (*qosmanagerUtil.EvictTaskInfo, error) {
	thresholdConfig := nodeSLO.Spec.ResourceUsedThresholdWithBE
	var evictReason string
	var getPodEvictInfoAndSortFunc func(*slov1alpha1.ResourceThresholdStrategy) []*qosmanagerUtil.PodEvictInfo
	// check config
	switch feature {
	case features.BEMemoryEvict:
		evictReason = qosmanagerUtil.EvictReasonPrefix + resourceexecutor.EvictBEPodByNodeMemoryUsage
		getPodEvictInfoAndSortFunc = m.getSortedBEPodInfos
	case features.MemoryEvict:
		evictReason = qosmanagerUtil.EvictReasonPrefix + resourceexecutor.EvictPodByMemoryUsedThresholdPercent
		getPodEvictInfoAndSortFunc = m.getPodEvictInfoAndSortByPriority
	default:
		return nil, fmt.Errorf("unknown feature: %v", feature)
	}
	if err := isConfigValid(thresholdConfig, feature); err != nil {
		return nil, fmt.Errorf("skip memory evict feature %v, invalid config, err=%v", feature, err)
	}
	release := m.calculateReleaseByUsedThresholdPercent(thresholdConfig, memoryCapacity)
	if release <= 0 {
		return nil, nil
	}
	sortedPodInfos := getPodEvictInfoAndSortFunc(thresholdConfig)
	return &qosmanagerUtil.EvictTaskInfo{
		Reason:          evictReason,
		SortedEvictPods: sortedPodInfos,
		ToReleaseResource: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(release, resource.BinarySI),
		},
		ReleaseTarget: qosmanagerUtil.ReleaseTargetTypeResourceUsed,
	}, nil
}

func isConfigValid(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, feature featuregate.Feature) error {
	if thresholdConfig == nil {
		return fmt.Errorf("resourceThresholdStrategy not config")
	}
	thresholdPercent := thresholdConfig.MemoryEvictThresholdPercent
	if thresholdPercent == nil {
		return fmt.Errorf("threshold percent is nil")
	} else if *thresholdPercent < 0 {
		return fmt.Errorf("threshold percent(%v) should greater than 0", *thresholdPercent)
	}

	lowerPercent := int64(0)
	if thresholdConfig.MemoryEvictLowerPercent != nil {
		lowerPercent = *thresholdConfig.MemoryEvictLowerPercent
	} else {
		lowerPercent = *thresholdPercent - memoryReleaseBufferPercent
	}

	if lowerPercent >= *thresholdPercent {
		return fmt.Errorf("lower percent(%v) should less than threshold percent(%v)", lowerPercent, *thresholdPercent)
	}
	if feature == features.MemoryEvict {
		if thresholdConfig.EvictEnabledPriorityThreshold == nil {
			return fmt.Errorf("EvictEnabledPriorityThreshold not config")
		}
	}
	return nil
}

func (m *memoryEvictor) calculateReleaseByUsedThresholdPercent(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, memoryCapacity int64) int64 {
	queryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
	if err != nil {
		klog.Warningf("get query failed, error %v", err)
		return 0
	}

	nodeMemoryUsed, err := helpers.CollectorNodeMetricLast(m.metricCache, queryMeta, m.metricCollectInterval)
	if err != nil {
		klog.Warningf("memoryEvict by usedTresholdPercent skippped, get node metrics error: %v", err)
		return 0
	}
	nodeMemoryUsage := int64(nodeMemoryUsed) * 100 / memoryCapacity
	thresholdPercent := thresholdConfig.MemoryEvictThresholdPercent
	if nodeMemoryUsage < *thresholdPercent {
		klog.V(5).Infof("memoryEvict by usedTresholdPercent skippped, node memory usage(%v) is below threshold(%v)", nodeMemoryUsage, *thresholdPercent)
		return 0
	}
	lowerPercent := int64(0)
	if thresholdConfig.MemoryEvictLowerPercent != nil {
		lowerPercent = *thresholdConfig.MemoryEvictLowerPercent
	} else {
		lowerPercent = *thresholdPercent - memoryReleaseBufferPercent
	}
	memoryNeedRelease := memoryCapacity * (nodeMemoryUsage - lowerPercent) / 100
	klog.Infof("memoryEvict by usedTresholdPercent start to evict %v, node memoryUsage(%v): %.2f, evictThresholdUsage: %.2f, evictLowerUsage: %.2f",
		memoryNeedRelease,
		nodeMemoryUsed,
		float64(nodeMemoryUsage)/100,
		float64(*thresholdPercent)/100,
		float64(lowerPercent)/100,
	)
	return memoryNeedRelease
}

func (m *memoryEvictor) getSortedBEPodInfos(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) []*qosmanagerUtil.PodEvictInfo {
	podMetricMap := helpers.CollectAllPodMetricsLast(m.statesInformer, m.metricCache, metriccache.PodMemUsageMetric, m.metricCollectInterval)
	var bePodInfos []*qosmanagerUtil.PodEvictInfo
	for _, podMeta := range m.statesInformer.GetAllPods() {
		pod := podMeta.Pod
		if extension.GetPodQoSClassRaw(pod) == extension.QoSBE {
			info := &qosmanagerUtil.PodEvictInfo{
				Pod:        pod,
				MemoryUsed: int64(podMetricMap[string(pod.UID)]),
			}
			bePodInfos = append(bePodInfos, info)
		}
	}

	sort.Slice(bePodInfos, func(i, j int) bool {
		// TODO: https://github.com/koordinator-sh/koordinator/pull/65#discussion_r849048467
		// compare priority > podMetric > name
		if bePodInfos[i].Pod.Spec.Priority != nil && bePodInfos[j].Pod.Spec.Priority != nil && *bePodInfos[i].Pod.Spec.Priority != *bePodInfos[j].Pod.Spec.Priority {
			return *bePodInfos[i].Pod.Spec.Priority < *bePodInfos[j].Pod.Spec.Priority
		}
		if bePodInfos[i].MemoryUsed != 0 && bePodInfos[j].MemoryUsed != 0 {
			//return bePodInfos[i].podMetric.MemoryUsed.MemoryWithoutCache.Value() > bePodInfos[j].podMetric.MemoryUsed.MemoryWithoutCache.Value()
			return bePodInfos[i].MemoryUsed > bePodInfos[j].MemoryUsed
		} else if bePodInfos[i].MemoryUsed == 0 && bePodInfos[j].MemoryUsed == 0 {

			return bePodInfos[i].Pod.Name > bePodInfos[j].Pod.Name
		}
		return bePodInfos[j].MemoryUsed == 0
	})

	return bePodInfos
}

func (m *memoryEvictor) getPodEvictInfoAndSortByPriority(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) []*qosmanagerUtil.PodEvictInfo {
	var podsInfos []*qosmanagerUtil.PodEvictInfo
	priorityThreshold := *thresholdConfig.EvictEnabledPriorityThreshold
	for _, podMeta := range m.statesInformer.GetAllPods() {
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
		queryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(pod.UID)))
		if err != nil {
			klog.Warningf("get pod %v query failed, error %v", pod.UID, err)
			continue
		}
		result, err := helpers.CollectPodMetricLast(m.metricCache, queryMeta, m.metricCollectInterval)
		if err != nil {
			klog.Warningf("get pod %v metrics failed, error %v", pod.UID, err)
			continue
		}
		podInfo.MemoryUsed = int64(result * 1000)
		milliRequestSum := int64(0)
		for _, container := range pod.Spec.Containers {
			containerMemReq := util.GetContainerBatchMemoryByteRequest(&container)
			if containerMemReq > 0 {
				milliRequestSum = milliRequestSum + containerMemReq
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
		return podsInfos[i].MemoryUsed > podsInfos[j].MemoryUsed
	})
	return podsInfos
}

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
	return (features.DefaultKoordletFeatureGate.Enabled(features.BEMemoryEvict) ||
		features.DefaultKoordletFeatureGate.Enabled(features.MemoryEvict) ||
		features.DefaultKoordletFeatureGate.Enabled(features.MemoryAllocatableEvict)) && m.evictInterval > 0
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

	if memoryCapacity := node.Status.Capacity.Memory().Value(); memoryCapacity <= 0 {
		klog.Warningf("skip memory evict, node memoryCapacity not valid, value: %d", memoryCapacity)
		return
	}
	// triggerFeatures defines the features that can trigger pod eviction based on memory pressure.
	// - features.BEMemoryEvict and features.MemoryEvict can operate independently.
	// - All evaluate eviction decisions using the same memory watermark (threshold).
	// - When both are enabled:
	//   * BEMemoryEvict runs first, followed by MemoryAllocatableEvict, then MemoryEvict.
	//   * They target different sets of pods (no duplicate victims), but the overall resource
	//     release effect is cumulative and considered together during scheduling and capacity planning.
	// - Although safe to enable concurrently, it is generally NOT RECOMMENDED to run both simultaneously,
	//   as this may lead to redundant eviction logic and increased system complexity without clear benefit.
	triggerFeatures := []featuregate.Feature{features.BEMemoryEvict, features.MemoryAllocatableEvict, features.MemoryEvict}
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
		task, err := m.buildEvictTask(feature, nodeSLO, node)
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
		klog.V(4).Infof("skip memory evict, no task to evict")
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
			klog.V(4).Infof("evict task %v succeed, released resourceTarget[%v]: %v", task.Reason, task.ReleaseTarget, task.ToReleaseResource)
		} else {
			klog.Warningf("evict task %v failed, failed to release resourceTarget[%v]: to release %v,  failed to release %v ", task.Reason, task.ReleaseTarget, task.ToReleaseResource, failedToRelease)
		}
	}
}

func (m *memoryEvictor) buildEvictTask(feature featuregate.Feature, nodeSLO *slov1alpha1.NodeSLO, node *corev1.Node) (*qosmanagerUtil.EvictTaskInfo, error) {
	thresholdConfig := nodeSLO.Spec.ResourceUsedThresholdWithBE
	var evictReason string
	var releaseTarget qosmanagerUtil.ReleaseTargetType
	var getPodEvictInfoAndSortFunc func(string, *slov1alpha1.ResourceThresholdStrategy, []*statesinformer.PodMeta) []*qosmanagerUtil.PodEvictInfo
	var genReleaseResource func(*slov1alpha1.ResourceThresholdStrategy, *corev1.Node, []*statesinformer.PodMeta) (corev1.ResourceList, func(podInfo *qosmanagerUtil.PodEvictInfo) corev1.ResourceList)
	evictReason = qosmanagerUtil.EvictReasonPrefix + string(feature)
	switch feature {
	case features.BEMemoryEvict:
		getPodEvictInfoAndSortFunc = m.getSortedBEPodInfos
		genReleaseResource = m.calculateReleaseByUsedThresholdPercent
		releaseTarget = qosmanagerUtil.ReleaseTargetTypeResourceUsed
	case features.MemoryEvict:
		getPodEvictInfoAndSortFunc = m.getPodEvictInfoAndSortByUsed
		genReleaseResource = m.calculateReleaseByUsedThresholdPercent
		releaseTarget = qosmanagerUtil.ReleaseTargetTypeResourceUsed
	case features.MemoryAllocatableEvict:
		getPodEvictInfoAndSortFunc = m.getPodEvictInfoAndSortByAllocatable
		genReleaseResource = m.calculateReleaseByAllocatableThresholdPercent
		releaseTarget = qosmanagerUtil.ReleaseTargetTypeResourceRequest
	default:
		return nil, fmt.Errorf("unknown feature: %v", feature)
	}
	if err := generateConfigCheck(feature)(thresholdConfig); err != nil {
		return nil, fmt.Errorf("skip memory evict feature %v, invalid config, err=%v", feature, err)
	}
	pods := m.statesInformer.GetAllPods()
	release, getPodResourceFunc := genReleaseResource(thresholdConfig, node, pods)
	if len(release) == 0 {
		klog.V(4).Infof("skip memory evict feature %v, no need to evict", feature)
		return nil, nil
	}
	sortedPodInfos := getPodEvictInfoAndSortFunc(string(feature), thresholdConfig, pods)
	return &qosmanagerUtil.EvictTaskInfo{
		Reason:             evictReason,
		SortedEvictPods:    sortedPodInfos,
		ToReleaseResource:  release,
		ReleaseTarget:      releaseTarget,
		GetPodResourceFunc: getPodResourceFunc,
	}, nil
}

func generateConfigCheck(feature featuregate.Feature) func(*slov1alpha1.ResourceThresholdStrategy) error {
	common := func(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) error {
		if thresholdConfig == nil {
			return fmt.Errorf("resourceThresholdStrategy not config")
		}
		thresholdPercent := thresholdConfig.MemoryEvictThresholdPercent
		if thresholdPercent == nil {
			return fmt.Errorf("threshold percent is nil")
		} else if *thresholdPercent < 0 {
			return fmt.Errorf("threshold percent(%v) should equal or greater than 0", *thresholdPercent)
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
		return nil
	}
	switch feature {
	case features.BEMemoryEvict:
		return common
	case features.MemoryEvict:
		return func(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) error {
			if err := common(thresholdConfig); err != nil {
				return err
			}
			if thresholdConfig.EvictEnabledPriorityThreshold == nil {
				return fmt.Errorf("EvictEnabledPriorityThreshold not config")
			}
			return nil
		}
	case features.MemoryAllocatableEvict:
		return isAllocatableThresholdConfigValid
	}
	return nil
}
func isAllocatableThresholdConfigValid(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) error {
	if thresholdConfig == nil {
		return fmt.Errorf("ResourceThresholdStrategy not config")
	}
	thresholdPercent := thresholdConfig.MemoryAllocatableEvictThresholdPercent
	if thresholdPercent == nil {
		return fmt.Errorf("MemoryAllocatableEvictThresholdPercent not config")
	} else if *thresholdPercent < 0 {
		return fmt.Errorf("threshold percent(%v) should greater than 0", *thresholdPercent)
	}
	lowerPercent := int64(0)
	if thresholdConfig.MemoryAllocatableEvictLowerPercent != nil {
		lowerPercent = *thresholdConfig.MemoryAllocatableEvictLowerPercent
	}
	if lowerPercent >= *thresholdPercent {
		return fmt.Errorf("lower percent(%v) should less than threshold percent(%v)", lowerPercent, *thresholdPercent)
	}
	priorityThresholdPercent := thresholdConfig.AllocatableEvictPriorityThreshold
	if priorityThresholdPercent == nil {
		return fmt.Errorf("AllocatableEvictPriorityThreshold not config")
	}
	if *priorityThresholdPercent > apiext.PriorityMidValueMax {
		return fmt.Errorf("priorityThresholdPercent(%v) should less than %v, koor-prod pods should not be killed", *priorityThresholdPercent, apiext.PriorityMidValueMax)
	}
	return nil
}

func (m *memoryEvictor) calculateReleaseByUsedThresholdPercent(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, node *corev1.Node, pods []*statesinformer.PodMeta) (overall corev1.ResourceList,
	calculateFunc func(podInfo *qosmanagerUtil.PodEvictInfo) corev1.ResourceList) {
	overall = make(corev1.ResourceList)
	resourceType := corev1.ResourceMemory
	calculateFunc = func(podInfo *qosmanagerUtil.PodEvictInfo) corev1.ResourceList {
		return corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(podInfo.MemoryUsed, resource.BinarySI),
		}
	}
	queryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
	if err != nil {
		klog.Warningf("get query failed, error %v", err)
		return
	}
	nodeMemoryUsed, err := helpers.CollectorNodeMetricLast(m.metricCache, queryMeta, m.metricCollectInterval)
	if err != nil {
		klog.Warningf("memoryEvict by usedTresholdPercent skippped, get node metrics error: %v", err)
		return
	}
	memoryCapacity := node.Status.Capacity.Memory().Value()
	nodeMemoryUsage := int64(nodeMemoryUsed) * 100 / memoryCapacity
	thresholdPercent := thresholdConfig.MemoryEvictThresholdPercent
	if nodeMemoryUsage < *thresholdPercent {
		klog.V(5).Infof("memoryEvict by usedTresholdPercent skippped, node memory usage(%v) is below threshold(%v)", nodeMemoryUsage, *thresholdPercent)
		return
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
	overall[resourceType] = *resource.NewQuantity(memoryNeedRelease, resource.BinarySI)
	return
}

func (m *memoryEvictor) calculateReleaseByAllocatableThresholdPercent(thresholdConfig *slov1alpha1.ResourceThresholdStrategy, node *corev1.Node, pods []*statesinformer.PodMeta) (overall corev1.ResourceList,
	calculateFunc func(podInfo *qosmanagerUtil.PodEvictInfo) corev1.ResourceList) {
	overall = make(corev1.ResourceList)
	requestedOnNode := make(corev1.ResourceList)
	priorityThreshold := *thresholdConfig.AllocatableEvictPriorityThreshold
	allocatableEvictThreshold := *thresholdConfig.MemoryAllocatableEvictThresholdPercent
	allocatableEvictLowerThreshold := *thresholdConfig.MemoryAllocatableEvictLowerPercent
	if allocatableEvictThreshold < 0 {
		// no need to release
		return
	}
	prioritiesMp := make(map[apiext.PriorityClass]bool)
	for _, podMeta := range pods {
		pod := podMeta.Pod
		if priority := apiext.GetPodPriorityValueWithDefault(pod); priority == nil || *priority > priorityThreshold {
			continue
		}
		request := qosmanagerUtil.GetRequestFromPod(pod, corev1.ResourceMemory)
		util.AddResourceList(requestedOnNode, request)
	}
	resourceOnNode := node.Status.Allocatable
	for rt, rq := range requestedOnNode {
		nq, ok := resourceOnNode[rt]
		if !ok || nq.IsZero() {
			overall[rt] = rq
			continue
		}
		rqValue := float32(qosmanagerUtil.ConvertQuantityToInt64(rt, rq))
		sumValue := float32(qosmanagerUtil.ConvertQuantityToInt64(rt, nq))
		if rqValue/sumValue > float32(allocatableEvictThreshold)/100 {
			overall[rt] = *resource.NewQuantity(int64((rqValue/sumValue-float32(allocatableEvictLowerThreshold)/100)*sumValue), resource.BinarySI)
		}
	}
	for r := range overall {
		// currently only support koord-batch/koord-mid
		if class, ok := apiext.ReverseResourceNameMap[r]; ok {
			prioritiesMp[class] = true
		}
	}
	calculateFunc = func(podInfo *qosmanagerUtil.PodEvictInfo) corev1.ResourceList {
		if _, ok := prioritiesMp[apiext.GetPodPriorityClassRaw(podInfo.Pod)]; !ok {
			return nil
		}
		return qosmanagerUtil.GetRequestFromPod(podInfo.Pod, corev1.ResourceMemory)
	}
	return
}

func (m *memoryEvictor) getSortedBEPodInfos(evictionPolicy string, thresholdConfig *slov1alpha1.ResourceThresholdStrategy, pods []*statesinformer.PodMeta) []*qosmanagerUtil.PodEvictInfo {
	podMetricMap := helpers.CollectAllPodMetricsLast(m.statesInformer, m.metricCache, metriccache.PodMemUsageMetric, m.metricCollectInterval)
	var bePodInfos []*qosmanagerUtil.PodEvictInfo
	for _, podMeta := range pods {
		pod := podMeta.Pod
		if extension.GetPodQoSClassRaw(pod) != extension.QoSBE {
			continue
		}
		if !qosmanagerUtil.IsEvictionPolicyAllowed(evictionPolicy, pod) {
			continue
		}
		info := &qosmanagerUtil.PodEvictInfo{
			Pod:        pod,
			MemoryUsed: int64(podMetricMap[string(pod.UID)]),
		}
		bePodInfos = append(bePodInfos, info)
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

func (m *memoryEvictor) getPodEvictInfoAndSortByAllocatable(evictionPolicy string, thresholdConfig *slov1alpha1.ResourceThresholdStrategy, pods []*statesinformer.PodMeta) []*qosmanagerUtil.PodEvictInfo {
	return m.getPodEvictInfoAndSortByPriority(evictionPolicy, *thresholdConfig.AllocatableEvictPriorityThreshold, pods, func(a, b *qosmanagerUtil.PodEvictInfo) bool {
		return a.MemoryRequest > b.MemoryRequest
	})
}

func (m *memoryEvictor) getPodEvictInfoAndSortByUsed(evictionPolicy string, thresholdConfig *slov1alpha1.ResourceThresholdStrategy, pods []*statesinformer.PodMeta) []*qosmanagerUtil.PodEvictInfo {
	return m.getPodEvictInfoAndSortByPriority(evictionPolicy, *thresholdConfig.EvictEnabledPriorityThreshold, pods, func(a, b *qosmanagerUtil.PodEvictInfo) bool {
		return a.MemoryUsed > b.MemoryUsed
	})
}
func (m *memoryEvictor) getPodEvictInfoAndSortByPriority(evictionPolicy string, priorityThreshold int32, pods []*statesinformer.PodMeta, subSortFun func(a, b *qosmanagerUtil.PodEvictInfo) bool) []*qosmanagerUtil.PodEvictInfo {
	var podsInfos []*qosmanagerUtil.PodEvictInfo
	for _, podMeta := range pods {
		pod := podMeta.Pod
		// higher priority pods are not allowed to evict
		// 1. filter inactive / priority / eviction policy
		if util.IsPodInactive(pod) {
			continue
		}
		if !qosmanagerUtil.IsEvictionPolicyAllowed(evictionPolicy, pod) {
			continue
		}
		podInfo := &qosmanagerUtil.PodEvictInfo{Pod: podMeta.Pod}
		if priority := apiext.GetPodPriorityValueWithDefault(pod); priority == nil || *priority > priorityThreshold {
			continue
		} else {
			podInfo.Priority = *priority
		}
		// 2. filter: exclude: koordinator.sh/eviction-enabled
		if !apiext.PodEvictEnabled(pod) {
			continue
		}
		// 3. sort :  koordinator.sh/priority
		podPriority := qosmanagerUtil.GetPodPriorityLabel(pod, int64(podInfo.Priority))
		podInfo.LabelPriority = podPriority
		// 4. filter no metrics
		queryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(pod.UID)))
		if err != nil {
			klog.Warningf("build pod %v query failed, error %v", pod.UID, err)
			continue
		}
		result, err := helpers.CollectPodMetricLast(m.metricCache, queryMeta, m.metricCollectInterval)
		if err != nil {
			klog.Warningf("get pod %v metrics failed, error %v", pod.UID, err)
			continue
		}
		podInfo.MemoryUsed = int64(result * 1000)
		_, rv := qosmanagerUtil.GetRequestTypeAndValueFromPod(pod, corev1.ResourceMemory)
		podInfo.MemoryRequest = rv
		podsInfos = append(podsInfos, podInfo)
	}

	sort.Slice(podsInfos, func(i, j int) bool {
		if podsInfos[i].Priority != podsInfos[j].Priority {
			return podsInfos[i].Priority < podsInfos[j].Priority
		}
		if podsInfos[i].LabelPriority != podsInfos[j].LabelPriority {
			return podsInfos[i].LabelPriority < podsInfos[j].LabelPriority
		}
		return subSortFun(podsInfos[i], podsInfos[j])
	})
	return podsInfos
}

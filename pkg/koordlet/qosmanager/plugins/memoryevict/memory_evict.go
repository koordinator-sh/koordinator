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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
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
	evictor               *framework.Evictor
	lastEvictTime         time.Time
}

type podInfo struct {
	pod     *corev1.Pod
	memUsed float64
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
	return features.DefaultKoordletFeatureGate.Enabled(features.BEMemoryEvict) && m.evictInterval > 0
}

func (m *memoryEvictor) Setup(ctx *framework.Context) {
	m.evictor = ctx.Evictor
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
	if disabled, err := features.IsFeatureDisabled(nodeSLO, features.BEMemoryEvict); err != nil {
		klog.Errorf("failed to acquire memory eviction feature-gate, error: %v", err)
		return
	} else if disabled {
		klog.V(4).Infof("skip memory evict, disabled in NodeSLO")
		return
	}

	thresholdConfig := nodeSLO.Spec.ResourceUsedThresholdWithBE
	thresholdPercent := thresholdConfig.MemoryEvictThresholdPercent
	if thresholdPercent == nil {
		klog.Warningf("skip memory evict, threshold percent is nil")
		return
	} else if *thresholdPercent < 0 {
		klog.Warningf("skip memory evict, threshold percent(%v) should greater than 0", *thresholdPercent)
		return
	}

	lowerPercent := int64(0)
	if thresholdConfig.MemoryEvictLowerPercent != nil {
		lowerPercent = *thresholdConfig.MemoryEvictLowerPercent
	} else {
		lowerPercent = *thresholdPercent - memoryReleaseBufferPercent
	}

	if lowerPercent >= *thresholdPercent {
		klog.Warningf("skip memory evict, lower percent(%v) should less than threshold percent(%v)", lowerPercent, *thresholdPercent)
		return
	}

	podMetrics := helpers.CollectAllPodMetricsLast(m.statesInformer, m.metricCache, metriccache.PodMemUsageMetric, m.metricCollectInterval)
	node := m.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("skip memory evict, Node is nil")
		return
	}

	memoryCapacity := node.Status.Capacity.Memory().Value()
	if memoryCapacity <= 0 {
		klog.Warningf("skip memory evict, memory capacity(%v) should greater than 0", memoryCapacity)
		return
	}

	queryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
	if err != nil {
		klog.Warningf("skip memory evict, get node query failed, error: %v", err)
		return
	}

	nodeMemoryUsed, err := helpers.CollectorNodeMetricLast(m.metricCache, queryMeta, m.metricCollectInterval)
	if err != nil {
		klog.Warningf("skip memory evict, get node metrics error: %v", err)
		return
	}
	nodeMemoryUsage := int64(nodeMemoryUsed) * 100 / memoryCapacity
	if nodeMemoryUsage < *thresholdPercent {
		klog.V(5).Infof("skip memory evict, node memory usage(%v) is below threshold(%v)", nodeMemoryUsage, *thresholdPercent)
		return
	}

	klog.Infof("node MemoryUsage(%v): %.2f, evictThresholdUsage: %.2f, evictLowerUsage: %.2f",
		nodeMemoryUsed,
		float64(nodeMemoryUsage)/100,
		float64(*thresholdPercent)/100,
		float64(lowerPercent)/100,
	)

	memoryNeedRelease := memoryCapacity * (nodeMemoryUsage - lowerPercent) / 100
	m.killAndEvictBEPods(node, podMetrics, memoryNeedRelease)
}

func (m *memoryEvictor) killAndEvictBEPods(node *corev1.Node, podMetrics map[string]float64, memoryNeedRelease int64) {
	bePodInfos := m.getSortedBEPodInfos(podMetrics)
	message := fmt.Sprintf("killAndEvictBEPods for node, need to release memory: %v", memoryNeedRelease)
	memoryReleased := int64(0)

	var killedPods []*corev1.Pod
	for _, bePod := range bePodInfos {
		if memoryReleased >= memoryNeedRelease {
			break
		}

		killMsg := fmt.Sprintf("%v, kill pod: %v", message, bePod.pod.Name)
		helpers.KillContainers(bePod.pod, killMsg)
		killedPods = append(killedPods, bePod.pod)
		if bePod.memUsed != 0 {
			memoryReleased += int64(bePod.memUsed)
		}
	}

	m.evictor.EvictPodsIfNotEvicted(killedPods, node, resourceexecutor.EvictPodByNodeMemoryUsage, message)

	m.lastEvictTime = time.Now()
	klog.Infof("killAndEvictBEPods completed, memoryNeedRelease(%v) memoryReleased(%v)", memoryNeedRelease, memoryReleased)
}

func (m *memoryEvictor) getSortedBEPodInfos(podMetricMap map[string]float64) []*podInfo {

	var bePodInfos []*podInfo
	for _, podMeta := range m.statesInformer.GetAllPods() {
		pod := podMeta.Pod
		if extension.GetPodQoSClassRaw(pod) == extension.QoSBE {
			info := &podInfo{
				pod:     pod,
				memUsed: podMetricMap[string(pod.UID)],
			}
			bePodInfos = append(bePodInfos, info)
		}
	}

	sort.Slice(bePodInfos, func(i, j int) bool {
		// TODO: https://github.com/koordinator-sh/koordinator/pull/65#discussion_r849048467
		// compare priority > podMetric > name
		if bePodInfos[i].pod.Spec.Priority != nil && bePodInfos[j].pod.Spec.Priority != nil && *bePodInfos[i].pod.Spec.Priority != *bePodInfos[j].pod.Spec.Priority {
			return *bePodInfos[i].pod.Spec.Priority < *bePodInfos[j].pod.Spec.Priority
		}
		if bePodInfos[i].memUsed != 0 && bePodInfos[j].memUsed != 0 {
			//return bePodInfos[i].podMetric.MemoryUsed.MemoryWithoutCache.Value() > bePodInfos[j].podMetric.MemoryUsed.MemoryWithoutCache.Value()
			return bePodInfos[i].memUsed > bePodInfos[j].memUsed
		} else if bePodInfos[i].memUsed == 0 && bePodInfos[j].memUsed == 0 {

			return bePodInfos[i].pod.Name > bePodInfos[j].pod.Name
		}
		return bePodInfos[j].memUsed == 0
	})

	return bePodInfos
}

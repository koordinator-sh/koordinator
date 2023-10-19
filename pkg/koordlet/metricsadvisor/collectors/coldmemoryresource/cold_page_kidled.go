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

package coldmemoryresource

import (
	"fmt"
	"time"

	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type kidledcoldPageCollector struct {
	collectInterval time.Duration
	started         *atomic.Bool
	cgroupReader    resourceexecutor.CgroupReader
	statesInformer  statesinformer.StatesInformer
	podFilter       framework.PodFilter
	appendableDB    metriccache.Appendable
	metricDB        metriccache.MetricCache
	coldBoundary    int
}

func (k *kidledcoldPageCollector) Run(stopCh <-chan struct{}) {
	go wait.Until(k.collectColdPageInfo, k.collectInterval, stopCh)
}

func (k *kidledcoldPageCollector) Started() bool {
	return k.started.Load()
}

func (k *kidledcoldPageCollector) Enabled() bool {
	if features.DefaultKoordletFeatureGate.Enabled(features.ColdPageCollector) {
		// start kidled
		kidledConfig := system.NewDefaultKidledConfig()
		err := system.SetKidledScanPeriodInSeconds(kidledConfig.ScanPeriodInseconds)
		if err != nil {
			klog.V(4).Infof("cold page collector start kidled err:", err)
			return false
		}
		err = system.SetKidledUseHierarchy(kidledConfig.UseHierarchy)
		if err != nil {
			klog.V(4).Infof("cold page collector start kidled err:", err)
			return false
		}
		system.SetIsStartColdMemory(true)
		system.SetKidledColdBoundary(k.coldBoundary)
		return true
	}
	return false
}

func (k *kidledcoldPageCollector) Setup(c1 *framework.Context) {}

func (k *kidledcoldPageCollector) collectColdPageInfo() {
	if k.statesInformer == nil {
		return
	}
	coldPageMetrics := make([]metriccache.MetricSample, 0)

	nodeColdPageInfoMetric, err := k.collectNodeColdPageInfo()
	if err != nil {
		klog.Warningf("generate node cold page info metrics failed, err %v", err)
	}
	coldPageMetrics = append(coldPageMetrics, nodeColdPageInfoMetric...)

	podsColdPageInfoMetric, err := k.collectPodsColdPageInfo()
	if err != nil {
		klog.Warningf("generate pods or container cold page info metrics failed, err %v", err)
	}
	coldPageMetrics = append(coldPageMetrics, podsColdPageInfoMetric...)

	appender := k.appendableDB.Appender()
	if err := appender.Append(coldPageMetrics); err != nil {
		klog.ErrorS(err, "Append node metrics error")
		return
	}

	if err := appender.Commit(); err != nil {
		klog.Warningf("Commit node metrics failed, reason: %v", err)
		return
	}

	k.started.Store(true)
}

func (k *kidledcoldPageCollector) collectNodeColdPageInfo() ([]metriccache.MetricSample, error) {
	coldPageMetrics := make([]metriccache.MetricSample, 0)
	collectTime := time.Now()
	nodeColdPageBytes, err := k.cgroupReader.ReadMemoryColdPageUsage("")
	if err != nil {
		return nil, err
	}
	nodeColdPageBytesValue := float64(nodeColdPageBytes)
	nodeColdPageMetrics, err := metriccache.NodeMemoryColdPageSizeMetric.GenerateSample(nil, collectTime, nodeColdPageBytesValue)
	if err != nil {
		return nil, err
	}
	coldPageMetrics = append(coldPageMetrics, nodeColdPageMetrics)

	memUsageWithHotPageBytes, err := koordletutil.GetNodeMemUsageWithHotPageCache(nodeColdPageBytes)
	if err != nil {
		return nil, err
	}
	memUsageWithHotPageValue := float64(memUsageWithHotPageBytes)
	memUsageWithHotPageMetrics, err := metriccache.NodeMemoryWithHotPageUsageMetric.GenerateSample(nil, collectTime, memUsageWithHotPageValue)
	if err != nil {
		return nil, err
	}
	coldPageMetrics = append(coldPageMetrics, memUsageWithHotPageMetrics)
	klog.V(4).Infof("collectNodeResUsed finished, count %v, memUsageWithHotPage[%v], coldPageSize[%v]",
		len(coldPageMetrics), memUsageWithHotPageValue, nodeColdPageBytes)
	return coldPageMetrics, nil
}

func (k *kidledcoldPageCollector) collectPodsColdPageInfo() ([]metriccache.MetricSample, error) {
	podMetas := k.statesInformer.GetAllPods()
	count := 0
	coldMetrics := make([]metriccache.MetricSample, 0)
	for _, meta := range podMetas {
		pod := meta.Pod
		uid := string(pod.UID) // types.UID
		podKey := util.GetPodKey(pod)
		if filtered, msg := k.FilterPod(meta); filtered {
			klog.V(5).Infof("skip collect pod %s, reason: %s", podKey, msg)
			continue
		}
		collectTime := time.Now()
		podCgroupDir := meta.CgroupDir
		podColdPageBytes, err := k.cgroupReader.ReadMemoryColdPageUsage(podCgroupDir)
		if err != nil {
			if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
				klog.V(6).Infof("failed to collect non-running pod cold page usage for %s, cold page err: %s",
					podKey, err)
			} else {
				klog.Warningf("can not get cold page info from memory.idle_page_stats file for pod %s/%s", pod.Namespace, pod.Name)
			}
			continue
		}
		podColdPageBytesValue := float64(podColdPageBytes)
		podColdPageMetrics, err := metriccache.PodMemoryColdPageSizeMetric.GenerateSample(metriccache.MetricPropertiesFunc.Pod(uid), collectTime, podColdPageBytesValue)
		if err != nil {
			return nil, err
		}
		coldMetrics = append(coldMetrics, podColdPageMetrics)

		podMemUsageWithHotPageBytes, err := koordletutil.GetPodMemUsageWithHotPageCache(k.cgroupReader, podCgroupDir, podColdPageBytes)
		if err != nil {
			klog.Warningf("failed to collect pod usage for Memory err: %s pod: %s/%s", err, pod.Namespace, pod.Name)
			continue
		}

		podMemUsageWithHotPageValue := float64(podMemUsageWithHotPageBytes)
		podMemUsageWithHotPageMetrics, err := metriccache.PodMemoryWithHotPageUsageMetric.GenerateSample(metriccache.MetricPropertiesFunc.Pod(uid), collectTime, podMemUsageWithHotPageValue)
		if err != nil {
			return nil, err
		}
		coldMetrics = append(coldMetrics, podMemUsageWithHotPageMetrics)
		count++
		containerColdPageMetrics, err := k.collectContainersColdPageInfo(meta)
		if err != nil {
			return nil, err
		}
		coldMetrics = append(coldMetrics, containerColdPageMetrics...)
	}
	klog.V(4).Infof("collectPodResUsed finished, pod num %d, collected %d", len(podMetas), count)
	return coldMetrics, nil
}

func (k *kidledcoldPageCollector) collectContainersColdPageInfo(meta *statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
	pod := meta.Pod
	count := 0
	coldMetrics := make([]metriccache.MetricSample, 0)
	for i := range pod.Status.ContainerStatuses {
		containerStat := &pod.Status.ContainerStatuses[i]
		containerKey := fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, containerStat.Name)
		collectTime := time.Now()
		if len(containerStat.ContainerID) == 0 {
			klog.Warningf("container %s id is empty, maybe not ready, skip this round", containerKey)
			continue
		}
		containerCgroupDir, err := koordletutil.GetContainerCgroupParentDir(meta.CgroupDir, containerStat)
		if err != nil {
			klog.Warningf("failed to collect container usage for %s, cannot get container cgroup, err: %s",
				containerKey, err)
			continue
		}
		containerColdPageBytes, err := k.cgroupReader.ReadMemoryColdPageUsage(containerCgroupDir)
		if err != nil {
			klog.Warningf("can not get cold page info from memory.idle_page_stats file for container %s", containerKey)
			continue
		}
		containerColdPageBytesValue := float64(containerColdPageBytes)
		containerColdPageMetrics, err := metriccache.ContainerMemoryColdPageSizeMetric.GenerateSample(metriccache.MetricPropertiesFunc.Container(containerStat.ContainerID), collectTime, containerColdPageBytesValue)
		if err != nil {
			return nil, err
		}
		coldMetrics = append(coldMetrics, containerColdPageMetrics)

		containerMemUsageWithHotPageBytes, err := koordletutil.GetContainerMemUsageWithHotPageCache(k.cgroupReader, containerCgroupDir, containerColdPageBytes)
		if err != nil {
			return nil, err
		}
		containerMemUsageWithHotPageValue := float64(containerMemUsageWithHotPageBytes)
		containerMemUsageWithHotPageMetrics, err := metriccache.ContainerMemoryWithHotPageUsageMetric.GenerateSample(metriccache.MetricPropertiesFunc.Container(containerStat.ContainerID), collectTime, containerMemUsageWithHotPageValue)
		if err != nil {
			return nil, err
		}
		coldMetrics = append(coldMetrics, containerMemUsageWithHotPageMetrics)
		count++
		klog.V(6).Infof("collect container %s, id %s finished, metric %+v", containerKey, pod.UID, coldMetrics)
	}
	klog.V(6).Infof("collect Container ColdPageInfo for pod %s/%s finished, container num %d, collected %d",
		pod.Namespace, pod.Name, len(pod.Status.ContainerStatuses), count)
	return coldMetrics, nil
}

func (k *kidledcoldPageCollector) FilterPod(meta *statesinformer.PodMeta) (bool, string) {
	return k.podFilter.FilterPod(meta)
}

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

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
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
}

func (k *kidledcoldPageCollector) Run(stopCh <-chan struct{}) {
	go wait.Until(k.collectColdPageInfo, k.collectInterval, stopCh)
}

func (k *kidledcoldPageCollector) Started() bool {
	return k.started.Load()
}

func (k *kidledcoldPageCollector) Enabled() bool {
	return true
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
	coldPageInfo, err := k.cgroupReader.ReadColdPageUsage("")
	if err != nil {
		return nil, err
	}
	nodeColdPageBytes := coldPageInfo.GetColdPageTotalBytes()
	nodeColdPageBytesValue := float64(nodeColdPageBytes)
	nodeColdPageMetrics, err := metriccache.NodeMemoryColdPageSizeMetric.GenerateSample(nil, collectTime, nodeColdPageBytesValue)
	if err != nil {
		return nil, err
	}
	coldPageMetrics = append(coldPageMetrics, nodeColdPageMetrics)
	memInfo, err := koordletutil.GetMemInfo()
	if err != nil {
		return nil, err
	}
	memUsageWithHotPageBytes := memInfo.MemTotal*1024 - memInfo.MemFree*1024 - nodeColdPageBytes
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
		coldPageInfo, err := k.cgroupReader.ReadColdPageUsage(podCgroupDir)
		if err != nil {
			klog.Errorf("can not get cold page info from memory.idle_page_stats file for pod %s/%s", pod.Namespace, pod.Name)
			continue
		}
		podColdPageBytes := coldPageInfo.GetColdPageTotalBytes()
		podColdPageBytesValue := float64(podColdPageBytes)
		podColdPageMetrics, err := metriccache.PodMemoryColdPageSizeMetric.GenerateSample(metriccache.MetricPropertiesFunc.Pod(uid), collectTime, podColdPageBytesValue)
		if err != nil {
			return nil, err
		}
		coldMetrics = append(coldMetrics, podColdPageMetrics)

		memStat, err := k.cgroupReader.ReadMemoryStat(podCgroupDir)
		if err != nil {
			// higher verbosity for probably non-running pods
			if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
				klog.Warningf("failed to collect non-running pod usage for Memory err: %s", err)
			} else {
				klog.Warningf("failed to collect pod usage for Memory err: %s", err)
			}
			continue
		}
		podMemUsageWithHotPageBytes := uint64(memStat.Usage()) + uint64(memStat.ActiveFile+memStat.InactiveFile) - podColdPageBytes
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
			klog.Error("container %s id is empty, maybe not ready, skip this round", containerKey)
			continue
		}
		containerCgroupDir, err := koordletutil.GetContainerCgroupParentDir(meta.CgroupDir, containerStat)
		if err != nil {
			klog.Error("failed to collect container usage for %s, cannot get container cgroup, err: %s",
				containerKey, err)
			continue
		}
		coldPageInfo, err := k.cgroupReader.ReadColdPageUsage(containerCgroupDir)
		if err != nil {
			klog.Errorf("can not get cold page info from memory.idle_page_stats file for container %s", containerKey)
			continue
		}
		containerColdPageBytes := coldPageInfo.GetColdPageTotalBytes()
		containerColdPageBytesValue := float64(containerColdPageBytes)
		containerColdPageMetrics, err := metriccache.ContainerMemoryColdPageSizeMetric.GenerateSample(metriccache.MetricPropertiesFunc.Container(containerStat.ContainerID), collectTime, containerColdPageBytesValue)
		if err != nil {
			return nil, err
		}
		coldMetrics = append(coldMetrics, containerColdPageMetrics)
		memStat, err := k.cgroupReader.ReadMemoryStat(containerCgroupDir)
		if err != nil {
			continue
		}
		containerMemUsageWithHotPageBytes := uint64(memStat.Usage()) + uint64(memStat.ActiveFile+memStat.InactiveFile) - containerColdPageBytes
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

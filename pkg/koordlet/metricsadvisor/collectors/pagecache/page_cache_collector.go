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

package pagecache

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

const (
	CollectorName = "PageCacheCollector"
)

var (
	timeNow = time.Now
)

type pageCacheCollector struct {
	collectInterval       time.Duration
	started               *atomic.Bool
	appendableDB          metriccache.Appendable
	metricDB              metriccache.MetricCache
	statesInformer        statesinformer.StatesInformer
	cgroupReader          resourceexecutor.CgroupReader
	podFilter             framework.PodFilter
	coldPageCollectorGate bool
}

func New(opt *framework.Options) framework.Collector {
	podFilter := framework.DefaultPodFilter
	if filter, ok := opt.PodFilters[CollectorName]; ok {
		podFilter = filter
	}
	return &pageCacheCollector{
		collectInterval:       opt.Config.CollectResUsedInterval,
		started:               atomic.NewBool(false),
		appendableDB:          opt.MetricCache,
		metricDB:              opt.MetricCache,
		statesInformer:        opt.StatesInformer,
		cgroupReader:          opt.CgroupReader,
		podFilter:             podFilter,
		coldPageCollectorGate: opt.Config.EnablePageCacheCollector,
	}
}

func (p *pageCacheCollector) Enabled() bool {
	return p.coldPageCollectorGate
}

func (p *pageCacheCollector) Setup(c *framework.Context) {}

func (p *pageCacheCollector) Run(stopCh <-chan struct{}) {
	go wait.Until(p.collectPageCache, p.collectInterval, stopCh)
}

func (p *pageCacheCollector) Started() bool {
	return p.started.Load()
}

func (p *pageCacheCollector) FilterPod(meta *statesinformer.PodMeta) (bool, string) {
	return p.podFilter.FilterPod(meta)
}

func (p *pageCacheCollector) collectPageCache() {
	if p.statesInformer == nil {
		return
	}
	p.collectNodePageCache()
	p.collectPodPageCache()
	p.started.Store(true)
}

func (p *pageCacheCollector) collectNodePageCache() {
	klog.V(6).Info("start collect node PageCache")
	nodeMetrics := make([]metriccache.MetricSample, 0)
	collectTime := timeNow()

	// NOTE: The collected memory usage is in kilobytes not bytes.
	memInfo, err := koordletutil.GetMemInfo()
	if err != nil {
		klog.Warningf("failed to collect page cache of node err: %s", err)
		return
	}

	memUsageWithPageCacheValue := float64(memInfo.MemUsageWithPageCache())
	memUsageWithPageCacheMetric, err := metriccache.NodeMemoryUsageWithPageCacheMetric.GenerateSample(nil, collectTime, memUsageWithPageCacheValue)
	if err != nil {
		klog.Warningf("generate node memory with page cache metrics failed, err %v", err)
		return
	}
	nodeMetrics = append(nodeMetrics, memUsageWithPageCacheMetric)

	appender := p.appendableDB.Appender()
	if err := appender.Append(nodeMetrics); err != nil {
		klog.ErrorS(err, "Append node metrics error")
		return
	}

	if err := appender.Commit(); err != nil {
		klog.Warningf("Commit node metrics failed, reason: %v", err)
		return
	}

	klog.V(4).Infof("collectNodePageCache finished, count %v, memUsageWithPageCache[%v]",
		len(nodeMetrics), memUsageWithPageCacheValue)
}

func (p *pageCacheCollector) collectPodPageCache() {
	klog.V(6).Info("start collect pods PageCache")
	podMetas := p.statesInformer.GetAllPods()
	count := 0
	metrics := make([]metriccache.MetricSample, 0)
	for _, meta := range podMetas {
		pod := meta.Pod
		uid := string(pod.UID) // types.UID
		podKey := util.GetPodKey(pod)
		if filtered, msg := p.FilterPod(meta); filtered {
			klog.V(5).Infof("skip collect pod %s, reason: %s", podKey, msg)
			continue
		}

		collectTime := time.Now()
		podCgroupDir := meta.CgroupDir
		memStat, err := p.cgroupReader.ReadMemoryStat(podCgroupDir)
		if err != nil {
			// higher verbosity for probably non-running pods
			if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
				klog.V(6).Infof("failed to collect non-running pod page cache for %s, page cache err: %s",
					podKey, err)
			} else {
				klog.Warningf("failed to collect pod page cache for %s, page cache err: %s", podKey, err)
			}
			continue
		}

		memUsageWithPageCacheValue := memStat.UsageWithPageCache()
		memUsageWithPageCacheMetric, err := metriccache.PodMemoryUsageWithPageCacheMetric.GenerateSample(
			metriccache.MetricPropertiesFunc.Pod(uid), collectTime, float64(memUsageWithPageCacheValue))
		if err != nil {
			klog.V(4).Infof("failed to generate pod mem with page cache metrics for pod %s , err %v", podKey, err)
			return
		}
		metrics = append(metrics, memUsageWithPageCacheMetric)

		klog.V(6).Infof("collect pod %s, uid %s finished, metric %+v", podKey, pod.UID, metrics)

		count++
		containerMetrics := p.collectContainerPageCache(meta)
		metrics = append(metrics, containerMetrics...)
	}

	appender := p.appendableDB.Appender()
	if err := appender.Append(metrics); err != nil {
		klog.Warningf("Append pod metrics error: %v", err)
		return
	}

	if err := appender.Commit(); err != nil {
		klog.Warningf("Commit pod metrics failed, error: %v", err)
		return
	}

	klog.V(4).Infof("collectPodPageCache finished, pod num %d, collected %d", len(podMetas), count)
}

func (p *pageCacheCollector) collectContainerPageCache(meta *statesinformer.PodMeta) []metriccache.MetricSample {
	klog.V(6).Infof("start collect containers pagecache")
	pod := meta.Pod
	count := 0
	containerMetrics := make([]metriccache.MetricSample, 0)
	for i := range pod.Status.ContainerStatuses {
		containerStat := &pod.Status.ContainerStatuses[i]
		containerKey := fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, containerStat.Name)
		collectTime := time.Now()
		if len(containerStat.ContainerID) == 0 {
			klog.V(5).Infof("container %s id is empty, maybe not ready, skip this round", containerKey)
			continue
		}

		containerCgroupDir, err := koordletutil.GetContainerCgroupParentDir(meta.CgroupDir, containerStat)
		if err != nil {
			klog.V(4).Infof("failed to collect container usage for %s, cannot get container cgroup, err: %s",
				containerKey, err)
			continue
		}

		memStat, err := p.cgroupReader.ReadMemoryStat(containerCgroupDir)

		if err != nil {
			// higher verbosity for probably non-running pods
			if containerStat.State.Running == nil {
				klog.V(6).Infof("failed to collect non-running container page cache for %s, page cache err: %s",
					containerKey, err)
			} else {
				klog.V(4).Infof("failed to collect container page cache for %s, page cache err: %s",
					containerKey, err)
			}
			continue
		}

		memUsagWithPageCacheValue := memStat.UsageWithPageCache()
		memUsageWithPageCacheMetric, err := metriccache.ContainerMemoryUsageWithPageCacheMetric.GenerateSample(
			metriccache.MetricPropertiesFunc.Container(containerStat.ContainerID), collectTime, float64(memUsagWithPageCacheValue))
		if err != nil {
			klog.Warningf("generate container %s metrics failed, page cache:%v", containerKey, err)
			continue
		}

		containerMetrics = append(containerMetrics, memUsageWithPageCacheMetric)

		klog.V(6).Infof("collect container %s, id %s finished, metric %+v", containerKey, pod.UID, containerMetrics)
		count++
	}
	klog.V(5).Infof("collectContainerPageCache for pod %s/%s finished, container num %d, collected %d",
		pod.Namespace, pod.Name, len(pod.Status.ContainerStatuses), count)
	return containerMetrics
}

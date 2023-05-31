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

package podthrottled

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	CollectorName = "PodThrottledCollector"
)

// TODO more ut is needed for this plugin
type podThrottledCollector struct {
	collectInterval time.Duration
	started         *atomic.Bool
	appendableDB    metriccache.Appendable
	statesInformer  statesinformer.StatesInformer
	cgroupReader    resourceexecutor.CgroupReader
	podFilter       framework.PodFilter

	lastPodCPUThrottled       *gocache.Cache
	lastContainerCPUThrottled *gocache.Cache
}

func New(opt *framework.Options) framework.Collector {
	collectInterval := opt.Config.CollectResUsedInterval
	podFilter := framework.DefaultPodFilter
	if filter, ok := opt.PodFilters[CollectorName]; ok {
		podFilter = filter
	}
	return &podThrottledCollector{
		collectInterval:           collectInterval,
		started:                   atomic.NewBool(false),
		appendableDB:              opt.MetricCache,
		statesInformer:            opt.StatesInformer,
		cgroupReader:              opt.CgroupReader,
		podFilter:                 podFilter,
		lastPodCPUThrottled:       gocache.New(collectInterval*framework.ContextExpiredRatio, framework.CleanupInterval),
		lastContainerCPUThrottled: gocache.New(collectInterval*framework.ContextExpiredRatio, framework.CleanupInterval),
	}
}

var _ framework.PodCollector = &podThrottledCollector{}

func (c *podThrottledCollector) Enabled() bool {
	return true
}

func (c *podThrottledCollector) Setup(ctx *framework.Context) {}

func (c *podThrottledCollector) Run(stopCh <-chan struct{}) {
	if !cache.WaitForCacheSync(stopCh, c.statesInformer.HasSynced) {
		// Koordlet exit because of statesInformer sync failed.
		klog.Fatalf("timed out waiting for states informer caches to sync")
	}
	go wait.Until(c.collectPodThrottledInfo, c.collectInterval, stopCh)
}

func (c *podThrottledCollector) Started() bool {
	return c.started.Load()
}

func (c *podThrottledCollector) FilterPod(meta *statesinformer.PodMeta) (bool, string) {
	return c.podFilter.FilterPod(meta)
}

func (c *podThrottledCollector) collectPodThrottledInfo() {
	klog.V(6).Info("start collectPodThrottledInfo")
	podMetas := c.statesInformer.GetAllPods()
	podAndContainerMetrics := make([]metriccache.MetricSample, 0)
	for _, meta := range podMetas {
		pod := meta.Pod
		uid := string(pod.UID) // types.UID
		if filtered, msg := c.FilterPod(meta); filtered {
			klog.V(5).Infof("skip collect pod %s/%s, reason: %s", pod.Namespace, pod.Name, msg)
			continue
		}

		collectTime := time.Now()
		podCgroupDir := meta.CgroupDir
		currentCPUStat, err := c.cgroupReader.ReadCPUStat(podCgroupDir)
		if err != nil || currentCPUStat == nil {
			if pod.Status.Phase == corev1.PodRunning {
				// print running pod collection error
				klog.V(4).Infof("collect pod %s/%s, uid %v cpu throttled failed, err %v, metric %v",
					pod.Namespace, pod.Name, uid, err, currentCPUStat)
			}
			continue
		}
		lastCPUThrottledValue, ok := c.lastPodCPUThrottled.Get(uid)
		c.lastPodCPUThrottled.Set(uid, currentCPUStat, gocache.DefaultExpiration)
		klog.V(6).Infof("last pod cpu stat size in pod throttled collector cache %v", c.lastPodCPUThrottled.ItemCount())
		if !ok {
			klog.V(6).Infof("collect pod %s/%s, uid %s cpu throttled first point",
				meta.Pod.Namespace, meta.Pod.Name, meta.Pod.UID)
			continue
		}
		lastCPUThrottled := lastCPUThrottledValue.(*system.CPUStatRaw)
		cpuThrottledRatio := system.CalcCPUThrottledRatio(currentCPUStat, lastCPUThrottled)

		klog.V(6).Infof("collect pod %s/%s, uid %s throttled finished, metric %v",
			meta.Pod.Namespace, meta.Pod.Name, meta.Pod.UID, cpuThrottledRatio)

		podMetric, err := metriccache.PodCPUThrottledMetric.GenerateSample(
			metriccache.MetricPropertiesFunc.Pod(string(meta.Pod.UID)),
			collectTime, cpuThrottledRatio)
		if err != nil {
			klog.Warningf("generate pod %v throttled metrics failed, err %v", util.GetPodKey(meta.Pod), err)
		} else {
			podAndContainerMetrics = append(podAndContainerMetrics, podMetric)
		}
	} // end for podMeta

	for _, meta := range podMetas {
		metrics := c.collectContainerThrottledInfo(meta)
		podAndContainerMetrics = append(podAndContainerMetrics, metrics...)
	}

	appender := c.appendableDB.Appender()
	if err := appender.Append(podAndContainerMetrics); err != nil {
		klog.Warningf("append pods throttled metrics failed, reason: %v", err)
		return
	}
	if err := appender.Commit(); err != nil {
		klog.Warningf("append pods throttled metrics failed, reason: %v", err)
		return
	}
	c.started.Store(true)
	klog.V(5).Infof("collectPodThrottledInfo finished, pod num %d", len(podMetas))
}

func (c *podThrottledCollector) collectContainerThrottledInfo(podMeta *statesinformer.PodMeta) []metriccache.MetricSample {
	pod := podMeta.Pod
	containersMetric := make([]metriccache.MetricSample, 0, len(pod.Status.ContainerStatuses))
	for i := range pod.Status.ContainerStatuses {
		collectTime := time.Now()
		containerStat := &pod.Status.ContainerStatuses[i]
		if len(containerStat.ContainerID) == 0 {
			klog.V(5).Infof("container %s/%s/%s id is empty, maybe not ready, skip this round",
				pod.Namespace, pod.Name, containerStat.Name)
			continue
		}

		containerCgroupDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, containerStat)
		if err != nil {
			klog.V(4).Infof("collect container %s/%s/%s cpu throttled failed, cannot get container cgroup, err: %s",
				pod.Namespace, pod.Name, containerStat.Name, err)
			continue
		}

		currentCPUStat, err := c.cgroupReader.ReadCPUStat(containerCgroupDir)
		if err != nil {
			// higher verbosity for probably non-running pods
			if containerStat.State.Running == nil {
				klog.V(6).Infof("collect non-running container %s/%s/%s cpu throttled failed, err: %s",
					pod.Namespace, pod.Name, containerStat.Name, err)
			} else {
				klog.V(4).Infof("collect container %s/%s/%s cpu throttled failed, err: %s",
					pod.Namespace, pod.Name, containerStat.Name, err)
			}
			continue
		}
		lastCPUThrottledValue, ok := c.lastContainerCPUThrottled.Get(containerStat.ContainerID)
		c.lastContainerCPUThrottled.Set(containerStat.ContainerID, currentCPUStat, gocache.DefaultExpiration)
		klog.V(6).Infof("last container cpu stat size in pod throttled collector cache %v", c.lastContainerCPUThrottled.ItemCount())
		if !ok {
			klog.V(6).Infof("collect container %s/%s/%s cpu throttled first point",
				pod.Namespace, pod.Name, containerStat.Name)
			continue
		}
		lastCPUThrottled := lastCPUThrottledValue.(*system.CPUStatRaw)
		cpuThrottledRatio := system.CalcCPUThrottledRatio(currentCPUStat, lastCPUThrottled)

		containerMetric, err := metriccache.ContainerCPUThrottledMetric.GenerateSample(
			metriccache.MetricPropertiesFunc.Container(containerStat.ContainerID),
			collectTime, cpuThrottledRatio)
		if err != nil {
			klog.Warningf("generate container throttled metrics failed, err %v", err)
		} else {
			containersMetric = append(containersMetric, containerMetric)
		}
	} // end for container status
	return containersMetric
}

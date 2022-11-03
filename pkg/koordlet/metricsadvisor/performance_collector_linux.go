//go:build linux
// +build linux

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

package metricsadvisor

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/perf"
)

type performanceCollector struct {
	statesInformer    statesinformer.StatesInformer
	metricCache       metriccache.MetricCache
	collectTimeWindow int
}

func NewPerformanceCollector(statesInformer statesinformer.StatesInformer, metricCache metriccache.MetricCache, collectTimeWindow int) *performanceCollector {
	return &performanceCollector{
		statesInformer:    statesInformer,
		metricCache:       metricCache,
		collectTimeWindow: collectTimeWindow,
	}
}

func (c *performanceCollector) collectContainerCPI() {
	klog.V(6).Infof("start collectContainerCPI")
	timeWindow := time.Now()
	containerStatusesMap := map[*corev1.ContainerStatus]*statesinformer.PodMeta{}
	podMetas := c.statesInformer.GetAllPods()
	for _, meta := range podMetas {
		pod := meta.Pod
		for i := range pod.Status.ContainerStatuses {
			containerStat := &pod.Status.ContainerStatuses[i]
			containerStatusesMap[containerStat] = meta
		}
	}
	collectors := sync.Map{}
	var wg sync.WaitGroup
	wg.Add(len(containerStatusesMap))
	nodeCpuInfo, err := c.metricCache.GetNodeCPUInfo(&metriccache.QueryParam{})
	if err != nil {
		klog.Errorf("failed to get node cpu info : %v", err)
		return
	}
	cpuNumber := nodeCpuInfo.TotalInfo.NumberCPUs
	for containerStatus, parentPod := range containerStatusesMap {
		go func(status *corev1.ContainerStatus, parent string) {
			defer wg.Done()
			collectorOnSingleContainer, err := c.getAndStartCollectorOnSingleContainer(parent, status, cpuNumber)
			if err != nil {
				return
			}
			collectors.Store(status, collectorOnSingleContainer)
		}(containerStatus, parentPod.CgroupDir)
	}
	wg.Wait()

	time.Sleep(time.Duration(c.collectTimeWindow) * time.Second)

	var wg1 sync.WaitGroup
	wg1.Add(len(containerStatusesMap))
	for containerStatus, podMeta := range containerStatusesMap {
		podUid := podMeta.Pod.UID
		go func(status *corev1.ContainerStatus, podUid string) {
			defer wg1.Done()
			oneCollector, ok := collectors.Load(status)
			if !ok {
				return
			}
			perfCollector, ok := oneCollector.(*perf.PerfCollector)
			if !ok {
				klog.Errorf("PerfCollector type convert failed")
				return
			}
			c.profilePerfOnSingleContainer(status, perfCollector, podUid)

			cleanErr := perfCollector.CleanUp()
			if cleanErr != nil {
				klog.Errorf("PerfCollector cleanup err : %v", cleanErr)
			}
		}(containerStatus, string(podUid))
	}
	wg1.Wait()
	klog.V(5).Infof("collectContainerCPI for time window %s finished at %s, container num %d",
		timeWindow, time.Now(), len(containerStatusesMap))
}

func (c *performanceCollector) getAndStartCollectorOnSingleContainer(podParentCgroupDir string, containerStatus *corev1.ContainerStatus, number int32) (*perf.PerfCollector, error) {
	perfCollector, err := util.GetContainerPerfCollector(podParentCgroupDir, containerStatus, number)
	if err != nil {
		klog.Errorf("get and start container %s collector err: %v", containerStatus.Name, err)
		return nil, err
	}
	return perfCollector, nil
}

func (c *performanceCollector) profilePerfOnSingleContainer(containerStatus *corev1.ContainerStatus, collector *perf.PerfCollector, podUid string) {
	collectTime := time.Now()
	cycles, instructions, err := util.GetContainerCyclesAndInstructions(collector)
	if err != nil {
		klog.Errorf("collect container %s cpi err: %v", containerStatus.Name, err)
		return
	}
	containerCpiMetric := &metriccache.ContainerInterferenceMetric{
		MetricName:  metriccache.MetricNameContainerCPI,
		PodUID:      podUid,
		ContainerID: containerStatus.ContainerID,
		MetricValue: &metriccache.CPIMetric{
			Cycles:       cycles,
			Instructions: instructions,
		},
	}
	err = c.metricCache.InsertContainerInterferenceMetrics(collectTime, containerCpiMetric)
	if err != nil {
		klog.Errorf("insert container cpi metrics failed, err %v", err)
	}
}

// todo: try to make this collector linux specific, e.g., by build tag linux

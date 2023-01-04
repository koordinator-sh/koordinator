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

package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	ContainerScaledCFSBurstUS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "container_scaled_cfs_burst_us",
		Help:      "The maximum accumulated run-time(in microseconds) in container-level set by koordlet",
	}, []string{NodeKey, PodNamespace, PodName, ContainerID, ContainerName})

	ContainerScaledCFSQuotaUS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "container_scaled_cfs_quota_us",
		Help:      "Run-time replenished within a period (in microseconds) in container-level set by koordlet",
	}, []string{NodeKey, PodNamespace, PodName, ContainerID, ContainerName})

	PodCPUThrottledRatio = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "pod_cpu_throttled_ratio",
		Help:      "The ratio of CPU throttled in pod-level",
	}, []string{NodeKey, PodNamespace, PodName})

	ContainerCPUThrottledRatio = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "container_cpu_throttled_ratio",
		Help:      "The ratio of CPU throttled in container-level",
	}, []string{NodeKey, PodNamespace, PodName, ContainerID, ContainerName})

	CPUBurstCollector = []prometheus.Collector{
		ContainerScaledCFSBurstUS,
		ContainerScaledCFSQuotaUS,
		PodCPUThrottledRatio,
		ContainerCPUThrottledRatio,
	}
)

func RecordContainerScaledCFSBurstUS(podNS, podName, containerID, containerName string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PodNamespace] = podNS
	labels[PodName] = podName
	labels[ContainerID] = containerID
	labels[ContainerName] = containerName
	ContainerScaledCFSBurstUS.With(labels).Set(value)
}

func RecordContainerScaledCFSQuotaUS(podNS, podName, containerID, containerName string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PodNamespace] = podNS
	labels[PodName] = podName
	labels[ContainerID] = containerID
	labels[ContainerName] = containerName
	ContainerScaledCFSQuotaUS.With(labels).Set(value)
}

func RecordPodCPUThrottledRatio(podNS, podName string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PodNamespace] = podNS
	labels[PodName] = podName
	PodCPUThrottledRatio.With(labels).Set(value)
}

func RecordContainerCPUThrottledRatio(podNS, podName, containerID, containerName string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PodNamespace] = podNS
	labels[PodName] = podName
	labels[ContainerID] = containerID
	labels[ContainerName] = containerName
	ContainerCPUThrottledRatio.With(labels).Set(value)
}

func ResetCPUBurstCollector() {
	ContainerScaledCFSBurstUS.Reset()
	ContainerScaledCFSQuotaUS.Reset()
}

func ResetPodCPUThrottledRatio() {
	PodCPUThrottledRatio.Reset()
}

func ResetContainerCPUThrottledRatio() {
	ContainerCPUThrottledRatio.Reset()
}

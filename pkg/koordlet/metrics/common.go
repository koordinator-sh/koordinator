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

import (
	"github.com/prometheus/client_golang/prometheus"
	k8smetrics "k8s.io/component-base/metrics"

	"github.com/koordinator-sh/koordinator/pkg/util/metrics"
)

var (
	KoordletStartTime = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "start_time",
		Help:           "the start time of koordlet",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey})

	CollectNodeCPUInfoStatus = k8smetrics.NewCounterVec(&k8smetrics.CounterOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "collect_node_cpu_info_status",
		Help:           "the count of CollectNodeCPUInfo status",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey, StatusKey})

	CollectNodeNUMAInfoStatus = k8smetrics.NewCounterVec(&k8smetrics.CounterOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "collect_node_numa_info_status",
		Help:           "the count of CollectNodeNUMAInfo status",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey, StatusKey})

	CollectNodeLocalStorageInfoStatus = k8smetrics.NewCounterVec(&k8smetrics.CounterOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "collect_node_local_storage_info_status",
		Help:           "the count of CollectNodeLocalStorageInfo status",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey, StatusKey})

	PodEviction = k8smetrics.NewCounterVec(&k8smetrics.CounterOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "pod_eviction",
		Help:           "Number of eviction launched by koordlet",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey, EvictionReasonKey})

	PodEvictionDetail = metrics.NewGCCounterVec("pod_eviction_detail", prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "pod_eviction_detail",
		Help:      "evict detail launched by koordlet",
	}, []string{NodeKey, PodNamespace, PodName, EvictionReasonKey}))

	NodeUsedCPU = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "node_used_cpu_cores",
		Help:           "Number of cpu cores used by node in realtime",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey})

	NodeUsedMemory = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "node_used_memory_bytes",
		Help:           "Memory used by node in realtime",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey})

	// CommonRegisterableCollectors holds the metrics defined with k8s.io/component-base/metrics, registered
	// through internalKubeMustRegister so the stability framework applies to them.
	CommonRegisterableCollectors = []k8smetrics.Registerable{
		KoordletStartTime,
		CollectNodeCPUInfoStatus,
		CollectNodeNUMAInfoStatus,
		CollectNodeLocalStorageInfoStatus,
		PodEviction,
		NodeUsedCPU,
		NodeUsedMemory,
	}

	// CommonCollectors keeps the collectors that cannot implement k8smetrics.Registerable: the pkg/util/metrics
	// GC wrappers expose the wrapped prometheus.Collector, and that wrapper captures the underlying MetricVec at
	// construction, which a component-base vec does not have until registration creates it.
	CommonCollectors = []prometheus.Collector{
		PodEvictionDetail.GetCounterVec(),
	}
)

func RecordKoordletStartTime(nodeName string, value float64) {
	labels := map[string]string{}
	// KoordletStartTime is usually recorded before the node Registering
	labels[NodeKey] = nodeName
	KoordletStartTime.With(labels).Set(value)
}

func RecordCollectNodeCPUInfoStatus(err error) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[StatusKey] = StatusSucceed
	if err != nil {
		labels[StatusKey] = StatusFailed
	}
	CollectNodeCPUInfoStatus.With(labels).Inc()
}

func RecordCollectNodeNUMAInfoStatus(err error) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[StatusKey] = StatusSucceed
	if err != nil {
		labels[StatusKey] = StatusFailed
	}
	CollectNodeNUMAInfoStatus.With(labels).Inc()
}

func RecordCollectNodeLocalStorageInfoStatus(err error) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[StatusKey] = StatusSucceed
	if err != nil {
		labels[StatusKey] = StatusFailed
	}
	CollectNodeLocalStorageInfoStatus.With(labels).Inc()
}

func RecordPodEviction(namespace, podName, reasonType string) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[EvictionReasonKey] = reasonType
	PodEviction.With(labels).Inc()

	detailLabels := labelsClone(labels)
	detailLabels[PodNamespace] = namespace
	detailLabels[PodName] = podName
	PodEvictionDetail.WithInc(detailLabels)
}

func RecordNodeUsedCPU(value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	NodeUsedCPU.With(labels).Set(value)
}

func RecordNodeUsedMemory(value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	NodeUsedMemory.With(labels).Set(value)
}

func labelsClone(labels prometheus.Labels) prometheus.Labels {
	copyLabels := prometheus.Labels{}
	for key, value := range labels {
		copyLabels[key] = value
	}
	return copyLabels
}

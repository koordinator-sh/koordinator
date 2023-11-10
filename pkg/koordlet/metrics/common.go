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
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/koordinator-sh/koordinator/pkg/util/metrics"
)

var (
	KoordletStartTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "start_time",
		Help:      "the start time of koordlet",
	}, []string{NodeKey})

	CollectNodeCPUInfoStatus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "collect_node_cpu_info_status",
		Help:      "the count of CollectNodeCPUInfo status",
	}, []string{NodeKey, StatusKey})

	CollectNodeNUMAInfoStatus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "collect_node_numa_info_status",
		Help:      "the count of CollectNodeNUMAInfo status",
	}, []string{NodeKey, StatusKey})

	CollectNodeLocalStorageInfoStatus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "collect_node_local_storage_info_status",
		Help:      "the count of CollectNodeLocalStorageInfo status",
	}, []string{NodeKey, StatusKey})

	PodEviction = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "pod_eviction",
		Help:      "Number of eviction launched by koordlet",
	}, []string{NodeKey, EvictionReasonKey})

	PodEvictionDetail = metrics.NewGCCounterVec("pod_eviction_detail", prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "pod_eviction_detail",
		Help:      "evict detail launched by koordlet",
	}, []string{NodeKey, PodNamespace, PodName, EvictionReasonKey}))

	NodeUsedCPU = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "node_used_cpu_cores",
		Help:      "Number of cpu cores used by node in realtime",
	}, []string{NodeKey})

	ReportNodeMetricStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "report_node_metric_status",
		Help:      "the status of ReportNodeMetric status",
	}, []string{NodeKey, StatusKey})

	ModuleHealthyStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "module_healthy_status",
		Help:      "the status of module healthy status",
	}, []string{NodeKey, ModuleKey, ModulePluginKey, StatusKey})

	CollectPromMetricsStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "collect_prom_metrics_status",
		Help:      "the status of CollectPromMetrics status",
	}, []string{NodeKey, PromScraperKey, StatusKey})

	HttpClientQueryStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "http_client_query_status",
		Help:      "the status of http client query status",
	}, []string{NodeKey, HttpURLKey, HttpResponseCodeKey})

	HttpClientQueryLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: KoordletSubsystem,
		Name:      "http_client_query_latency",
		Help:      "the latency of invoking http client query (in milli-second)",
	}, []string{NodeKey, HttpURLKey})

	CommonCollectors = []prometheus.Collector{
		KoordletStartTime,
		CollectNodeCPUInfoStatus,
		CollectNodeNUMAInfoStatus,
		CollectNodeLocalStorageInfoStatus,
		PodEviction,
		PodEvictionDetail.GetCounterVec(),
		NodeUsedCPU,
		ReportNodeMetricStatus,
		ModuleHealthyStatus,
		CollectPromMetricsStatus,
		HttpClientQueryStatus,
		HttpClientQueryLatency,
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

func RecordReportNodeMetricStatus(err error) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[StatusKey] = StatusSucceed
	if err != nil {
		labels[StatusKey] = StatusFailed
	}
	ReportNodeMetricStatus.With(labels).Inc()
}

func RecordModuleHealthyStatus(module string, pluginName string, isHealthy bool) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[ModuleKey] = module
	labels[ModulePluginKey] = pluginName
	labels[StatusKey] = StatusSucceed
	if !isHealthy {
		labels[StatusKey] = StatusFailed
	}
	ModuleHealthyStatus.With(labels).Inc()
}

func RecordCollectPromMetricsStatus(scraper string, isHealthy bool) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PromScraperKey] = scraper
	labels[StatusKey] = StatusSucceed
	if !isHealthy {
		labels[StatusKey] = StatusFailed
	}
	CollectPromMetricsStatus.With(labels).Inc()
}

func RecordHttpClientQueryStatus(url string, code int) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[HttpURLKey] = url
	labels[HttpResponseCodeKey] = strconv.Itoa(code)
	HttpClientQueryStatus.With(labels).Inc()
}

func RecordHttpClientQueryLatency(url string, latencyMilliSeconds float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[HttpURLKey] = url
	HttpClientQueryLatency.With(labels).Observe(latencyMilliSeconds)
}

func labelsClone(labels prometheus.Labels) prometheus.Labels {
	copyLabels := prometheus.Labels{}
	for key, value := range labels {
		copyLabels[key] = value
	}
	return copyLabels
}

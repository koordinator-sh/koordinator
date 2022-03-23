package metrics

import "github.com/prometheus/client_golang/prometheus"

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

	PodEviction = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "pod_eviction",
		Help:      "Number of eviction launched by koordlet",
	}, []string{NodeKey, EvictionReasonKey})

	BESuppressCPU = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "be_suppress_cpu_cores",
		Help:      "Number of cores suppress by koordlet",
	}, []string{NodeKey, BESuppressTypeKey})

	CommonCollectors = []prometheus.Collector{
		KoordletStartTime,
		CollectNodeCPUInfoStatus,
		PodEviction,
		BESuppressCPU,
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

func RecordPodEviction(reasonType string) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[EvictionReasonKey] = reasonType
	PodEviction.With(labels).Inc()
}

func RecordBESuppressCores(suppressType string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[BESuppressTypeKey] = suppressType
	BESuppressCPU.With(labels).Set(value)
}

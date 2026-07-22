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
)

const (
	// NUMANodeKey is the label key of the NUMA node ID.
	NUMANodeKey = "numa_node_id"
)

// The metrics below record the main info reported in the NodeMetric status, which are enabled by the
// koordlet feature-gate NodeMetricPromMetrics. On every NodeMetric sync, they are maintained by
// resetting all the series first and then recording the full set of the latest reported values, so
// the stale series (e.g. the NUMA usage is degraded) are cleaned up.
// NOTE: the predicted peak is NOT recorded here since the prediction module already records it as
// node_predicted_resource_peak.
var (
	NodeMetricNodeUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "node_metric_node_usage",
		Help:      "the node usage reported in the NodeMetric",
	}, []string{NodeKey, ResourceKey, UnitKey})

	NodeMetricSystemUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "node_metric_system_usage",
		Help:      "the system usage reported in the NodeMetric",
	}, []string{NodeKey, ResourceKey, UnitKey})

	NodeMetricNUMANodeUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "node_metric_numa_node_usage",
		Help:      "the per-NUMA node usage reported in the NodeMetric",
	}, []string{NodeKey, NUMANodeKey, ResourceKey, UnitKey})

	NodeMetricNUMASystemUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "node_metric_numa_system_usage",
		Help:      "the per-NUMA system usage reported in the NodeMetric",
	}, []string{NodeKey, NUMANodeKey, ResourceKey, UnitKey})

	NodeMetricCollectors = []prometheus.Collector{
		NodeMetricNodeUsage,
		NodeMetricSystemUsage,
		NodeMetricNUMANodeUsage,
		NodeMetricNUMASystemUsage,
	}
)

// ResetNodeMetricUsages resets all the NodeMetric usage metrics. It should be called before recording
// the full set of the latest values on each NodeMetric sync.
func ResetNodeMetricUsages() {
	NodeMetricNodeUsage.Reset()
	NodeMetricSystemUsage.Reset()
	NodeMetricNUMANodeUsage.Reset()
	NodeMetricNUMASystemUsage.Reset()
}

func recordNodeMetricUsage(vec *prometheus.GaugeVec, resourceName, unit string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[ResourceKey] = resourceName
	labels[UnitKey] = unit
	vec.With(labels).Set(value)
}

// RecordNodeMetricNodeUsage records the node usage reported in the NodeMetric.
func RecordNodeMetricNodeUsage(resourceName, unit string, value float64) {
	recordNodeMetricUsage(NodeMetricNodeUsage, resourceName, unit, value)
}

// RecordNodeMetricSystemUsage records the system usage reported in the NodeMetric.
func RecordNodeMetricSystemUsage(resourceName, unit string, value float64) {
	recordNodeMetricUsage(NodeMetricSystemUsage, resourceName, unit, value)
}

func recordNodeMetricNUMAUsage(vec *prometheus.GaugeVec, numaNodeID int32, resourceName, unit string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[NUMANodeKey] = strconv.FormatInt(int64(numaNodeID), 10)
	labels[ResourceKey] = resourceName
	labels[UnitKey] = unit
	vec.With(labels).Set(value)
}

// RecordNodeMetricNUMANodeUsage records the per-NUMA node usage reported in the NodeMetric.
func RecordNodeMetricNUMANodeUsage(numaNodeID int32, resourceName, unit string, value float64) {
	recordNodeMetricNUMAUsage(NodeMetricNUMANodeUsage, numaNodeID, resourceName, unit, value)
}

// RecordNodeMetricNUMASystemUsage records the per-NUMA system usage reported in the NodeMetric.
func RecordNodeMetricNUMASystemUsage(numaNodeID int32, resourceName, unit string, value float64) {
	recordNodeMetricNUMAUsage(NodeMetricNUMASystemUsage, numaNodeID, resourceName, unit, value)
}

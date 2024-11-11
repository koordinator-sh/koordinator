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

	"github.com/koordinator-sh/koordinator/pkg/util/metrics/koordmanager"
)

func init() {
	koordmanager.InternalMustRegister(CommonCollectors...)
}

var (
	NodeResourceReconcileCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: SLOControllerSubsystem,
		Name:      "node_resource_reconcile_count",
		Help:      "the status count of reconciling node resource",
	}, []string{StatusKey, ReasonKey})

	NodeMetricReconcileCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: SLOControllerSubsystem,
		Name:      "nodemetric_reconcile_count",
		Help:      "the status count of reconciling NodeMetric",
	}, []string{StatusKey, ReasonKey})

	NodeMetricSpecParseCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: SLOControllerSubsystem,
		Name:      "nodemetric_spec_parse_count",
		Help:      "the count of parsing NodeMetric spec",
	}, []string{StatusKey, ReasonKey})

	NodeSLOReconcileCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: SLOControllerSubsystem,
		Name:      "nodeslo_reconcile_count",
		Help:      "the status count of reconciling NodeSLO",
	}, []string{StatusKey, ReasonKey})

	NodeSLOSpecParseCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: SLOControllerSubsystem,
		Name:      "nodeslo_spec_parse_count",
		Help:      "the count of parsing NodeSLO spec",
	}, []string{StatusKey, ReasonKey})

	CommonCollectors = []prometheus.Collector{
		NodeResourceReconcileCount,
		NodeMetricReconcileCount,
		NodeMetricSpecParseCount,
		NodeSLOReconcileCount,
		NodeSLOSpecParseCount,
	}
)

func RecordNodeResourceReconcileCount(isSucceeded bool, reason string) {
	recordNodeCountMetric(NodeResourceReconcileCount, isSucceeded, reason)
}

func RecordNodeMetricReconcileCount(isSucceeded bool, reason string) {
	recordNodeCountMetric(NodeMetricReconcileCount, isSucceeded, reason)
}

func RecordNodeMetricSpecParseCount(isSucceeded bool, reason string) {
	recordNodeCountMetric(NodeMetricSpecParseCount, isSucceeded, reason)
}

func RecordNodeSLOReconcileCount(isSucceeded bool, reason string) {
	recordNodeCountMetric(NodeSLOReconcileCount, isSucceeded, reason)
}

func RecordNodeSLOSpecParseCount(isSucceeded bool, reason string) {
	recordNodeCountMetric(NodeSLOSpecParseCount, isSucceeded, reason)
}

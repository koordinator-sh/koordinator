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
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/util/metrics"
	"github.com/koordinator-sh/koordinator/pkg/util/metrics/koordmanager"
)

func init() {
	koordmanager.InternalMustRegister(NodeResourceCollectors...)
}

var (
	NodeResourceRunPluginStatus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: SLOControllerSubsystem,
		Name:      "node_resource_run_plugin_status",
		Help:      "the status count of running node resource plugins",
	}, []string{PluginKey, StatusKey, ReasonKey})

	NodeExtendedResourceAllocatableInternal = metrics.NewGCGaugeVec("node_extended_resource_allocatable_internal", prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: SLOControllerSubsystem,
		Name:      "node_extended_resource_allocatable_internal",
		Help:      "the internal allocatable of node extended resources which may not be updated at the node status",
	}, []string{NodeKey, ResourceKey, UnitKey}))

	NodeResourceCollectors = []prometheus.Collector{
		NodeResourceRunPluginStatus,
		NodeExtendedResourceAllocatableInternal.GetGaugeVec(),
	}
)

func RecordNodeResourceRunPluginStatus(pluginName string, isSucceeded bool, reason string) {
	labels := map[string]string{}
	labels[PluginKey] = pluginName
	NodeResourceRunPluginStatus.With(withNodeStatusLabels(labels, isSucceeded, reason)).Inc()
}

func RecordNodeExtendedResourceAllocatableInternal(node *corev1.Node, resourceName string, unit string, value float64) {
	labels := genNodeLabels(node)
	labels[ResourceKey] = resourceName
	labels[UnitKey] = unit
	NodeExtendedResourceAllocatableInternal.WithSet(labels, value)
}

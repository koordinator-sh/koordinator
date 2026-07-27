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

	"github.com/koordinator-sh/koordinator/pkg/util/metrics"
)

var (
	ContainerOOMScoreAdj = metrics.NewGCGaugeVec("container_oom_score_adj", prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "container_oom_score_adj",
		Help:      "the oom_score_adj value set for the container",
	}, []string{NodeKey, PodName, PodNamespace, PodUID, ContainerName}))

	OOMScoreAdjCollector = []prometheus.Collector{
		ContainerOOMScoreAdj.GetGaugeVec(),
	}
)

// RecordContainerOOMScoreAdj records the oom_score_adj value for a container.
func RecordContainerOOMScoreAdj(namespace, podName, podUID, containerName string, val int64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PodNamespace] = namespace
	labels[PodName] = podName
	labels[PodUID] = podUID
	labels[ContainerName] = containerName
	ContainerOOMScoreAdj.WithSet(labels, float64(val))
}

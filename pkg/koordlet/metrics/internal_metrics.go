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
	"k8s.io/component-base/metrics/legacyregistry"
)

const (
	InternalHTTPPath = "/internal-metrics"
)

var (
	// InternalRegistry only register metrics of koordlet itself for performance and functional monitor
	// TODO consider using k8s.io/component-base/metrics to replace github.com/prometheus/client_golang/prometheus
	InternalRegistry = legacyregistry.DefaultGatherer
)

func internalMustRegister(metrics ...prometheus.Collector) {
	legacyregistry.RawMustRegister(metrics...)
}

func init() {
	internalMustRegister(CommonCollectors...)
	internalMustRegister(CPUSuppressCollector...)
	internalMustRegister(CPUBurstCollector...)
	internalMustRegister(PredictionCollectors...)
	internalMustRegister(CoreSchedCollector...)
	internalMustRegister(ResourceExecutorCollector...)
	internalMustRegister(KubeletStubCollector...)
	internalMustRegister(RuntimeHookCollectors...)
	internalMustRegister(HostApplicationCollectors...)
}

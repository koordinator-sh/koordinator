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

package koordmanager

import (
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/component-base/metrics/legacyregistry"
)

const (
	// DefaultHTTPPath use /all-metrics since /metrics is occupied by controller manager default registry
	DefaultHTTPPath  = "/all-metrics"
	ExternalHTTPPath = "/external-metrics"
	InternalHTTPPath = "/internal-metrics"
)

var (
	// ExternalRegistry	register metrics for users
	ExternalRegistry = prometheus.NewRegistry()

	// InternalRegistry only register metrics of koord-manager itself for performance and functional monitor
	InternalRegistry = legacyregistry.DefaultGatherer
)

func ExternalMustRegister(cs ...prometheus.Collector) {
	ExternalRegistry.MustRegister(cs...)
}

func InternalMustRegister(cs ...prometheus.Collector) {
	legacyregistry.RawMustRegister(cs...)
}

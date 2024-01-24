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

const (
	ExternalHTTPPath = "/external-metrics"
)

var (
	// ExternalRegistry	register metrics for users such as PMU or extended resources settings
	ExternalRegistry = prometheus.NewRegistry()
)

func ExternalMustRegister(metrics ...prometheus.Collector) {
	ExternalRegistry.MustRegister(metrics...)
}

func init() {
	ExternalMustRegister(ResourceSummaryCollectors...)
	ExternalMustRegister(CPICollectors...)
	ExternalMustRegister(PSICollectors...)
}

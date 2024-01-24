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
	dto "github.com/prometheus/client_model/go"
)

var _ prometheus.Gatherer = &mergedGather{}

type mergedGather struct {
	gathers []prometheus.Gatherer
}

// MergedGatherFunc returns a Gatherer that merges the results of multiple Gatherers
func MergedGatherFunc(g ...prometheus.Gatherer) prometheus.Gatherer {
	return &mergedGather{gathers: g}
}

func (m *mergedGather) Gather() ([]*dto.MetricFamily, error) {
	result := make([]*dto.MetricFamily, 0)
	for _, g := range m.gathers {
		if metrics, err := g.Gather(); err != nil {
			return result, err
		} else {
			result = append(result, metrics...)
		}
	}
	return result, nil
}

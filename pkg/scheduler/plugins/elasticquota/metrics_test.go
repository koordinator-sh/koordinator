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

package elasticquota

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/component-base/metrics"

	koordschedulermetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

var testGaugeCounter atomic.Uint64

// newRegisteredTestGaugeVec returns a GaugeVec registered with the scheduler metrics
// registry. k8s component-base GaugeVecs are lazily initialized and noop until registered.
func newRegisteredTestGaugeVec(t *testing.T) *metrics.GaugeVec {
	t.Helper()
	id := testGaugeCounter.Add(1)
	gauge := metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: "test",
			Name:      fmt.Sprintf("elastic_quota_metrics_test_%d", id),
			Help:      "test gauge for elastic quota metrics helpers",
		},
		[]string{"name", "resource", "tree", "is_parent", "parent", "field"},
	)
	koordschedulermetrics.RegisterMetrics(gauge)
	t.Cleanup(func() {
		gauge.Reset()
	})
	return gauge
}

func baseQuotaLabels() map[string]string {
	return map[string]string{
		"name":      "test-quota",
		"tree":      "tree-1",
		"is_parent": "false",
		"parent":    "root",
	}
}

func collectGauge(t *testing.T, g *metrics.GaugeVec) []*dto.Metric {
	t.Helper()
	metricsCh := make(chan prometheus.Metric, 32)
	go func() {
		g.Collect(metricsCh)
		close(metricsCh)
	}()
	var ms []prometheus.Metric
	for metric := range metricsCh {
		ms = append(ms, metric)
	}

	var dtoMetrics []*dto.Metric
	for _, m := range ms {
		dm := &dto.Metric{}
		assert.NoError(t, m.Write(dm))
		dtoMetrics = append(dtoMetrics, dm)
	}
	sort.Slice(dtoMetrics, func(i, j int) bool {
		bi, _ := json.Marshal(dtoMetrics[i])
		bj, _ := json.Marshal(dtoMetrics[j])
		return string(bi) < string(bj)
	})
	return dtoMetrics
}

func labelValue(m *dto.Metric, name string) string {
	for _, lp := range m.Label {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

func gaugeValue(m *dto.Metric) float64 {
	return m.GetGauge().GetValue()
}

func findMetricByResourceAndField(metrics []*dto.Metric, resource, field string) *dto.Metric {
	for _, m := range metrics {
		if labelValue(m, "resource") == resource && labelValue(m, "field") == field {
			return m
		}
	}
	return nil
}

func TestRecordElasticQuotaMetric(t *testing.T) {
	tests := []struct {
		name      string
		resources v1.ResourceList
		field     string
		wantCount int
		check     func(t *testing.T, collected []*dto.Metric)
	}{
		{
			name:      "CPU uses MilliValue",
			resources: MakeResourceList().CPU(2).Obj(),
			field:     "runtime",
			wantCount: 1,
			check: func(t *testing.T, collected []*dto.Metric) {
				m := findMetricByResourceAndField(collected, "cpu", "runtime")
				assert.NotNil(t, m)
				assert.Equal(t, float64(2000), gaugeValue(m))
				assert.Equal(t, "test-quota", labelValue(m, "name"))
				assert.Equal(t, "tree-1", labelValue(m, "tree"))
			},
		},
		{
			name:      "memory uses Value",
			resources: MakeResourceList().Mem(100).Obj(),
			field:     "request",
			wantCount: 1,
			check: func(t *testing.T, collected []*dto.Metric) {
				m := findMetricByResourceAndField(collected, "memory", "request")
				assert.NotNil(t, m)
				assert.Equal(t, float64(100), gaugeValue(m))
			},
		},
		{
			name:      "GPU uses Value",
			resources: MakeResourceList().GPU(1).Obj(),
			field:     "allocated",
			wantCount: 1,
			check: func(t *testing.T, collected []*dto.Metric) {
				m := findMetricByResourceAndField(collected, "nvidia.com/gpu", "allocated")
				assert.NotNil(t, m)
				assert.Equal(t, float64(1), gaugeValue(m))
			},
		},
		{
			name:      "multi-resource records one series per resource",
			resources: MakeResourceList().CPU(3).Mem(50).GPU(2).Obj(),
			field:     "runtime",
			wantCount: 3,
			check: func(t *testing.T, collected []*dto.Metric) {
				cpu := findMetricByResourceAndField(collected, "cpu", "runtime")
				assert.NotNil(t, cpu)
				assert.Equal(t, float64(3000), gaugeValue(cpu))

				mem := findMetricByResourceAndField(collected, "memory", "runtime")
				assert.NotNil(t, mem)
				assert.Equal(t, float64(50), gaugeValue(mem))

				gpu := findMetricByResourceAndField(collected, "nvidia.com/gpu", "runtime")
				assert.NotNil(t, gpu)
				assert.Equal(t, float64(2), gaugeValue(gpu))
			},
		},
		{
			name:      "empty ResourceList records zero series",
			resources: v1.ResourceList{},
			field:     "runtime",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gauge := newRegisteredTestGaugeVec(t)
			labels := baseQuotaLabels()
			RecordElasticQuotaMetric(gauge, tt.resources, tt.field, labels)

			collected := collectGauge(t, gauge)
			assert.Equal(t, tt.wantCount, len(collected))
			if tt.check != nil {
				tt.check(t, collected)
			}
		})
	}
}

func TestDeleteElasticQuotaMetric(t *testing.T) {
	t.Run("full delete removes all recorded series", func(t *testing.T) {
		gauge := newRegisteredTestGaugeVec(t)
		labels := baseQuotaLabels()
		resources := MakeResourceList().CPU(2).Mem(100).Obj()
		field := "runtime"

		RecordElasticQuotaMetric(gauge, resources, field, labels)
		assert.Equal(t, 2, len(collectGauge(t, gauge)))

		DeleteElasticQuotaMetric(gauge, resources, field, labels)
		assert.Equal(t, 0, len(collectGauge(t, gauge)))
	})

	t.Run("subset delete removes only matching series", func(t *testing.T) {
		gauge := newRegisteredTestGaugeVec(t)
		labels := baseQuotaLabels()
		allResources := MakeResourceList().CPU(2).Mem(100).GPU(1).Obj()
		field := "runtime"

		RecordElasticQuotaMetric(gauge, allResources, field, labels)
		assert.Equal(t, 3, len(collectGauge(t, gauge)))

		subset := MakeResourceList().CPU(2).Obj()
		DeleteElasticQuotaMetric(gauge, subset, field, labels)

		collected := collectGauge(t, gauge)
		assert.Equal(t, 2, len(collected))
		assert.Nil(t, findMetricByResourceAndField(collected, "cpu", field))
		assert.NotNil(t, findMetricByResourceAndField(collected, "memory", field))
		assert.NotNil(t, findMetricByResourceAndField(collected, "nvidia.com/gpu", field))
	})
}

func TestRecordElasticQuotaMetric_LabelMutation(t *testing.T) {
	gauge := newRegisteredTestGaugeVec(t)
	labels := baseQuotaLabels()
	resources := MakeResourceList().CPU(1).Mem(50).Obj()
	field := "request"

	RecordElasticQuotaMetric(gauge, resources, field, labels)

	collected := collectGauge(t, gauge)
	assert.Equal(t, 2, len(collected))

	resourcesSeen := map[string]bool{}
	for _, m := range collected {
		assert.Equal(t, field, labelValue(m, "field"))
		assert.Equal(t, "test-quota", labelValue(m, "name"))
		resource := labelValue(m, "resource")
		assert.False(t, resourcesSeen[resource], "duplicate resource label series: %s", resource)
		resourcesSeen[resource] = true
	}
	assert.True(t, resourcesSeen["cpu"])
	assert.True(t, resourcesSeen["memory"])
}

func TestMetricsRegistered(t *testing.T) {
	assert.NotNil(t, ElasticQuotaSpecMetric)
	assert.NotNil(t, ElasticQuotaStatusMetric)
	assert.NotNil(t, UpdateElasticQuotaStatusLatency)

	t.Cleanup(func() {
		ElasticQuotaSpecMetric.Reset()
		ElasticQuotaStatusMetric.Reset()
	})

	labels := baseQuotaLabels()
	resources := MakeResourceList().CPU(1).Obj()
	RecordElasticQuotaMetric(ElasticQuotaSpecMetric, resources, "min", labels)

	collected := collectGauge(t, ElasticQuotaSpecMetric)
	assert.GreaterOrEqual(t, len(collected), 1)
	m := findMetricByResourceAndField(collected, "cpu", "min")
	assert.NotNil(t, m)
	assert.Equal(t, float64(1000), gaugeValue(m))
}

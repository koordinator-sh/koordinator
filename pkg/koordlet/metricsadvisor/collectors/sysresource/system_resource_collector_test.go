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

package sysresource

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_systemResourceCollector_collectSysResUsed(t *testing.T) {
	testNow := time.Now()
	timeNow = func() time.Time {
		return testNow
	}
	config := framework.NewDefaultConfig()
	type usageField struct {
		ts     time.Time
		cpu    float64
		memory float64
	}
	type fields struct {
		nodeUsage    *usageField
		podUsage     map[string]usageField
		hostAppUsage *usageField

		nodeUsageWithPageCache *usageField
		podUsageWithPageCache  map[string]usageField
		hostAppUsageWithPageCache *usageField

		nodeUsageWithHotPageCache *usageField
		podUsageWithHotPageCache  map[string]usageField
		hostAppUsageWithHotPageCache *usageField
	}
	type want struct {
		systemCPU                *float64
		systemMemory             *float64
		systemMemoryWithPageCache *float64
		systemMemoryWithHotPageCache *float64
	}
	tests := []struct {
		name   string
		fields fields
		want   want
	}{
		{
			name:   "node metric not exist",
			fields: fields{},
			want:   want{},
		},
		{
			name: "one pod collector",
			fields: fields{
				nodeUsage: &usageField{
					ts:     timeNow(),
					cpu:    1,
					memory: 1024,
				},
				podUsage: map[string]usageField{
					"test-collector": {
						ts:     timeNow(),
						cpu:    0.5,
						memory: 512,
					},
				},
				hostAppUsage: &usageField{
					ts:     timeNow(),
					cpu:    0,
					memory: 0,
				},
			},
			want: want{
				systemCPU:    ptr.To[float64](0.5),
				systemMemory: ptr.To[float64](512),
			},
		},
		{
			name: "with page cache metrics",
			fields: fields{
				nodeUsage: &usageField{ts: timeNow(), cpu: 1, memory: 1024},
				podUsage:  map[string]usageField{"test": {ts: timeNow(), cpu: 0.5, memory: 512}},
				hostAppUsage: &usageField{ts: timeNow(), cpu: 0, memory: 0},
				
				nodeUsageWithPageCache: &usageField{ts: timeNow(), memory: 2048},
				podUsageWithPageCache:  map[string]usageField{"test": {ts: timeNow(), memory: 1024}},
				hostAppUsageWithPageCache: &usageField{ts: timeNow(), memory: 256},
			},
			want: want{
				systemCPU:                ptr.To[float64](0.5),
				systemMemory:             ptr.To[float64](512),
				systemMemoryWithPageCache: ptr.To[float64](768), // 2048 - 1024 - 256 = 768
			},
		},
		{
			name: "with hot page cache metrics",
			fields: fields{
				nodeUsage: &usageField{ts: timeNow(), cpu: 1, memory: 1024},
				podUsage:  map[string]usageField{"test": {ts: timeNow(), cpu: 0.5, memory: 512}},
				hostAppUsage: &usageField{ts: timeNow(), cpu: 0, memory: 0},
				
				nodeUsageWithHotPageCache: &usageField{ts: timeNow(), memory: 3000},
				podUsageWithHotPageCache:  map[string]usageField{"test": {ts: timeNow(), memory: 1000}},
				hostAppUsageWithHotPageCache: &usageField{ts: timeNow(), memory: 500},
			},
			want: want{
				systemCPU:                ptr.To[float64](0.5),
				systemMemory:             ptr.To[float64](512),
				systemMemoryWithHotPageCache: ptr.To[float64](1500), // 3000 - 1000 - 500 = 1500
			},
		},
	}
	for _, tt := range tests {
		helper := system.NewFileTestUtil(t)

		metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
			TSDBPath:              t.TempDir(),
			TSDBEnablePromMetrics: false,
		})
		assert.NoError(t, err)

		t.Run(tt.name, func(t *testing.T) {
			si := New(&framework.Options{
				Config:      config,
				MetricCache: metricCache,
			})
			si.Setup(&framework.Context{
				State: framework.NewSharedState(),
			})
			s := si.(*systemResourceCollector)
			if tt.fields.nodeUsage != nil {
				s.sharedState.UpdateNodeUsage(
					metriccache.Point{Timestamp: tt.fields.nodeUsage.ts, Value: tt.fields.nodeUsage.cpu},
					metriccache.Point{Timestamp: tt.fields.nodeUsage.ts, Value: tt.fields.nodeUsage.memory},
				)
			}
			if tt.fields.nodeUsageWithPageCache != nil {
				s.sharedState.UpdateNodeMemoryWithPageCache(metriccache.Point{Timestamp: tt.fields.nodeUsageWithPageCache.ts, Value: tt.fields.nodeUsageWithPageCache.memory})
			}
			if tt.fields.nodeUsageWithHotPageCache != nil {
				s.sharedState.UpdateNodeMemoryWithHotPageCache(metriccache.Point{Timestamp: tt.fields.nodeUsageWithHotPageCache.ts, Value: tt.fields.nodeUsageWithHotPageCache.memory})
			}

			for collector, pod := range tt.fields.podUsage {
				s.sharedState.UpdatePodUsage(collector,
					metriccache.Point{Timestamp: pod.ts, Value: pod.cpu},
					metriccache.Point{Timestamp: pod.ts, Value: pod.memory},
				)
			}
			for collector, pod := range tt.fields.podUsageWithPageCache {
				s.sharedState.UpdatePodsMemoryWithPageCache(collector, metriccache.Point{Timestamp: pod.ts, Value: pod.memory})
			}
			for collector, pod := range tt.fields.podUsageWithHotPageCache {
				s.sharedState.UpdatePodsMemoryWithHotPageCache(collector, metriccache.Point{Timestamp: pod.ts, Value: pod.memory})
			}

			if tt.fields.hostAppUsage != nil {
				s.sharedState.UpdateHostAppUsage(
					metriccache.Point{Timestamp: tt.fields.hostAppUsage.ts, Value: tt.fields.hostAppUsage.cpu},
					metriccache.Point{Timestamp: tt.fields.hostAppUsage.ts, Value: tt.fields.hostAppUsage.memory},
				)
			}
			if tt.fields.hostAppUsageWithPageCache != nil {
				s.sharedState.UpdateHostAppMemoryWithPageCache(metriccache.Point{Timestamp: tt.fields.hostAppUsageWithPageCache.ts, Value: tt.fields.hostAppUsageWithPageCache.memory})
			}
			if tt.fields.hostAppUsageWithHotPageCache != nil {
				s.sharedState.UpdateHostAppMemoryWithHotPageCache(metriccache.Point{Timestamp: tt.fields.hostAppUsageWithHotPageCache.ts, Value: tt.fields.hostAppUsageWithHotPageCache.memory})
			}

			s.collectSysResUsed()

			querier, err := metricCache.Querier(timeNow().Add(-s.outdatedInterval), timeNow())
			assert.NoError(t, err)

			// check default metrics
			cpuResult, err := testQuery(querier, metriccache.SystemCPUUsageMetric, nil)
			assert.NoError(t, err)
			if tt.want.systemCPU != nil {
				cpuValue, _ := cpuResult.Value(metriccache.AggregationTypeLast)
				assert.Equal(t, *tt.want.systemCPU, cpuValue)
			}

			memoryResult, err := testQuery(querier, metriccache.SystemMemoryUsageMetric, nil)
			assert.NoError(t, err)
			if tt.want.systemMemory != nil {
				memoryValue, _ := memoryResult.Value(metriccache.AggregationTypeLast)
				assert.Equal(t, *tt.want.systemMemory, memoryValue)
			}

			// check new metrics
			if tt.want.systemMemoryWithPageCache != nil {
				res, err := testQuery(querier, metriccache.SystemMemoryUsageWithPageCacheMetric, nil)
				assert.NoError(t, err)
				val, _ := res.Value(metriccache.AggregationTypeLast)
				assert.Equal(t, *tt.want.systemMemoryWithPageCache, val)
			}
			if tt.want.systemMemoryWithHotPageCache != nil {
				res, err := testQuery(querier, metriccache.SystemMemoryWithHotPageUsageMetric, nil)
				assert.NoError(t, err)
				val, _ := res.Value(metriccache.AggregationTypeLast)
				assert.Equal(t, *tt.want.systemMemoryWithHotPageCache, val)
			}
		})
		err = metricCache.Close()
		assert.NoError(t, err)
		helper.Cleanup()
	}
}

func testQuery(querier metriccache.Querier, resource metriccache.MetricResource, properties map[metriccache.MetricProperty]string) (metriccache.AggregateResult, error) {
	queryMeta, err := resource.BuildQueryMeta(properties)
	if err != nil {
		return nil, err
	}
	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err = querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}
	return aggregateResult, nil
}

func Test_systemResourceCollector_Enabled(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		s := &systemResourceCollector{}
		if got := s.Enabled(); !got {
			t.Errorf("Enabled() = %v", got)
		}
	})
}

func Test_systemResourceCollector_Started(t *testing.T) {
	type fields struct {
		started *atomic.Bool
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "started",
			fields: fields{
				started: atomic.NewBool(true),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &systemResourceCollector{
				started: tt.fields.started,
			}
			if got := s.Started(); got != tt.want {
				t.Errorf("Started() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_systemResourceCollector_Run(t *testing.T) {
	type fields struct {
		sharedState *framework.SharedState
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "node has not synced",
			fields: fields{
				sharedState: &framework.SharedState{
					LatestMetric: framework.LatestMetric{},
				},
			},
		},
	}
	for _, tt := range tests {
		metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
			TSDBPath:              t.TempDir(),
			TSDBEnablePromMetrics: false,
		})

		assert.NoError(t, err)
		t.Run(tt.name, func(t *testing.T) {
			s := &systemResourceCollector{
				collectInterval: time.Second,
				appendableDB:    metricCache,
				sharedState:     framework.NewSharedState(),
			}
			s.sharedState.UpdateNodeUsage(metriccache.Point{Timestamp: time.Now(), Value: 1},
				metriccache.Point{Timestamp: time.Now(), Value: 1024})
			s.sharedState.UpdatePodUsage("pod-collector",
				metriccache.Point{Timestamp: time.Now(), Value: 0.5},
				metriccache.Point{Timestamp: time.Now(), Value: 512})

			stopCh := make(chan struct{}, 1)
			close(stopCh)
			assert.NotPanics(t, func() {
				s.Run(stopCh)
			})
			err = metricCache.Close()
			assert.NoError(t, err)
		})
	}
}

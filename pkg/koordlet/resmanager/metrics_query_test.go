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

package resmanager

import (
	"github.com/golang/mock/gomock"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
	"testing"
	"time"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
)

func Test_collectPodMetricLast(t *testing.T) {
	now := time.Now()
	timeNow = func() time.Time {
		return now // Some time that you need
	}
	type metricInfos struct {
		timestamp  time.Time
		podResUsed *metriccache.PodResourceMetric
	}
	type args struct {
		name            string
		metricInfos     []*metricInfos
		pod             *statesinformer.PodMeta
		expectPodMetric *metriccache.PodResourceMetric
	}

	tests := []args{
		{
			name:            "test no metrics in db",
			pod:             &statesinformer.PodMeta{Pod: createTestPod(extension.QoSLSR, "test_pod")},
			expectPodMetric: nil,
		},
		{
			name: "test normal",
			pod:  &statesinformer.PodMeta{Pod: createTestPod(extension.QoSLSR, "test_pod")},
			metricInfos: []*metricInfos{
				{
					timestamp: time.Now().Add(-3 * time.Second),
					podResUsed: &metriccache.PodResourceMetric{
						PodUID:     "test_pod",
						CPUUsed:    metriccache.CPUMetric{CPUUsed: resource.MustParse("14")},
						MemoryUsed: metriccache.MemoryMetric{MemoryWithoutCache: resource.MustParse("60G")},
					},
				},
				{
					timestamp: time.Now().Add(-1 * time.Second),
					podResUsed: &metriccache.PodResourceMetric{
						PodUID:     "test_pod",
						CPUUsed:    metriccache.CPUMetric{CPUUsed: resource.MustParse("16")},
						MemoryUsed: metriccache.MemoryMetric{MemoryWithoutCache: resource.MustParse("70G")},
					},
				},
			},
			expectPodMetric: &metriccache.PodResourceMetric{
				PodUID:     "test_pod",
				CPUUsed:    metriccache.CPUMetric{CPUUsed: *resource.NewMilliQuantity(16000, resource.DecimalSI)},
				MemoryUsed: metriccache.MemoryMetric{MemoryWithoutCache: *resource.NewQuantity(70000000000, resource.BinarySI)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metricCache, _ := metriccache.NewCacheNotShareMetricCache(&metriccache.Config{MetricGCIntervalSeconds: 60, MetricExpireSeconds: 60})
			for _, metricInfo := range tt.metricInfos {
				_ = metricCache.InsertPodResourceMetric(metricInfo.timestamp, metricInfo.podResUsed)
			}

			ctl := gomock.NewController(t)
			defer ctl.Finish()
			mockstatesinformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockstatesinformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{tt.pod}).AnyTimes()

			resmanager := &resmanager{metricCache: metricCache, statesInformer: mockstatesinformer, collectResUsedIntervalSeconds: 60}
			gotPodMetrics := resmanager.collectPodMetricLast()
			if tt.expectPodMetric == nil {
				assert.True(t, len(gotPodMetrics) == 0)
			} else {
				assert.Equal(t, tt.expectPodMetric, gotPodMetrics[0])
			}
		})
	}
}

func Test_resmanager_collectorNodeMetricLast(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cpuQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
	assert.NoError(t, err)
	memQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
	assert.NoError(t, err)

	type fields struct {
		metricCache func(ctrl *gomock.Controller) metriccache.MetricCache
	}
	type args struct {
		queryMeta metriccache.MetricMeta
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    float64
		wantErr bool
	}{
		{
			name: "cpu query",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
					mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctrl)
					metriccache.DefaultAggregateResultFactory = mockResultFactory
					mockQuerier := mock_metriccache.NewMockQuerier(ctrl)
					mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

					cpuResult := mock_metriccache.NewMockAggregateResult(ctrl)
					cpuResult.EXPECT().Value(metriccache.AggregationTypeLast).Return(float64(16), nil).AnyTimes()
					cpuResult.EXPECT().Count().Return(1).AnyTimes()
					mockResultFactory.EXPECT().New(cpuQueryMeta).Return(cpuResult).AnyTimes()
					mockQuerier.EXPECT().Query(cpuQueryMeta, gomock.Any(), cpuResult).SetArg(2, *cpuResult).Return(nil).AnyTimes()

					return mockMetricCache
				},
			},
			args: args{
				queryMeta: cpuQueryMeta,
			},
			want:    16,
			wantErr: false,
		},
		{
			name: "memory query",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
					mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctrl)
					metriccache.DefaultAggregateResultFactory = mockResultFactory
					mockQuerier := mock_metriccache.NewMockQuerier(ctrl)
					mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

					memResult := mock_metriccache.NewMockAggregateResult(ctrl)
					memResult.EXPECT().Value(metriccache.AggregationTypeLast).Return(float64(10), nil).AnyTimes()
					memResult.EXPECT().Count().Return(1).AnyTimes()
					mockResultFactory.EXPECT().New(memQueryMeta).Return(memResult).AnyTimes()
					mockQuerier.EXPECT().Query(memQueryMeta, gomock.Any(), memResult).SetArg(2, *memResult).Return(nil).AnyTimes()

					return mockMetricCache
				},
			},
			args: args{
				queryMeta: memQueryMeta,
			},
			want:    10,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			r := &resmanager{metricCache: tt.fields.metricCache(ctrl), collectResUsedIntervalSeconds: 60}
			got, err := r.collectorNodeMetricLast(tt.args.queryMeta)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.Equalf(t, tt.want, got, "collectorNodeMetricLast(%v)", tt.args.queryMeta)
		})
	}
}

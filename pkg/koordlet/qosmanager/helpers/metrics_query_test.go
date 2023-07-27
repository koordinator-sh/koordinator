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

package helpers

import (
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
)

func Test_collectAllPodMetricsLast(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	now := time.Now()
	timeNow = func() time.Time {
		return now // Some time that you need
	}
	type fields struct {
		metricCache func(ctrl *gomock.Controller) metriccache.MetricCache
	}
	type args struct {
		name            string
		fields          fields
		metricResource  metriccache.MetricResource
		pod             *statesinformer.PodMeta
		expectPodMetric map[string]float64
	}

	tests := []args{
		{
			name: "test no metrics in db",
			fields: fields{metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
				mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
				mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctrl)
				metriccache.DefaultAggregateResultFactory = mockResultFactory
				mockQuerier := mock_metriccache.NewMockQuerier(ctrl)
				mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

				cpuResult := mock_metriccache.NewMockAggregateResult(ctrl)
				cpuResult.EXPECT().Value(metriccache.AggregationTypeLast).Return(float64(0), errors.New("empty value")).AnyTimes()
				cpuResult.EXPECT().Count().Return(0).AnyTimes()
				mockResultFactory.EXPECT().New(gomock.Any()).Return(cpuResult).AnyTimes()
				mockQuerier.EXPECT().Query(gomock.Any(), gomock.Any(), cpuResult).SetArg(2, *cpuResult).Return(nil).AnyTimes()
				return mockMetricCache
			}},
			metricResource:  metriccache.PodCPUUsageMetric,
			pod:             &statesinformer.PodMeta{Pod: testutil.MockTestPod(extension.QoSLSR, "test_pod")},
			expectPodMetric: map[string]float64{},
		},
		{
			name: "cpu test",
			pod:  &statesinformer.PodMeta{Pod: testutil.MockTestPod(extension.QoSLSR, "test_pod")},
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
					cpuQueryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod("test_pod"))
					assert.NoError(t, err)
					mockResultFactory.EXPECT().New(cpuQueryMeta).Return(cpuResult).AnyTimes()
					mockQuerier.EXPECT().Query(cpuQueryMeta, gomock.Any(), cpuResult).SetArg(2, *cpuResult).Return(nil).AnyTimes()
					return mockMetricCache
				},
			},
			metricResource:  metriccache.PodCPUUsageMetric,
			expectPodMetric: map[string]float64{"test_pod": 16},
		},
		{
			name: "memory test",
			pod:  &statesinformer.PodMeta{Pod: testutil.MockTestPod(extension.QoSLSR, "test_pod")},
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
					mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctrl)
					metriccache.DefaultAggregateResultFactory = mockResultFactory
					mockQuerier := mock_metriccache.NewMockQuerier(ctrl)
					mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

					memResult := mock_metriccache.NewMockAggregateResult(ctrl)
					memResult.EXPECT().Value(metriccache.AggregationTypeLast).Return(float64(70000000000), nil).AnyTimes()
					memResult.EXPECT().Count().Return(1).AnyTimes()
					memQueryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod("test_pod"))
					assert.NoError(t, err)
					mockResultFactory.EXPECT().New(memQueryMeta).Return(memResult).AnyTimes()
					mockQuerier.EXPECT().Query(memQueryMeta, gomock.Any(), memResult).SetArg(2, *memResult).Return(nil).AnyTimes()
					return mockMetricCache
				},
			},
			metricResource:  metriccache.PodMemUsageMetric,
			expectPodMetric: map[string]float64{"test_pod": 70000000000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockstatesinformer := mock_statesinformer.NewMockStatesInformer(ctrl)
			mockstatesinformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{tt.pod}).AnyTimes()

			gotPodMetrics := CollectAllPodMetricsLast(mockstatesinformer, tt.fields.metricCache(ctrl), tt.metricResource, 60*time.Second)
			assert.Equal(t, tt.expectPodMetric, gotPodMetrics)
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
			got, err := CollectorNodeMetricLast(tt.fields.metricCache(ctrl), tt.args.queryMeta, 60*time.Second)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.Equalf(t, tt.want, got, "collectorNodeMetricLast(%v)", tt.args.queryMeta)
		})
	}
}

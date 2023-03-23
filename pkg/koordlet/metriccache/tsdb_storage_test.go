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

package metriccache

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testMetricSample struct {
	property map[MetricProperty]string
	point    Point
}

func Test_tsdbStorage_Append_And_Querier(t *testing.T) {
	pod1Property := map[MetricProperty]string{
		MetricPropertyPodUID: "test-pod-uid1",
	}
	pod1Meta, _ := PodCPUUsageMetric.BuildQueryMeta(pod1Property)
	pod2Property := map[MetricProperty]string{
		MetricPropertyPodUID: "test-pod-uid2",
	}
	pod2Meta, _ := PodCPUUsageMetric.BuildQueryMeta(pod2Property)

	now := time.UnixMilli(time.Now().UnixMilli())

	type args struct {
		metricSamples [][]testMetricSample
		startTime     time.Time
		endTime       time.Time
		aggregateType AggregationType
	}
	type queryValue struct {
		startTime time.Time
		endTime   time.Time
		queryMeta MetricMeta
		value     float64
	}
	type want struct {
		values []queryValue
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "insert pod cpu usage",
			args: args{
				metricSamples: [][]testMetricSample{
					{
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-4 * time.Second),
								Value:     4,
							},
						},
						{
							property: pod2Property,
							point: Point{
								Timestamp: now.Add(-3 * time.Second),
								Value:     300,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-2 * time.Second),
								Value:     1,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-1 * time.Second),
								Value:     1,
							},
						},
					},
				},
				startTime:     now.Add(-5 * time.Second),
				endTime:       now,
				aggregateType: AggregationTypeAVG,
			},
			want: want{
				values: []queryValue{
					{
						startTime: now.Add(-4 * time.Second),
						endTime:   now.Add(-1 * time.Second),
						queryMeta: pod1Meta,
						value:     2,
					},
					{
						startTime: now.Add(-3 * time.Second),
						endTime:   now.Add(-3 * time.Second),
						queryMeta: pod2Meta,
						value:     300,
					},
				},
			},
		},
		{
			name: "insert different pod cpu usage at same ts",
			args: args{
				metricSamples: [][]testMetricSample{
					{
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-4 * time.Second),
								Value:     4,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-2 * time.Second),
								Value:     1,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-1 * time.Second),
								Value:     1,
							},
						},
					},
					{
						{
							property: pod2Property,
							point: Point{
								Timestamp: now.Add(-2 * time.Second),
								Value:     300,
							},
						},
					},
				},
				startTime:     now.Add(-5 * time.Second),
				endTime:       now,
				aggregateType: AggregationTypeAVG,
			},
			want: want{
				values: []queryValue{
					{
						startTime: now.Add(-4 * time.Second),
						endTime:   now.Add(-1 * time.Second),
						queryMeta: pod1Meta,
						value:     2,
					},
					{
						startTime: now.Add(-2 * time.Second),
						endTime:   now.Add(-2 * time.Second),
						queryMeta: pod2Meta,
						value:     300,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			defer os.RemoveAll(dir)
			conf := NewDefaultConfig()
			conf.TSDBPath = dir
			conf.TSDBEnablePromMetrics = false
			db, err := NewTSDBStorage(conf)
			defer func() {
				db.Close()
			}()
			assert.NoError(t, err)

			t.Log(dir)
			for _, sampleList := range tt.args.metricSamples {
				appendSample := make([]MetricSample, 0, len(sampleList))
				for _, sample := range sampleList {
					s, err := PodCPUUsageMetric.GenerateSample(sample.property, sample.point.Timestamp, sample.point.Value)
					assert.NoError(t, err)
					appendSample = append(appendSample, s)
				}
				appender := db.Appender()
				err = appender.Append(appendSample)
				assert.NoError(t, err)

				err = appender.Commit()
				assert.NoError(t, err)
			}

			for _, want := range tt.want.values {
				querier, err := db.Querier(tt.args.startTime, tt.args.endTime)
				assert.NoError(t, err)

				aggregateResult := &aggregateResult{}
				err = querier.Query(want.queryMeta, nil, aggregateResult)
				assert.NoError(t, err)
				gotValue, err := aggregateResult.Value(tt.args.aggregateType)
				assert.NoError(t, err)

				assert.True(t, aggregateResult.metricStart.Equal(want.startTime), "metric start time should be equal, want %v, got %v",
					want.startTime, aggregateResult.metricStart)
				assert.True(t, aggregateResult.metricsEnd.Equal(want.endTime), "metric end time should be equal, want %v, got %v",
					want.endTime, aggregateResult.metricsEnd)
				assert.Equal(t, want.value, gotValue, "metric aggregate value should be equal")
			}
		})
	}
}

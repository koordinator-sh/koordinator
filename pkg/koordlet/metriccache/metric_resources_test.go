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

func Test_metricCache_ContainerThrottledMetric_Append_and_Query(t *testing.T) {
	now := time.Now()
	container1Property := map[MetricProperty]string{
		MetricPropertyContainerID: "container-id-1",
	}
	container2Property := map[MetricProperty]string{
		MetricPropertyContainerID: "container-id-2",
	}
	type args struct {
		containerID string
		samples     []testMetricSample
		startTime   time.Time
		endTime     time.Time
	}
	type want struct {
		aggregatedValue map[AggregationType]float64
		count           int
		rangeStart      time.Time
		rangeEnd        time.Time
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "container-throttled-metric-latest-crud",
			args: args{
				containerID: "container-id-1",
				startTime:   now.Add(-time.Second * 120),
				endTime:     now,
				samples: []testMetricSample{
					{
						property: container1Property,
						point: Point{
							Timestamp: now.Add(-time.Second * 120),
							Value:     0.7,
						},
					},
					{
						property: container1Property,
						point: Point{
							Timestamp: now.Add(-time.Second * 10),
							Value:     0.6,
						},
					},
					{
						property: container1Property,
						point: Point{
							Timestamp: now.Add(-time.Second * 5),
							Value:     0.5,
						},
					},
					{
						property: container2Property,
						point: Point{
							Timestamp: now.Add(-time.Second * 4),
							Value:     0.4,
						},
					},
				},
			},
			want: want{
				aggregatedValue: map[AggregationType]float64{
					AggregationTypeLast: 0.5,
				},
				count:      3,
				rangeStart: now.Add(-time.Second * 120),
				rangeEnd:   now.Add(-time.Second * 5),
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

			appendSample := make([]MetricSample, 0, len(tt.args.samples))
			for _, sample := range tt.args.samples {
				s, err := ContainerCPUThrottledMetric.GenerateSample(sample.property, sample.point.Timestamp, sample.point.Value)
				assert.NoError(t, err)
				appendSample = append(appendSample, s)
			}

			// append sample
			appender := db.Appender()
			err = appender.Append(appendSample)
			assert.NoError(t, err)

			// commit sample
			err = appender.Commit()
			assert.NoError(t, err)

			// build querier
			querier, err := db.Querier(tt.args.startTime, tt.args.endTime)
			assert.NoError(t, err)

			// build query meta
			queryMeta, err := ContainerCPUThrottledMetric.BuildQueryMeta(MetricPropertiesFunc.Container(tt.args.containerID))
			assert.NoError(t, err)

			// query sample
			aggregateResult := &aggregateResult{}
			err = querier.Query(queryMeta, nil, aggregateResult)
			assert.NoError(t, err)

			// check equal
			for aggregateType, wantVal := range tt.want.aggregatedValue {
				gotVal, err := aggregateResult.Value(aggregateType)
				assert.NoError(t, err)
				assert.Equal(t, wantVal, gotVal)
			}
			assert.Equal(t, tt.want.count, aggregateResult.Count())
		})
	}
}

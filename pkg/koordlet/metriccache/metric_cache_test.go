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
	"reflect"
	"testing"
	"time"

	// "github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
)

func Test_metricCache_BECPUResourceMetric_CRUD(t *testing.T) {
	now := time.Now()
	type args struct {
		config  *Config
		samples map[time.Time]BECPUResourceMetric
	}
	tests := []struct {
		name            string
		args            args
		want            BECPUResourceQueryResult
		wantAfterDelete BECPUResourceQueryResult
	}{
		{
			name: "node-resource-metric-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				samples: map[time.Time]BECPUResourceMetric{
					now.Add(-time.Second * 120): {
						CPUUsed:      *resource.NewQuantity(2, resource.DecimalSI),
						CPURequest:   *resource.NewQuantity(4, resource.DecimalSI),
						CPURealLimit: *resource.NewQuantity(2, resource.DecimalSI),
					},
					now.Add(-time.Second * 10): {
						CPUUsed:      *resource.NewQuantity(2, resource.DecimalSI),
						CPURequest:   *resource.NewQuantity(4, resource.DecimalSI),
						CPURealLimit: *resource.NewQuantity(2, resource.DecimalSI),
					},
					now.Add(-time.Second * 5): {
						CPUUsed:      *resource.NewMilliQuantity(500, resource.DecimalSI),
						CPURequest:   *resource.NewMilliQuantity(1000, resource.DecimalSI),
						CPURealLimit: *resource.NewMilliQuantity(500, resource.DecimalSI),
					},
				},
			},
			want: BECPUResourceQueryResult{
				Metric: &BECPUResourceMetric{
					CPUUsed:      *resource.NewMilliQuantity(1500, resource.DecimalSI),
					CPURequest:   *resource.NewMilliQuantity(3000, resource.DecimalSI),
					CPURealLimit: *resource.NewMilliQuantity(1500, resource.DecimalSI),
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 3}},
			},
			wantAfterDelete: BECPUResourceQueryResult{
				Metric: &BECPUResourceMetric{
					CPUUsed:      *resource.NewMilliQuantity(1250, resource.DecimalSI),
					CPURequest:   *resource.NewMilliQuantity(2500, resource.DecimalSI),
					CPURealLimit: *resource.NewMilliQuantity(1250, resource.DecimalSI),
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 2}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStorage()
			defer s.Close()
			m := &metricCache{
				config: tt.args.config,
				db:     s,
			}
			for ts, sample := range tt.args.samples {
				err := m.InsertBECPUResourceMetric(ts, &sample)
				if err != nil {
					t.Errorf("InsertBECPUResourceMetric failed %v", err)
				}
			}

			oldStartTime := time.Unix(0, 0)
			params := &QueryParam{
				Aggregate: AggregationTypeAVG,
				Start:     &oldStartTime,
				End:       &now,
			}
			got := m.GetBECPUResourceMetric(params)
			if got.Error != nil {
				t.Errorf("GetBECPUResourceMetric failed %v", got.Error)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetBECPUResourceMetric() got = %+v, want %+v", got, tt.want)
			}

			// delete expire items
			m.recycleDB()

			gotAfterDel := m.GetBECPUResourceMetric(params)
			if gotAfterDel.Error != nil {
				t.Errorf("GetBECPUResourceMetric failed %v", gotAfterDel.Error)
			}

			if !reflect.DeepEqual(gotAfterDel, tt.wantAfterDelete) {
				t.Errorf("GetBECPUResourceMetric() after delete, got = %+v, want %+v", gotAfterDel, tt.wantAfterDelete)
			}
		})
	}
}

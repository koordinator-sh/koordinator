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

	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
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

func Test_metricCache_NodeCPUInfo_CRUD(t *testing.T) {
	type args struct {
		config  *Config
		samples []NodeCPUInfo
	}
	tests := []struct {
		name  string
		args  args
		want  *NodeCPUInfo
		want1 *NodeCPUInfo
	}{
		{
			name: "node-cpu-info-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				samples: []NodeCPUInfo{
					{
						ProcessorInfos: []koordletutil.ProcessorInfo{
							{
								CPUID:    0,
								CoreID:   0,
								SocketID: 0,
								NodeID:   0,
							},
							{
								CPUID:    1,
								CoreID:   1,
								SocketID: 0,
								NodeID:   0,
							},
						},
					},
					{
						ProcessorInfos: []koordletutil.ProcessorInfo{
							// HT on
							{
								CPUID:    0,
								CoreID:   0,
								SocketID: 0,
								NodeID:   0,
							},
							{
								CPUID:    1,
								CoreID:   1,
								SocketID: 0,
								NodeID:   0,
							},
							{
								CPUID:    2,
								CoreID:   0,
								SocketID: 0,
								NodeID:   0,
							},
							{
								CPUID:    3,
								CoreID:   1,
								SocketID: 0,
								NodeID:   0,
							},
						},
					},
				},
			},
			want: &NodeCPUInfo{
				ProcessorInfos: []koordletutil.ProcessorInfo{
					{
						CPUID:    0,
						CoreID:   0,
						SocketID: 0,
						NodeID:   0,
					},
					{
						CPUID:    1,
						CoreID:   1,
						SocketID: 0,
						NodeID:   0,
					},
					{
						CPUID:    2,
						CoreID:   0,
						SocketID: 0,
						NodeID:   0,
					},
					{
						CPUID:    3,
						CoreID:   1,
						SocketID: 0,
						NodeID:   0,
					},
				},
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
			for _, sample := range tt.args.samples {
				err := m.InsertNodeCPUInfo(&sample)
				if err != nil {
					t.Errorf("insert node cpu info failed %v", err)
				}
			}

			params := &QueryParam{}
			got, err := m.GetNodeCPUInfo(params)
			if err != nil {
				t.Errorf("get node cpu info failed %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetNodeCPUInfo() got = %v, want %v", got, tt.want)
			}

			// delete expire items, should not change nodeCPUInfo record
			m.recycleDB()

			gotAfterDel, err := m.GetNodeCPUInfo(params)
			if err != nil {
				t.Errorf("get node cpu info failed %v", err)
			}
			if !reflect.DeepEqual(gotAfterDel, tt.want) {
				t.Errorf("GetNodeCPUInfo() got = %v, want %v", gotAfterDel, tt.want)
			}
		})
	}
}

func Test_metricCache_aggregateGPUUsages(t *testing.T) {
	type fields struct {
		config *Config
	}
	type args struct {
		gpuResourceMetrics [][]gpuResourceMetric
		aggregateFunc      AggregationFunc
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []GPUMetric
		wantErr bool
	}{
		{
			name: "sample device",
			fields: fields{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
			},
			args: args{
				aggregateFunc: getAggregateFunc(AggregationTypeAVG),
				gpuResourceMetrics: [][]gpuResourceMetric{
					{
						{DeviceUUID: "1-1", Minor: 0, SMUtil: 20, MemoryUsed: 1000, MemoryTotal: 10000},
						{DeviceUUID: "2-1", Minor: 1, SMUtil: 40, MemoryUsed: 2000, MemoryTotal: 20000},
					},
					{
						{DeviceUUID: "1-1", Minor: 0, SMUtil: 40, MemoryUsed: 4000, MemoryTotal: 10000},
						{DeviceUUID: "2-1", Minor: 1, SMUtil: 30, MemoryUsed: 1000, MemoryTotal: 20000},
					},
				},
			},
			want: []GPUMetric{
				{
					DeviceUUID:  "1-1",
					Minor:       0,
					SMUtil:      30,
					MemoryUsed:  *resource.NewQuantity(2500, resource.BinarySI),
					MemoryTotal: *resource.NewQuantity(10000, resource.BinarySI),
				},
				{
					DeviceUUID:  "2-1",
					Minor:       1,
					SMUtil:      35,
					MemoryUsed:  *resource.NewQuantity(1500, resource.BinarySI),
					MemoryTotal: *resource.NewQuantity(20000, resource.BinarySI),
				},
			},
		},

		{
			name: "difference device",
			fields: fields{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
			},
			args: args{
				aggregateFunc: getAggregateFunc(AggregationTypeAVG),
				gpuResourceMetrics: [][]gpuResourceMetric{
					{
						{DeviceUUID: "1-1", Minor: 0, SMUtil: 20, MemoryUsed: 1000, MemoryTotal: 8000},
						{DeviceUUID: "2-1", Minor: 1, SMUtil: 40, MemoryUsed: 4000, MemoryTotal: 10000},
					},
					{
						{DeviceUUID: "3-1", Minor: 2, SMUtil: 40, MemoryUsed: 4000, MemoryTotal: 12000},
						{DeviceUUID: "4-1", Minor: 3, SMUtil: 30, MemoryUsed: 1000, MemoryTotal: 14000},
					},
				},
			},
			want: []GPUMetric{
				{
					DeviceUUID:  "3-1",
					Minor:       2,
					SMUtil:      30,
					MemoryUsed:  *resource.NewQuantity(2500, resource.BinarySI),
					MemoryTotal: *resource.NewQuantity(12000, resource.BinarySI),
				},
				{
					DeviceUUID:  "4-1",
					Minor:       3,
					SMUtil:      35,
					MemoryUsed:  *resource.NewQuantity(2500, resource.BinarySI),
					MemoryTotal: *resource.NewQuantity(14000, resource.BinarySI),
				},
			},
		},
		{
			name: "single device",
			fields: fields{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
			},
			args: args{
				aggregateFunc: getAggregateFunc(AggregationTypeAVG),
				gpuResourceMetrics: [][]gpuResourceMetric{
					{
						{DeviceUUID: "2-1", Minor: 1, SMUtil: 40, MemoryUsed: 2000, MemoryTotal: 10000},
					},
					{
						{DeviceUUID: "2-1", Minor: 1, SMUtil: 30, MemoryUsed: 1000, MemoryTotal: 12000},
					},
				},
			},
			want: []GPUMetric{
				{
					DeviceUUID:  "2-1",
					Minor:       1,
					SMUtil:      35,
					MemoryUsed:  *resource.NewQuantity(1500, resource.BinarySI),
					MemoryTotal: *resource.NewQuantity(12000, resource.BinarySI),
				},
			},
		},
		{
			name: "single device and multiple device",
			fields: fields{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
			},
			args: args{
				aggregateFunc: getAggregateFunc(AggregationTypeAVG),
				gpuResourceMetrics: [][]gpuResourceMetric{
					{
						{DeviceUUID: "1-1", Minor: 0, SMUtil: 20, MemoryUsed: 1000, MemoryTotal: 10000},
						{DeviceUUID: "3-1", Minor: 3, SMUtil: 40, MemoryUsed: 2000, MemoryTotal: 20000},
					},
					{
						{DeviceUUID: "1-1", Minor: 0, SMUtil: 40, MemoryUsed: 1000, MemoryTotal: 10000},
						{DeviceUUID: "3-1", Minor: 3, SMUtil: 30, MemoryUsed: 1000, MemoryTotal: 20000},
					},
				},
			},
			want: []GPUMetric{
				{
					DeviceUUID:  "1-1",
					Minor:       0,
					SMUtil:      30,
					MemoryUsed:  *resource.NewQuantity(1000, resource.BinarySI),
					MemoryTotal: *resource.NewQuantity(10000, resource.BinarySI),
				},
				{
					DeviceUUID:  "3-1",
					Minor:       3,
					SMUtil:      35,
					MemoryUsed:  *resource.NewQuantity(1500, resource.BinarySI),
					MemoryTotal: *resource.NewQuantity(20000, resource.BinarySI),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStorage()
			defer s.Close()
			m := &metricCache{
				config: tt.fields.config,
				db:     s,
			}
			got, err := m.aggregateGPUUsages(tt.args.gpuResourceMetrics, tt.args.aggregateFunc)
			if (err != nil) != tt.wantErr {
				t.Errorf("metricCache.aggregateGPUUsages() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("metricCache.aggregateGPUUsages() = %v, want %v", got, tt.want)
			}
		})
	}
}

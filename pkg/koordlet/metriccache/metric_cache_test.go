package metriccache

import (
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

func Test_metricCache_NodeResourceMetric_CRUD(t *testing.T) {
	now := time.Now()
	type args struct {
		config  *Config
		samples map[time.Time]NodeResourceMetric
	}

	tests := []struct {
		name            string
		args            args
		want            NodeResourceQueryResult
		wantAfterDelete NodeResourceQueryResult
	}{
		{
			name: "node-resource-metric-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				samples: map[time.Time]NodeResourceMetric{
					now.Add(-time.Second * 120): {
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(30, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 10): {
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(10, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 5): {
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
						},
					},
				},
			},
			want: NodeResourceQueryResult{
				Metric: &NodeResourceMetric{
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(1500, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 3}},
			},
			wantAfterDelete: NodeResourceQueryResult{
				Metric: &NodeResourceMetric{
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(1250, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(15, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 2}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStorage()
			m := &metricCache{
				config: tt.args.config,
				db:     s,
			}
			for ts, sample := range tt.args.samples {
				err := m.InsertNodeResourceMetric(ts, &sample)
				if err != nil {
					t.Errorf("insert node metric failed %v", err)
				}
			}

			oldStartTime := time.Unix(0, 0)
			params := &QueryParam{
				Aggregate: AggregationTypeAVG,
				Start:     &oldStartTime,
				End:       &now,
			}
			got := m.GetNodeResourceMetric(params)
			if got.Error != nil {
				t.Errorf("get node metric failed %v", got.Error)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetNodeResourceMetric() got = %v, want %v", got, tt.want)
			}

			// delete expire items
			m.recycleDB()

			gotAfterDel := m.GetNodeResourceMetric(params)
			if gotAfterDel.Error != nil {
				t.Errorf("get node metric failed %v", gotAfterDel.Error)
			}
			if !reflect.DeepEqual(gotAfterDel, tt.wantAfterDelete) {
				t.Errorf("GetNodeResourceMetric() after delete, got = %+v, want %+v", gotAfterDel, tt.wantAfterDelete)
			}
		})
	}
}

func Test_metricCache_PodResourceMetric_CRUD(t *testing.T) {
	now := time.Now()
	type args struct {
		config  *Config
		podUID  string
		samples map[time.Time]PodResourceMetric
	}

	tests := []struct {
		name            string
		args            args
		want            PodResourceQueryResult
		wantAfterDelete PodResourceQueryResult
	}{
		{
			name: "pod-resource-metric-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				podUID: "pod-uid-1",
				samples: map[time.Time]PodResourceMetric{
					now.Add(-time.Second * 120): {
						PodUID: "pod-uid-1",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(30, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 10): {
						PodUID: "pod-uid-1",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(10, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 5): {
						PodUID: "pod-uid-1",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 4): {
						PodUID: "pod-uid-2",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
						},
					},
				},
			},
			want: PodResourceQueryResult{
				Metric: &PodResourceMetric{
					PodUID: "pod-uid-1",
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(1500, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 3}},
			},

			wantAfterDelete: PodResourceQueryResult{
				Metric: &PodResourceMetric{
					PodUID: "pod-uid-1",
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(1250, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(15, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 2}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStorage()
			m := &metricCache{
				config: tt.args.config,
				db:     s,
			}
			for ts, sample := range tt.args.samples {
				err := m.InsertPodResourceMetric(ts, &sample)
				if err != nil {
					t.Errorf("insert pod metric failed %v", err)
				}
			}

			oldStartTime := time.Unix(0, 0)
			params := &QueryParam{
				Aggregate: AggregationTypeAVG,
				Start:     &oldStartTime,
				End:       &now,
			}

			got := m.GetPodResourceMetric(&tt.args.podUID, params)
			if got.Error != nil {
				t.Errorf("get pod metric failed %v", got.Error)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetPodResourceMetric() got = %v, want %v", got, tt.want)
			}
			// delete expire items
			m.recycleDB()

			gotAfterDel := m.GetPodResourceMetric(&tt.args.podUID, params)
			if gotAfterDel.Error != nil {
				t.Errorf("get pod metric failed %v", gotAfterDel.Error)
			}
			if !reflect.DeepEqual(gotAfterDel, tt.wantAfterDelete) {
				t.Errorf("GetNodeResourceMetric() after delete, got = %v, want %v", gotAfterDel, tt.wantAfterDelete)
			}
		})
	}
}

func Test_metricCache_ContainerResourceMetric_CRUD(t *testing.T) {
	now := time.Now()
	type args struct {
		config       *Config
		containerID  string
		aggregateArg AggregationType
		samples      map[time.Time]ContainerResourceMetric
	}

	tests := []struct {
		name            string
		args            args
		want            ContainerResourceQueryResult
		wantAfterDelete ContainerResourceQueryResult
	}{
		{
			name: "container-resource-metric-avg-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				containerID:  "container-id-1",
				aggregateArg: AggregationTypeAVG,
				samples: map[time.Time]ContainerResourceMetric{
					now.Add(-time.Second * 120): {
						ContainerID: "container-id-1",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(30, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 10): {
						ContainerID: "container-id-1",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(10, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 5): {
						ContainerID: "container-id-1",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 4): {
						ContainerID: "container-id-2",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
						},
					},
				},
			},
			want: ContainerResourceQueryResult{
				Metric: &ContainerResourceMetric{
					ContainerID: "container-id-1",
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(1500, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 3}},
			},
			wantAfterDelete: ContainerResourceQueryResult{
				Metric: &ContainerResourceMetric{
					ContainerID: "container-id-1",
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(1250, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(15, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 2}},
			},
		},
		{
			name: "container-resource-metric-latest-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				containerID:  "container-id-3",
				aggregateArg: AggregationTypeLast,
				samples: map[time.Time]ContainerResourceMetric{
					now.Add(-time.Second * 120): {
						ContainerID: "container-id-3",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(30, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 10): {
						ContainerID: "container-id-3",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewQuantity(2, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(10, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 5): {
						ContainerID: "container-id-3",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
						},
					},
					now.Add(-time.Second * 4): {
						ContainerID: "container-id-4",
						CPUUsed: CPUMetric{
							CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
						},
						MemoryUsed: MemoryMetric{
							MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
						},
					},
				},
			},
			want: ContainerResourceQueryResult{
				Metric: &ContainerResourceMetric{
					ContainerID: "container-id-3",
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 3}},
			},
			wantAfterDelete: ContainerResourceQueryResult{
				Metric: &ContainerResourceMetric{
					ContainerID: "container-id-3",
					CPUUsed: CPUMetric{
						CPUUsed: *resource.NewMilliQuantity(500, resource.DecimalSI),
					},
					MemoryUsed: MemoryMetric{
						MemoryWithoutCache: *resource.NewQuantity(20, resource.BinarySI),
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 2}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStorage()
			m := &metricCache{
				config: tt.args.config,
				db:     s,
			}
			for ts, sample := range tt.args.samples {
				err := m.InsertContainerResourceMetric(ts, &sample)
				if err != nil {
					t.Errorf("insert container metric failed %v", err)
				}
			}

			oldStartTime := time.Unix(0, 0)
			params := &QueryParam{
				Aggregate: tt.args.aggregateArg,
				Start:     &oldStartTime,
				End:       &now,
			}

			got := m.GetContainerResourceMetric(&tt.args.containerID, params)
			if got.Error != nil {
				t.Errorf("get container metric failed %v", got.Error)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetContainerResourceMetric() got = %v, want %v", got, tt.want)
			}
			// delete expire items
			m.recycleDB()

			gotAfterDel := m.GetContainerResourceMetric(&tt.args.containerID, params)
			if gotAfterDel.Error != nil {
				t.Errorf("get container metric failed %v", gotAfterDel.Error)
			}
			if !reflect.DeepEqual(gotAfterDel, tt.wantAfterDelete) {
				t.Errorf("GetContainerResourceMetric() after delete, got = %v, want %v",
					gotAfterDel, tt.wantAfterDelete)
			}
		})
	}
}

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
						ProcessorInfos: []util.ProcessorInfo{
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
						ProcessorInfos: []util.ProcessorInfo{
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
				ProcessorInfos: []util.ProcessorInfo{
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

func Test_metricCache_ContainerThrottledMetric_CRUD(t *testing.T) {
	now := time.Now()
	type args struct {
		config       *Config
		containerID  string
		aggregateArg AggregationType
		samples      map[time.Time]ContainerThrottledMetric
	}

	tests := []struct {
		name            string
		args            args
		want            ContainerThrottledQueryResult
		wantAfterDelete ContainerThrottledQueryResult
	}{
		{
			name: "container-throttled-metric-latest-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				containerID:  "container-id-1",
				aggregateArg: AggregationTypeLast,
				samples: map[time.Time]ContainerThrottledMetric{
					now.Add(-time.Second * 120): {
						ContainerID: "container-id-1",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.7,
						},
					},
					now.Add(-time.Second * 10): {
						ContainerID: "container-id-1",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.6,
						},
					},
					now.Add(-time.Second * 5): {
						ContainerID: "container-id-1",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.5,
						},
					},
					now.Add(-time.Second * 4): {
						ContainerID: "container-id-2",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.4,
						},
					},
				},
			},
			want: ContainerThrottledQueryResult{
				Metric: &ContainerThrottledMetric{
					ContainerID: "container-id-1",
					CPUThrottledMetric: &CPUThrottledMetric{
						ThrottledRatio: 0.5,
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 3}},
			},
			wantAfterDelete: ContainerThrottledQueryResult{
				Metric: &ContainerThrottledMetric{
					ContainerID: "container-id-1",
					CPUThrottledMetric: &CPUThrottledMetric{
						ThrottledRatio: 0.5,
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 2}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStorage()
			m := &metricCache{
				config: tt.args.config,
				db:     s,
			}
			for ts, sample := range tt.args.samples {
				err := m.InsertContainerThrottledMetrics(ts, &sample)
				if err != nil {
					t.Errorf("insert container metric failed %v", err)
				}
			}

			oldStartTime := time.Unix(0, 0)
			params := &QueryParam{
				Aggregate: tt.args.aggregateArg,
				Start:     &oldStartTime,
				End:       &now,
			}

			got := m.GetContainerThrottledMetric(&tt.args.containerID, params)
			if got.Error != nil {
				t.Errorf("get container metric failed %v", got.Error)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetContainerThrottledMetric() got = %v, want %v", got, tt.want)
			}
			// delete expire items
			m.recycleDB()

			gotAfterDel := m.GetContainerThrottledMetric(&tt.args.containerID, params)
			if gotAfterDel.Error != nil {
				t.Errorf("get container metric failed %v", gotAfterDel.Error)
			}
			if !reflect.DeepEqual(gotAfterDel, tt.wantAfterDelete) {
				t.Errorf("GetContainerThrottledMetric() after delete, got = %v, want %v",
					gotAfterDel, tt.wantAfterDelete)
			}
		})
	}
}

func Test_metricCache_PodThrottledMetric_CRUD(t *testing.T) {
	now := time.Now()
	type args struct {
		config       *Config
		podUID       string
		aggregateArg AggregationType
		samples      map[time.Time]PodThrottledMetric
	}

	tests := []struct {
		name            string
		args            args
		want            PodThrottledQueryResult
		wantAfterDelete PodThrottledQueryResult
	}{
		{
			name: "pod-throttled-metric-latest-crud",
			args: args{
				config: &Config{
					MetricGCIntervalSeconds: 60,
					MetricExpireSeconds:     60,
				},
				podUID:       "pod-uid-1",
				aggregateArg: AggregationTypeLast,
				samples: map[time.Time]PodThrottledMetric{
					now.Add(-time.Second * 120): {
						PodUID: "pod-uid-1",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.7,
						},
					},
					now.Add(-time.Second * 10): {
						PodUID: "pod-uid-1",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.6,
						},
					},
					now.Add(-time.Second * 5): {
						PodUID: "pod-uid-1",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.5,
						},
					},
					now.Add(-time.Second * 4): {
						PodUID: "pod-uid-2",
						CPUThrottledMetric: &CPUThrottledMetric{
							ThrottledRatio: 0.4,
						},
					},
				},
			},
			want: PodThrottledQueryResult{
				Metric: &PodThrottledMetric{
					PodUID: "pod-uid-1",
					CPUThrottledMetric: &CPUThrottledMetric{
						ThrottledRatio: 0.5,
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 3}},
			},
			wantAfterDelete: PodThrottledQueryResult{
				Metric: &PodThrottledMetric{
					PodUID: "pod-uid-1",
					CPUThrottledMetric: &CPUThrottledMetric{
						ThrottledRatio: 0.5,
					},
				},
				QueryResult: QueryResult{AggregateInfo: &AggregateInfo{MetricsCount: 2}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStorage()
			m := &metricCache{
				config: tt.args.config,
				db:     s,
			}
			for ts, sample := range tt.args.samples {
				err := m.InsertPodThrottledMetrics(ts, &sample)
				if err != nil {
					t.Errorf("insert pod metric failed %v", err)
				}
			}

			oldStartTime := time.Unix(0, 0)
			params := &QueryParam{
				Aggregate: tt.args.aggregateArg,
				Start:     &oldStartTime,
				End:       &now,
			}

			got := m.GetPodThrottledMetric(&tt.args.podUID, params)
			if got.Error != nil {
				t.Errorf("get pod metric failed %v", got.Error)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetPodThrottledMetric() got = %v, want %v", got, tt.want)
			}
			// delete expire items
			m.recycleDB()

			gotAfterDel := m.GetPodThrottledMetric(&tt.args.podUID, params)
			if gotAfterDel.Error != nil {
				t.Errorf("get pod metric failed %v", gotAfterDel.Error)
			}
			if !reflect.DeepEqual(gotAfterDel, tt.wantAfterDelete) {
				t.Errorf("GetPodThrottledMetric() after delete, got = %v, want %v",
					gotAfterDel, tt.wantAfterDelete)
			}
		})
	}
}

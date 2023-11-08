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

package hostapplication

import (
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_hostAppCollector_collectHostAppResUsed(t *testing.T) {
	testNow := time.Now()
	timeNow = func() time.Time {
		return testNow
	}
	testParentDir := "kubepods.slice/kubepods-besteffort.slice/test-host-app/"
	type fields struct {
		SetSysUtil   func(helper *system.FileTestUtil)
		getNodeSLO   *slov1alpha1.NodeSLO
		initLastStat func(lastState *gocache.Cache)
	}
	type wants struct {
		hostAppCPU    map[string]float64
		hostAppMemory map[string]float64
	}
	tests := []struct {
		name   string
		fields fields
		wants  wants
	}{
		{
			name: "nil node slo do nothing",
			fields: fields{
				getNodeSLO: nil,
			},
			wants: wants{},
		},
		{
			name: "host app metric with bad cpu format",
			fields: fields{
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.WriteCgroupFileContents(testParentDir, system.CPUAcctUsage, `bad format`)
					helper.WriteCgroupFileContents(testParentDir, system.MemoryStat, `
total_cache 104857600
total_rss 104857600
total_inactive_anon 104857600
total_active_anon 0
total_inactive_file 104857600
total_active_file 0
total_unevictable 0
`)
				},
				getNodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						HostApplications: []slov1alpha1.HostApplicationSpec{
							{
								Name: "test-host-app",
								CgroupPath: &slov1alpha1.CgroupPath{
									Base:         slov1alpha1.CgroupBaseTypeKubeBesteffort,
									RelativePath: "test-host-app/",
								},
							},
						},
					},
				},
				initLastStat: func(lastState *gocache.Cache) {
					lastState.Set("test-host-app", framework.CPUStat{
						CPUUsage:  0,
						Timestamp: testNow.Add(-time.Second),
					}, gocache.DefaultExpiration)
				},
			},
			wants: wants{},
		},
		{
			name: "host app metric ignore first cpu",
			fields: fields{
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.WriteCgroupFileContents(testParentDir, system.CPUAcctUsage, `1000000000`)
					helper.WriteCgroupFileContents(testParentDir, system.MemoryStat, `
total_cache 104857600
total_rss 104857600
total_inactive_anon 104857600
total_active_anon 0
total_inactive_file 104857600
total_active_file 0
total_unevictable 0
`)
				},
				getNodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						HostApplications: []slov1alpha1.HostApplicationSpec{
							{
								Name: "test-host-app",
								CgroupPath: &slov1alpha1.CgroupPath{
									Base:         slov1alpha1.CgroupBaseTypeKubeBesteffort,
									RelativePath: "test-host-app/",
								},
							},
						},
					},
				},
			},
			wants: wants{},
		},
		{
			name: "get host app metric",
			fields: fields{
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.WriteCgroupFileContents(testParentDir, system.CPUAcctUsage, `1000000000`)
					helper.WriteCgroupFileContents(testParentDir, system.MemoryStat, `
total_cache 104857600
total_rss 104857600
total_inactive_anon 104857600
total_active_anon 0
total_inactive_file 104857600
total_active_file 0
total_unevictable 0
`)
				},
				getNodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						HostApplications: []slov1alpha1.HostApplicationSpec{
							{
								Name: "test-host-app",
								CgroupPath: &slov1alpha1.CgroupPath{
									Base:         slov1alpha1.CgroupBaseTypeKubeBesteffort,
									RelativePath: "test-host-app/",
								},
							},
						},
					},
				},
				initLastStat: func(lastState *gocache.Cache) {
					lastState.Set("test-host-app", framework.CPUStat{
						CPUUsage:  0,
						Timestamp: testNow.Add(-time.Second),
					}, gocache.DefaultExpiration)
				},
			},
			wants: wants{
				hostAppCPU: map[string]float64{
					"test-host-app": 1,
				},
				hostAppMemory: map[string]float64{
					"test-host-app": 104857600,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.SetSysUtil != nil {
				tt.fields.SetSysUtil(helper)
			}

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
				TSDBPath:              t.TempDir(),
				TSDBEnablePromMetrics: false,
			})

			assert.NoError(t, err)
			defer func() {
				metricCache.Close()
			}()
			statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
			statesInformer.EXPECT().HasSynced().Return(true).AnyTimes()
			statesInformer.EXPECT().GetNodeSLO().Return(tt.fields.getNodeSLO).Times(1)

			collector := New(&framework.Options{
				Config: &framework.Config{
					CollectResUsedInterval: time.Second,
				},
				StatesInformer: statesInformer,
				MetricCache:    metricCache,
				CgroupReader:   resourceexecutor.NewCgroupReader(),
			})
			collector.Setup(&framework.Context{
				State: framework.NewSharedState(),
			})
			c := collector.(*hostAppCollector)
			if tt.fields.initLastStat != nil {
				tt.fields.initLastStat(c.lastAppCPUStat)
			}

			assert.NotPanics(t, func() {
				c.collectHostAppResUsed()
			})

			querier, err := metricCache.Querier(testNow.Add(-time.Minute), testNow)
			assert.NoError(t, err)

			for appName, wantCPU := range tt.wants.hostAppCPU {
				queryMeta, err := metriccache.HostAppCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.HostApplication(appName))
				assert.NoError(t, err)
				aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
				assert.NoError(t, querier.Query(queryMeta, nil, aggregateResult))
				gotCPU, err := aggregateResult.Value(metriccache.AggregationTypeLast)
				assert.NoError(t, err)
				assert.Equal(t, wantCPU, gotCPU)
			}
			for appName, wantMemory := range tt.wants.hostAppMemory {
				queryMeta, err := metriccache.HostAppMemoryUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.HostApplication(appName))
				assert.NoError(t, err)
				aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
				assert.NoError(t, querier.Query(queryMeta, nil, aggregateResult))
				gotMemory, err := aggregateResult.Value(metriccache.AggregationTypeLast)
				assert.NoError(t, err)
				assert.Equal(t, wantMemory, gotMemory)
			}
		})
	}
}

func Test_hostAppCollector_Started(t *testing.T) {
	type fields struct {
		started *atomic.Bool
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "return started true",
			fields: fields{
				started: atomic.NewBool(true),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &hostAppCollector{
				started: tt.fields.started,
			}
			if got := h.Started(); got != tt.want {
				t.Errorf("Started() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_hostAppCollector_Enabled(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{
			name: "default enabled is true",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &hostAppCollector{}
			if got := h.Enabled(); got != tt.want {
				t.Errorf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_hostAppCollector_Run(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	metricCacheCfg := metriccache.NewDefaultConfig()
	metricCacheCfg.TSDBEnablePromMetrics = false
	metricCacheCfg.TSDBPath = helper.TempDir
	metricCache, err := metriccache.NewMetricCache(metricCacheCfg)
	assert.NoError(t, err)
	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
	mockStatesInformer.EXPECT().HasSynced().Return(true).AnyTimes()
	mockStatesInformer.EXPECT().GetNodeSLO().Return(&slov1alpha1.NodeSLO{}).AnyTimes()
	c := New(&framework.Options{
		Config:         framework.NewDefaultConfig(),
		StatesInformer: mockStatesInformer,
		MetricCache:    metricCache,
		CgroupReader:   resourceexecutor.NewCgroupReader(),
	})
	collector := c.(*hostAppCollector)
	collector.started = atomic.NewBool(true)
	collector.Setup(&framework.Context{
		State: framework.NewSharedState(),
	})
	assert.True(t, collector.Enabled())
	assert.True(t, collector.Started())
	assert.NotPanics(t, func() {
		stopCh := make(chan struct{}, 1)
		collector.Run(stopCh)
		close(stopCh)
	})
}

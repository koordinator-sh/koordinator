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

package beresource

import (
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_collectBECPUResourceMetric(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(false)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := metriccache.NewDefaultConfig()
	cfg.TSDBPath = t.TempDir()
	cfg.TSDBEnablePromMetrics = false
	metricCache, err := metriccache.NewMetricCache(cfg)
	assert.NoError(t, err)
	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
	collector := beResourceCollector{
		collectInterval: 0,
		started:         atomic.NewBool(true),
		metricCache:     metricCache,
		statesInformer:  mockStatesInformer,
		cgroupReader:    resourceexecutor.NewCgroupReader(),
		lastBECPUStat:   &framework.CPUStat{},
	}

	// prepare be request, expect 1500 milliCores
	bePod := mockBEPod()
	lsPod := mockLSPod()
	mockStatesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{{Pod: bePod}, {Pod: lsPod}}).AnyTimes()
	mockStatesInformer.EXPECT().HasSynced().Return(true).AnyTimes()

	// prepare BECPUUsageCores data,expect 4 cores usage
	helper.WriteCgroupFileContents(util.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUAcctUsage, "12004000000000")

	// prepare limit data,expect 8 cores limit
	helper.WriteCgroupFileContents(util.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet, "1-15")
	helper.WriteCgroupFileContents(util.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUCFSQuota, "800000")
	helper.WriteCgroupFileContents(util.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUCFSPeriod, "100000")

	collector.lastBECPUStat = &framework.CPUStat{CPUUsage: 12000000000000, Timestamp: time.Now().Add(-1 * time.Second)}

	assert.NotPanics(t, func() {
		collector.collectBECPUResourceMetric()
	})

	oldStartTime := time.Unix(0, 0)
	now := time.Now()
	querier, err := collector.metricCache.Querier(oldStartTime, now)
	assert.NoError(t, err)

	beCPUUsageProperties := metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResouceAllocationUsage))
	beCPURequestProperties := metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResouceAllocationRequest))
	beCPURealLimitProperties := metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResouceAllocationRealLimit))

	beCPUUsageQueryMeta, err := metriccache.NodeBEMetric.BuildQueryMeta(beCPUUsageProperties)
	assert.NoError(t, err)
	beCPURequestQueryMeta, err := metriccache.NodeBEMetric.BuildQueryMeta(beCPURequestProperties)
	assert.NoError(t, err)
	beCPURealLimitQueryMeta, err := metriccache.NodeBEMetric.BuildQueryMeta(beCPURealLimitProperties)
	assert.NoError(t, err)

	beCPUUsageAggregateResult := metriccache.DefaultAggregateResultFactory.New(beCPUUsageQueryMeta)
	if err := querier.Query(beCPUUsageQueryMeta, nil, beCPUUsageAggregateResult); err != nil {
		assert.NoError(t, err)
	}

	beCPURequeestAggregateResult := metriccache.DefaultAggregateResultFactory.New(beCPURequestQueryMeta)
	if err := querier.Query(beCPURequestQueryMeta, nil, beCPURequeestAggregateResult); err != nil {
		assert.NoError(t, err)
	}

	beCPURealLimitAggregateResult := metriccache.DefaultAggregateResultFactory.New(beCPURealLimitQueryMeta)
	if err := querier.Query(beCPURealLimitQueryMeta, nil, beCPURealLimitAggregateResult); err != nil {
		assert.NoError(t, err)
	}

	// BECPUUsage
	currentBECPUMilliUsage, err := beCPUUsageAggregateResult.Value(metriccache.AggregationTypeLast)
	assert.NoError(t, err)
	// check roughly; tolerant 1 second error
	assert.True(t, currentBECPUMilliUsage > float64(3*1000) && currentBECPUMilliUsage < float64(5*1000), "checkUsage")
	// BECPURequest
	currentBECPUMilliRequest, _ := beCPURequeestAggregateResult.Value(metriccache.AggregationTypeLast)
	assert.NoError(t, err)
	assert.Equal(t, float64(1500), currentBECPUMilliRequest, "checkRequest")
	// BECPULimit
	currentBECPUMilliRealLimit, _ := beCPURealLimitAggregateResult.Value(metriccache.AggregationTypeLast)
	assert.NoError(t, err)
	assert.Equal(t, float64(8*1000), currentBECPUMilliRealLimit, "checkLimit")
}

func Test_getBECPUUsageMilliCores(t *testing.T) {
	tests := []struct {
		name                    string
		cpuacctUsage            string
		lastBeCPUStat           *framework.CPUStat
		expectCPUUsedMilliCores int64
		expectCurrentCPUUsage   uint64
		expectNil               bool
		expectError             bool
	}{
		{
			name:                    "test_get_first_time",
			cpuacctUsage:            "12000000000000\n",
			lastBeCPUStat:           nil,
			expectCPUUsedMilliCores: int64(0),
			expectCurrentCPUUsage:   12000000000000,
			expectNil:               true,
			expectError:             false,
		},
		{
			name:          "test_get_correct",
			cpuacctUsage:  "12004000000000\n",
			lastBeCPUStat: &framework.CPUStat{CPUUsage: 12000000000000},
			// expectCPUUsedCores:    resource.NewQuantity(4, resource.DecimalSI),
			expectCPUUsedMilliCores: int64(4000),
			expectCurrentCPUUsage:   12004000000000,
			expectNil:               false,
			expectError:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			helper.WriteCgroupFileContents(util.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUAcctUsage, tt.cpuacctUsage)

			collector := beResourceCollector{
				collectInterval: 0,
				started:         atomic.NewBool(true),
				cgroupReader:    resourceexecutor.NewCgroupReader(),
				lastBECPUStat:   &framework.CPUStat{},
			}
			if tt.lastBeCPUStat != nil {
				collector.lastBECPUStat = tt.lastBeCPUStat
				collector.lastBECPUStat.Timestamp = time.Now().Add(-1 * time.Second)
			}

			gotCPUUsedMilliCores, gotErr := collector.getBECPUUsageMilliCores()
			assert.Equal(t, tt.expectError, gotErr != nil, "checkError")
			if !tt.expectNil {
				assert.Equal(t, tt.expectCPUUsedMilliCores, gotCPUUsedMilliCores, "checkCPU")
			}
			assert.Equal(t, tt.expectCurrentCPUUsage, collector.lastBECPUStat.CPUUsage, "checkCPUUsage")
		})
	}
}

func Test_getBECPURealMilliLimit(t *testing.T) {
	tests := []struct {
		name        string
		cpuset      string
		cfsQuota    string
		expect      int
		UseCgroupV2 bool
	}{
		{
			name:        "test_suppress_by_cpuset",
			cpuset:      "1-2",
			cfsQuota:    "-1",
			expect:      2000,
			UseCgroupV2: false,
		},
		{
			name:        "test_suppress_by_cfsquota",
			cpuset:      "1-15",
			cfsQuota:    "800000",
			expect:      8000,
			UseCgroupV2: false,
		},
		{
			name:        "test_suppress_by_cfsquota-v2",
			cpuset:      "1-15",
			cfsQuota:    "800000",
			expect:      8000,
			UseCgroupV2: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			helper.SetCgroupsV2(tt.UseCgroupV2)
			defer helper.Cleanup()
			c := beResourceCollector{
				collectInterval: 0,
				started:         atomic.NewBool(true),
				cgroupReader:    resourceexecutor.NewCgroupReader(),
				lastBECPUStat:   &framework.CPUStat{},
			}

			BECgroupParentDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
			if tt.UseCgroupV2 {
				helper.WriteCgroupFileContents(BECgroupParentDir, system.CPUCFSQuotaV2, "800000 100000")
				helper.WriteCgroupFileContents(BECgroupParentDir, system.CPUSetEffectiveV2, tt.cpuset)
				helper.WriteCgroupFileContents(BECgroupParentDir, system.CPUCFSPeriodV2, "800000 100000")
			} else {
				helper.WriteCgroupFileContents(BECgroupParentDir, system.CPUCFSQuota, tt.cfsQuota)
				helper.WriteCgroupFileContents(BECgroupParentDir, system.CPUSet, tt.cpuset)
				helper.WriteCgroupFileContents(BECgroupParentDir, system.CPUCFSPeriod, "100000")
			}

			milliLimit, err := c.getBECPURealMilliLimit()
			assert.NoError(t, err)
			assert.Equal(t, tt.expect, milliLimit)
		})
	}
}

func Test_getBECPURequestMilliSum(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()
	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)

	bePod := mockBEPod()
	lsPod := mockLSPod()
	mockStatesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{{Pod: bePod}, {Pod: lsPod}}).AnyTimes()
	mockStatesInformer.EXPECT().HasSynced().Return(true).AnyTimes()

	c := beResourceCollector{
		collectInterval: 0,
		started:         atomic.NewBool(true),
		statesInformer:  mockStatesInformer,
	}
	beRequest := c.getBECPURequestMilliCores()
	assert.Equal(t, int64(1500), beRequest)
}

func mockBEPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-name-be",
			UID:       "test-pod-uid-be",
			Labels: map[string]string{
				apiext.LabelPodQoS: string(apiext.QoSBE),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-1",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							apiext.BatchCPU: *resource.NewQuantity(500, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							apiext.BatchCPU: *resource.NewQuantity(500, resource.DecimalSI),
						},
					},
				},
				{
					Name: "test-container-2",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							apiext.BatchCPU: *resource.NewQuantity(1000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							apiext.BatchCPU: *resource.NewQuantity(1000, resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}

func mockLSPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-name-ls",
			UID:       "test-pod-uid-ls",
			Labels: map[string]string{
				apiext.LabelPodQoS: string(apiext.QoSLS),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-1",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
				},
				{
					Name: "test-container-2",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}

func Test_beResourceCollector_Run(t *testing.T) {
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
	c := New(&framework.Options{
		Config:         framework.NewDefaultConfig(),
		StatesInformer: mockStatesInformer,
		MetricCache:    metricCache,
		CgroupReader:   resourceexecutor.NewCgroupReader(),
	})
	collector := c.(*beResourceCollector)
	collector.started = atomic.NewBool(true)
	collector.Setup(&framework.Context{})
	assert.True(t, collector.Enabled())
	assert.True(t, collector.Started())
	stopCh := make(chan struct{})
	defer close(stopCh)
	assert.NotPanics(t, func() {
		collector.Run(stopCh)
	})
}

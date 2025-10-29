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

package cpuevict

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/component-base/featuregate"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	critesting "k8s.io/cri-api/pkg/apis/testing"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	maframework "github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	qosmanagerUtil "github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/util"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime/handler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/utils"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_cpuEvict_Enabled(t *testing.T) {
	c := cpuEvictor{}
	type args struct {
		BECPUEvictEnabled bool
		CPUEvictEnabled   bool
		evictInterval     time.Duration
	}
	tests := []struct {
		name   string
		args   args
		expect bool
	}{
		{
			name: "BECPUEvictEnabled/CPUEvictEnabled=false",
			args: args{
				BECPUEvictEnabled: false,
				CPUEvictEnabled:   false,
				evictInterval:     10 * time.Second,
			},
			expect: false,
		},
		{
			name: "CPUEvictEnabled=true",
			args: args{
				BECPUEvictEnabled: false,
				CPUEvictEnabled:   true,
				evictInterval:     10 * time.Second,
			},
			expect: true,
		},
		{
			name: "BECPUEvictEnabled=true",
			args: args{
				BECPUEvictEnabled: true,
				CPUEvictEnabled:   false,
				evictInterval:     10 * time.Second,
			},
			expect: true,
		},
		{
			name: "evictInterval<0",
			args: args{
				BECPUEvictEnabled: false,
				CPUEvictEnabled:   true,
				evictInterval:     -10 * time.Second,
			},
			expect: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.evictInterval = tt.args.evictInterval
			defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, features.BECPUEvict, tt.args.BECPUEvictEnabled)()
			defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, features.CPUEvict, tt.args.CPUEvictEnabled)()
			assert.Equal(t, tt.expect, c.Enabled())
		})
	}

}

func Test_CPUEvict_calculateMilliReleaseBySatisfaction(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("60"),
				corev1.ResourceMemory: resource.MustParse("120Gi"),
				apiext.BatchCPU:       resource.MustParse("30000"),
				apiext.BatchMemory:    resource.MustParse("50Gi"),
			},
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("60"),
				corev1.ResourceMemory: resource.MustParse("120Gi"),
				apiext.BatchCPU:       resource.MustParse("30000"),
				apiext.BatchMemory:    resource.MustParse("50Gi"),
			},
		},
	}
	testNode1 := testNode.DeepCopy()
	testNode1.Status.Capacity[apiext.BatchCPU] = resource.MustParse("8000")
	testNode1.Status.Allocatable[apiext.BatchCPU] = resource.MustParse("8000")
	testNode2 := testNode.DeepCopy()
	testNode2.Status.Capacity[apiext.BatchCPU] = resource.MustParse("0")
	testNode2.Status.Allocatable[apiext.BatchCPU] = resource.MustParse("0")
	thresholdConfig := sloconfig.DefaultResourceThresholdStrategy()
	thresholdConfig.CPUEvictBESatisfactionUpperPercent = pointer.Int64(40)
	thresholdConfig.CPUEvictBESatisfactionLowerPercent = pointer.Int64(30)
	collectResUsedIntervalSeconds := int64(1)
	thresholdConfig1 := thresholdConfig.DeepCopy()
	thresholdConfig1.CPUEvictBEUsageThresholdPercent = pointer.Int64(50)
	thresholdConfig2 := thresholdConfig1.DeepCopy()
	thresholdConfig2.CPUEvictPolicy = slov1alpha1.EvictByAllocatablePolicy
	type queryResult struct {
		count        int
		cpuRealLimit float64
		cpuUsed      float64
		cpuRequest   float64
	}

	type Test struct {
		name                     string
		thresholdConfig          *slov1alpha1.ResourceThresholdStrategy
		avgMetricQueryResult     queryResult
		currentMetricQueryResult queryResult
		currentNode              *corev1.Node
		expectRelease            int64
	}

	tests := []Test{
		{
			name:            "test_avgMetricQueryResult_Error",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        0,
				cpuRealLimit: 0,
				cpuUsed:      0,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_Metric_nil",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        0,
				cpuRealLimit: 0,
				cpuUsed:      0,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_aggregateInfo_nil",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        0,
				cpuRealLimit: 0,
				cpuUsed:      0,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_count_not_enough",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        10,
				cpuRealLimit: 0,
				cpuUsed:      0,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_CPURealLimit_zero",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 0,
				cpuUsed:      20000,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_cpuUsage_not_enough",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 20000,
				cpuUsed:      10000,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_cpuRequest_zero",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 20000,
				cpuUsed:      19000,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_ResourceSatisfaction_enough",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 20000,
				cpuUsed:      19000,
				cpuRequest:   40000,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_need_release_but_currentMetricQueryResult_invalid",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      9500,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 0,
				cpuUsed:      0,
				cpuRequest:   0,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgMetricQueryResult_need_release_but_currentMetricQueryResult_cpuUsage_not_enough",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      9500,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      5 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 0,
		},
		{
			name:            "test_avgRelease>currentRelease",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      9500,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 14 * 1000,
				cpuUsed:      13 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 6 * 1000,
		},
		{
			name:            "test_avgRelease<currentRelease",
			thresholdConfig: thresholdConfig,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 13 * 1000,
				cpuUsed:      12 * 1000,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 11 * 1000,
				cpuUsed:      10 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 7 * 1000,
		},
		{
			name:            fmt.Sprintf("test_BEUsageThresholdPercent_%d_avgMetricQueryResult_cpuUsage_not_enough", *thresholdConfig1.CPUEvictBEUsageThresholdPercent),
			thresholdConfig: thresholdConfig1,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 20 * 1000,
				cpuUsed:      9 * 1000,
			},
			expectRelease: 0,
		},
		{
			name:            fmt.Sprintf("test_BEUsageThresholdPercent_%d_avgMetricQueryResult_need_release_but_currentMetricQueryResult_cpuUsage_not_enough", *thresholdConfig1.CPUEvictBEUsageThresholdPercent),
			thresholdConfig: thresholdConfig1,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      6 * 1000,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      4 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 0,
		},
		{
			name:            fmt.Sprintf("test_BEUsageThresholdPercent_%d_avgRelease>currentRelease", *thresholdConfig1.CPUEvictBEUsageThresholdPercent),
			thresholdConfig: thresholdConfig1,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      6 * 1000,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 14 * 1000,
				cpuUsed:      8 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 6 * 1000,
		},
		{
			name:            fmt.Sprintf("test_BEUsageThresholdPercent_%d_avgMetricQueryResult_skip_release_when_allocatable_small_but_policy_by_reallimit", *thresholdConfig1.CPUEvictBEUsageThresholdPercent),
			thresholdConfig: thresholdConfig1,
			currentNode:     testNode1,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      6 * 1000,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      4 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 0,
		},
		{
			name:            fmt.Sprintf("test_BEUsageThresholdPercent_%d_avgMetricQueryResult_need_release_since_allocatable_is_small", *thresholdConfig1.CPUEvictBEUsageThresholdPercent),
			thresholdConfig: thresholdConfig2,
			currentNode:     testNode1,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      6 * 1000,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      4 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 12 * 1000,
		},
		{
			name:            fmt.Sprintf("test_BEUsageThresholdPercent_%d_avgMetricQueryResult_need_release_since_allocatable_is_zero", *thresholdConfig1.CPUEvictBEUsageThresholdPercent),
			thresholdConfig: thresholdConfig2,
			currentNode:     testNode2,
			avgMetricQueryResult: queryResult{
				count:        59,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      6 * 1000,
				cpuRequest:   50 * 1000,
			},
			currentMetricQueryResult: queryResult{
				count:        1,
				cpuRealLimit: 10 * 1000,
				cpuUsed:      4 * 1000,
				cpuRequest:   50 * 1000,
			},
			expectRelease: 20*1000 - defaultMinAllocatableBatchMilliCPU,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			mockStateInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			if tt.currentNode != nil {
				mockStateInformer.EXPECT().GetNode().Return(tt.currentNode).AnyTimes()
			} else {
				mockStateInformer.EXPECT().GetNode().Return(testNode).AnyTimes()
			}
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			beUsage := metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationUsage))
			beRequest := metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationRequest))
			beLimit := metriccache.MetricPropertiesFunc.NodeBE(string(metriccache.BEResourceCPU), string(metriccache.BEResourceAllocationRealLimit))

			beUsageQueryMeta, err := metriccache.NodeBEMetric.BuildQueryMeta(beUsage)
			assert.NoError(t, err)
			beRequestQueryMeta, err := metriccache.NodeBEMetric.BuildQueryMeta(beRequest)
			assert.NoError(t, err)
			beLimitQueryMeta, err := metriccache.NodeBEMetric.BuildQueryMeta(beLimit)
			assert.NoError(t, err)

			// mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()
			result := buildMockQueryResultAndCount(ctl, mockQuerier, mockResultFactory, beUsageQueryMeta)
			result.EXPECT().Value(metriccache.AggregationTypeAVG).Return(tt.avgMetricQueryResult.cpuUsed, nil).Times(1)
			result.EXPECT().Count().Return(tt.avgMetricQueryResult.count).Times(1)
			if tt.avgMetricQueryResult.count >= 59 && tt.currentMetricQueryResult.count > 0 {
				result.EXPECT().Value(metriccache.AggregationTypeLast).Return(tt.currentMetricQueryResult.cpuUsed, nil).Times(1)
				result.EXPECT().Count().Return(tt.currentMetricQueryResult.count).Times(1)
			}
			result = buildMockQueryResultAndCount(ctl, mockQuerier, mockResultFactory, beRequestQueryMeta)
			result.EXPECT().Value(metriccache.AggregationTypeAVG).Return(tt.avgMetricQueryResult.cpuRequest, nil).Times(1)
			result.EXPECT().Count().Return(tt.avgMetricQueryResult.count).Times(1)
			if tt.avgMetricQueryResult.count >= 59 && tt.currentMetricQueryResult.count > 0 {
				result.EXPECT().Value(metriccache.AggregationTypeLast).Return(tt.currentMetricQueryResult.cpuRequest, nil).Times(1)
				result.EXPECT().Count().Return(tt.currentMetricQueryResult.count).Times(1)
			}
			result = buildMockQueryResultAndCount(ctl, mockQuerier, mockResultFactory, beLimitQueryMeta)
			result.EXPECT().Value(metriccache.AggregationTypeAVG).Return(tt.avgMetricQueryResult.cpuRealLimit, nil).Times(1)
			result.EXPECT().Count().Return(tt.avgMetricQueryResult.count).Times(1)
			if tt.avgMetricQueryResult.count >= 59 && tt.currentMetricQueryResult.count > 0 {
				result.EXPECT().Value(metriccache.AggregationTypeLast).Return(tt.currentMetricQueryResult.cpuRealLimit, nil).Times(1)
				result.EXPECT().Count().Return(tt.currentMetricQueryResult.count).Times(1)
			}
			c := cpuEvictor{
				statesInformer:        mockStateInformer,
				metricCache:           mockMetricCache,
				metricCollectInterval: time.Duration(collectResUsedIntervalSeconds) * time.Second,
			}
			gotRelease := c.calculateMilliReleaseBySatisfaction(tt.thresholdConfig, 0)
			assert.Equal(t, tt.expectRelease, gotRelease, "checkRelease")
		})
	}
}

func Test_getBEPodEvictInfoAndSort(t *testing.T) {
	type podMetricSample struct {
		UID     string
		CPUUsed float64
	}

	type BECPUResourceMetric struct {
		CPUUsed      resource.Quantity // cpuUsed cores for BestEffort Cgroup
		CPURealLimit resource.Quantity // suppressCPUQuantity: if suppress by cfs_quota then this  value is cfs_quota/cfs_period
		CPURequest   resource.Quantity // sum(extendResources_Cpu:request) by all qos:BE pod
	}

	tests := []struct {
		name       string
		podMetrics []podMetricSample
		pods       []*corev1.Pod
		beMetric   BECPUResourceMetric
		expect     []*podEvictCPUInfo
	}{
		{
			name: "test_sort",
			podMetrics: []podMetricSample{
				{UID: "pod_lsr", CPUUsed: 12},
				{UID: "pod_ls", CPUUsed: 12},
				{UID: "pod_be_1_priority100", CPUUsed: 3},
				{UID: "pod_be_2_priority100", CPUUsed: 4},
				{UID: "pod_be_3_priority10", CPUUsed: 4},
			},
			pods: []*corev1.Pod{
				mockNonBEPodForCPUEvict("pod_lsr", apiext.QoSLSR, 16*1000),
				mockNonBEPodForCPUEvict("pod_ls", apiext.QoSLS, 16*1000),
				mockBEPodForCPUEvict("pod_be_1_priority100", 16*1000, 100),
				mockBEPodForCPUEvict("pod_be_2_priority100", 16*1000, 100),
				mockBEPodForCPUEvict("pod_be_3_priority10", 16*1000, 10),
			},
			beMetric: BECPUResourceMetric{
				CPUUsed:    *resource.NewMilliQuantity(11*1000, resource.DecimalSI),
				CPURequest: *resource.NewMilliQuantity(48*1000, resource.DecimalSI),
			},
			expect: []*podEvictCPUInfo{
				{
					pod:            mockBEPodForCPUEvict("pod_be_3_priority10", 16*1000, 10),
					milliRequest:   16 * 1000,
					milliUsedCores: 4 * 1000,
					cpuUsage:       float64(4*1000) / float64(16*1000),
				},
				{
					pod:            mockBEPodForCPUEvict("pod_be_2_priority100", 16*1000, 100),
					milliRequest:   16 * 1000,
					milliUsedCores: 4 * 1000,
					cpuUsage:       float64(4*1000) / float64(16*1000),
				},
				{
					pod:            mockBEPodForCPUEvict("pod_be_1_priority100", 16*1000, 100),
					milliRequest:   16 * 1000,
					milliUsedCores: 3 * 1000,
					cpuUsage:       float64(3*1000) / float64(16*1000),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()

			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.pods)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, podMetric := range tt.podMetrics {
				podQueryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				buildMockQueryResult(ctl, mockQuerier, mockResultFactory, podQueryMeta, podMetric.CPUUsed)
			}
			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			c := New(opt)
			cpuEvictor := c.(*cpuEvictor)
			got := cpuEvictor.getBEPodEvictInfoAndSort(nil)
			assert.Equal(t, len(tt.expect), len(got), "checkLen")
			for i, expectPodInfo := range tt.expect {
				gotPodInfo := got[i]
				assert.Equal(t, expectPodInfo.pod.UID, gotPodInfo.Pod.UID, "checkPodID")
				assert.Equal(t, fmt.Sprintf("%.2f", expectPodInfo.cpuUsage), fmt.Sprintf("%.2f", gotPodInfo.CpuUsage), "checkCpuUsage")
				assert.Equal(t, expectPodInfo.milliRequest, gotPodInfo.MilliCPURequest, "checkMilliRequest")
				assert.Equal(t, expectPodInfo.milliUsedCores, gotPodInfo.MilliCPUUsed, "checkMilliUsedCores")
			}
		})
	}
}

func Test_isSatisfactionConfigValid(t *testing.T) {
	tests := []struct {
		name            string
		thresholdConfig *slov1alpha1.ResourceThresholdStrategy
		expectErr       error
	}{
		{
			name:            "nil thresholdConfig",
			thresholdConfig: nil,
			expectErr:       fmt.Errorf("ResourceThresholdStrategy not config"),
		},
		{
			name:            "test_nil",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{},
			expectErr:       fmt.Errorf("CPUEvictBESatisfactionLowerPercent or CPUEvictBESatisfactionUpperPercent not config"),
		},
		{
			name:            "test_lowPercent_invalid",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(0), CPUEvictBESatisfactionUpperPercent: pointer.Int64(50)},
			expectErr:       fmt.Errorf("CPUEvictBESatisfactionLowerPercent(0) is not valid! must (0,60]"),
		},
		{
			name:            "test_upperPercent_invalid",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(30), CPUEvictBESatisfactionUpperPercent: pointer.Int64(100)},
			expectErr:       fmt.Errorf("CPUEvictBESatisfactionUpperPercent(100) is not valid,must (0,100)"),
		},
		{
			name:            "test_lowPercent>upperPercent",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(40), CPUEvictBESatisfactionUpperPercent: pointer.Int64(30)},
			expectErr:       fmt.Errorf("CPUEvictBESatisfactionUpperPercent(30) < CPUEvictBESatisfactionLowerPercent(40)"),
		},
		{
			name:            "test_valid",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(30), CPUEvictBESatisfactionUpperPercent: pointer.Int64(40)},
			expectErr:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := isSatisfactionConfigValid(tt.thresholdConfig)
			assert.Equal(t, tt.expectErr, err)
		})
	}
}

func Test_isUsedThresholdConfigValid(t *testing.T) {
	tests := []struct {
		name            string
		thresholdConfig *slov1alpha1.ResourceThresholdStrategy
		expectErr       error
	}{
		{
			name:            "ResourceThresholdStrategy nil",
			thresholdConfig: nil,
			expectErr:       fmt.Errorf("ResourceThresholdStrategy not config"),
		},
		{
			name: "CPUEvictThresholdPercent nil",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				CPUEvictThresholdPercent: nil,
			},
			expectErr: fmt.Errorf("CPUEvictThresholdPercent not config"),
		},
		{
			name: "CPUEvictThresholdPercent < 0",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				CPUEvictThresholdPercent: pointer.Int64(-1),
			},
			expectErr: fmt.Errorf("threshold percent(-1) should greater than 0"),
		},
		{
			name: "CPUEvictLowerPercent == nil",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				CPUEvictThresholdPercent:      pointer.Int64(20),
				EvictEnabledPriorityThreshold: pointer.Int32(0),
			},
			expectErr: nil,
		},
		{
			name: "CPUEvictLowerPercent > CPUEvictThresholdPercent",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				CPUEvictThresholdPercent:      pointer.Int64(20),
				CPUEvictLowerPercent:          pointer.Int64(30),
				EvictEnabledPriorityThreshold: nil,
			},
			expectErr: fmt.Errorf("lower percent(30) should less than threshold percent(20)"),
		},
		{
			name: "EvictEnabledPriorityThreshold nil",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				CPUEvictThresholdPercent:      pointer.Int64(20),
				CPUEvictLowerPercent:          pointer.Int64(10),
				EvictEnabledPriorityThreshold: nil,
			},
			expectErr: fmt.Errorf("EvictEnabledPriorityThreshold not config"),
		},
		{
			name: "nil error",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				CPUEvictThresholdPercent:      pointer.Int64(20),
				CPUEvictLowerPercent:          pointer.Int64(10),
				EvictEnabledPriorityThreshold: pointer.Int32(0),
			},
			expectErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := isUsedThresholdConfigValid(tt.thresholdConfig)
			assert.Equal(t, tt.expectErr, err)
		})
	}
}

func mockBEPodForCPUEvict(name string, request int64, priority int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      name,
			UID:       types.UID(name),
			Labels: map[string]string{
				apiext.LabelPodQoS: string(apiext.QoSBE),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: fmt.Sprintf("%s_%s", name, "main"),
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							apiext.BatchCPU: *resource.NewQuantity(request, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							apiext.BatchCPU: *resource.NewQuantity(request, resource.DecimalSI),
						},
					},
				},
			},
			Priority: &priority,
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        fmt.Sprintf("%s_%s", name, "main"),
					ContainerID: fmt.Sprintf("docker://%s_%s", name, "main"),
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{StartedAt: metav1.Time{Time: time.Now()}},
					},
				},
			},
		},
	}
}

func mockNonBEPodForCPUEvict(name string, qosClass apiext.QoSClass, request int64) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      name,
			UID:       types.UID(name),
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qosClass),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: fmt.Sprintf("%s_%s", name, "main"),
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(request, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(request, resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}

func buildMockQueryResult(ctrl *gomock.Controller, querier *mock_metriccache.MockQuerier, factory *mock_metriccache.MockAggregateResultFactory,
	queryMeta metriccache.MetricMeta, value float64) {
	result := mock_metriccache.NewMockAggregateResult(ctrl)
	result.EXPECT().Value(gomock.Any()).Return(value, nil).AnyTimes()
	result.EXPECT().Count().Return(1).AnyTimes()
	factory.EXPECT().New(queryMeta).Return(result).AnyTimes()
	querier.EXPECT().QueryAndClose(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().Query(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().Close().AnyTimes()
}

func buildMockQueryResultAndCount(ctrl *gomock.Controller, querier *mock_metriccache.MockQuerier, factory *mock_metriccache.MockAggregateResultFactory,
	queryMeta metriccache.MetricMeta) *mock_metriccache.MockAggregateResult {
	result := mock_metriccache.NewMockAggregateResult(ctrl)
	factory.EXPECT().New(queryMeta).Return(result).AnyTimes()
	querier.EXPECT().QueryAndClose(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().Query(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().Close().AnyTimes()
	return result
}

func Test_calculateReleaseByUsedThresholdPercent(t *testing.T) {
	tests := []struct {
		name            string
		node            *corev1.Node
		nodeCPUUsed     resource.Quantity
		thresholdConfig *slov1alpha1.ResourceThresholdStrategy
		expectRelease   int64
	}{
		{
			name:        "test_cpuevict_CPUEvictThresholdPercent_lower78",
			node:        testutil.MockTestNode("100", "120G"),
			nodeCPUUsed: resource.MustParse("81"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        pointer.Bool(true),
				CPUEvictThresholdPercent:      pointer.Int64(80),
				EvictEnabledPriorityThreshold: pointer.Int32(3000),
			},
			expectRelease: 3000,
		},
		{
			name:        "test_cpuevict_CPUEvictThresholdPercent_lower80",
			node:        testutil.MockTestNode("100", "120G"),
			nodeCPUUsed: resource.MustParse("81"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        pointer.Bool(true),
				CPUEvictThresholdPercent:      pointer.Int64(80),
				CPUEvictLowerPercent:          pointer.Int64(80),
				EvictEnabledPriorityThreshold: pointer.Int32(3000),
			},
			expectRelease: 1000,
		},
		{
			name:        "lower than threshold",
			node:        testutil.MockTestNode("100", "120G"),
			nodeCPUUsed: resource.MustParse("11"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        pointer.Bool(true),
				CPUEvictThresholdPercent:      pointer.Int64(80),
				CPUEvictLowerPercent:          pointer.Int64(80),
				EvictEnabledPriorityThreshold: pointer.Int32(3000),
			},
			expectRelease: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			nodeCPUQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			result := mock_metriccache.NewMockAggregateResult(ctl)
			result.EXPECT().Value(gomock.Any()).Return(float64(tt.nodeCPUUsed.Value()), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			mockResultFactory.EXPECT().New(nodeCPUQueryMeta).Return(result).AnyTimes()
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockQuerier.EXPECT().QueryAndClose(nodeCPUQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			stop := make(chan struct{})
			evictor := qosmanagerUtil.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			evictor.Start(stop)
			defer func() { stop <- struct{}{} }()

			runtime.DockerHandler = handler.NewFakeRuntimeHandler()

			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			c := New(opt)
			CPUEvictor := c.(*cpuEvictor)
			CPUEvictor.Setup(&framework.Context{Evictor: evictor, OnlyEvictByAPI: true})
			CPUEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
			nodeMilliCPUCapacity := tt.node.Status.Capacity.Cpu().MilliValue()
			res := CPUEvictor.calculateMilliReleaseByUsedThresholdPercent(tt.thresholdConfig, nodeMilliCPUCapacity)
			assert.Equal(t, tt.expectRelease, res)
		})
	}
}

type podCPUSample struct {
	UID     string
	CpuUsed resource.Quantity
	BEInfo  *struct {
		ResourceAllocationUsage     resource.Quantity
		ResourceAllocationRequest   resource.Quantity
		ResourceAllocationRealLimit resource.Quantity
	}
}

func Test_cpuEvict(t *testing.T) {
	pod0 := createCPUEvictTestPodWithLabels("test_failed_pod", apiext.QoSLSR, 1000, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "1000"}, corev1.PodFailed)
	pod1 := createCPUEvictTestPodWithLabels("test_evictDisabled_pod", apiext.QoSLSR, 1000, map[string]string{apiext.LabelPodPriority: "1000"}, corev1.PodRunning)
	pod2 := createCPUEvictTestPodWithLabels("test_higher_priority", apiext.QoSLS, 3400, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "1000"}, corev1.PodRunning)
	pod3 := createCPUEvictTestPodWithLabels("test_podPriority_120_2000", apiext.QoSNone, 120, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "2000"}, corev1.PodRunning)
	pod4 := createCPUEvictTestPodWithLabels("test_podPriority_120_3000", apiext.QoSBE, 120, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "3000"}, corev1.PodRunning)
	pod5 := createCPUEvictTestPodWithLabels("test_podPriority_120_4000", apiext.QoSBE, 120, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "4000"}, corev1.PodRunning)
	pod6 := createCPUEvictTestPodWithLabels("test_podPriority_100_9999", apiext.QoSBE, 100, map[string]string{apiext.LabelPodEvictEnabled: "true"}, corev1.PodRunning)
	pod7 := createCPUEvictTestPodWithLabels("test_podPriority_100_9999_1", apiext.QoSBE, 100, map[string]string{apiext.LabelPodEvictEnabled: "true"}, corev1.PodRunning)

	tests := []struct {
		name              string
		triggerFeatures   []featuregate.Feature
		node              *corev1.Node
		pods              []*corev1.Pod
		podMetrics        []podCPUSample
		thresholdConfig   *slov1alpha1.ResourceThresholdStrategy
		nodeCPUUsed       resource.Quantity
		customEvictor     bool
		expectEvictedPods []*corev1.Pod
	}{
		{
			name: "invalid nodeCapacity",
			node: testutil.MockTestNode("0", "120G"),
		},
		{
			name:            "no thresholdConfig",
			node:            testutil.MockTestNode("100", "120G"),
			thresholdConfig: nil,
		},
		{
			name:            "disable",
			node:            testutil.MockTestNode("100", "120G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{Enable: pointer.Bool(false)},
		},
		{
			name:            "only feature: CPUEvict",
			triggerFeatures: []featuregate.Feature{features.CPUEvict},
			node:            testutil.MockTestNode("100", "120G"),
			nodeCPUUsed:     resource.MustParse("81"),
			pods:            []*corev1.Pod{pod0, pod1, pod2, pod3, pod4, pod5, pod6, pod7},
			podMetrics: []podCPUSample{
				{UID: "test_evictDisabled_pod", CpuUsed: resource.MustParse("4")},
				{UID: "test_higher_priority", CpuUsed: resource.MustParse("3")},
				{UID: "test_podPriority_120_2000", CpuUsed: resource.MustParse("1")},
				{UID: "test_podPriority_120_3000", CpuUsed: resource.MustParse("5")},
				{UID: "test_podPriority_120_4000", CpuUsed: resource.MustParse("2")},
				{UID: "test_podPriority_100_9999", CpuUsed: resource.MustParse("1")},
				{UID: "test_podPriority_100_9999_1", CpuUsed: resource.MustParse("2")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        pointer.Bool(true),
				CPUEvictThresholdPercent:      pointer.Int64(78),
				EvictEnabledPriorityThreshold: pointer.Int32(3000),
			},
			expectEvictedPods: []*corev1.Pod{pod3, pod4, pod6, pod7},
			customEvictor:     true,
		},
		{
			name:            "feature CPUEvict ok, BECPUEvict invalid configuration",
			triggerFeatures: []featuregate.Feature{features.CPUEvict, features.BECPUEvict},
			node:            testutil.MockTestNode("100", "120G"),
			nodeCPUUsed:     resource.MustParse("81"),
			pods:            []*corev1.Pod{pod0, pod1, pod2, pod3, pod4, pod5, pod6, pod7},
			podMetrics: []podCPUSample{
				{UID: "test_evictDisabled_pod", CpuUsed: resource.MustParse("4")},
				{UID: "test_higher_priority", CpuUsed: resource.MustParse("3")},
				{UID: "test_podPriority_120_2000", CpuUsed: resource.MustParse("1")},
				{UID: "test_podPriority_120_3000", CpuUsed: resource.MustParse("5")},
				{UID: "test_podPriority_120_4000", CpuUsed: resource.MustParse("2")},
				{UID: "test_podPriority_100_9999", CpuUsed: resource.MustParse("1")},
				{UID: "test_podPriority_100_9999_1", CpuUsed: resource.MustParse("2")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        pointer.Bool(true),
				CPUEvictThresholdPercent:      pointer.Int64(78),
				EvictEnabledPriorityThreshold: pointer.Int32(3000),
			},
			expectEvictedPods: []*corev1.Pod{pod3, pod4, pod6, pod7},
			customEvictor:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, f := range tt.triggerFeatures {
				defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, f, true)()
			}
			evictedPod := make(map[string]bool)
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			executor := qosmanagerUtil.NewMockEvictionExecutor(ctl)
			executor.EXPECT().
				Evict(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				AnyTimes().
				DoAndReturn(func(pod *corev1.Pod, node *corev1.Node, releaseReason, message string) bool {
					uid := string(pod.UID)
					evictedPod[uid] = true
					return true
				})

			executor.EXPECT().
				IsPodEvicted(gomock.Any()).
				AnyTimes().
				DoAndReturn(func(pod *corev1.Pod) bool {
					return evictedPod[string(pod.UID)]
				})

			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetNode().Return(tt.node).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeSLO().Return(testutil.GetNodeSLOByThreshold(tt.thresholdConfig)).AnyTimes()
			mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.pods)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			nodeCPUQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			result := mock_metriccache.NewMockAggregateResult(ctl)
			result.EXPECT().Value(gomock.Any()).Return(float64(tt.nodeCPUUsed.Value()), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			mockResultFactory.EXPECT().New(nodeCPUQueryMeta).Return(result).AnyTimes()
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockQuerier.EXPECT().QueryAndClose(nodeCPUQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, podMetric := range tt.podMetrics {
				result := mock_metriccache.NewMockAggregateResult(ctl)
				result.EXPECT().Value(gomock.Any()).Return(float64(podMetric.CpuUsed.Value()), nil).AnyTimes()
				result.EXPECT().Count().Return(1).AnyTimes()
				podQueryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				mockResultFactory.EXPECT().New(podQueryMeta).Return(result).AnyTimes()
				mockQuerier.EXPECT().QueryAndClose(podQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
				if podMetric.BEInfo == nil {
					continue
				}
			}

			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			stop := make(chan struct{})
			evictor := qosmanagerUtil.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			evictor.Start(stop)
			defer func() { stop <- struct{}{} }()

			qosmanagerUtil.SetCustomEvictionExecutorInitializer(func(*qosmanagerUtil.Evictor, bool) qosmanagerUtil.EvictionExecutor {
				return executor
			})

			runtime.DockerHandler = handler.NewFakeRuntimeHandler()
			var containers []*critesting.FakeContainer
			for _, pod := range tt.pods {
				_, err := client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err, "createPod ERROR!")
				for _, containerStatus := range pod.Status.ContainerStatuses {
					_, containerId, _ := util.ParseContainerId(containerStatus.ContainerID)
					fakeContainer := &critesting.FakeContainer{
						SandboxID:       string(pod.UID),
						ContainerStatus: runtimeapi.ContainerStatus{Id: containerId},
					}
					containers = append(containers, fakeContainer)
				}
			}
			runtime.DockerHandler.(*handler.FakeRuntimeHandler).SetFakeContainers(containers)

			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			m := New(opt)
			CPUEvictor := m.(*cpuEvictor)
			CPUEvictor.Setup(&framework.Context{Evictor: evictor, OnlyEvictByAPI: true})
			CPUEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
			CPUEvictor.cpuEvict()
			for _, pod := range tt.expectEvictedPods {
				assert.True(t, CPUEvictor.evictExecutor.IsPodEvicted(pod), "pod %v should be evicted", pod.Name)
			}
		})
	}
}

func createCPUEvictTestPodWithLabels(name string, qosClass apiext.QoSClass, priority int32, labels map[string]string, phase corev1.PodPhase) *corev1.Pod {
	pod := createCPUEvictTestPod(name, qosClass, priority)
	pod.Labels = utils.MergeMap(pod.Labels, labels)
	pod.Status.Phase = phase
	return pod
}
func createCPUEvictTestPod(name string, qosClass apiext.QoSClass, priority int32) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(name),
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qosClass),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: fmt.Sprintf("%s_%s", name, "main"),
				},
			},
			Priority: &priority,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        fmt.Sprintf("%s_%s", name, "main"),
					ContainerID: fmt.Sprintf("docker://%s_%s", name, "main"),
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{StartedAt: metav1.Time{Time: time.Now()}},
					},
				},
			},
		},
	}
}

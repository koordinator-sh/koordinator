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

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers/copilot"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	critesting "k8s.io/cri-api/pkg/apis/testing"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	maframework "github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime/handler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

// TODO: unit test for cpuEvict() to improve coverage
func Test_cpuEvict(t *testing.T) {}

func Test_CPUEvict_calculateMilliRelease(t *testing.T) {
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
	windowSize := int64(60)
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
			gotRelease := c.calculateMilliRelease(tt.thresholdConfig, windowSize)
			assert.Equal(t, tt.expectRelease, gotRelease, "checkRelease")
		})
	}
}

func Test_getPodEvictInfoAndSort(t *testing.T) {
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
			got := cpuEvictor.getPodEvictInfoAndSort()
			assert.Equal(t, len(tt.expect), len(got), "checkLen")
			for i, expectPodInfo := range tt.expect {
				gotPodInfo := got[i]
				assert.Equal(t, expectPodInfo.pod.UID, gotPodInfo.pod.UID, "checkPodID")
				assert.Equal(t, fmt.Sprintf("%.2f", expectPodInfo.cpuUsage), fmt.Sprintf("%.2f", gotPodInfo.cpuUsage), "checkCpuUsage")
				assert.Equal(t, expectPodInfo.milliRequest, gotPodInfo.milliRequest, "checkMilliRequest")
				assert.Equal(t, expectPodInfo.milliUsedCores, gotPodInfo.milliUsedCores, "checkMilliUsedCores")
			}
		})
	}
}

func Test_killAndEvictBEPodsRelease(t *testing.T) {
	podEvictInfosSorted := []*podEvictCPUInfo{
		{
			pod:          mockBEPodForCPUEvict("pod_be_3_priority10", 16*1000, 10),
			milliRequest: 16 * 1000,
		},
		{
			pod:          mockBEPodForCPUEvict("pod_be_2_priority100", 16*1000, 100),
			milliRequest: 16 * 1000,
		},
		{
			pod:          mockBEPodForCPUEvict("pod_be_1_priority100", 16*1000, 100),
			milliRequest: 16 * 1000,
		},
	}

	ctl := gomock.NewController(t)
	defer ctl.Finish()

	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()

	stop := make(chan struct{})
	evictor := framework.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
	evictor.Start(stop)
	defer func() { stop <- struct{}{} }()

	node := testutil.MockTestNode("100", "500G")
	runtime.DockerHandler = handler.NewFakeRuntimeHandler()
	// create pod
	var containers []*critesting.FakeContainer
	for _, podInfo := range podEvictInfosSorted {
		pod := podInfo.pod
		_, _ = client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
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

	cpuEvictor := &cpuEvictor{
		evictor:       evictor,
		lastEvictTime: time.Now().Add(-5 * time.Minute),
	}

	cpuEvictor.killAndEvictBEPodsRelease(node, podEvictInfosSorted, 18*1000)

	// evict subresource will not be creat or update in client go testing, check evict object
	// https://github.com/kubernetes/client-go/blob/v0.28.7/testing/fixture.go#L117

	// evict by API is false, so podsEvicted cache will not record killed pod
	assert.False(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[0].pod))
	assert.False(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[1].pod))
	assert.False(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[2].pod))
}

func Test_killAndEvictBEPodsReleaseWithEvictionAPIOnly(t *testing.T) {
	podEvictInfosSorted := []*podEvictCPUInfo{
		{
			pod:          mockBEPodForCPUEvict("pod_be_3_priority10", 16*1000, 10),
			milliRequest: 16 * 1000,
		},
		{
			pod:          mockBEPodForCPUEvict("pod_be_2_priority100", 16*1000, 100),
			milliRequest: 16 * 1000,
		},
		{
			pod:          mockBEPodForCPUEvict("pod_be_1_priority100", 16*1000, 100),
			milliRequest: 16 * 1000,
		},
	}

	ctl := gomock.NewController(t)
	defer ctl.Finish()

	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()

	stop := make(chan struct{})
	evictor := framework.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
	evictor.Start(stop)
	defer func() { stop <- struct{}{} }()

	node := testutil.MockTestNode("100", "500G")
	runtime.DockerHandler = handler.NewFakeRuntimeHandler()
	// create pod
	var containers []*critesting.FakeContainer
	for _, podInfo := range podEvictInfosSorted {
		pod := podInfo.pod
		_, _ = client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
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

	cpuEvictor := &cpuEvictor{
		evictor:        evictor,
		onlyEvictByAPI: true,
		lastEvictTime:  time.Now().Add(-5 * time.Minute),
	}

	cpuEvictor.killAndEvictBEPodsRelease(node, podEvictInfosSorted, 18*1000)

	// evict subresource will not be creat or update in client go testing, check evict object
	// https://github.com/kubernetes/client-go/blob/v0.28.7/testing/fixture.go#L117
	assert.True(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[0].pod))
	assert.True(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[1].pod))
	assert.False(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[2].pod))
}

func Test_killAndEvictYarnContainerWithCopilotAgent(t *testing.T) {
	var ttt = resource.NewMilliQuantity(18000, resource.DecimalSI)
	klog.V(4).Infof("%d", ttt)
	node := testutil.MockTestNode("100", "500G")
	socketPath := "/tmp/copilot-test.sock"
	yarnCopilotServer := copilot.NewYarnCopilotServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	go yarnCopilotServer.Run(ctx)
	defer cancel()
	time.Sleep(1 * time.Second)
	labels := map[string]string{}
	labels[apiext.LabelPodQoS] = string(apiext.QoSBE)
	labels["app.kubernetes.io/component"] = "node-manager"
	podEvictInfosSorted := []*podEvictCPUInfo{
		{
			pod:          mockBEPodForCPUEvict("pod_be_3_priority10", 16*1000, 10),
			milliRequest: 16 * 1000,
		},
		{
			pod:          mockBEPodForCPUEvictWithLabels("pod_be_2_priority100", 16*1000, 100, labels),
			milliRequest: 16 * 1000,
		},
		{
			pod:          mockBEPodForCPUEvict("pod_be_1_priority100", 16*1000, 100),
			milliRequest: 16 * 1000,
		},
	}

	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
	mockQuerier := mock_metriccache.NewMockQuerier(ctl)
	mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()
	mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
	mockStateInformer := mock_statesinformer.NewMockStatesInformer(ctl)
	mockStateInformer.EXPECT().GetNode().Return(node).AnyTimes()

	metriccache.DefaultAggregateResultFactory = mockResultFactory

	nodeCpuUsageQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
	assert.NoError(t, err)
	result := buildMockQueryResultAndCount(ctl, mockQuerier, mockResultFactory, nodeCpuUsageQueryMeta)
	result.EXPECT().Value(metriccache.AggregationTypeLast).Return(float64(0.5), nil).Times(1)
	assert.NoError(t, err)
	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()

	stop := make(chan struct{})
	evictor := framework.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
	evictor.Start(stop)
	defer func() { stop <- struct{}{} }()

	runtime.DockerHandler = handler.NewFakeRuntimeHandler()
	// create pod
	var containers []*critesting.FakeContainer
	for _, podInfo := range podEvictInfosSorted {
		pod := podInfo.pod
		_, _ = client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
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

	cpuEvictor := &cpuEvictor{
		evictor:                     evictor,
		statesInformer:              mockStateInformer,
		metricCache:                 mockMetricCache,
		lastEvictTime:               time.Now().Add(-5 * time.Minute),
		onlyEvictByAPI:              true,
		evictByCopilotAgent:         true,
		copilotAgent:                copilot.NewCopilotAgent(socketPath),
		evictByCopilotPodLabelKey:   "app.kubernetes.io/component",
		evictByCopilotPodLabelValue: "node-manager",
	}
	cpuEvictor.killAndEvictBEPodsRelease(node, podEvictInfosSorted, 18*1000)
	assert.True(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[0].pod))
	assert.False(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[1].pod))
	assert.True(t, cpuEvictor.evictor.IsPodEvicted(podEvictInfosSorted[2].pod))
}

func Test_isSatisfactionConfigValid(t *testing.T) {
	tests := []struct {
		name            string
		thresholdConfig slov1alpha1.ResourceThresholdStrategy
		expect          bool
	}{
		{
			name:            "test_nil",
			thresholdConfig: slov1alpha1.ResourceThresholdStrategy{},
			expect:          false,
		},
		{
			name:            "test_lowPercent_invalid",
			thresholdConfig: slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(0), CPUEvictBESatisfactionUpperPercent: pointer.Int64(50)},
			expect:          false,
		},
		{
			name:            "test_upperPercent_invalid",
			thresholdConfig: slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(30), CPUEvictBESatisfactionUpperPercent: pointer.Int64(100)},
			expect:          false,
		},
		{
			name:            "test_lowPercent>upperPercent",
			thresholdConfig: slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(40), CPUEvictBESatisfactionUpperPercent: pointer.Int64(30)},
			expect:          false,
		},
		{
			name:            "test_valid",
			thresholdConfig: slov1alpha1.ResourceThresholdStrategy{CPUEvictBESatisfactionLowerPercent: pointer.Int64(30), CPUEvictBESatisfactionUpperPercent: pointer.Int64(40)},
			expect:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSatisfactionConfigValid(&tt.thresholdConfig)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func mockBEPodForCPUEvictWithLabels(name string, request int64, priority int32, labels map[string]string) *corev1.Pod {
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[apiext.LabelPodQoS] = string(apiext.QoSBE)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      name,
			UID:       types.UID(name),
			Labels:    labels,
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

func mockBEPodForCPUEvict(name string, request int64, priority int32) *corev1.Pod {
	return mockBEPodForCPUEvictWithLabels(name, request, priority, nil)
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

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

package memoryevict

import (
	"context"
	"fmt"
	"testing"
	"time"

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
)

type podMemSample struct {
	UID     string
	MemUsed resource.Quantity
}

func Test_memoryEvict(t *testing.T) {
	type args struct {
		name               string
		node               *corev1.Node
		nodeMemUsed        resource.Quantity
		podMetrics         []podMemSample
		pods               []*corev1.Pod
		thresholdConfig    *slov1alpha1.ResourceThresholdStrategy
		expectEvictPods    []*corev1.Pod
		expectNotEvictPods []*corev1.Pod
	}

	tests := []args{
		{
			name: "test_memoryevict_no_thresholdConfig",
		},
		{
			name: "test_MemoryEvictThresholdPercent_not_valid",
			// invalid
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64(-1)},
		},
		{
			name:            "test_nodeMetric_nil",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64(80)},
		},
		{
			name:            "test_node_nil",
			nodeMemUsed:     resource.MustParse("115G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64(80)},
		},
		{
			name:            "test_node_memorycapacity_invalid",
			node:            testutil.MockTestNode("80", "0"),
			nodeMemUsed:     resource.MustParse("115G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64(80)},
		},
		{
			name: "test_memory_under_evict_line",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("80G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("20G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("4G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("8G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("8G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				MemoryEvictThresholdPercent: pointer.Int64(80),
			},
			expectEvictPods: []*corev1.Pod{},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name: "test_memoryevict_MemoryEvictThresholdPercent_sort_by_priority_and_usage_82",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				MemoryEvictThresholdPercent: pointer.Int64(82),
			}, // >96G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name: "test_memoryevict_MemoryEvictThresholdPercent_80",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				MemoryEvictThresholdPercent: pointer.Int64(80),
			}, // >91.2G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name: "test_memoryevict_MemoryEvictThresholdPercent_50",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				MemoryEvictThresholdPercent: pointer.Int64(50),
			}, // >60G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
			},
		},
		{
			name: "test_memoryevict_MemoryEvictLowerPercent_80",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				MemoryEvictThresholdPercent: pointer.Int64(82),
				MemoryEvictLowerPercent:     pointer.Int64(80),
			}, // >96G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name: "test_memoryevict_MemoryEvictLowerPercent_78",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				MemoryEvictThresholdPercent: pointer.Int64(82),
				MemoryEvictLowerPercent:     pointer.Int64(78),
			}, // >93.6G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name: "test_memoryevict_MemoryEvictLowerPercent_74",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				MemoryEvictThresholdPercent: pointer.Int64(82),
				MemoryEvictLowerPercent:     pointer.Int64(74),
			}, // >88.8G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			if tt.name == "test_memoryevict_MemoryEvictThresholdPercent_sort_by_priority_and_usage_82" {
				t.Log("00000")
			}
			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.pods)).AnyTimes()
			mockStatesInformer.EXPECT().GetNode().Return(tt.node).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeSLO().Return(testutil.GetNodeSLOByThreshold(tt.thresholdConfig)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			nodeMemQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			result := mock_metriccache.NewMockAggregateResult(ctl)
			result.EXPECT().Value(gomock.Any()).Return(float64(tt.nodeMemUsed.Value()), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			mockResultFactory.EXPECT().New(nodeMemQueryMeta).Return(result).AnyTimes()
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockQuerier.EXPECT().QueryAndClose(nodeMemQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, podMetric := range tt.podMetrics {
				result := mock_metriccache.NewMockAggregateResult(ctl)
				result.EXPECT().Value(gomock.Any()).Return(float64(podMetric.MemUsed.Value()), nil).AnyTimes()
				result.EXPECT().Count().Return(1).AnyTimes()
				podQueryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				mockResultFactory.EXPECT().New(podQueryMeta).Return(result).AnyTimes()
				mockQuerier.EXPECT().QueryAndClose(podQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
				//mockPodQueryResult := metriccache.PodResourceQueryResult{Metric: podMetric}
				//mockMetricCache.EXPECT().GetPodResourceMetric(&podMetric.PodUID, gomock.Any()).Return(mockPodQueryResult).AnyTimes()
			}

			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			stop := make(chan struct{})
			evictor := framework.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			evictor.Start(stop)
			defer func() { stop <- struct{}{} }()

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
			memoryEvictor := m.(*memoryEvictor)
			memoryEvictor.Setup(&framework.Context{Evictor: evictor})
			memoryEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
			memoryEvictor.onlyEvictByAPI = true
			memoryEvictor.memoryEvict()

			// evict subresource will not be creat or update in client go testing, check evict object
			// https://github.com/kubernetes/client-go/blob/v0.28.7/testing/fixture.go#L117
			for _, pod := range tt.expectEvictPods {
				assert.True(t, memoryEvictor.evictor.IsPodEvicted(pod))
			}

			for _, pod := range tt.expectNotEvictPods {
				assert.False(t, memoryEvictor.evictor.IsPodEvicted(pod))
			}
		})
	}
}

func Test_killAndEvictYarnContainerWithCopilotAgent(t *testing.T) {
	socketPath := "/tmp/copilot-test.sock"
	yarnCopilotServer := copilot.NewYarnCopilotServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	go yarnCopilotServer.Run(ctx)
	defer cancel()
	time.Sleep(1 * time.Second)
	labels := map[string]string{}
	labels[apiext.LabelPodQoS] = string(apiext.QoSBE)
	labels["app.kubernetes.io/component"] = "node-manager"
	pods := []*corev1.Pod{
		createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
		createMemoryEvictTestPodWithLabel("test_be_pod_priority100_1", apiext.QoSBE, 100, labels),
		createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
	}
	node := testutil.MockTestNode("80", "120G")
	nodeMemUsed := resource.MustParse("110G")
	thresholdConfig := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.Bool(true),
		MemoryEvictThresholdPercent: pointer.Int64(82),
	}
	podMetrics := []podMemSample{
		{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("10G")},
		{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("10G")},
		{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
	}
	ctl := gomock.NewController(t)
	defer ctl.Finish()
	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
	mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(pods)).AnyTimes()
	mockStatesInformer.EXPECT().GetNode().Return(node).AnyTimes()
	mockStatesInformer.EXPECT().GetNodeSLO().Return(testutil.GetNodeSLOByThreshold(thresholdConfig)).AnyTimes()

	mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
	mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
	nodeMemQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
	assert.NoError(t, err)
	result := mock_metriccache.NewMockAggregateResult(ctl)
	result.EXPECT().Value(gomock.Any()).Return(float64(nodeMemUsed.Value()), nil).AnyTimes()
	result.EXPECT().Count().Return(1).AnyTimes()
	mockResultFactory.EXPECT().New(nodeMemQueryMeta).Return(result).AnyTimes()
	metriccache.DefaultAggregateResultFactory = mockResultFactory
	mockQuerier := mock_metriccache.NewMockQuerier(ctl)
	mockQuerier.EXPECT().QueryAndClose(nodeMemQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
	mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()
	for _, podMetric := range podMetrics {
		result := mock_metriccache.NewMockAggregateResult(ctl)
		result.EXPECT().Value(gomock.Any()).Return(float64(podMetric.MemUsed.Value()), nil).AnyTimes()
		result.EXPECT().Count().Return(1).AnyTimes()
		podQueryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
		assert.NoError(t, err)
		mockResultFactory.EXPECT().New(podQueryMeta).Return(result).AnyTimes()
		mockQuerier.EXPECT().QueryAndClose(podQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
		//mockPodQueryResult := metriccache.PodResourceQueryResult{Metric: podMetric}
		//mockMetricCache.EXPECT().GetPodResourceMetric(&podMetric.PodUID, gomock.Any()).Return(mockPodQueryResult).AnyTimes()
	}
	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()

	stop := make(chan struct{})
	evictor := framework.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
	evictor.Start(stop)
	defer func() { stop <- struct{}{} }()

	runtime.DockerHandler = handler.NewFakeRuntimeHandler()
	var containers []*critesting.FakeContainer
	for _, pod := range pods {
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
	config := framework.NewDefaultConfig()
	config.EvictByCopilotAgent = true
	config.EvictByCopilotEndPoint = socketPath
	opt := &framework.Options{
		StatesInformer:      mockStatesInformer,
		MetricCache:         mockMetricCache,
		Config:              config,
		MetricAdvisorConfig: maframework.NewDefaultConfig(),
		CopilotAgent:        copilot.NewCopilotAgent(socketPath),
	}
	m := New(opt)
	memoryEvictor := m.(*memoryEvictor)
	memoryEvictor.Setup(&framework.Context{Evictor: evictor})
	memoryEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
	memoryEvictor.onlyEvictByAPI = true
	memoryEvictor.memoryEvict()
	assert.True(t, memoryEvictor.evictor.IsPodEvicted(pods[0]))
	assert.False(t, memoryEvictor.evictor.IsPodEvicted(pods[1]))
	assert.False(t, memoryEvictor.evictor.IsPodEvicted(pods[2]))

}

func createMemoryEvictTestPodWithLabel(name string, qosClass apiext.QoSClass, priority int32, labels map[string]string) *corev1.Pod {
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[apiext.LabelPodQoS] = string(qosClass)
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			UID:    types.UID(name),
			Labels: labels,
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

func createMemoryEvictTestPod(name string, qosClass apiext.QoSClass, priority int32) *corev1.Pod {
	return createMemoryEvictTestPodWithLabel(name, qosClass, priority, nil)
}

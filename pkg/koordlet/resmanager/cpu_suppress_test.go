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

package resmanager

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mockmetriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func Test_cpuSuppress_suppressBECPU(t *testing.T) {
	nodeCPUInfo := &metriccache.NodeCPUInfo{
		ProcessorInfos: []util.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 0},
			{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 0},
			{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 0},
			{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 0},
			{CPUID: 8, CoreID: 4, SocketID: 2, NodeID: 1},
			{CPUID: 9, CoreID: 4, SocketID: 2, NodeID: 1},
			{CPUID: 10, CoreID: 5, SocketID: 2, NodeID: 1},
			{CPUID: 11, CoreID: 5, SocketID: 2, NodeID: 1},
			{CPUID: 12, CoreID: 6, SocketID: 3, NodeID: 1},
			{CPUID: 13, CoreID: 6, SocketID: 3, NodeID: 1},
			{CPUID: 14, CoreID: 7, SocketID: 3, NodeID: 1},
			{CPUID: 15, CoreID: 7, SocketID: 3, NodeID: 1},
		},
	}
	type args struct {
		node            *corev1.Node
		nodeMetric      *metriccache.NodeResourceMetric
		podMetrics      []*metriccache.PodResourceMetric
		podMetas        []*statesinformer.PodMeta
		thresholdConfig *slov1alpha1.ResourceThresholdStrategy
		nodeCPUSet      string
		preBECPUSet     string
		preBECFSQuota   int64
	}
	tests := []struct {
		name                     string
		args                     args
		wantBECFSQuota           int64
		wantCFSQuotaPolicyStatus *suppressPolicyStatus
		wantBECPUSet             string
		wantCPUSetPolicyStatus   *suppressPolicyStatus
	}{
		{
			name: "does not panic on empty (non-nil) input",
			args: args{
				node:          &corev1.Node{},
				nodeMetric:    &metriccache.NodeResourceMetric{},
				podMetrics:    []*metriccache.PodResourceMetric{},
				podMetas:      []*statesinformer.PodMeta{},
				nodeCPUSet:    "0-15",
				preBECPUSet:   "0-9",
				preBECFSQuota: 16 * defaultCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.BoolPtr(true),
					CPUSuppressPolicy:           slov1alpha1.CPUCfsQuotaPolicy,
					CPUSuppressThresholdPercent: pointer.Int64Ptr(70),
				},
			},
			wantBECFSQuota:           16 * defaultCFSPeriod,
			wantCFSQuotaPolicyStatus: nil,
			wantBECPUSet:             "0-9",
			wantCPUSetPolicyStatus:   nil,
		},
		{
			name: "suppress by cfsQuota calculate correctly for missing podMeta or transient metrics",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				},
				nodeMetric: &metriccache.NodeResourceMetric{
					CPUUsed: metriccache.CPUMetric{
						CPUUsed: resource.MustParse("12"),
					},
					MemoryUsed: metriccache.MemoryMetric{
						MemoryWithoutCache: resource.MustParse("18G"),
					},
				},
				podMetrics: []*metriccache.PodResourceMetric{
					{
						PodUID: "ls-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("8"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("10G"),
						},
					},
					{
						PodUID: "be-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("4"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("4G"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod",
								UID:  "be-pod",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				nodeCPUSet:    "0-15",
				preBECPUSet:   "0-9",
				preBECFSQuota: 10 * defaultCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.BoolPtr(true),
					CPUSuppressPolicy:           slov1alpha1.CPUCfsQuotaPolicy,
					CPUSuppressThresholdPercent: pointer.Int64Ptr(70),
				},
			},
			wantBECFSQuota:           3.2 * defaultCFSPeriod,
			wantCFSQuotaPolicyStatus: &policyUsing,
			wantBECPUSet:             "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15",
			wantCPUSetPolicyStatus:   &policyRecovered,
		},
		{
			name: "calculate be suppress cfsQuota correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				},
				nodeMetric: &metriccache.NodeResourceMetric{
					CPUUsed: metriccache.CPUMetric{
						CPUUsed: resource.MustParse("12"),
					},
					MemoryUsed: metriccache.MemoryMetric{
						MemoryWithoutCache: resource.MustParse("18G"),
					},
				},
				podMetrics: []*metriccache.PodResourceMetric{
					{
						PodUID: "ls-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("8"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("10G"),
						},
					},
					{
						PodUID: "be-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("2"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("4G"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "ls-pod",
								UID:  "ls-pod",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod",
								UID:  "be-pod",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				nodeCPUSet:    "0-15",
				preBECPUSet:   "0-9",
				preBECFSQuota: 15 * defaultCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.BoolPtr(true),
					CPUSuppressPolicy:           slov1alpha1.CPUCfsQuotaPolicy,
					CPUSuppressThresholdPercent: pointer.Int64Ptr(70),
				},
			},
			wantBECFSQuota:           1.2 * defaultCFSPeriod,
			wantCFSQuotaPolicyStatus: &policyUsing,
			wantBECPUSet:             "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15",
			wantCPUSetPolicyStatus:   &policyRecovered,
		},
		{
			name: "calculate be suppress cpus correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				},
				nodeMetric: &metriccache.NodeResourceMetric{
					CPUUsed: metriccache.CPUMetric{
						CPUUsed: resource.MustParse("12"),
					},
					MemoryUsed: metriccache.MemoryMetric{
						MemoryWithoutCache: resource.MustParse("18G"),
					},
				},
				podMetrics: []*metriccache.PodResourceMetric{
					{
						PodUID: "ls-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("8"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("10G"),
						},
					},
					{
						PodUID: "be-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("2"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("4G"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "ls-pod",
								UID:  "ls-pod",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod",
								UID:  "be-pod",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				nodeCPUSet:    "0-15",
				preBECPUSet:   "0-9",
				preBECFSQuota: 8 * defaultCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.BoolPtr(true),
					CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
					CPUSuppressThresholdPercent: pointer.Int64Ptr(70),
				},
			},
			wantBECFSQuota:           -1,
			wantCFSQuotaPolicyStatus: &policyRecovered,
			wantBECPUSet:             "15,14",
			wantCPUSetPolicyStatus:   &policyUsing,
		},
		{
			name: "reset cpuset and cfs quota if cpu qos disabled",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				},
				nodeMetric: &metriccache.NodeResourceMetric{
					CPUUsed: metriccache.CPUMetric{
						CPUUsed: resource.MustParse("12"),
					},
					MemoryUsed: metriccache.MemoryMetric{
						MemoryWithoutCache: resource.MustParse("18G"),
					},
				},
				podMetrics: []*metriccache.PodResourceMetric{
					{
						PodUID: "ls-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("8"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("10G"),
						},
					},
					{
						PodUID: "be-pod",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("2"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("4G"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "ls-pod",
								UID:  "ls-pod",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod",
								UID:  "be-pod",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				nodeCPUSet:    "0-15",
				preBECPUSet:   "0-9",
				preBECFSQuota: 8 * defaultCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.BoolPtr(false),
					CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
					CPUSuppressThresholdPercent: pointer.Int64Ptr(70),
				},
			},
			wantBECFSQuota:           -1,
			wantCFSQuotaPolicyStatus: &policyRecovered,
			wantBECPUSet:             "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15",
			wantCPUSetPolicyStatus:   &policyRecovered,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()

			// prepareData: statesinformer pods node
			si := mockstatesinformer.NewMockStatesInformer(ctl)
			si.EXPECT().GetAllPods().Return(tt.args.podMetas).AnyTimes()
			si.EXPECT().GetNode().Return(tt.args.node).AnyTimes()
			si.EXPECT().GetNodeSLO().Return(getNodeSLOByThreshold(tt.args.thresholdConfig)).AnyTimes()

			// prepareData: mockMetricCache pods node beMetrics(AVG,current)
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctl)

			nodeMetricQueryResult := metriccache.NodeResourceQueryResult{
				Metric:      tt.args.nodeMetric,
				QueryResult: metriccache.QueryResult{AggregateInfo: &metriccache.AggregateInfo{MetricsCount: 59}},
			}
			mockMetricCache.EXPECT().GetNodeResourceMetric(gomock.Any()).Return(nodeMetricQueryResult).AnyTimes()
			mockMetricCache.EXPECT().GetNodeCPUInfo(gomock.Any()).Return(nodeCPUInfo, nil).AnyTimes()
			for _, podMetric := range tt.args.podMetrics {
				mockPodQueryResult := metriccache.PodResourceQueryResult{Metric: podMetric}
				mockMetricCache.EXPECT().GetPodResourceMetric(&podMetric.PodUID, gomock.Any()).Return(mockPodQueryResult).AnyTimes()
			}

			// prepare testing files
			helper := system.NewFileTestUtil(t)
			helper.WriteCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.CPUSet, tt.args.nodeCPUSet)
			helper.WriteCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet, tt.args.preBECPUSet)
			helper.WriteCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUCFSQuota, strconv.FormatInt(tt.args.preBECFSQuota, 10))
			helper.WriteCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUCFSPeriod, strconv.FormatInt(defaultCFSPeriod, 10))
			for _, podMeta := range tt.args.podMetas {
				podMeta.CgroupDir = util.GetPodKubeRelativePath(podMeta.Pod)
				helper.WriteCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), podMeta.CgroupDir), system.CPUSet, tt.args.preBECPUSet)
			}

			r := &resmanager{
				statesInformer:                si,
				metricCache:                   mockMetricCache,
				config:                        NewDefaultConfig(),
				collectResUsedIntervalSeconds: 1,
			}
			cpuSuppress := NewCPUSuppress(r)
			cpuSuppress.suppressBECPU()

			// checkCFSQuota
			gotBECFSQuota := helper.ReadCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUCFSQuota)
			assert.Equal(t, strconv.FormatInt(tt.wantBECFSQuota, 10), gotBECFSQuota, util.GetKubeQosRelativePath(corev1.PodQOSBestEffort))
			gotCFSQuotaPolicy, exist := cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)]
			assert.Equal(t, tt.wantCFSQuotaPolicyStatus == nil, !exist, "check_CFSQuotaPolicyStatus_exist")
			if tt.wantCFSQuotaPolicyStatus != nil {
				assert.Equal(t, *tt.wantCFSQuotaPolicyStatus, gotCFSQuotaPolicy, "check_CFSQuotaPolicyStatus_equal")
			}

			// checkCPUSet
			gotCPUSetPolicyStatus, exist := cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)]
			assert.Equal(t, tt.wantCPUSetPolicyStatus == nil, !exist, "check_CPUSetPolicyStatus_exist")
			if tt.wantCPUSetPolicyStatus != nil {
				assert.Equal(t, *tt.wantCPUSetPolicyStatus, gotCPUSetPolicyStatus, "check_CPUSetPolicyStatus_equal")
			}
			gotCPUSetBECgroup := helper.ReadCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantBECPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podMeta := range tt.args.podMetas {
				if util.GetKubeQosClass(podMeta.Pod) == corev1.PodQOSBestEffort {
					gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), podMeta.CgroupDir), system.CPUSet)
					assert.Equal(t, tt.wantBECPUSet, gotPodCPUSet, "checkPodCPUSet", filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), podMeta.CgroupDir))
				}
			}
		})
	}
}

func Test_cpuSuppress_calculateBESuppressCPU(t *testing.T) {
	type args struct {
		node               *corev1.Node
		nodeMetric         *metriccache.NodeResourceMetric
		podMetrics         []*metriccache.PodResourceMetric
		podMetas           []*statesinformer.PodMeta
		beCPUUsedThreshold int64
	}
	tests := []struct {
		name string
		args args
		want *resource.Quantity
	}{
		{
			name: "does not panic on empty (non-nil) input",
			args: args{
				node:               &corev1.Node{},
				nodeMetric:         &metriccache.NodeResourceMetric{},
				podMetrics:         []*metriccache.PodResourceMetric{},
				podMetas:           []*statesinformer.PodMeta{},
				beCPUUsedThreshold: 70,
			},
			want: resource.NewMilliQuantity(0, resource.DecimalSI),
		},
		{
			name: "calculate correctly for missing podMeta or transient metrics",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				},
				nodeMetric: &metriccache.NodeResourceMetric{
					CPUUsed: metriccache.CPUMetric{
						CPUUsed: resource.MustParse("11"),
					},
					MemoryUsed: metriccache.MemoryMetric{
						MemoryWithoutCache: resource.MustParse("18G"),
					},
				},
				podMetrics: []*metriccache.PodResourceMetric{
					{
						PodUID: "abc",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("8"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("10G"),
						},
					},
					{
						PodUID: "def",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("4"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("4G"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "podB",
								UID:  "def",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				beCPUUsedThreshold: 70,
			},
			want: resource.NewQuantity(6, resource.DecimalSI),
		},
		{
			name: "calculate be suppress cpus correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				},
				nodeMetric: &metriccache.NodeResourceMetric{
					CPUUsed: metriccache.CPUMetric{
						CPUUsed: resource.MustParse("12"),
					},
					MemoryUsed: metriccache.MemoryMetric{
						MemoryWithoutCache: resource.MustParse("18G"),
					},
				},
				podMetrics: []*metriccache.PodResourceMetric{
					{
						PodUID: "abc",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("8"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("10G"),
						},
					},
					{
						PodUID: "def",
						CPUUsed: metriccache.CPUMetric{
							CPUUsed: resource.MustParse("2"),
						},
						MemoryUsed: metriccache.MemoryMetric{
							MemoryWithoutCache: resource.MustParse("4G"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "podA",
								UID:  "abc",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "podB",
								UID:  "def",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("4"),
												apiext.BatchMemory: resource.MustParse("6G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				beCPUUsedThreshold: 70,
			},
			want: resource.NewQuantity(4, resource.DecimalSI),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resmanager{}
			cpuSuppress := NewCPUSuppress(&r)
			got := cpuSuppress.calculateBESuppressCPU(tt.args.node, tt.args.nodeMetric, tt.args.podMetrics, tt.args.podMetas,
				tt.args.beCPUUsedThreshold)
			assert.Equal(t, tt.want.MilliValue(), got.MilliValue())
		})
	}
}

func Test_cpuSuppress_recoverCPUSetIfNeed(t *testing.T) {
	type args struct {
		oldCPUSets          string
		rootCPUSets         string
		currentPolicyStatus *suppressPolicyStatus
	}
	tests := []struct {
		name             string
		args             args
		wantCPUSet       string
		wantPolicyStatus *suppressPolicyStatus
	}{
		{
			name: "test need recover. currentPolicyStatus is nil",
			args: args{
				oldCPUSets:          "7,6,3,2",
				rootCPUSets:         "0-15",
				currentPolicyStatus: nil,
			},
			wantCPUSet:       "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15",
			wantPolicyStatus: &policyRecovered,
		},
		{
			name: "test need recover. currentPolicyStatus is policyUsing",
			args: args{
				oldCPUSets:          "7,6,3,2",
				rootCPUSets:         "0-15",
				currentPolicyStatus: &policyUsing,
			},
			wantCPUSet:       "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15",
			wantPolicyStatus: &policyRecovered,
		},
		{
			name: "test not need recover. currentPolicyStatus is policyRecovered",
			args: args{
				oldCPUSets:          "7,6,3,2",
				rootCPUSets:         "0-15",
				currentPolicyStatus: &policyRecovered,
			},
			wantCPUSet:       "7,6,3,2",
			wantPolicyStatus: &policyRecovered,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare testing files
			helper := system.NewFileTestUtil(t)
			podDirs := []string{"pod1", "pod2", "pod3"}
			testingPrepareBECgroupData(helper, podDirs, tt.args.oldCPUSets)
			helper.WriteCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.CPUSet, tt.args.rootCPUSets)

			r := resmanager{}
			cpuSuppress := NewCPUSuppress(&r)
			if tt.args.currentPolicyStatus != nil {
				cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)] = *tt.args.currentPolicyStatus
			}
			cpuSuppress.recoverCPUSetIfNeed()
			gotPolicyStatus := cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)]
			assert.Equal(t, *tt.wantPolicyStatus, gotPolicyStatus, "checkStatus")
			gotCPUSetBECgroup := helper.ReadCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantCPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podDir := range podDirs {
				gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
				assert.Equal(t, tt.wantCPUSet, gotPodCPUSet, "checkPodCPUSet")
			}
		})
	}
}

func Test_cpuSuppress_recoverCFSQuotaIfNeed(t *testing.T) {
	type args struct {
		name                string
		preBECfsQuota       int64
		currentPolicyStatus *suppressPolicyStatus
		wantBECfsQuota      int64
		wantPolicyStatus    *suppressPolicyStatus
	}
	testCases := []args{
		{
			name:             "test need recover. currentPolicyStatus is nil",
			preBECfsQuota:    10 * cfsPeriod,
			wantBECfsQuota:   -1,
			wantPolicyStatus: &policyRecovered,
		},
		{
			name:                "test need recover. currentPolicyStatus is policyUsing",
			preBECfsQuota:       10 * cfsPeriod,
			currentPolicyStatus: &policyUsing,
			wantBECfsQuota:      -1,
			wantPolicyStatus:    &policyRecovered,
		},
		{
			name:                "test not need recover. currentPolicyStatus is policyRecovered",
			preBECfsQuota:       10 * cfsPeriod,
			currentPolicyStatus: &policyRecovered,
			wantBECfsQuota:      10 * cfsPeriod,
			wantPolicyStatus:    &policyRecovered,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			beQosDir := util.GetKubeQosRelativePath(corev1.PodQOSBestEffort)
			helper.CreateCgroupFile(beQosDir, system.CPUCFSQuota)

			helper.WriteCgroupFileContents(beQosDir, system.CPUCFSQuota, strconv.FormatInt(tt.preBECfsQuota, 10))

			r := resmanager{}
			cpuSuppress := NewCPUSuppress(&r)
			if tt.currentPolicyStatus != nil {
				cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)] = *tt.currentPolicyStatus
			}
			cpuSuppress.recoverCFSQuotaIfNeed()
			gotPolicyStatus := cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)]
			assert.Equal(t, *tt.wantPolicyStatus, gotPolicyStatus, "checkStatus")
			gotBECfsQuota := helper.ReadCgroupFileContents(beQosDir, system.CPUCFSQuota)
			assert.Equal(t, strconv.FormatInt(tt.wantBECfsQuota, 10), gotBECfsQuota)
		})
	}
}

func Test_calculateBESuppressCPUSetPolicy(t *testing.T) {
	type args struct {
		cpusetQuantity *resource.Quantity
		nodeCPUInfo    *metriccache.NodeCPUInfo
		oldCPUSetNum   int
	}
	tests := []struct {
		name string
		args args
		want []int32
	}{
		{
			name: "do not panic but return empty cpuset for insufficient cpus",
			args: args{
				cpusetQuantity: resource.NewQuantity(0, resource.DecimalSI),
				nodeCPUInfo:    &metriccache.NodeCPUInfo{},
				oldCPUSetNum:   0,
			},
			want: nil,
		},
		{
			name: "at least allocate 2 cpus",
			args: args{
				cpusetQuantity: resource.NewQuantity(0, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 6, CoreID: 3, SocketID: 0, NodeID: 0},
						{CPUID: 7, CoreID: 3, SocketID: 0, NodeID: 0},
						{CPUID: 8, CoreID: 4, SocketID: 0, NodeID: 0},
						{CPUID: 9, CoreID: 4, SocketID: 0, NodeID: 0},
						{CPUID: 10, CoreID: 5, SocketID: 0, NodeID: 0},
						{CPUID: 11, CoreID: 5, SocketID: 0, NodeID: 0},
					},
				},
				oldCPUSetNum: 2,
			},
			want: []int32{11, 10},
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 0.",
			args: args{
				cpusetQuantity: resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 2, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 3, CoreID: 3, SocketID: 1, NodeID: 1},
						{CPUID: 4, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 5, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 6, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
					},
				},
				oldCPUSetNum: 3,
			},
			want: []int32{7, 3, 5},
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 1.",
			args: args{
				cpusetQuantity: resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
					},
				},
				oldCPUSetNum: 5,
			},
			want: []int32{7, 6, 3},
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 2. (also scattering on sockets)",
			args: args{
				cpusetQuantity: resource.NewQuantity(5, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 0},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 0},
						{CPUID: 8, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 9, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 10, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 11, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 12, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 13, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 14, CoreID: 7, SocketID: 3, NodeID: 1},
						{CPUID: 15, CoreID: 7, SocketID: 3, NodeID: 1},
					},
				},
				oldCPUSetNum: 8,
			},
			want: []int32{15, 14, 11, 10, 7},
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 3. (regardless of the initial order)",
			args: args{
				cpusetQuantity: resource.NewQuantity(5, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 12, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 13, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 14, CoreID: 7, SocketID: 3, NodeID: 1},
						{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 11, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 8, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 15, CoreID: 7, SocketID: 3, NodeID: 1},
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 9, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 10, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 0},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 0},
					},
				},
				oldCPUSetNum: 8,
			},
			want: []int32{15, 14, 11, 10, 7},
		},
		{
			name: "allocate cpus for slow scale up:increase cpunum > maxIncreaseCPUNum",
			args: args{
				cpusetQuantity: resource.NewQuantity(5, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 12, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 13, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 14, CoreID: 7, SocketID: 3, NodeID: 1},
						{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 11, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 8, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 15, CoreID: 7, SocketID: 3, NodeID: 1},
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 9, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 10, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 0},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 0},
					},
				},
				oldCPUSetNum: 1, // maxNewCPUSet := oldCPUSetNum + beMaxIncreaseCPUPercent*totalCPUNum = 3
			},
			want: []int32{15, 14, 11},
		},
		{
			name: "allocate cpus for slow scale up:increase cpunum == maxIncreaseCPUNum",
			args: args{
				cpusetQuantity: resource.NewQuantity(5, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 12, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 13, CoreID: 6, SocketID: 3, NodeID: 1},
						{CPUID: 14, CoreID: 7, SocketID: 3, NodeID: 1},
						{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 11, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 0},
						{CPUID: 8, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 15, CoreID: 7, SocketID: 3, NodeID: 1},
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 9, CoreID: 4, SocketID: 2, NodeID: 1},
						{CPUID: 10, CoreID: 5, SocketID: 2, NodeID: 1},
						{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 0},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 0},
					},
				},
				oldCPUSetNum: 3,
			},
			want: []int32{15, 14, 11, 10, 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateBESuppressCPUSetPolicy(tt.args.cpusetQuantity, tt.args.oldCPUSetNum, tt.args.nodeCPUInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_applyBESuppressCPUSetPolicy(t *testing.T) {
	// prepare testing files
	helper := system.NewFileTestUtil(t)
	podDirs := []string{"pod1", "pod2", "pod3"}
	testingPrepareBECgroupData(helper, podDirs, "1,2")

	cpuset := []int32{3, 2, 1}
	wantCPUSetStr := "3,2,1"

	oldCPUSet, err := util.GetRootCgroupCurCPUSet(corev1.PodQOSBestEffort)
	assert.NoError(t, err)

	err = applyBESuppressCPUSetPolicy(cpuset, oldCPUSet)
	assert.NoError(t, err)
	gotCPUSetBECgroup := helper.ReadCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
	assert.Equal(t, wantCPUSetStr, gotCPUSetBECgroup, "checkBECPUSet")
	for _, podDir := range podDirs {
		gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
		assert.Equal(t, wantCPUSetStr, gotPodCPUSet, "checkPodCPUSet")
	}
}

func Test_getBECgroupCPUSetPathsRecursive(t *testing.T) {
	// prepare testing files
	helper := system.NewFileTestUtil(t)
	podDirs := []string{"pod1", "pod2", "pod3"}
	testingPrepareBECgroupData(helper, podDirs, "1,2")
	var wantPaths []string
	wantPaths = append(wantPaths, util.GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort))
	for _, podDir := range podDirs {
		wantPaths = append(wantPaths, filepath.Join(util.GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort), podDir))
	}

	paths, err := getBECgroupCPUSetPathsRecursive()
	assert.NoError(t, err)
	assert.Equal(t, len(wantPaths), len(paths))
}

func Test_adjustByCPUSet(t *testing.T) {
	type args struct {
		cpusetQuantity *resource.Quantity
		nodeCPUInfo    *metriccache.NodeCPUInfo
		oldCPUSets     string
	}
	tests := []struct {
		name       string
		args       args
		wantCPUSet string
	}{
		{
			name: "test scale lower by cpuset.",
			args: args{
				cpusetQuantity: resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
					},
				},
				oldCPUSets: "7,6,3,2",
			},
			wantCPUSet: "7,6,3",
		},
		{
			name: "test scale up by cpuset.",
			args: args{
				cpusetQuantity: resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []util.ProcessorInfo{
						{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
						{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
						{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
						{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
						{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
					},
				},
				oldCPUSets: "7,6",
			},
			wantCPUSet: "7,6,3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare testing files
			helper := system.NewFileTestUtil(t)
			podDirs := []string{"pod1", "pod2", "pod3"}
			testingPrepareBECgroupData(helper, podDirs, tt.args.oldCPUSets)

			adjustByCPUSet(tt.args.cpusetQuantity, tt.args.nodeCPUInfo)

			gotCPUSetBECgroup := helper.ReadCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantCPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podDir := range podDirs {
				gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
				assert.Equal(t, tt.wantCPUSet, gotPodCPUSet, "checkPodCPUSet")
			}
		})
	}
}

func Test_adjustByCfsQuota(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	beQosDir := util.GetKubeQosRelativePath(corev1.PodQOSBestEffort)
	helper.CreateCgroupFile(beQosDir, system.CPUCFSQuota)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node0",
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("80"),
			},
		},
	}
	type args struct {
		name           string
		cpuQuantity    *resource.Quantity
		preBECfsQuota  int64
		wantBECfsQuota int64
	}
	testCases := []args{
		{
			name:           "increase CFSQuota: cpuQuantity > preBECPU + maxIncreaseCPU",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  10 * cfsPeriod,
			wantBECfsQuota: 10*cfsPeriod + int64(beMaxIncreaseCPUPercent*80*float64(cfsPeriod)),
		},
		{
			name:           "increase CFSQuota: preBECPU < cpuQuantity < preBECPU + maxIncreaseCPU",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  19 * cfsPeriod,
			wantBECfsQuota: 20 * cfsPeriod,
		},
		{
			name:           "suppress beCPU: preBECPU > cpuQuantity",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  24 * cfsPeriod,
			wantBECfsQuota: 20 * cfsPeriod,
		},
		{
			name:           "bypass minDeltaQuota!",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  int64(19.8 * float64(cfsPeriod)),
			wantBECfsQuota: int64(19.8 * float64(cfsPeriod)),
		},
		{
			name:           "minQuota 2000 not bypass!",
			cpuQuantity:    resource.NewMilliQuantity(1, resource.BinarySI),
			preBECfsQuota:  int64(0.8 * float64(cfsPeriod)),
			wantBECfsQuota: 2000,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			helper.WriteCgroupFileContents(beQosDir, system.CPUCFSQuota, strconv.FormatInt(tt.preBECfsQuota, 10))
			adjustByCfsQuota(tt.cpuQuantity, node)
			gotBECfsQuota := helper.ReadCgroupFileContents(beQosDir, system.CPUCFSQuota)
			if gotBECfsQuota != strconv.FormatInt(tt.wantBECfsQuota, 10) {
				t.Errorf("failed to adjustByCfsQuota, want file %v cfs_quota %v, got %v", system.GetCgroupFilePath(beQosDir, system.CPUCFSQuota), tt.wantBECfsQuota,
					gotBECfsQuota)
				return
			}
		})
	}
}

func Test_writeBECgroupsCPUSet(t *testing.T) {
	// prepare testing files
	helper := system.NewFileTestUtil(t)
	podDirs := []string{"pod1", "pod2", "pod3"}
	testingPrepareBECgroupData(helper, podDirs, "1,2")

	var dirPaths []string
	dirPaths = append(dirPaths, util.GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort))
	for _, podDir := range podDirs {
		dirPaths = append(dirPaths, filepath.Join(util.GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort), podDir))
	}

	cpuSetStr := "0,1,2"
	writeBECgroupsCPUSet(dirPaths, cpuSetStr, false)

	gotCPUSetBECgroup := helper.ReadCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
	assert.Equal(t, cpuSetStr, gotCPUSetBECgroup, "checkBECPUSet_reversed_false")
	for _, podDir := range podDirs {
		gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
		assert.Equal(t, cpuSetStr, gotPodCPUSet, "checkPodCPUSet_reversed_false")
	}

	cpuSetStr = "0,1"
	writeBECgroupsCPUSet(dirPaths, cpuSetStr, true)
	gotCPUSetBECgroup = helper.ReadCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
	assert.Equal(t, cpuSetStr, gotCPUSetBECgroup, "checkBECPUSet_reversed_true")
	for _, podDir := range podDirs {
		gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
		assert.Equal(t, cpuSetStr, gotPodCPUSet, "checkPodCPUSet_reversed_true")
	}
}

func testingPrepareBECgroupData(helper *system.FileTestUtil, podDirs []string, cpusets string) {
	helper.WriteCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.CPUSet, cpusets)
	helper.WriteCgroupFileContents(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.CPUSet, cpusets)
	for _, podDir := range podDirs {
		helper.WriteCgroupFileContents(filepath.Join(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet, cpusets)
	}
}

func getNodeSLOByThreshold(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) *slov1alpha1.NodeSLO {
	return &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: thresholdConfig,
		},
	}
}

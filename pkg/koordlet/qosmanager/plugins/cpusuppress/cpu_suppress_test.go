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

package cpusuppress

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mockmetriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	maframework "github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/util/cache"
)

func newTestCPUSuppress(opt *framework.Options) *CPUSuppress {
	return &CPUSuppress{
		interval:              time.Duration(opt.Config.CPUSuppressIntervalSeconds) * time.Second,
		metricCollectInterval: opt.MetricAdvisorConfig.CollectResUsedInterval,
		statesInformer:        opt.StatesInformer,
		metricCache:           opt.MetricCache,
		executor: &resourceexecutor.ResourceUpdateExecutorImpl{
			Config:        resourceexecutor.NewDefaultConfig(),
			ResourceCache: cache.NewCacheDefault(),
		},
		cgroupReader:           resourceexecutor.NewCgroupReader(),
		suppressPolicyStatuses: map[string]suppressPolicyStatus{},
	}
}

func Test_cpuSuppress_suppressBECPU(t *testing.T) {
	nodeCPUInfo := &metriccache.NodeCPUInfo{
		ProcessorInfos: []koordletutil.ProcessorInfo{
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
	type podMetricSample struct {
		UID     string
		CPUUsed resource.Quantity
	}
	type args struct {
		node                *corev1.Node
		nodeCPUUsed         *resource.Quantity
		podMetrics          []podMetricSample
		podMetas            []*statesinformer.PodMeta
		thresholdConfig     *slov1alpha1.ResourceThresholdStrategy
		nodeCPUSet          string
		preBECPUSet         string
		preBECFSQuota       int64
		beCPUManagerEnabled bool
	}
	tests := []struct {
		name                     string
		args                     args
		wantBECFSQuota           int64
		wantCFSQuotaPolicyStatus *suppressPolicyStatus
		wantBECPUSet             string
		wantBECPUSetOfContainer  map[string]string // key is container id
		wantBECPUSetOfSandbox    map[string]string // key is pod name
		wantCPUSetPolicyStatus   *suppressPolicyStatus
	}{
		{
			name: "does not panic on empty (non-nil) input",
			args: args{
				node:          &corev1.Node{},
				nodeCPUUsed:   nil,
				podMetrics:    []podMetricSample{},
				podMetas:      []*statesinformer.PodMeta{},
				nodeCPUSet:    "0-15",
				preBECPUSet:   "0-9",
				preBECFSQuota: 16 * system.DefaultCPUCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      ptr.To[bool](true),
					CPUSuppressPolicy:           slov1alpha1.CPUCfsQuotaPolicy,
					CPUSuppressThresholdPercent: ptr.To[int64](70),
				},
			},
			wantBECFSQuota:           16 * system.DefaultCPUCFSPeriod,
			wantCFSQuotaPolicyStatus: nil,
			wantBECPUSet:             "0-9",
			wantCPUSetPolicyStatus:   nil,
		},
		{
			name: "recover when be cpu manager enabled",
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
				nodeCPUUsed: resource.NewQuantity(12, resource.DecimalSI),
				podMetrics: []podMetricSample{
					{UID: "ls-pod", CPUUsed: resource.MustParse("8")},
					{UID: "be-pod", CPUUsed: resource.MustParse("2")},
					{UID: "be-pod-numa", CPUUsed: resource.MustParse("1")},
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
								ContainerStatuses: []corev1.ContainerStatus{
									{
										ContainerID: "containerd://ls-pod-container",
									},
								},
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
								ContainerStatuses: []corev1.ContainerStatus{
									{
										ContainerID: "containerd://be-pod-container",
									},
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod-numa",
								UID:  "be-pod-numa",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
								Annotations: map[string]string{
									apiext.AnnotationResourceStatus: "{\"numaNodeResources\":[{\"node\": 0}]}",
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
								ContainerStatuses: []corev1.ContainerStatus{
									{
										ContainerID: "containerd://be-pod-numa-container",
									},
								},
							},
						},
					},
				},
				nodeCPUSet:    "0-15",
				preBECPUSet:   "1-9",
				preBECFSQuota: 10 * system.DefaultCPUCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      ptr.To[bool](true),
					CPUSuppressPolicy:           slov1alpha1.CPUCfsQuotaPolicy,
					CPUSuppressThresholdPercent: ptr.To[int64](70),
				},
				beCPUManagerEnabled: true,
			},
			wantBECFSQuota:           -1,
			wantCFSQuotaPolicyStatus: &policyRecovered,
			wantBECPUSet:             "0-15",
			wantBECPUSetOfContainer: map[string]string{
				"containerd://be-pod-numa-container": "1-9",
			},
			wantBECPUSetOfSandbox: map[string]string{
				"be-pod-numa": "1-9",
			},
			wantCPUSetPolicyStatus: &policyRecovered,
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
				nodeCPUUsed: resource.NewQuantity(12, resource.DecimalSI),
				podMetrics: []podMetricSample{
					{UID: "ls-pod", CPUUsed: resource.MustParse("8")},
					{UID: "be-pod", CPUUsed: resource.MustParse("4")},
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
				preBECFSQuota: 10 * system.DefaultCPUCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      ptr.To[bool](true),
					CPUSuppressPolicy:           slov1alpha1.CPUCfsQuotaPolicy,
					CPUSuppressThresholdPercent: ptr.To[int64](70),
				},
			},
			wantBECFSQuota:           int64(3.2 * float64(system.DefaultCPUCFSPeriod)),
			wantCFSQuotaPolicyStatus: &policyUsing,
			wantBECPUSet:             "0-15",
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
				nodeCPUUsed: resource.NewQuantity(12, resource.DecimalSI),
				podMetrics: []podMetricSample{
					{UID: "ls-pod", CPUUsed: resource.MustParse("8")},
					{UID: "be-pod", CPUUsed: resource.MustParse("2")},
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
				preBECFSQuota: 15 * system.DefaultCPUCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      ptr.To[bool](true),
					CPUSuppressPolicy:           slov1alpha1.CPUCfsQuotaPolicy,
					CPUSuppressThresholdPercent: ptr.To[int64](70),
				},
			},
			wantBECFSQuota:           int64(1.2 * float64(system.DefaultCPUCFSPeriod)),
			wantCFSQuotaPolicyStatus: &policyUsing,
			wantBECPUSet:             "0-15",
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
				nodeCPUUsed: resource.NewQuantity(12, resource.DecimalSI),
				podMetrics: []podMetricSample{
					{UID: "ls-pod", CPUUsed: resource.MustParse("8")},
					{UID: "be-pod", CPUUsed: resource.MustParse("2")},
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
				preBECFSQuota: 8 * system.DefaultCPUCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      ptr.To[bool](true),
					CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
					CPUSuppressThresholdPercent: ptr.To[int64](70),
				},
			},
			wantBECFSQuota:           -1,
			wantCFSQuotaPolicyStatus: &policyRecovered,
			wantBECPUSet:             "0-1",
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
				nodeCPUUsed: resource.NewQuantity(12, resource.DecimalSI),
				podMetrics: []podMetricSample{
					{UID: "ls-pod", CPUUsed: resource.MustParse("8")},
					{UID: "be-pod", CPUUsed: resource.MustParse("2")},
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
				preBECFSQuota: 8 * system.DefaultCPUCFSPeriod,
				thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      ptr.To[bool](false),
					CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
					CPUSuppressThresholdPercent: ptr.To[int64](70),
				},
			},
			wantBECFSQuota:           -1,
			wantCFSQuotaPolicyStatus: &policyRecovered,
			wantBECPUSet:             "0-15",
			wantCPUSetPolicyStatus:   &policyRecovered,
		},
	}
	defaultSandboxContainerIDPrefix := "containerd://sandbox-"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			// prepareData: statesinformer pods node
			si := mockstatesinformer.NewMockStatesInformer(ctl)
			si.EXPECT().GetAllPods().Return(tt.args.podMetas).AnyTimes()
			si.EXPECT().GetNode().Return(tt.args.node).AnyTimes()
			si.EXPECT().GetNodeSLO().Return(testutil.GetNodeSLOByThreshold(tt.args.thresholdConfig)).AnyTimes()
			si.EXPECT().GetNodeTopo().Return(&topov1alpha1.NodeResourceTopology{}).AnyTimes()

			// prepareData: mockMetricCache pods node beMetrics(AVG,current)
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctl)
			mockMetricCache.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(nodeCPUInfo, true).AnyTimes()
			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			nodeResult := mockmetriccache.NewMockAggregateResult(ctl)
			if tt.args.nodeCPUUsed == nil {
				nodeResult.EXPECT().Count().Return(0).AnyTimes()
			} else {
				nodeResult.EXPECT().Count().Return(1).AnyTimes()
				nodeResult.EXPECT().Value(gomock.Any()).Return(float64(tt.args.nodeCPUUsed.Value()), nil).AnyTimes()
			}
			nodeCPUQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			mockResultFactory.EXPECT().New(nodeCPUQueryMeta).Return(nodeResult).AnyTimes()
			mockQuerier.EXPECT().QueryAndClose(nodeCPUQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *nodeResult).Return(nil).AnyTimes()

			for _, podMetric := range tt.args.podMetrics {
				podQueryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				testutil.BuildMockQueryResult(ctl, mockQuerier, mockResultFactory, podQueryMeta, float64(podMetric.CPUUsed.Value()))
			}
			// prepare testing files
			helper := system.NewFileTestUtil(t)
			helper.WriteCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), system.CPUSet, tt.args.nodeCPUSet)
			helper.WriteCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet, tt.args.preBECPUSet)
			helper.WriteCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUCFSQuota, strconv.FormatInt(tt.args.preBECFSQuota, 10))
			helper.WriteCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUCFSPeriod, strconv.FormatInt(system.DefaultCPUCFSPeriod, 10))
			for _, podMeta := range tt.args.podMetas {
				podMeta.CgroupDir = koordletutil.GetPodCgroupParentDir(podMeta.Pod)
				helper.WriteCgroupFileContents(podMeta.CgroupDir, system.CPUSet, tt.args.preBECPUSet)
				for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
					containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &containerStat)
					assert.NoError(t, err)
					helper.WriteCgroupFileContents(containerDir, system.CPUSet, tt.args.preBECPUSet)
				}
				defaultSandboxContainerID := defaultSandboxContainerIDPrefix + podMeta.Pod.Name
				sandboxContainerDir, err := koordletutil.GetContainerCgroupParentDirByID(podMeta.CgroupDir, defaultSandboxContainerID)
				assert.NoError(t, err)
				helper.WriteCgroupFileContents(sandboxContainerDir, system.CPUSet, tt.args.preBECPUSet)
			}

			assert.NoError(t, features.DefaultMutableKoordletFeatureGate.SetFromMap(map[string]bool{
				string(features.BECPUManager):  tt.args.beCPUManagerEnabled,
				string(features.BECPUSuppress): true}))

			opt := &framework.Options{
				StatesInformer:      si,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			cpuSuppress := newTestCPUSuppress(opt)
			stop := make(chan struct{})
			assert.NotPanics(t, func() {
				cpuSuppress.init(stop)
			})

			cpuSuppress.suppressBECPU()

			// checkCFSQuota
			gotBECFSQuota := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUCFSQuota)
			assert.Equal(t, strconv.FormatInt(tt.wantBECFSQuota, 10), gotBECFSQuota, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort))
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
			gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantBECPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podMeta := range tt.args.podMetas {
				if apiext.GetKubeQosClass(podMeta.Pod) == corev1.PodQOSBestEffort {
					gotPodCPUSet := helper.ReadCgroupFileContents(podMeta.CgroupDir, system.CPUSet)
					assert.Equal(t, tt.wantBECPUSet, gotPodCPUSet, "checkPodCPUSet", podMeta.CgroupDir)

					for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
						wantCPUSetContainer := tt.wantBECPUSet
						if cpuset, exist := tt.wantBECPUSetOfContainer[containerStat.ContainerID]; exist {
							wantCPUSetContainer = cpuset
						}
						sandboxContainerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &containerStat)
						gotContainerCPUSet := helper.ReadCgroupFileContents(sandboxContainerDir, system.CPUSet)
						assert.NoError(t, err)
						assert.Equal(t, wantCPUSetContainer, gotContainerCPUSet, "checkPodCPUSet", sandboxContainerDir)
					}

					wantCPUSetSandbox := tt.wantBECPUSet
					if cpuset, exist := tt.wantBECPUSetOfSandbox[podMeta.Pod.Name]; exist {
						wantCPUSetSandbox = cpuset
					}
					defaultSandboxContainerID := defaultSandboxContainerIDPrefix + podMeta.Pod.Name
					sandboxContainerDir, err := koordletutil.GetContainerCgroupParentDirByID(podMeta.CgroupDir, defaultSandboxContainerID)
					assert.NoError(t, err)
					gotSandboxCPUSet := helper.ReadCgroupFileContents(sandboxContainerDir, system.CPUSet)
					assert.Equal(t, wantCPUSetSandbox, gotSandboxCPUSet, "checkPodCPUSet", sandboxContainerDir)
				}
			}
		})
	}
}

func Test_cpuSuppress_calculateBESuppressCPU(t *testing.T) {
	type args struct {
		node               *corev1.Node
		nodeUsedCPU        float64
		podMetrics         map[string]float64
		podMetas           []*statesinformer.PodMeta
		hostApps           []slov1alpha1.HostApplicationSpec
		hostMetrics        map[string]float64
		beCPUUsedThreshold int64
		beCPUMinThreshold  *int64
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
				nodeUsedCPU:        0,
				podMetrics:         map[string]float64{},
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
				nodeUsedCPU: 11,
				podMetrics:  map[string]float64{"abc": 8, "def": 4},
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
				nodeUsedCPU: 12,
				podMetrics:  map[string]float64{"abc": 8, "def": 2},
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
			// 20*0.7-(12-8-2)-8
			want: resource.NewQuantity(4, resource.DecimalSI),
		},
		{
			name: "calculate be suppress cpus correctly with ls host app",
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
				nodeUsedCPU: 12,
				podMetrics:  map[string]float64{"abc": 8, "def": 2},
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
				hostMetrics: map[string]float64{
					"test-ls-app": 1,
				},
				hostApps: []slov1alpha1.HostApplicationSpec{
					{
						Name:     "test-ls-app",
						Priority: apiext.PriorityProd,
						QoS:      apiext.QoSLS,
					},
				},
				beCPUUsedThreshold: 70,
			},
			// 20*0.7-(12-8-2-1)-8-1
			want: resource.NewQuantity(4, resource.DecimalSI),
		},
		{
			name: "calculate be suppress cpus correctly with ls host app",
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
				nodeUsedCPU: 12,
				podMetrics:  map[string]float64{"abc": 8, "def": 2},
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
				hostMetrics: map[string]float64{
					"test-be-app": 1,
				},
				hostApps: []slov1alpha1.HostApplicationSpec{
					{
						Name:     "test-be-app",
						Priority: apiext.PriorityBatch,
						QoS:      apiext.QoSBE,
						CgroupPath: &slov1alpha1.CgroupPath{
							Base: slov1alpha1.CgroupBaseTypeKubeBesteffort,
						},
					},
				},
				beCPUUsedThreshold: 70,
			},
			// 20*0.7-(12-8-2-1)-8
			want: resource.NewQuantity(5, resource.DecimalSI),
		},
		{
			name: "calculate be suppress cpus with min",
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
				nodeUsedCPU: 12,
				podMetrics:  map[string]float64{"abc": 8, "def": 2},
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
				beCPUMinThreshold:  ptr.To[int64](60),
			},
			// 20*0.6
			want: resource.NewQuantity(12, resource.DecimalSI),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := &framework.Options{
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			cpuSuppress := newTestCPUSuppress(opt)
			got := cpuSuppress.calculateBESuppressCPU(tt.args.node, tt.args.nodeUsedCPU, tt.args.podMetrics, tt.args.podMetas,
				tt.args.hostApps, tt.args.hostMetrics, tt.args.beCPUUsedThreshold, tt.args.beCPUMinThreshold)
			assert.Equal(t, tt.want.MilliValue(), got.MilliValue())
		})
	}
}

func Test_cpuSuppress_recoverCPUSetIfNeed(t *testing.T) {
	type args struct {
		oldCPUSets          string
		currentPolicyStatus *suppressPolicyStatus
		nodeTopo            *topov1alpha1.NodeResourceTopology
	}
	mockNodeInfo := metriccache.NodeCPUInfo{
		ProcessorInfos: []koordletutil.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 8, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 9, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 10, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 11, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 12, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 13, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 14, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 15, CoreID: 3, SocketID: 1, NodeID: 1},
		},
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
				currentPolicyStatus: nil,
				nodeTopo:            &topov1alpha1.NodeResourceTopology{},
			},
			wantCPUSet:       "0-6,8-15",
			wantPolicyStatus: &policyRecovered,
		},
		{
			name: "test need recover. currentPolicyStatus is policyUsing",
			args: args{
				oldCPUSets:          "7,6,3,2",
				currentPolicyStatus: &policyUsing,
				nodeTopo:            &topov1alpha1.NodeResourceTopology{},
			},
			wantCPUSet:       "0-6,8-15",
			wantPolicyStatus: &policyRecovered,
		},
		{
			name: "test need recover. currentPolicyStatus is policyRecovered",
			args: args{
				oldCPUSets:          "7,6,3,2",
				currentPolicyStatus: &policyRecovered,
				nodeTopo:            &topov1alpha1.NodeResourceTopology{},
			},
			wantCPUSet:       "0-6,8-15",
			wantPolicyStatus: &policyRecovered,
		},
		{
			name: "test need recover, exclude node reserved",
			args: args{
				oldCPUSets:          "7,6,3,2",
				currentPolicyStatus: &policyUsing,
				nodeTopo: &topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							apiext.AnnotationNodeReservation: `{"reservedCPUs": "0-1"}`,
						},
					},
				},
			},
			wantCPUSet:       "2-6,8-15",
			wantPolicyStatus: &policyRecovered,
		},
		{
			name: "test need recover, exclude system qos exclusive",
			args: args{
				oldCPUSets:          "7,6,3,2",
				currentPolicyStatus: &policyUsing,
				nodeTopo: &topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							apiext.AnnotationNodeSystemQOSResource: `{"cpuset": "0-1"}`,
						},
					},
				},
			},
			wantCPUSet:       "2-6,8-15",
			wantPolicyStatus: &policyRecovered,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare testing files
			helper := system.NewFileTestUtil(t)
			podDirs := []string{"pod1", "pod2", "pod3"}
			testingPrepareBECgroupData(helper, podDirs, tt.args.oldCPUSets)
			lsePod := mockLSEPod()
			ctl := gomock.NewController(t)
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctl)
			mockStatesInformer := mockstatesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{{Pod: lsePod}}).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeTopo().Return(tt.args.nodeTopo).AnyTimes()
			mockMetricCache.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(&mockNodeInfo, true).AnyTimes()
			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			cpuSuppress := newTestCPUSuppress(opt)
			stopCh := make(chan struct{})
			cpuSuppress.executor.Run(stopCh)
			defer func() {
				close(stopCh)
			}()
			if tt.args.currentPolicyStatus != nil {
				cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)] = *tt.args.currentPolicyStatus
			}
			cpuSuppress.recoverCPUSetIfNeed(koordletutil.ContainerCgroupPathRelativeDepth)
			gotPolicyStatus := cpuSuppress.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)]
			assert.Equal(t, *tt.wantPolicyStatus, gotPolicyStatus, "checkStatus")
			gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantCPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podDir := range podDirs {
				gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
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
			preBECfsQuota:    10 * system.DefaultCPUCFSPeriod,
			wantBECfsQuota:   -1,
			wantPolicyStatus: &policyRecovered,
		},
		{
			name:                "test need recover. currentPolicyStatus is policyUsing",
			preBECfsQuota:       10 * system.DefaultCPUCFSPeriod,
			currentPolicyStatus: &policyUsing,
			wantBECfsQuota:      -1,
			wantPolicyStatus:    &policyRecovered,
		},
		{
			name:                "test not need recover. currentPolicyStatus is policyRecovered",
			preBECfsQuota:       10 * system.DefaultCPUCFSPeriod,
			currentPolicyStatus: &policyRecovered,
			wantBECfsQuota:      10 * system.DefaultCPUCFSPeriod,
			wantPolicyStatus:    &policyRecovered,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			beQosDir := koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
			helper.CreateCgroupFile(beQosDir, system.CPUCFSQuota)

			helper.WriteCgroupFileContents(beQosDir, system.CPUCFSQuota, strconv.FormatInt(tt.preBECfsQuota, 10))

			r := framework.Options{
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			cpuSuppress := newTestCPUSuppress(&r)
			stop := make(chan struct{})
			assert.NotPanics(t, func() {
				cpuSuppress.init(stop)
			})

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
		cpus          int32
		processorInfo []koordletutil.ProcessorInfo
		oldCPUSetNum  int
	}
	tests := []struct {
		name string
		args args
		want []int32
	}{
		{
			name: "do not panic but return empty cpuset for insufficient cpus",
			args: args{
				cpus:          0,
				processorInfo: []koordletutil.ProcessorInfo{},
				oldCPUSetNum:  0,
			},
			want: nil,
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 0.",
			args: args{
				cpus: 3,
				processorInfo: []koordletutil.ProcessorInfo{
					{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
					{CPUID: 1, CoreID: 1, SocketID: 0, NodeID: 0},
					{CPUID: 2, CoreID: 2, SocketID: 1, NodeID: 1},
					{CPUID: 3, CoreID: 3, SocketID: 1, NodeID: 1},
					{CPUID: 4, CoreID: 0, SocketID: 0, NodeID: 0},
					{CPUID: 5, CoreID: 1, SocketID: 0, NodeID: 0},
					{CPUID: 6, CoreID: 2, SocketID: 1, NodeID: 1},
					{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
				},

				oldCPUSetNum: 3,
			},
			want: []int32{0, 4, 2},
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 1.",
			args: args{
				cpus: 3,
				processorInfo: []koordletutil.ProcessorInfo{
					{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
					{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
					{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
					{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
					{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
					{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
					{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
					{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
				},

				oldCPUSetNum: 5,
			},
			want: []int32{0, 1, 4},
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 2. (also scattering on sockets)",
			args: args{
				cpus: 5,
				processorInfo: []koordletutil.ProcessorInfo{
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

				oldCPUSetNum: 8,
			},
			want: []int32{0, 1, 4, 5, 8},
		},
		{
			name: "allocate cpus with scattering on numa nodes and stacking on HTs 3. (regardless of the initial order)",
			args: args{
				cpus: 5,
				processorInfo: []koordletutil.ProcessorInfo{
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

				oldCPUSetNum: 8,
			},
			want: []int32{0, 1, 4, 5, 8},
		},
		{
			name: "allocate cpus for slow scale up:increase cpunum == maxIncreaseCPUNum",
			args: args{
				cpus: 5,
				processorInfo: []koordletutil.ProcessorInfo{
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

				oldCPUSetNum: 3,
			},
			want: []int32{0, 1, 4, 5, 8},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateBESuppressCPUSetPolicy(tt.args.cpus, tt.args.processorInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_cpuSuppress_applyCPUSetWithNonePolicy(t *testing.T) {
	// prepare testing files
	helper := system.NewFileTestUtil(t)
	podDirs := []string{"pod1", "pod2", "pod3"}
	testingPrepareBECgroupData(helper, podDirs, "1,2")

	cpuset := []int32{3, 2, 1}
	wantCPUSetStr := "1-3"

	oldCPUSet, err := koordletutil.GetBECgroupCurCPUSet()
	assert.NoError(t, err)

	opt := &framework.Options{
		Config:              framework.NewDefaultConfig(),
		MetricAdvisorConfig: maframework.NewDefaultConfig(),
	}
	r := newTestCPUSuppress(opt)
	stop := make(chan struct{})
	assert.NotPanics(t, func() {
		r.init(stop)
	})

	err = r.applyCPUSetWithNonePolicy(cpuset, oldCPUSet)
	assert.NoError(t, err)
	gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
	assert.Equal(t, wantCPUSetStr, gotCPUSetBECgroup, "checkBECPUSet")
	for _, podDir := range podDirs {
		gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
		assert.Equal(t, wantCPUSetStr, gotPodCPUSet, "checkPodCPUSet")
	}
}

func Test_getBECgroupCPUSetPathsRecursive(t *testing.T) {
	// prepare testing files
	helper := system.NewFileTestUtil(t)
	podDirs := []string{"pod1", "pod2", "pod3"}
	testingPrepareBECgroupData(helper, podDirs, "1,2")
	var wantPaths []string
	wantPaths = append(wantPaths, koordletutil.GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort))
	for _, podDir := range podDirs {
		wantPaths = append(wantPaths, filepath.Join(koordletutil.GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort), podDir))
	}

	paths, err := koordletutil.GetBECPUSetPathsByMaxDepth(koordletutil.ContainerCgroupPathRelativeDepth)
	assert.NoError(t, err)
	assert.Equal(t, len(wantPaths), len(paths))
}

func Test_cpuSuppress_adjustByCPUSet(t *testing.T) {
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
					ProcessorInfos: []koordletutil.ProcessorInfo{
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
			wantCPUSet: "2-4",
		},
		{
			name: "test scale up by cpuset.",
			args: args{
				cpusetQuantity: resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					ProcessorInfos: []koordletutil.ProcessorInfo{
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
			wantCPUSet: "2-4",
		},
	}
	ctrl := gomock.NewController(t)
	mockStatesInformer := mockstatesinformer.NewMockStatesInformer(ctrl)
	lsrPod := mockLSRPod()
	lsePod := mockLSEPod()
	mockStatesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{{Pod: lsrPod}, {Pod: lsePod}}).AnyTimes()
	mockStatesInformer.EXPECT().GetNodeTopo().Return(&topov1alpha1.NodeResourceTopology{}).AnyTimes()
	opt := &framework.Options{
		StatesInformer:      mockStatesInformer,
		Config:              framework.NewDefaultConfig(),
		MetricAdvisorConfig: maframework.NewDefaultConfig(),
	}
	cpuSuppress := newTestCPUSuppress(opt)
	stop := make(chan struct{})
	assert.NotPanics(t, func() {
		cpuSuppress.init(stop)
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare testing files
			helper := system.NewFileTestUtil(t)
			podDirs := []string{"pod1", "pod2", "pod3"}
			testingPrepareBECgroupData(helper, podDirs, tt.args.oldCPUSets)

			cpuSuppress.adjustByCPUSet(tt.args.cpusetQuantity, tt.args.nodeCPUInfo)

			gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantCPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podDir := range podDirs {
				gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
				assert.Equal(t, tt.wantCPUSet, gotPodCPUSet, "checkPodCPUSet")
			}
		})
	}
}

func genNodeResourceTopo(annoReserv string) *topov1alpha1.NodeResourceTopology {
	return &topov1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				apiext.AnnotationNodeReservation: annoReserv,
			},
		},
	}
}

func genNodeTopoWithSystemQOSRes(annoReserv string) *topov1alpha1.NodeResourceTopology {
	return &topov1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				apiext.AnnotationNodeSystemQOSResource: annoReserv,
			},
		},
	}
}

func Test_cpuSuppress_adjustByCPUSet_withCPUsReservedFromNodeAnno(t *testing.T) {
	fakeNodeCPUInfo := metriccache.NodeCPUInfo{
		ProcessorInfos: []koordletutil.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
		},
	}
	type args struct {
		cpusetQuantity   *resource.Quantity
		nodeCPUInfo      *metriccache.NodeCPUInfo
		oldCPUSets       string
		nodeResourceTopo *topov1alpha1.NodeResourceTopology
	}
	tests := []struct {
		name       string
		args       args
		wantCPUSet string
	}{
		{ // total - node.anno.reserv - LSE.used > be.quantity, just select enough cpus to modify cgroup.
			name: "test scale by cpuset and ensure there is enough cpu available for bepod.",
			args: args{
				cpusetQuantity:   resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo:      &fakeNodeCPUInfo,
				oldCPUSets:       "7,6,3,2",
				nodeResourceTopo: genNodeResourceTopo(`{"reservedCPUs":"3"}`),
			},
			wantCPUSet: "0,4-5",
		},
		{ // total - node.anno.reserv - LSE.used < be.quantity, do nothing
			name: "test scale by cpuset and cpus that bepod can used less than bepod needed.",
			args: args{
				cpusetQuantity:   resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo:      &fakeNodeCPUInfo,
				oldCPUSets:       "7,6,3,2",
				nodeResourceTopo: genNodeResourceTopo(`{"reservedCPUs":"1-6"}`),
			},
			wantCPUSet: "7,6,3,2",
		},
		{ // total - node.anno.reserv - LSE.used == be.quantity, then change cgroup.
			name: "test scale by cpuset and just enough cpu allocated to bepod.",
			args: args{
				cpusetQuantity:   resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo:      &fakeNodeCPUInfo,
				oldCPUSets:       "7,6,3,2",
				nodeResourceTopo: genNodeResourceTopo(`{"reservedCPUs":"0-1,5-6"}`),
			},
			wantCPUSet: "2-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockStatesInformer := mockstatesinformer.NewMockStatesInformer(ctrl)
			lsrPod := mockLSRPod()
			lsePod := mockLSEPod()
			mockStatesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{{Pod: lsrPod}, {Pod: lsePod}}).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeTopo().Return(tt.args.nodeResourceTopo).AnyTimes()
			r := &framework.Options{
				StatesInformer:      mockStatesInformer,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			cpuSuppress := newTestCPUSuppress(r)
			stop := make(chan struct{})
			assert.NotPanics(t, func() {
				cpuSuppress.init(stop)
			})

			// prepare testing files
			helper := system.NewFileTestUtil(t)
			podDirs := []string{"pod1", "pod2", "pod3"}
			testingPrepareBECgroupData(helper, podDirs, tt.args.oldCPUSets)

			cpuSuppress.adjustByCPUSet(tt.args.cpusetQuantity, tt.args.nodeCPUInfo)

			gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantCPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podDir := range podDirs {
				gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
				assert.Equal(t, tt.wantCPUSet, gotPodCPUSet, "checkPodCPUSet")
			}
		})
	}
}

func Test_cpuSuppress_adjustByCPUSet_withSystemQOSResourceFromNodeAnno(t *testing.T) {
	fakeNodeCPUInfo := metriccache.NodeCPUInfo{
		ProcessorInfos: []koordletutil.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
		},
	}
	type args struct {
		cpusetQuantity   *resource.Quantity
		nodeCPUInfo      *metriccache.NodeCPUInfo
		oldCPUSets       string
		nodeResourceTopo *topov1alpha1.NodeResourceTopology
	}
	tests := []struct {
		name       string
		args       args
		wantCPUSet string
	}{
		{ // total - node.anno.reserv - LSE.used > be.quantity, just select enough cpus to modify cgroup.
			name: "test scale by cpuset and ensure there is enough cpu available for bepod.",
			args: args{
				cpusetQuantity:   resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo:      &fakeNodeCPUInfo,
				oldCPUSets:       "7,6,3,2",
				nodeResourceTopo: genNodeTopoWithSystemQOSRes(`{"cpuset":"3"}`),
			},
			wantCPUSet: "0,4-5",
		},
		{ // total - node.anno.reserv - LSE.used < be.quantity, do nothing
			name: "test scale by cpuset and cpus that be pod can used less than be pod needed. there is nothing to do because the cpu runs out",
			args: args{
				cpusetQuantity:   resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo:      &fakeNodeCPUInfo,
				oldCPUSets:       "7,6,3,2",
				nodeResourceTopo: genNodeTopoWithSystemQOSRes(`{"cpuset":"1-6"}`),
			},
			wantCPUSet: "7,6,3,2",
		},
		{ // total - node.anno.reserv - LSE.used == be.quantity, then change cgroup.
			name: "test scale by cpuset and just enough cpu allocated to bepod.",
			args: args{
				cpusetQuantity:   resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo:      &fakeNodeCPUInfo,
				oldCPUSets:       "7,6,3,2",
				nodeResourceTopo: genNodeTopoWithSystemQOSRes(`{"cpuset":"0-1,5-6"}`),
			},
			wantCPUSet: "2-4",
		},
		{ // system qos cpu is not exclusive
			name: "system qos cpu is not exclusive",
			args: args{
				cpusetQuantity:   resource.NewQuantity(3, resource.DecimalSI),
				nodeCPUInfo:      &fakeNodeCPUInfo,
				oldCPUSets:       "7,6,3,2",
				nodeResourceTopo: genNodeTopoWithSystemQOSRes(`{"cpuset":"1-6", "cpusetExclusive":false}`),
			},
			wantCPUSet: "2-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockStatesInformer := mockstatesinformer.NewMockStatesInformer(ctrl)
			lsrPod := mockLSRPod()
			lsePod := mockLSEPod()
			mockStatesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{{Pod: lsrPod}, {Pod: lsePod}}).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeTopo().Return(tt.args.nodeResourceTopo).AnyTimes()
			r := &framework.Options{
				StatesInformer:      mockStatesInformer,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			cpuSuppress := newTestCPUSuppress(r)
			stop := make(chan struct{})
			assert.NotPanics(t, func() {
				cpuSuppress.init(stop)
			})

			// prepare testing files
			helper := system.NewFileTestUtil(t)
			podDirs := []string{"pod1", "pod2", "pod3"}
			testingPrepareBECgroupData(helper, podDirs, tt.args.oldCPUSets)

			cpuSuppress.adjustByCPUSet(tt.args.cpusetQuantity, tt.args.nodeCPUInfo)

			gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wantCPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podDir := range podDirs {
				gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
				assert.Equal(t, tt.wantCPUSet, gotPodCPUSet, "checkPodCPUSet")
			}
		})
	}
}

func Test_cpuSuppress_adjustByCfsQuota(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	beQosDir := koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
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
			preBECfsQuota:  10 * system.DefaultCPUCFSPeriod,
			wantBECfsQuota: 10*system.DefaultCPUCFSPeriod + int64(beMaxIncreaseCPUPercent*80*float64(system.DefaultCPUCFSPeriod)),
		},
		{
			name:           "increase CFSQuota: preBECPU < cpuQuantity < preBECPU + maxIncreaseCPU",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  19 * system.DefaultCPUCFSPeriod,
			wantBECfsQuota: 20 * system.DefaultCPUCFSPeriod,
		},
		{
			name:           "suppress beCPU: preBECPU > cpuQuantity",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  24 * system.DefaultCPUCFSPeriod,
			wantBECfsQuota: 20 * system.DefaultCPUCFSPeriod,
		},
		{
			name:           "bypass minDeltaQuota!",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  int64(19.8 * float64(system.DefaultCPUCFSPeriod)),
			wantBECfsQuota: int64(19.8 * float64(system.DefaultCPUCFSPeriod)),
		},
		{
			name:           "minQuota 2000 not bypass!",
			cpuQuantity:    resource.NewMilliQuantity(1, resource.BinarySI),
			preBECfsQuota:  int64(0.8 * float64(system.DefaultCPUCFSPeriod)),
			wantBECfsQuota: 2000,
		},
		{
			name:           "increase CFSQuota: first time set quota with beUnsetQuota, scale without step limit",
			cpuQuantity:    resource.NewMilliQuantity(20*1000, resource.BinarySI),
			preBECfsQuota:  -1,
			wantBECfsQuota: 20 * system.DefaultCPUCFSPeriod,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			helper.WriteCgroupFileContents(beQosDir, system.CPUCFSQuota, strconv.FormatInt(tt.preBECfsQuota, 10))
			opt := &framework.Options{
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			r := newTestCPUSuppress(opt)
			stop := make(chan struct{})
			assert.NotPanics(t, func() {
				r.init(stop)
			})
			r.adjustByCfsQuota(tt.cpuQuantity, node)
			gotBECfsQuota := helper.ReadCgroupFileContents(beQosDir, system.CPUCFSQuota)
			if gotBECfsQuota != strconv.FormatInt(tt.wantBECfsQuota, 10) {
				t.Errorf("failed to adjustByCfsQuota, want file %v cfs_quota %v, got %v", system.GetCgroupFilePath(beQosDir, system.CPUCFSQuota), tt.wantBECfsQuota,
					gotBECfsQuota)
				return
			}
		})
	}
}

func Test_cpuSuppress_writeBECgroupsCPUSet(t *testing.T) {
	// prepare testing files
	helper := system.NewFileTestUtil(t)
	podDirs := []string{"pod1", "pod2", "pod3"}
	testingPrepareBECgroupData(helper, podDirs, "1,2")

	var dirPaths []string
	dirPaths = append(dirPaths, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort))
	for _, podDir := range podDirs {
		dirPaths = append(dirPaths, filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir))
	}

	opt := &framework.Options{
		Config:              framework.NewDefaultConfig(),
		MetricAdvisorConfig: maframework.NewDefaultConfig(),
	}
	r := newTestCPUSuppress(opt)
	stop := make(chan struct{})
	assert.NotPanics(t, func() {
		r.init(stop)
	})

	cpuSetStr := "0,1,2"
	r.writeBECgroupsCPUSet(dirPaths, cpuSetStr, false)

	gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
	assert.Equal(t, cpuSetStr, gotCPUSetBECgroup, "checkBECPUSet_reversed_false")
	for _, podDir := range podDirs {
		gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
		assert.Equal(t, cpuSetStr, gotPodCPUSet, "checkPodCPUSet_reversed_false")
	}

	cpuSetStr = "0,1"
	r.writeBECgroupsCPUSet(dirPaths, cpuSetStr, true)
	gotCPUSetBECgroup = helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
	assert.Equal(t, cpuSetStr, gotCPUSetBECgroup, "checkBECPUSet_reversed_true")
	for _, podDir := range podDirs {
		gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
		assert.Equal(t, cpuSetStr, gotPodCPUSet, "checkPodCPUSet_reversed_true")
	}
}

func testingPrepareBECgroupData(helper *system.FileTestUtil, podDirs []string, cpusets string) {
	helper.WriteCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), system.CPUSet, cpusets)
	helper.WriteCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet, cpusets)
	for _, podDir := range podDirs {
		helper.WriteCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet, cpusets)
	}
}

func testingPrepareBEContainerCgroupData(helper *system.FileTestUtil, groupDirs []string, cpusets string) {
	helper.WriteCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet, cpusets)
	for _, dir := range groupDirs {
		helper.WriteCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), dir), system.CPUSet, cpusets)
	}
}

func mockLSRPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-name-lsr",
			UID:       "test-pod-uid-lsr",
			Labels: map[string]string{
				apiext.LabelPodQoS: string(apiext.QoSLSR),
			},
			Annotations: map[string]string{
				apiext.AnnotationResourceStatus: "{\"cpuset\": \"0,6\" }",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-1",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(500, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(500, resource.DecimalSI),
						},
					},
				},
				{
					Name: "test-container-2",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(1000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(1000, resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}

func mockLSEPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-name-lsr",
			UID:       "test-pod-uid-lsr",
			Labels: map[string]string{
				apiext.LabelPodQoS: string(apiext.QoSLSE),
			},
			Annotations: map[string]string{
				apiext.AnnotationResourceStatus: "{\"cpuset\": \"7\" }",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-1",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(500, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(500, resource.DecimalSI),
						},
					},
				},
				{
					Name: "test-container-2",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(1000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(1000, resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}

func TestCPUSuppress_applyBESuppressCPUSet(t *testing.T) {
	mockNodeInfo := &metriccache.NodeCPUInfo{
		ProcessorInfos: []koordletutil.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 1},
			{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 8, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 9, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 10, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 11, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 12, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 13, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 14, CoreID: 3, SocketID: 1, NodeID: 1},
			{CPUID: 15, CoreID: 3, SocketID: 1, NodeID: 1},
		},
	}
	type fields struct {
		cpuPolicy *apiext.KubeletCPUManagerPolicy
	}
	type args struct {
		beCPUSet     []int32
		oldCPUSet    []int32
		oldCPUSetStr string
	}
	type wants struct {
		beDirCPUSet        string
		podDirCPUSet       string
		containerDirCPUSet string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		wants  wants
	}{
		{
			name: "apply with static poicy",
			fields: fields{
				cpuPolicy: &apiext.KubeletCPUManagerPolicy{
					Policy: apiext.KubeletCPUManagerPolicyStatic,
				},
			},
			args: args{
				beCPUSet:     []int32{0, 1, 2, 3},
				oldCPUSet:    []int32{0, 1, 2},
				oldCPUSetStr: "0-2",
			},
			wants: wants{
				beDirCPUSet:        "0-15",
				podDirCPUSet:       "0-15",
				containerDirCPUSet: "0-3",
			},
		},
	}
	for _, tt := range tests {
		helper := system.NewFileTestUtil(t)
		podDirs := []string{"pod1", "pod2", "pod3"}
		containerDirs := []string{
			"pod1/container11", "pod1/container12",
			"pod2/container21", "pod2/container22",
			"pod3/container31",
		}
		testDirs := []string{}
		testDirs = append(testDirs, podDirs...)
		testDirs = append(testDirs, containerDirs...)
		testingPrepareBEContainerCgroupData(helper, testDirs, tt.args.oldCPUSetStr)

		ctl := gomock.NewController(t)
		defer ctl.Finish()
		nodeTopo := &topov1alpha1.NodeResourceTopology{}
		if tt.fields.cpuPolicy != nil {
			cpuPolicyStr, _ := json.Marshal(tt.fields.cpuPolicy)
			nodeTopo.Annotations = map[string]string{
				apiext.AnnotationKubeletCPUManagerPolicy: string(cpuPolicyStr),
			}
		}
		t.Run(tt.name, func(t *testing.T) {
			si := mockstatesinformer.NewMockStatesInformer(ctl)
			si.EXPECT().GetNodeTopo().Return(nodeTopo).AnyTimes()
			si.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{}).AnyTimes()
			mc := mockmetriccache.NewMockMetricCache(ctl)
			mc.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(mockNodeInfo, true).AnyTimes()
			r := newTestCPUSuppress(&framework.Options{
				StatesInformer:      si,
				MetricCache:         mc,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			})
			stopCh := make(chan struct{})
			r.executor.Run(stopCh)
			defer func() {
				close(stopCh)
			}()

			err := r.applyBESuppressCPUSet(tt.args.beCPUSet, tt.args.oldCPUSet)

			assert.NoError(t, err)
			gotCPUSetBECgroup := helper.ReadCgroupFileContents(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), system.CPUSet)
			assert.Equal(t, tt.wants.beDirCPUSet, gotCPUSetBECgroup, "checkBECPUSet")
			for _, podDir := range podDirs {
				gotPodCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), podDir), system.CPUSet)
				assert.Equal(t, tt.wants.podDirCPUSet, gotPodCPUSet, "checkPodCPUSet")
			}
			for _, containerDir := range containerDirs {
				gotContainerCPUSet := helper.ReadCgroupFileContents(filepath.Join(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), containerDir), system.CPUSet)
				assert.Equal(t, tt.wants.containerDirCPUSet, gotContainerCPUSet, "checkContainerCPUSet")
			}
		})
	}
}

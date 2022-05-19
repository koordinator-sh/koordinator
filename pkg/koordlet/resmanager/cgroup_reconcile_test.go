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
	"encoding/json"
	"fmt"
	"math"
	"reflect"
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
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

var (
	testingPodMemRequestLimitBytes int64 = 1 << 30
)

func Test_calculateAndUpdateResources(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		},
	}
	testingQoSStrategyBE := &slov1alpha1.ResourceQoSStrategy{
		LSR: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		LS: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		BE: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(50),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
	}
	testingQoSStrategyLS := &slov1alpha1.ResourceQoSStrategy{
		LSR: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(100),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		LS: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(100),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		BE: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(50),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
	}
	testingQoSStrategyLSR := &slov1alpha1.ResourceQoSStrategy{
		LSR: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(100),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		LS: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		BE: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(50),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
	}
	testingQoSStrategyNone := &slov1alpha1.ResourceQoSStrategy{
		LSR: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		LS: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
		BE: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(50),
					PriorityEnable:    pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
				},
			},
		},
	}
	testingNonRunningPod := createPod(corev1.PodQOSBestEffort, apiext.QoSBE)
	testingNonRunningPod.Pod.Status.Phase = corev1.PodSucceeded
	type args struct {
		name        string
		qosStrategy *slov1alpha1.ResourceQoSStrategy
		podMetas    []*statesinformer.PodMeta
		expect      *slov1alpha1.ResourceQoSStrategy
	}
	tests := []args{
		{
			name:        "empty config with no pod",
			qosStrategy: defaultQoSStrategy(),
			expect:      defaultQoSStrategy(),
		},
		{
			name:        "valid config with no pod",
			qosStrategy: newValidQoSStrategy(),
			expect:      mergeWithDefaultQoSStrategy(newValidQoSStrategy()), // memory.wmark_xxx use default
		},
		{
			name: "mixed config with no pod",
			qosStrategy: &slov1alpha1.ResourceQoSStrategy{
				LSR: &slov1alpha1.ResourceQoS{
					MemoryQoS: &slov1alpha1.MemoryQoSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQoS: slov1alpha1.MemoryQoS{
							WmarkRatio:  pointer.Int64Ptr(101),
							WmarkMinAdj: pointer.Int64Ptr(-51),
						},
					},
				},
				LS: &slov1alpha1.ResourceQoS{
					MemoryQoS: &slov1alpha1.MemoryQoSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQoS: slov1alpha1.MemoryQoS{
							Priority:       pointer.Int64Ptr(6),
							PriorityEnable: pointer.Int64Ptr(1),
						},
					},
				},
				BE: &slov1alpha1.ResourceQoS{
					MemoryQoS: &slov1alpha1.MemoryQoSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQoS: slov1alpha1.MemoryQoS{
							WmarkRatio:        pointer.Int64Ptr(-1),
							WmarkScalePermill: pointer.Int64Ptr(20),
							WmarkMinAdj:       pointer.Int64Ptr(53),
							OomKillGroup:      pointer.Int64Ptr(1),
							PriorityEnable:    pointer.Int64Ptr(1),
						},
					},
				},
			},
			expect: &slov1alpha1.ResourceQoSStrategy{
				LSR: &slov1alpha1.ResourceQoS{
					MemoryQoS: &slov1alpha1.MemoryQoSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQoS: slov1alpha1.MemoryQoS{
							MinLimitPercent:   pointer.Int64Ptr(0),
							LowLimitPercent:   pointer.Int64Ptr(0),
							ThrottlingPercent: pointer.Int64Ptr(0),
							WmarkRatio:        pointer.Int64Ptr(95),
							WmarkScalePermill: pointer.Int64Ptr(20),
							WmarkMinAdj:       pointer.Int64Ptr(0),
							OomKillGroup:      pointer.Int64Ptr(0),
							Priority:          pointer.Int64Ptr(0),
							PriorityEnable:    pointer.Int64Ptr(0),
						},
					},
				},
				LS: &slov1alpha1.ResourceQoS{
					MemoryQoS: &slov1alpha1.MemoryQoSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQoS: slov1alpha1.MemoryQoS{
							MinLimitPercent:   pointer.Int64Ptr(0),
							LowLimitPercent:   pointer.Int64Ptr(0),
							ThrottlingPercent: pointer.Int64Ptr(0),
							WmarkRatio:        pointer.Int64Ptr(95),
							WmarkScalePermill: pointer.Int64Ptr(20),
							WmarkMinAdj:       pointer.Int64Ptr(0),
							OomKillGroup:      pointer.Int64Ptr(0),
							Priority:          pointer.Int64Ptr(6),
							PriorityEnable:    pointer.Int64Ptr(1),
						},
					},
				},
				BE: &slov1alpha1.ResourceQoS{
					MemoryQoS: &slov1alpha1.MemoryQoSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQoS: slov1alpha1.MemoryQoS{
							MinLimitPercent:   pointer.Int64Ptr(0),
							LowLimitPercent:   pointer.Int64Ptr(0),
							ThrottlingPercent: pointer.Int64Ptr(0),
							WmarkRatio:        pointer.Int64Ptr(80),
							WmarkScalePermill: pointer.Int64Ptr(20),
							WmarkMinAdj:       pointer.Int64Ptr(0),
							OomKillGroup:      pointer.Int64Ptr(1),
							Priority:          pointer.Int64Ptr(0),
							PriorityEnable:    pointer.Int64Ptr(1),
						},
					},
				},
			},
		},
		{
			name:        "calculate qos resources from a pod",
			qosStrategy: testingQoSStrategyBE,
			podMetas: []*statesinformer.PodMeta{
				createPod(corev1.PodQOSBestEffort, apiext.QoSBE),
			},
			expect: mergeWithDefaultQoSStrategy(testingQoSStrategyBE),
		},
		{
			name:        "calculate qos resources from a pod 1",
			qosStrategy: testingQoSStrategyLS,
			podMetas: []*statesinformer.PodMeta{
				createPod(corev1.PodQOSBurstable, apiext.QoSLS),
			},
			expect: mergeWithDefaultQoSStrategy(testingQoSStrategyLS),
		},
		{
			name:        "calculate qos resources from a pod 2",
			qosStrategy: testingQoSStrategyLSR,
			podMetas: []*statesinformer.PodMeta{
				createPod(corev1.PodQOSGuaranteed, apiext.QoSLSR),
			},
			expect: mergeWithDefaultQoSStrategy(testingQoSStrategyLSR),
		},
		{
			name:        "node disabled",
			qosStrategy: testingQoSStrategyNone,
			podMetas: []*statesinformer.PodMeta{
				createPodWithMemoryQoS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQoSConfig{Policy: slov1alpha1.PodMemoryQoSPolicyDefault}),
			},
			expect: mergeWithDefaultQoSStrategy(testingQoSStrategyNone),
		},
		{
			name:        "pod enabled while node disabled",
			qosStrategy: testingQoSStrategyBE,
			podMetas: []*statesinformer.PodMeta{
				createPodWithMemoryQoS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQoSConfig{Policy: slov1alpha1.PodMemoryQoSPolicyAuto}),
			},
			expect: mergeWithDefaultQoSStrategy(testingQoSStrategyBE),
		},
		{
			name:        "ignore non-running pod",
			qosStrategy: testingQoSStrategyBE,
			podMetas: []*statesinformer.PodMeta{
				testingNonRunningPod,
			},
			expect: defaultQoSStrategy(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			statesinformer := mockstatesinformer.NewMockStatesInformer(ctrl)
			resmgr := &resmanager{config: &Config{ReconcileIntervalSeconds: 1}, statesInformer: statesinformer}
			statesinformer.EXPECT().GetNode().Return(testingNode).MaxTimes(1)
			statesinformer.EXPECT().GetAllPods().Return(tt.podMetas).MaxTimes(1)

			reconciler := NewCgroupResourcesReconcile(resmgr)
			stop := make(chan struct{})
			err := reconciler.RunInit(stop)
			assert.NoError(t, err)
			defer func() { stop <- struct{}{} }()

			helper := system.NewFileTestUtil(t)

			initQoSStrategy := defaultQoSStrategy()
			initQoSCgroupFile(initQoSStrategy, helper)

			reconciler.calculateAndUpdateResources(createNodeSLOWithQoSStrategy(tt.qosStrategy))
			got := gotQoSStrategyFromFile()
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestCgroupResourceReconcile_calculateResources(t *testing.T) {
	testingPodLS := createPod(corev1.PodQOSBurstable, apiext.QoSLS)
	podParentDirLS := util.GetPodCgroupDirWithKube(testingPodLS.CgroupDir)
	containerDirLS, _ := util.GetContainerCgroupPathWithKube(testingPodLS.CgroupDir, &testingPodLS.Pod.Status.ContainerStatuses[0])
	containerDirLS1, _ := util.GetContainerCgroupPathWithKube(testingPodLS.CgroupDir, &testingPodLS.Pod.Status.ContainerStatuses[1])
	testingPodBEWithMemQoS := createPodWithMemoryQoS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQoSConfig{
		Policy: slov1alpha1.PodMemoryQoSPolicyAuto,
		MemoryQoS: slov1alpha1.MemoryQoS{
			MinLimitPercent:   pointer.Int64Ptr(100),
			LowLimitPercent:   pointer.Int64Ptr(0),
			ThrottlingPercent: pointer.Int64Ptr(80),
			WmarkRatio:        pointer.Int64Ptr(95),
			WmarkScalePermill: pointer.Int64Ptr(20),
			WmarkMinAdj:       pointer.Int64Ptr(50),
		},
	})
	testingPodBEWithMemQoS1 := createPodWithMemoryQoS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQoSConfig{
		Policy: slov1alpha1.PodMemoryQoSPolicyAuto,
		MemoryQoS: slov1alpha1.MemoryQoS{
			MinLimitPercent:   pointer.Int64Ptr(50),
			LowLimitPercent:   pointer.Int64Ptr(0),
			ThrottlingPercent: pointer.Int64Ptr(40),
			WmarkRatio:        pointer.Int64Ptr(95),
			WmarkScalePermill: pointer.Int64Ptr(20),
			WmarkMinAdj:       pointer.Int64Ptr(50),
		},
	})
	podParentDirBE := util.GetPodCgroupDirWithKube(testingPodBEWithMemQoS.CgroupDir)
	containerDirBE, _ := util.GetContainerCgroupPathWithKube(testingPodBEWithMemQoS.CgroupDir, &testingPodBEWithMemQoS.Pod.Status.ContainerStatuses[0])
	containerDirBE1, _ := util.GetContainerCgroupPathWithKube(testingPodBEWithMemQoS.CgroupDir, &testingPodBEWithMemQoS.Pod.Status.ContainerStatuses[1])
	type fields struct {
		resmanager *resmanager
	}
	type args struct {
		nodeCfg  *slov1alpha1.ResourceQoSStrategy
		node     *corev1.Node
		podMetas []*statesinformer.PodMeta
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []MergeableResourceUpdater // qosLevelResources
		want1  []MergeableResourceUpdater // podLevelResources
		want2  []MergeableResourceUpdater // containerLevelResources
	}{
		{
			name:   "not panic when no pods exists",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			// no resourceUpdater generated
		},
		{
			name:   "not panic when no pods exists with a valid resourceQoS config",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQoSStrategy{
					LSR: &slov1alpha1.ResourceQoS{},
					LS:  &slov1alpha1.ResourceQoS{},
					BE:  &slov1alpha1.ResourceQoS{},
				},
			},
			// no resourceUpdater generated
		},
		{
			name:   "config is empty",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQoSStrategy{
					LSR: &slov1alpha1.ResourceQoS{},
					LS:  &slov1alpha1.ResourceQoS{},
					BE:  &slov1alpha1.ResourceQoS{},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						CgroupDir: "pod0",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "pod0",
							},
						},
					},
				},
			},
			// no resourceUpdater generated
		},
		{
			name:   "single pod using node-level config",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQoSStrategy{
					LSR: defaultQoSStrategy().LSR,
					LS:  defaultQoSStrategy().LS,
					BE:  defaultQoSStrategy().BE,
				},
				podMetas: []*statesinformer.PodMeta{
					testingPodLS,
				},
			},
			want: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemOomGroup, "0"),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), util.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), util.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), util.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), util.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), util.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemOomGroup, "0"),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemOomGroup, "0"),
			},
			want1: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemWmarkMinAdj, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemOomGroup, "0"),
			},
			want2: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemHigh, strconv.FormatInt(math.MaxInt64, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemWmarkMinAdj, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemOomGroup, "0"),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemHigh, strconv.FormatInt(math.MaxInt64, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemWmarkMinAdj, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemOomGroup, "0"),
			},
		},
		{
			name:   "single pod using pod-level config",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQoSStrategy{
					LSR: &slov1alpha1.ResourceQoS{},
					LS:  &slov1alpha1.ResourceQoS{},
					BE:  &slov1alpha1.ResourceQoS{},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					testingPodBEWithMemQoS,
				},
			},
			want: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
			},
			want1: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemOomGroup, "0"),
			},
			want2: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*80/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemOomGroup, "0"),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemHigh, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemOomGroup, "0"),
			},
		},
		{
			name:   "multiple pods",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQoSStrategy{
					LSR: &slov1alpha1.ResourceQoS{},
					LS: &slov1alpha1.ResourceQoS{
						MemoryQoS: &slov1alpha1.MemoryQoSCfg{
							Enable: pointer.BoolPtr(false),
							MemoryQoS: slov1alpha1.MemoryQoS{
								MinLimitPercent:   pointer.Int64Ptr(0),
								LowLimitPercent:   pointer.Int64Ptr(0),
								ThrottlingPercent: pointer.Int64Ptr(0),
								WmarkRatio:        pointer.Int64Ptr(0),
								WmarkScalePermill: pointer.Int64Ptr(50),
								WmarkMinAdj:       pointer.Int64Ptr(0),
							},
						},
					},
					BE: &slov1alpha1.ResourceQoS{},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					testingPodLS,
					testingPodBEWithMemQoS,
				},
			},
			want: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), util.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), util.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
			},
			want1: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemWmarkRatio, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemWmarkScaleFactor, "50"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemWmarkMinAdj, "0"),
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemOomGroup, "0"),
			},
			want2: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemHigh, strconv.FormatInt(math.MaxInt64, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemWmarkRatio, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemWmarkScaleFactor, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemWmarkMinAdj, "0"),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemHigh, strconv.FormatInt(math.MaxInt64, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemWmarkRatio, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemWmarkScaleFactor, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemWmarkMinAdj, "0"),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*80/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemOomGroup, "0"),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemHigh, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemOomGroup, "0"),
			},
		},
		{
			name:   "single pod with memory.high is no less than memory.min",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQoSStrategy{
					LSR: &slov1alpha1.ResourceQoS{},
					LS:  &slov1alpha1.ResourceQoS{},
					BE:  &slov1alpha1.ResourceQoS{},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					testingPodBEWithMemQoS1,
				},
			},
			want: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSGuaranteed)), util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBestEffort)), util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
			},
			want1: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(PodOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name), podParentDirBE, system.MemOomGroup, "0"),
			},
			want2: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemMin, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*40/100, 10), mergeFuncUpdateCgroupIfLarger), // node allocatable * throttling factor
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "test"), containerDirBE, system.MemOomGroup, "0"),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemLow, "0", mergeFuncUpdateCgroupIfLarger),
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemWmarkMinAdj, "50"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemPriority, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemUsePriorityOom, "0"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef(testingPodBEWithMemQoS.Pod.Namespace, testingPodBEWithMemQoS.Pod.Name, "main"), containerDirBE1, system.MemOomGroup, "0"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewCgroupResourcesReconcile(tt.fields.resmanager)
			stop := make(chan struct{})
			err := m.RunInit(stop)
			assert.NoError(t, err)
			defer func() { stop <- struct{}{} }()

			system.NewFileTestUtil(t)

			got, got1, got2 := m.calculateResources(tt.args.nodeCfg, tt.args.node, tt.args.podMetas)
			assertCgroupResourceEqual(t, tt.want, got)
			assertCgroupResourceEqual(t, tt.want1, got1)
			assertCgroupResourceEqual(t, tt.want2, got2)
		})
	}
}

func TestCgroupResourcesReconcile_getMergedPodResourceQoS(t *testing.T) {
	testingNodeNoneResourceQoS := util.NoneResourceQoSStrategy().BE
	testingMemoryQoSEnableResourceQoS := util.DefaultResourceQoSStrategy().BE // qos enable
	testingMemoryQoSEnableResourceQoS.MemoryQoS.Enable = pointer.BoolPtr(true)
	testingMemoryQoSNoneResourceQoS := util.NoneResourceQoSStrategy().BE // qos disable
	testingMemoryQoSNoneResourceQoS.MemoryQoS = util.NoneResourceQoSStrategy().BE.MemoryQoS
	testingMemoryQoSNoneResourceQoS1 := util.DefaultResourceQoSStrategy().BE // qos partially disable
	testingMemoryQoSNoneResourceQoS1.MemoryQoS = util.NoneResourceQoSStrategy().BE.MemoryQoS
	testingMemoryQoSAutoResourceQoS := util.NoneResourceQoSStrategy().BE
	testingMemoryQoSAutoResourceQoS.MemoryQoS.MemoryQoS = *util.DefaultMemoryQoS(apiext.QoSBE)
	testingMemoryQoSAutoResourceQoS1 := util.DefaultResourceQoSStrategy().BE
	testingMemoryQoSAutoResourceQoS1.MemoryQoS.ThrottlingPercent = pointer.Int64Ptr(90)
	testingMemoryQoSAutoResourceQoS2 := &slov1alpha1.ResourceQoS{
		MemoryQoS: &slov1alpha1.MemoryQoSCfg{
			MemoryQoS: *util.DefaultMemoryQoS(apiext.QoSBE),
		},
	}
	testingMemoryQoSAutoResourceQoS2.MemoryQoS.ThrottlingPercent = pointer.Int64Ptr(90)
	type args struct {
		pod *corev1.Pod
		cfg *slov1alpha1.ResourceQoS
	}
	type fields struct {
		resmanager *resmanager
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *slov1alpha1.ResourceQoS
		wantErr bool
	}{
		{
			name: "node enabled, use node config",
			fields: fields{
				resmanager: &resmanager{
					config: NewDefaultConfig(),
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "",
						Labels: map[string]string{
							apiext.LabelPodQoS: string(apiext.QoSBE),
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
				cfg: defaultQoSStrategy().BE,
			},
			want: defaultQoSStrategy().BE,
		},
		{
			name: "pod policy is None, use pod config",
			fields: fields{
				resmanager: &resmanager{
					config: NewDefaultConfig(),
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels: map[string]string{
							apiext.LabelPodQoS: string(apiext.QoSBE),
						},
						Annotations: map[string]string{
							apiext.AnnotationPodMemoryQoS: `{"policy":"none"}`,
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
				cfg: util.DefaultResourceQoSStrategy().BE,
			},
			want: testingMemoryQoSNoneResourceQoS1,
		},
		{
			name: "pod policy is Auto, use pod config even if node disabled",
			fields: fields{
				resmanager: &resmanager{
					config: NewDefaultConfig(),
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels: map[string]string{
							apiext.LabelPodQoS: string(apiext.QoSBE),
						},
						Annotations: map[string]string{
							apiext.AnnotationPodMemoryQoS: `{"policy":"auto"}`,
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
				cfg: testingNodeNoneResourceQoS,
			},
			want: testingMemoryQoSAutoResourceQoS,
		},
		{
			name: "pod policy is Auto, use merged pod config",
			fields: fields{
				resmanager: &resmanager{
					config: NewDefaultConfig(),
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels: map[string]string{
							apiext.LabelPodQoS: string(apiext.QoSBE),
						},
						Annotations: map[string]string{
							apiext.AnnotationPodMemoryQoS: `{"policy":"auto","throttlingPercent":90}`,
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
				cfg: util.DefaultResourceQoSStrategy().BE,
			},
			want: testingMemoryQoSAutoResourceQoS1,
		},
		{
			name: "pod policy is Auto, use merged pod config when qos=None, kubeQoS=Besteffort",
			fields: fields{
				resmanager: &resmanager{
					config: NewDefaultConfig(),
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Annotations: map[string]string{
							// qosNone
							apiext.AnnotationPodMemoryQoS: `{"policy":"auto","throttlingPercent":90}`,
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
				cfg: &slov1alpha1.ResourceQoS{},
			},
			want: testingMemoryQoSAutoResourceQoS2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CgroupResourcesReconcile{resmanager: tt.fields.resmanager}
			got, gotErr := c.getMergedPodResourceQoS(tt.args.pod, tt.args.cfg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func Test_makeCgroupResources(t *testing.T) {
	type fields struct {
		notAnolisOS bool
	}
	type args struct {
		owner     *OwnerRef
		parentDir string
		summary   *cgroupResourceSummary
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []MergeableResourceUpdater
	}{
		{
			name:   "return nothing when kernel is not AnolisOS",
			fields: fields{notAnolisOS: true},
			want:   nil,
		},
		{
			name: "make qos resources",
			args: args{
				owner:     GroupOwnerRef(string(corev1.PodQOSBurstable)),
				parentDir: "burstable",
				summary: &cgroupResourceSummary{
					memoryWmarkRatio: pointer.Int64Ptr(90),
				},
			},
			want: []MergeableResourceUpdater{
				NewCommonCgroupResourceUpdater(GroupOwnerRef(string(corev1.PodQOSBurstable)), "burstable", system.MemWmarkRatio, "90"),
			},
		},
		{
			name: "make pod resources",
			args: args{
				owner:     PodOwnerRef("", "pod0"),
				parentDir: "pod0",
				summary: &cgroupResourceSummary{
					memoryMin:              pointer.Int64Ptr(testingPodMemRequestLimitBytes),
					memoryWmarkRatio:       pointer.Int64Ptr(95),
					memoryWmarkScaleFactor: pointer.Int64Ptr(20),
					memoryWmarkMinAdj:      pointer.Int64Ptr(-25),
				},
			},
			want: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(PodOwnerRef("", "pod0"), "pod0", system.MemMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(PodOwnerRef("", "pod0"), "pod0", system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(PodOwnerRef("", "pod0"), "pod0", system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(PodOwnerRef("", "pod0"), "pod0", system.MemWmarkMinAdj, "-25"),
			},
		},
		{
			name: "make container resources",
			args: args{
				owner:     ContainerOwnerRef("", "pod0", "container1"),
				parentDir: "pod0/container1",
				summary: &cgroupResourceSummary{
					memoryHigh:             pointer.Int64Ptr(testingPodMemRequestLimitBytes * 80 / 100),
					memoryWmarkRatio:       pointer.Int64Ptr(95),
					memoryWmarkScaleFactor: pointer.Int64Ptr(20),
					memoryWmarkMinAdj:      pointer.Int64Ptr(-25),
				},
			},
			want: []MergeableResourceUpdater{
				NewMergeableCgroupResourceUpdater(ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*80/100, 10), mergeFuncUpdateCgroupIfLarger),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemWmarkRatio, "95"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemWmarkScaleFactor, "20"),
				NewCommonCgroupResourceUpdater(ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemWmarkMinAdj, "-25"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsAnolisOS := system.HostSystemInfo.IsAnolisOS
			system.HostSystemInfo.IsAnolisOS = !tt.fields.notAnolisOS
			defer func() {
				system.HostSystemInfo.IsAnolisOS = oldIsAnolisOS
			}()

			got := makeCgroupResources(tt.args.owner, tt.args.parentDir, tt.args.summary)
			assertCgroupResourceEqual(t, tt.want, got)
		})
	}
}

func Test_getPodResourceQoSByQoSClass(t *testing.T) {
	type args struct {
		pod      *corev1.Pod
		strategy *slov1alpha1.ResourceQoSStrategy
		config   *Config
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceQoS
	}{
		{
			name: "return nil",
			args: args{},
			want: nil,
		},
		{
			name: "get qos=LS config",
			args: args{
				pod:      createPod(corev1.PodQOSBurstable, apiext.QoSLS).Pod,
				strategy: defaultQoSStrategy(),
				config:   NewDefaultConfig(),
			},
			want: defaultQoSStrategy().LS,
		},
		{
			name: "get qos=None kubeQoS=Burstable config",
			args: args{
				pod:      createPod(corev1.PodQOSBurstable, apiext.QoSNone).Pod,
				strategy: defaultQoSStrategy(),
				config:   NewDefaultConfig(),
			},
			want: defaultQoSStrategy().LS,
		},
		{
			name: "get qos=None kubeQoS=Besteffort config",
			args: args{
				pod:      createPod(corev1.PodQOSBestEffort, apiext.QoSNone).Pod,
				strategy: defaultQoSStrategy(),
				config:   NewDefaultConfig(),
			},
			want: defaultQoSStrategy().BE,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPodResourceQoSByQoSClass(tt.args.pod, tt.args.strategy, tt.args.config)
			assert.Equal(t, tt.want, got)
		})
	}
}

func defaultQoSStrategy() *slov1alpha1.ResourceQoSStrategy {
	return &slov1alpha1.ResourceQoSStrategy{
		LSR: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					PriorityEnable:    pointer.Int64Ptr(0),
				},
			},
		},
		LS: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					PriorityEnable:    pointer.Int64Ptr(0),
				},
			},
		},
		BE: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(80),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(0),
					OomKillGroup:      pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(0),
					PriorityEnable:    pointer.Int64Ptr(0),
				},
			},
		},
	}
}

func newValidQoSStrategy() *slov1alpha1.ResourceQoSStrategy {
	return &slov1alpha1.ResourceQoSStrategy{
		LSR: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(96),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					OomKillGroup:      pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(12),
					PriorityEnable:    pointer.Int64Ptr(1),
				},
			},
		},
		LS: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(95),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(-25),
					OomKillGroup:      pointer.Int64Ptr(0),
					Priority:          pointer.Int64Ptr(6),
					PriorityEnable:    pointer.Int64Ptr(1),
				},
			},
		},
		BE: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					MinLimitPercent:   pointer.Int64Ptr(0),
					LowLimitPercent:   pointer.Int64Ptr(0),
					ThrottlingPercent: pointer.Int64Ptr(0),
					WmarkRatio:        pointer.Int64Ptr(85),
					WmarkScalePermill: pointer.Int64Ptr(20),
					WmarkMinAdj:       pointer.Int64Ptr(50),
					OomKillGroup:      pointer.Int64Ptr(1),
					Priority:          pointer.Int64Ptr(0),
					PriorityEnable:    pointer.Int64Ptr(1),
				},
			},
		},
	}
}

func mergeWithDefaultQoSStrategy(cfg *slov1alpha1.ResourceQoSStrategy) *slov1alpha1.ResourceQoSStrategy {
	defaultCfg := defaultQoSStrategy()
	cfg.LSR.MemoryQoS.WmarkRatio = defaultCfg.LSR.MemoryQoS.WmarkRatio
	cfg.LSR.MemoryQoS.WmarkScalePermill = defaultCfg.LSR.MemoryQoS.WmarkScalePermill
	cfg.LSR.MemoryQoS.WmarkMinAdj = defaultCfg.LSR.MemoryQoS.WmarkMinAdj
	cfg.LS.MemoryQoS.WmarkRatio = defaultCfg.LS.MemoryQoS.WmarkRatio
	cfg.LS.MemoryQoS.WmarkScalePermill = defaultCfg.LS.MemoryQoS.WmarkScalePermill
	cfg.LS.MemoryQoS.WmarkMinAdj = defaultCfg.LS.MemoryQoS.WmarkMinAdj
	cfg.BE.MemoryQoS.WmarkRatio = defaultCfg.BE.MemoryQoS.WmarkRatio
	cfg.BE.MemoryQoS.WmarkScalePermill = defaultCfg.BE.MemoryQoS.WmarkScalePermill
	cfg.BE.MemoryQoS.WmarkMinAdj = defaultCfg.BE.MemoryQoS.WmarkMinAdj
	return cfg
}

func createPod(kubeQosClass corev1.PodQOSClass, qosClass apiext.QoSClass) *statesinformer.PodMeta {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test_pod",
			UID:  "test_pod",
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qosClass),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test",
				},
				{
					Name: "main",
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test",
					ContainerID: fmt.Sprintf("docker://%s", "test"),
				},
				{
					Name:        "main",
					ContainerID: fmt.Sprintf("docker://%s", "main"),
				},
			},
			QOSClass: kubeQosClass,
			Phase:    corev1.PodRunning,
		},
	}

	if qosClass == apiext.QoSBE {
		pod.Spec.Containers[1].Resources = corev1.ResourceRequirements{
			Requests: map[corev1.ResourceName]resource.Quantity{
				apiext.BatchCPU:    resource.MustParse("1024"),
				apiext.BatchMemory: resource.MustParse("1Gi"),
			},
			Limits: map[corev1.ResourceName]resource.Quantity{
				apiext.BatchCPU:    resource.MustParse("1024"),
				apiext.BatchMemory: resource.MustParse("1Gi"),
			},
		}
	} else {
		pod.Spec.Containers[1].Resources = corev1.ResourceRequirements{
			Requests: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Limits: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		}
	}

	return &statesinformer.PodMeta{
		CgroupDir: util.GetPodKubeRelativePath(pod),
		Pod:       pod,
	}
}

func createPodWithMemoryQoS(kubeQosClass corev1.PodQOSClass, qosClass apiext.QoSClass,
	memQoS *slov1alpha1.PodMemoryQoSConfig) *statesinformer.PodMeta {
	podMeta := createPod(kubeQosClass, qosClass)

	memQoSConfigBytes, _ := json.Marshal(memQoS)
	if podMeta.Pod.Annotations == nil {
		podMeta.Pod.Annotations = map[string]string{}
	}
	podMeta.Pod.Annotations[apiext.AnnotationPodMemoryQoS] = string(memQoSConfigBytes)
	return podMeta
}

func createNodeSLOWithQoSStrategy(qosStrategy *slov1alpha1.ResourceQoSStrategy) *slov1alpha1.NodeSLO {
	return &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceQoSStrategy: qosStrategy,
		},
	}
}

func assertCgroupResourceEqual(t *testing.T, expect, got []MergeableResourceUpdater) {
	assert.Equal(t, len(expect), len(got))
	for i := range expect {
		if i >= len(got) {
			t.Errorf("index %v of expect exceeds size of got (%v)", i, len(got))
			return
		}
		e, ok := expect[i].(*CgroupResourceUpdater)
		assert.Equal(t, true, ok, fmt.Sprintf("check for index %v", i))
		g, ok := got[i].(*CgroupResourceUpdater)
		assert.Equal(t, true, ok, fmt.Sprintf("check for index %v", i))
		// assert not support func arguments
		e.updateFunc = nil
		e.mergeUpdateFunc = nil
		g.updateFunc = nil
		g.mergeUpdateFunc = nil
		assert.Equal(t, e, g, fmt.Sprintf("check for index %v", i))
	}
}

func gotQoSStrategyFromFile() *slov1alpha1.ResourceQoSStrategy {
	strategy := &slov1alpha1.ResourceQoSStrategy{}
	strategy.LSR = readMemFromCgroupFile(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed))
	strategy.LS = readMemFromCgroupFile(util.GetKubeQosRelativePath(corev1.PodQOSBurstable))
	strategy.BE = readMemFromCgroupFile(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort))
	return strategy
}

func initQoSCgroupFile(qos *slov1alpha1.ResourceQoSStrategy, helper *system.FileTestUtil) {
	writeMemToCgroupFile(util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), qos.LSR, helper)
	writeMemToCgroupFile(util.GetKubeQosRelativePath(corev1.PodQOSBurstable), qos.LS, helper)
	writeMemToCgroupFile(util.GetKubeQosRelativePath(corev1.PodQOSBestEffort), qos.BE, helper)
}

func readMemFromCgroupFile(parentDir string) *slov1alpha1.ResourceQoS {
	resourceQoS := &slov1alpha1.ResourceQoS{
		MemoryQoS: &slov1alpha1.MemoryQoSCfg{},
	}

	// dynamic resources, calculate with pod request/limit=1GiB
	// testingPodMemRequestLimitBytes = 1073741824
	minLimitPercent, _ := system.CgroupFileReadInt(parentDir, system.MemMin)
	if minLimitPercent != nil {
		resourceQoS.MemoryQoS.MinLimitPercent = pointer.Int64Ptr((*minLimitPercent) * 100 / testingPodMemRequestLimitBytes)
	}
	lowLimitPercent, _ := system.CgroupFileReadInt(parentDir, system.MemLow)
	if lowLimitPercent != nil {
		resourceQoS.MemoryQoS.LowLimitPercent = pointer.Int64Ptr((*lowLimitPercent) * 100 / testingPodMemRequestLimitBytes)
	}
	throttlingPercent, _ := system.CgroupFileReadInt(parentDir, system.MemHigh)
	if throttlingPercent != nil {
		resourceQoS.MemoryQoS.ThrottlingPercent = pointer.Int64Ptr(0) // assert test setting disabled
	}
	// static resources
	resourceQoS.MemoryQoS.WmarkRatio, _ = system.CgroupFileReadInt(parentDir, system.MemWmarkRatio)
	resourceQoS.MemoryQoS.WmarkScalePermill, _ = system.CgroupFileReadInt(parentDir, system.MemWmarkScaleFactor)
	resourceQoS.MemoryQoS.WmarkMinAdj, _ = system.CgroupFileReadInt(parentDir, system.MemWmarkMinAdj)
	resourceQoS.MemoryQoS.PriorityEnable, _ = system.CgroupFileReadInt(parentDir, system.MemUsePriorityOom)
	resourceQoS.MemoryQoS.Priority, _ = system.CgroupFileReadInt(parentDir, system.MemPriority)
	resourceQoS.MemoryQoS.OomKillGroup, _ = system.CgroupFileReadInt(parentDir, system.MemOomGroup)

	// assume NONE cfg equals to disabled
	memoryQoSDisabled := reflect.DeepEqual(util.NoneMemoryQoS(), &resourceQoS.MemoryQoS)
	resourceQoS.MemoryQoS.Enable = pointer.BoolPtr(!memoryQoSDisabled)

	return resourceQoS
}

func writeMemToCgroupFile(parentDir string, qos *slov1alpha1.ResourceQoS, helper *system.FileTestUtil) {
	helper.WriteCgroupFileContents(parentDir, system.MemMin, "0")
	helper.WriteCgroupFileContents(parentDir, system.MemLow, "0")
	helper.WriteCgroupFileContents(parentDir, system.MemHigh, strconv.FormatInt(math.MaxInt64, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemWmarkRatio, strconv.FormatInt(*qos.MemoryQoS.WmarkRatio, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemWmarkMinAdj, strconv.FormatInt(*qos.MemoryQoS.WmarkMinAdj, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemWmarkScaleFactor, strconv.FormatInt(*qos.MemoryQoS.WmarkScalePermill, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemUsePriorityOom, strconv.FormatInt(*qos.MemoryQoS.PriorityEnable, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemPriority, strconv.FormatInt(*qos.MemoryQoS.Priority, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemOomGroup, strconv.FormatInt(*qos.MemoryQoS.OomKillGroup, 10))
}

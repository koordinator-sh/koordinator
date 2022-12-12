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
	"github.com/koordinator-sh/koordinator/pkg/koordlet/executor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
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
	testingQOSStrategyBE := &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
	testingQOSStrategyLS := &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
	testingQOSStrategyLSR := &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
	testingQOSStrategyNone := &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		qosStrategy *slov1alpha1.ResourceQOSStrategy
		podMetas    []*statesinformer.PodMeta
		expect      *slov1alpha1.ResourceQOSStrategy
	}
	tests := []args{
		{
			name:        "empty config with no pod",
			qosStrategy: defaultQOSStrategy(),
			expect:      defaultQOSStrategy(),
		},
		{
			name:        "valid config with no pod",
			qosStrategy: newValidQOSStrategy(),
			expect:      mergeWithDefaultQOSStrategy(newValidQOSStrategy()), // memory.wmark_xxx use default
		},
		{
			name: "mixed config with no pod",
			qosStrategy: &slov1alpha1.ResourceQOSStrategy{
				LSRClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							WmarkRatio:  pointer.Int64Ptr(101),
							WmarkMinAdj: pointer.Int64Ptr(-51),
						},
					},
				},
				LSClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							Priority:       pointer.Int64Ptr(6),
							PriorityEnable: pointer.Int64Ptr(1),
						},
					},
				},
				BEClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							WmarkRatio:        pointer.Int64Ptr(-1),
							WmarkScalePermill: pointer.Int64Ptr(20),
							WmarkMinAdj:       pointer.Int64Ptr(53),
							OomKillGroup:      pointer.Int64Ptr(1),
							PriorityEnable:    pointer.Int64Ptr(1),
						},
					},
				},
			},
			expect: &slov1alpha1.ResourceQOSStrategy{
				LSRClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
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
				LSClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
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
				BEClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.BoolPtr(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
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
			qosStrategy: testingQOSStrategyBE,
			podMetas: []*statesinformer.PodMeta{
				createPod(corev1.PodQOSBestEffort, apiext.QoSBE),
			},
			expect: mergeWithDefaultQOSStrategy(testingQOSStrategyBE),
		},
		{
			name:        "calculate qos resources from a pod 1",
			qosStrategy: testingQOSStrategyLS,
			podMetas: []*statesinformer.PodMeta{
				createPod(corev1.PodQOSBurstable, apiext.QoSLS),
			},
			expect: mergeWithDefaultQOSStrategy(testingQOSStrategyLS),
		},
		{
			name:        "calculate qos resources from a pod 2",
			qosStrategy: testingQOSStrategyLSR,
			podMetas: []*statesinformer.PodMeta{
				createPod(corev1.PodQOSGuaranteed, apiext.QoSLSR),
			},
			expect: mergeWithDefaultQOSStrategy(testingQOSStrategyLSR),
		},
		{
			name:        "node disabled",
			qosStrategy: testingQOSStrategyNone,
			podMetas: []*statesinformer.PodMeta{
				createPodWithMemoryQOS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQOSConfig{Policy: slov1alpha1.PodMemoryQOSPolicyDefault}),
			},
			expect: mergeWithDefaultQOSStrategy(testingQOSStrategyNone),
		},
		{
			name:        "pod enabled while node disabled",
			qosStrategy: testingQOSStrategyBE,
			podMetas: []*statesinformer.PodMeta{
				createPodWithMemoryQOS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQOSConfig{Policy: slov1alpha1.PodMemoryQOSPolicyAuto}),
			},
			expect: mergeWithDefaultQOSStrategy(testingQOSStrategyBE),
		},
		{
			name:        "ignore non-running pod",
			qosStrategy: testingQOSStrategyBE,
			podMetas: []*statesinformer.PodMeta{
				testingNonRunningPod,
			},
			expect: defaultQOSStrategy(),
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
			helper.SetAnolisOSResourcesSupported(true)

			initQOSStrategy := defaultQOSStrategy()
			initQOSCgroupFile(initQOSStrategy, helper)

			reconciler.calculateAndUpdateResources(createNodeSLOWithQOSStrategy(tt.qosStrategy))
			got := gotQOSStrategyFromFile()
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestCgroupResourceReconcile_calculateResources(t *testing.T) {
	testingPodLS := createPod(corev1.PodQOSBurstable, apiext.QoSLS)
	podParentDirLS := koordletutil.GetPodCgroupDirWithKube(testingPodLS.CgroupDir)
	containerDirLS, _ := koordletutil.GetContainerCgroupPathWithKube(testingPodLS.CgroupDir, &testingPodLS.Pod.Status.ContainerStatuses[0])
	containerDirLS1, _ := koordletutil.GetContainerCgroupPathWithKube(testingPodLS.CgroupDir, &testingPodLS.Pod.Status.ContainerStatuses[1])
	testingPodBEWithMemQOS := createPodWithMemoryQOS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQOSConfig{
		Policy: slov1alpha1.PodMemoryQOSPolicyAuto,
		MemoryQOS: slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64Ptr(100),
			LowLimitPercent:   pointer.Int64Ptr(0),
			ThrottlingPercent: pointer.Int64Ptr(80),
			WmarkRatio:        pointer.Int64Ptr(95),
			WmarkScalePermill: pointer.Int64Ptr(20),
			WmarkMinAdj:       pointer.Int64Ptr(50),
		},
	})
	testingPodBEWithMemQoS1 := createPodWithMemoryQOS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQOSConfig{
		Policy: slov1alpha1.PodMemoryQOSPolicyAuto,
		MemoryQOS: slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64Ptr(50),
			LowLimitPercent:   pointer.Int64Ptr(0),
			ThrottlingPercent: pointer.Int64Ptr(40),
			WmarkRatio:        pointer.Int64Ptr(95),
			WmarkScalePermill: pointer.Int64Ptr(20),
			WmarkMinAdj:       pointer.Int64Ptr(50),
		},
	})
	podParentDirBE := koordletutil.GetPodCgroupDirWithKube(testingPodBEWithMemQOS.CgroupDir)
	containerDirBE, _ := koordletutil.GetContainerCgroupPathWithKube(testingPodBEWithMemQOS.CgroupDir, &testingPodBEWithMemQOS.Pod.Status.ContainerStatuses[0])
	containerDirBE1, _ := koordletutil.GetContainerCgroupPathWithKube(testingPodBEWithMemQOS.CgroupDir, &testingPodBEWithMemQOS.Pod.Status.ContainerStatuses[1])
	type fields struct {
		resmanager *resmanager
	}
	type args struct {
		nodeCfg  *slov1alpha1.ResourceQOSStrategy
		node     *corev1.Node
		podMetas []*statesinformer.PodMeta
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []executor.MergeableResourceUpdater // qosLevelResources
		want1  []executor.MergeableResourceUpdater // podLevelResources
		want2  []executor.MergeableResourceUpdater // containerLevelResources
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
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: &slov1alpha1.ResourceQOS{},
					LSClass:  &slov1alpha1.ResourceQOS{},
					BEClass:  &slov1alpha1.ResourceQOS{},
				},
			},
			// no resourceUpdater generated
		},
		{
			name:   "config is empty",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: &slov1alpha1.ResourceQOS{},
					LSClass:  &slov1alpha1.ResourceQOS{},
					BEClass:  &slov1alpha1.ResourceQOS{},
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
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: defaultQOSStrategy().LSRClass,
					LSClass:  defaultQOSStrategy().LSClass,
					BEClass:  defaultQOSStrategy().BEClass,
				},
				podMetas: []*statesinformer.PodMeta{
					testingPodLS,
				},
			},
			want: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryOomGroup, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemoryOomGroup, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryOomGroup, "0"),
			},
			want1: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryWmarkMinAdj, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryOomGroup, "0"),
			},
			want2: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryHigh, strconv.FormatInt(math.MaxInt64, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryWmarkMinAdj, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryOomGroup, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryHigh, strconv.FormatInt(math.MaxInt64, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryWmarkMinAdj, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryOomGroup, "0"),
			},
		},
		{
			name:   "single pod using pod-level config",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: &slov1alpha1.ResourceQOS{},
					LSClass:  &slov1alpha1.ResourceQOS{},
					BEClass:  &slov1alpha1.ResourceQOS{},
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
					testingPodBEWithMemQOS,
				},
			},
			want: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
			},
			want1: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryOomGroup, "0"),
			},
			want2: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*80/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryOomGroup, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryHigh, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryOomGroup, "0"),
			},
		},
		{
			name:   "multiple pods",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: &slov1alpha1.ResourceQOS{},
					LSClass: &slov1alpha1.ResourceQOS{
						MemoryQOS: &slov1alpha1.MemoryQOSCfg{
							Enable: pointer.BoolPtr(false),
							MemoryQOS: slov1alpha1.MemoryQOS{
								MinLimitPercent:   pointer.Int64Ptr(0),
								LowLimitPercent:   pointer.Int64Ptr(0),
								ThrottlingPercent: pointer.Int64Ptr(0),
								WmarkRatio:        pointer.Int64Ptr(0),
								WmarkScalePermill: pointer.Int64Ptr(50),
								WmarkMinAdj:       pointer.Int64Ptr(0),
							},
						},
					},
					BEClass: &slov1alpha1.ResourceQOS{},
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
					testingPodBEWithMemQOS,
				},
			},
			want: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
			},
			want1: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryWmarkRatio, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryWmarkScaleFactor, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name), podParentDirLS, system.MemoryWmarkMinAdj, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryOomGroup, "0"),
			},
			want2: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryHigh, strconv.FormatInt(math.MaxInt64, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryWmarkRatio, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryWmarkScaleFactor, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "test"), containerDirLS, system.MemoryWmarkMinAdj, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryHigh, strconv.FormatInt(math.MaxInt64, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryWmarkRatio, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryWmarkScaleFactor, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodLS.Pod.Namespace, testingPodLS.Pod.Name, "main"), containerDirLS1, system.MemoryWmarkMinAdj, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*80/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryOomGroup, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryHigh, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryOomGroup, "0"),
			},
		},
		{
			name:   "single pod with memory.high is no less than memory.min",
			fields: fields{resmanager: &resmanager{config: NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: &slov1alpha1.ResourceQOS{},
					LSClass:  &slov1alpha1.ResourceQOS{},
					BEClass:  &slov1alpha1.ResourceQOS{},
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
			want: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSGuaranteed)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBestEffort)), koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
			},
			want1: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name), podParentDirBE, system.MemoryOomGroup, "0"),
			},
			want2: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryMin, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*40/100, 10), executor.MergeFuncUpdateCgroupIfLarger), // node allocatable * throttling factor
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "test"), containerDirBE, system.MemoryOomGroup, "0"),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryLow, "0", executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryWmarkMinAdj, "50"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryPriority, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryUsePriorityOom, "0"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef(testingPodBEWithMemQOS.Pod.Namespace, testingPodBEWithMemQOS.Pod.Name, "main"), containerDirBE1, system.MemoryOomGroup, "0"),
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
	testingNodeNoneResourceQoS := util.NoneResourceQOSStrategy().BEClass
	testingMemoryQoSEnableResourceQoS := util.DefaultResourceQOSStrategy().BEClass // qos enable
	testingMemoryQoSEnableResourceQoS.MemoryQOS.Enable = pointer.BoolPtr(true)
	testingMemoryQoSNoneResourceQoS := util.NoneResourceQOSStrategy().BEClass // qos disable
	testingMemoryQoSNoneResourceQoS.MemoryQOS = util.NoneResourceQOSStrategy().BEClass.MemoryQOS
	testingMemoryQoSNoneResourceQoS1 := util.DefaultResourceQOSStrategy().BEClass // qos partially disable
	testingMemoryQoSNoneResourceQoS1.MemoryQOS = util.NoneResourceQOSStrategy().BEClass.MemoryQOS
	testingMemoryQoSAutoResourceQoS := util.NoneResourceQOSStrategy().BEClass
	testingMemoryQoSAutoResourceQoS.MemoryQOS.MemoryQOS = *util.DefaultMemoryQOS(apiext.QoSBE)
	testingMemoryQoSAutoResourceQoS1 := util.DefaultResourceQOSStrategy().BEClass
	testingMemoryQoSAutoResourceQoS1.MemoryQOS.ThrottlingPercent = pointer.Int64Ptr(90)
	testingMemoryQoSAutoResourceQoS2 := &slov1alpha1.ResourceQOS{
		MemoryQOS: &slov1alpha1.MemoryQOSCfg{
			MemoryQOS: *util.DefaultMemoryQOS(apiext.QoSBE),
		},
	}
	testingMemoryQoSAutoResourceQoS2.MemoryQOS.ThrottlingPercent = pointer.Int64Ptr(90)
	type args struct {
		pod *corev1.Pod
		cfg *slov1alpha1.ResourceQOS
	}
	type fields struct {
		resmanager *resmanager
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *slov1alpha1.ResourceQOS
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
				cfg: defaultQOSStrategy().BEClass,
			},
			want: defaultQOSStrategy().BEClass,
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
				cfg: util.DefaultResourceQOSStrategy().BEClass,
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
				cfg: util.DefaultResourceQOSStrategy().BEClass,
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
				cfg: &slov1alpha1.ResourceQOS{},
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
		owner     *executor.OwnerRef
		parentDir string
		summary   *cgroupResourceSummary
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []executor.MergeableResourceUpdater
	}{
		{
			name:   "return nothing when kernel is not AnolisOS",
			fields: fields{notAnolisOS: true},
			want:   nil,
		},
		{
			name: "make qos resources",
			args: args{
				owner:     executor.GroupOwnerRef(string(corev1.PodQOSBurstable)),
				parentDir: "burstable",
				summary: &cgroupResourceSummary{
					memoryWmarkRatio: pointer.Int64Ptr(90),
				},
			},
			want: []executor.MergeableResourceUpdater{
				executor.NewCommonCgroupResourceUpdater(executor.GroupOwnerRef(string(corev1.PodQOSBurstable)), "burstable", system.MemoryWmarkRatio, "90"),
			},
		},
		{
			name: "make pod resources",
			args: args{
				owner:     executor.PodOwnerRef("", "pod0"),
				parentDir: "pod0",
				summary: &cgroupResourceSummary{
					memoryMin:              pointer.Int64Ptr(testingPodMemRequestLimitBytes),
					memoryWmarkRatio:       pointer.Int64Ptr(95),
					memoryWmarkScaleFactor: pointer.Int64Ptr(20),
					memoryWmarkMinAdj:      pointer.Int64Ptr(-25),
				},
			},
			want: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.PodOwnerRef("", "pod0"), "pod0", system.MemoryMin, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef("", "pod0"), "pod0", system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef("", "pod0"), "pod0", system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.PodOwnerRef("", "pod0"), "pod0", system.MemoryWmarkMinAdj, "-25"),
			},
		},
		{
			name: "make container resources",
			args: args{
				owner:     executor.ContainerOwnerRef("", "pod0", "container1"),
				parentDir: "pod0/container1",
				summary: &cgroupResourceSummary{
					memoryHigh:             pointer.Int64Ptr(testingPodMemRequestLimitBytes * 80 / 100),
					memoryWmarkRatio:       pointer.Int64Ptr(95),
					memoryWmarkScaleFactor: pointer.Int64Ptr(20),
					memoryWmarkMinAdj:      pointer.Int64Ptr(-25),
				},
			},
			want: []executor.MergeableResourceUpdater{
				executor.NewMergeableCgroupResourceUpdater(executor.ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemoryHigh, strconv.FormatInt(testingPodMemRequestLimitBytes*80/100, 10), executor.MergeFuncUpdateCgroupIfLarger),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemoryWmarkRatio, "95"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemoryWmarkScaleFactor, "20"),
				executor.NewCommonCgroupResourceUpdater(executor.ContainerOwnerRef("", "pod0", "container1"), "pod0/container1", system.MemoryWmarkMinAdj, "-25"),
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
		strategy *slov1alpha1.ResourceQOSStrategy
		config   *Config
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceQOS
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
				strategy: defaultQOSStrategy(),
				config:   NewDefaultConfig(),
			},
			want: defaultQOSStrategy().LSClass,
		},
		{
			name: "get qos=None kubeQoS=Burstable config",
			args: args{
				pod:      createPod(corev1.PodQOSBurstable, apiext.QoSNone).Pod,
				strategy: defaultQOSStrategy(),
				config:   NewDefaultConfig(),
			},
			want: defaultQOSStrategy().LSClass,
		},
		{
			name: "get qos=None kubeQoS=Besteffort config",
			args: args{
				pod:      createPod(corev1.PodQOSBestEffort, apiext.QoSNone).Pod,
				strategy: defaultQOSStrategy(),
				config:   NewDefaultConfig(),
			},
			want: defaultQOSStrategy().BEClass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPodResourceQoSByQoSClass(tt.args.pod, tt.args.strategy, tt.args.config)
			assert.Equal(t, tt.want, got)
		})
	}
}

func defaultQOSStrategy() *slov1alpha1.ResourceQOSStrategy {
	return &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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

func newValidQOSStrategy() *slov1alpha1.ResourceQOSStrategy {
	return &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
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

func mergeWithDefaultQOSStrategy(cfg *slov1alpha1.ResourceQOSStrategy) *slov1alpha1.ResourceQOSStrategy {
	defaultCfg := defaultQOSStrategy()
	cfg.LSRClass.MemoryQOS.WmarkRatio = defaultCfg.LSRClass.MemoryQOS.WmarkRatio
	cfg.LSRClass.MemoryQOS.WmarkScalePermill = defaultCfg.LSRClass.MemoryQOS.WmarkScalePermill
	cfg.LSRClass.MemoryQOS.WmarkMinAdj = defaultCfg.LSRClass.MemoryQOS.WmarkMinAdj
	cfg.LSClass.MemoryQOS.WmarkRatio = defaultCfg.LSClass.MemoryQOS.WmarkRatio
	cfg.LSClass.MemoryQOS.WmarkScalePermill = defaultCfg.LSClass.MemoryQOS.WmarkScalePermill
	cfg.LSClass.MemoryQOS.WmarkMinAdj = defaultCfg.LSClass.MemoryQOS.WmarkMinAdj
	cfg.BEClass.MemoryQOS.WmarkRatio = defaultCfg.BEClass.MemoryQOS.WmarkRatio
	cfg.BEClass.MemoryQOS.WmarkScalePermill = defaultCfg.BEClass.MemoryQOS.WmarkScalePermill
	cfg.BEClass.MemoryQOS.WmarkMinAdj = defaultCfg.BEClass.MemoryQOS.WmarkMinAdj
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
		CgroupDir: koordletutil.GetPodKubeRelativePath(pod),
		Pod:       pod,
	}
}

func createPodWithMemoryQOS(kubeQosClass corev1.PodQOSClass, qosClass apiext.QoSClass,
	memQoS *slov1alpha1.PodMemoryQOSConfig) *statesinformer.PodMeta {
	podMeta := createPod(kubeQosClass, qosClass)

	memQoSConfigBytes, _ := json.Marshal(memQoS)
	if podMeta.Pod.Annotations == nil {
		podMeta.Pod.Annotations = map[string]string{}
	}
	podMeta.Pod.Annotations[apiext.AnnotationPodMemoryQoS] = string(memQoSConfigBytes)
	return podMeta
}

func createNodeSLOWithQOSStrategy(qosStrategy *slov1alpha1.ResourceQOSStrategy) *slov1alpha1.NodeSLO {
	return &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceQOSStrategy: qosStrategy,
		},
	}
}

func assertCgroupResourceEqual(t *testing.T, expect, got []executor.MergeableResourceUpdater) {
	assert.Equal(t, len(expect), len(got))
	for i := range expect {
		if i >= len(got) {
			t.Errorf("index %v of expect exceeds size of got (%v)", i, len(got))
			return
		}
		e, ok := expect[i].(*executor.CgroupResourceUpdater)
		assert.Equal(t, true, ok, fmt.Sprintf("check for index %v", i))
		g, ok := got[i].(*executor.CgroupResourceUpdater)
		assert.Equal(t, true, ok, fmt.Sprintf("check for index %v", i))
		// assert not support func arguments
		e.ClearUpdateFunc()
		g.ClearUpdateFunc()
		assert.Equal(t, e, g, fmt.Sprintf("check for index %v", i))
	}
}

func gotQOSStrategyFromFile() *slov1alpha1.ResourceQOSStrategy {
	strategy := &slov1alpha1.ResourceQOSStrategy{}
	strategy.LSRClass = readMemFromCgroupFile(koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed))
	strategy.LSClass = readMemFromCgroupFile(koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable))
	strategy.BEClass = readMemFromCgroupFile(koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort))
	return strategy
}

func initQOSCgroupFile(qos *slov1alpha1.ResourceQOSStrategy, helper *system.FileTestUtil) {
	writeMemToCgroupFile(koordletutil.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), qos.LSRClass, helper)
	writeMemToCgroupFile(koordletutil.GetKubeQosRelativePath(corev1.PodQOSBurstable), qos.LSClass, helper)
	writeMemToCgroupFile(koordletutil.GetKubeQosRelativePath(corev1.PodQOSBestEffort), qos.BEClass, helper)
}

func readMemFromCgroupFile(parentDir string) *slov1alpha1.ResourceQOS {
	resourceQoS := &slov1alpha1.ResourceQOS{
		MemoryQOS: &slov1alpha1.MemoryQOSCfg{},
	}

	// dynamic resources, calculate with pod request/limit=1GiB
	// testingPodMemRequestLimitBytes = 1073741824
	minLimitPercent, _ := system.CgroupFileReadInt(parentDir, system.MemoryMin)
	if minLimitPercent != nil {
		resourceQoS.MemoryQOS.MinLimitPercent = pointer.Int64Ptr((*minLimitPercent) * 100 / testingPodMemRequestLimitBytes)
	}
	lowLimitPercent, _ := system.CgroupFileReadInt(parentDir, system.MemoryLow)
	if lowLimitPercent != nil {
		resourceQoS.MemoryQOS.LowLimitPercent = pointer.Int64Ptr((*lowLimitPercent) * 100 / testingPodMemRequestLimitBytes)
	}
	throttlingPercent, _ := system.CgroupFileReadInt(parentDir, system.MemoryHigh)
	if throttlingPercent != nil {
		resourceQoS.MemoryQOS.ThrottlingPercent = pointer.Int64Ptr(0) // assert test setting disabled
	}
	// static resources
	resourceQoS.MemoryQOS.WmarkRatio, _ = system.CgroupFileReadInt(parentDir, system.MemoryWmarkRatio)
	resourceQoS.MemoryQOS.WmarkScalePermill, _ = system.CgroupFileReadInt(parentDir, system.MemoryWmarkScaleFactor)
	resourceQoS.MemoryQOS.WmarkMinAdj, _ = system.CgroupFileReadInt(parentDir, system.MemoryWmarkMinAdj)
	resourceQoS.MemoryQOS.PriorityEnable, _ = system.CgroupFileReadInt(parentDir, system.MemoryUsePriorityOom)
	resourceQoS.MemoryQOS.Priority, _ = system.CgroupFileReadInt(parentDir, system.MemoryPriority)
	resourceQoS.MemoryQOS.OomKillGroup, _ = system.CgroupFileReadInt(parentDir, system.MemoryOomGroup)

	// assume NONE cfg equals to disabled
	memoryQoSDisabled := reflect.DeepEqual(util.NoneMemoryQOS(), &resourceQoS.MemoryQOS)
	resourceQoS.MemoryQOS.Enable = pointer.BoolPtr(!memoryQoSDisabled)

	return resourceQoS
}

func writeMemToCgroupFile(parentDir string, qos *slov1alpha1.ResourceQOS, helper *system.FileTestUtil) {
	helper.WriteCgroupFileContents(parentDir, system.MemoryMin, "0")
	helper.WriteCgroupFileContents(parentDir, system.MemoryLow, "0")
	helper.WriteCgroupFileContents(parentDir, system.MemoryHigh, strconv.FormatInt(math.MaxInt64, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemoryWmarkRatio, strconv.FormatInt(*qos.MemoryQOS.WmarkRatio, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemoryWmarkMinAdj, strconv.FormatInt(*qos.MemoryQOS.WmarkMinAdj, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemoryWmarkScaleFactor, strconv.FormatInt(*qos.MemoryQOS.WmarkScalePermill, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemoryUsePriorityOom, strconv.FormatInt(*qos.MemoryQOS.PriorityEnable, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemoryPriority, strconv.FormatInt(*qos.MemoryQOS.Priority, 10))
	helper.WriteCgroupFileContents(parentDir, system.MemoryOomGroup, strconv.FormatInt(*qos.MemoryQOS.OomKillGroup, 10))
}

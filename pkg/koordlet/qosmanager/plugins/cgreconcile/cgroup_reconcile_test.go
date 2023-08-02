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

package cgreconcile

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/util/cache"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

var (
	testingPodMemRequestLimitBytes int64 = 1 << 30
)

func TestNewCgroupResourcesReconcile(t *testing.T) {
	opt := &framework.Options{
		Config: framework.NewDefaultConfig(),
	}
	assert.NotPanics(t, func() {
		r := New(opt)
		assert.NotNil(t, r)
	})
}

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
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(50),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
	}
	testingQOSStrategyLS := &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(100),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(100),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(50),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
	}
	testingQOSStrategyLSR := &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(100),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(50),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
	}
	testingQOSStrategyNone := &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(50),
					PriorityEnable:    pointer.Int64(0),
					Priority:          pointer.Int64(0),
					OomKillGroup:      pointer.Int64(0),
				},
			},
		},
	}
	testingNonRunningPod := testutil.MockTestPodWithQOS(corev1.PodQOSBestEffort, apiext.QoSBE)
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
			qosStrategy: testutil.DefaultQOSStrategy(),
			expect:      testutil.DefaultQOSStrategy(),
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
						Enable: pointer.Bool(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							WmarkRatio:  pointer.Int64(101),
							WmarkMinAdj: pointer.Int64(-51),
						},
					},
				},
				LSClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.Bool(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							Priority:       pointer.Int64(6),
							PriorityEnable: pointer.Int64(1),
						},
					},
				},
				BEClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.Bool(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							WmarkRatio:        pointer.Int64(-1),
							WmarkScalePermill: pointer.Int64(20),
							WmarkMinAdj:       pointer.Int64(53),
							OomKillGroup:      pointer.Int64(1),
							PriorityEnable:    pointer.Int64(1),
						},
					},
				},
			},
			expect: &slov1alpha1.ResourceQOSStrategy{
				LSRClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.Bool(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							MinLimitPercent:   pointer.Int64(0),
							LowLimitPercent:   pointer.Int64(0),
							ThrottlingPercent: pointer.Int64(0),
							WmarkRatio:        pointer.Int64(95),
							WmarkScalePermill: pointer.Int64(20),
							WmarkMinAdj:       pointer.Int64(0),
							OomKillGroup:      pointer.Int64(0),
							Priority:          pointer.Int64(0),
							PriorityEnable:    pointer.Int64(0),
						},
					},
				},
				LSClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.Bool(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							MinLimitPercent:   pointer.Int64(0),
							LowLimitPercent:   pointer.Int64(0),
							ThrottlingPercent: pointer.Int64(0),
							WmarkRatio:        pointer.Int64(95),
							WmarkScalePermill: pointer.Int64(20),
							WmarkMinAdj:       pointer.Int64(0),
							OomKillGroup:      pointer.Int64(0),
							Priority:          pointer.Int64(6),
							PriorityEnable:    pointer.Int64(1),
						},
					},
				},
				BEClass: &slov1alpha1.ResourceQOS{
					MemoryQOS: &slov1alpha1.MemoryQOSCfg{
						Enable: pointer.Bool(true),
						MemoryQOS: slov1alpha1.MemoryQOS{
							MinLimitPercent:   pointer.Int64(0),
							LowLimitPercent:   pointer.Int64(0),
							ThrottlingPercent: pointer.Int64(0),
							WmarkRatio:        pointer.Int64(80),
							WmarkScalePermill: pointer.Int64(20),
							WmarkMinAdj:       pointer.Int64(0),
							OomKillGroup:      pointer.Int64(1),
							Priority:          pointer.Int64(0),
							PriorityEnable:    pointer.Int64(1),
						},
					},
				},
			},
		},
		{
			name:        "calculate qos resources from a pod",
			qosStrategy: testingQOSStrategyBE,
			podMetas: []*statesinformer.PodMeta{
				testutil.MockTestPodWithQOS(corev1.PodQOSBestEffort, apiext.QoSBE),
			},
			expect: mergeWithDefaultQOSStrategy(testingQOSStrategyBE),
		},
		{
			name:        "calculate qos resources from a pod 1",
			qosStrategy: testingQOSStrategyLS,
			podMetas: []*statesinformer.PodMeta{
				testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS),
			},
			expect: mergeWithDefaultQOSStrategy(testingQOSStrategyLS),
		},
		{
			name:        "calculate qos resources from a pod 2",
			qosStrategy: testingQOSStrategyLSR,
			podMetas: []*statesinformer.PodMeta{
				testutil.MockTestPodWithQOS(corev1.PodQOSGuaranteed, apiext.QoSLSR),
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
			expect: testutil.DefaultQOSStrategy(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			statesInformer := mockstatesinformer.NewMockStatesInformer(ctrl)
			opt := &framework.Options{
				StatesInformer: statesInformer,
				Config:         framework.NewDefaultConfig(),
			}
			statesInformer.EXPECT().GetNode().Return(testingNode).MaxTimes(1)
			statesInformer.EXPECT().GetAllPods().Return(tt.podMetas).MaxTimes(1)

			reconciler := newTestCgroupResourcesReconcile(opt)
			stop := make(chan struct{})
			assert.NotPanics(t, func() {
				reconciler.init(stop)
			})
			defer func() { stop <- struct{}{} }()

			helper := system.NewFileTestUtil(t)
			helper.SetAnolisOSResourcesSupported(true)

			initQOSStrategy := testutil.DefaultQOSStrategy()
			initQOSCgroupFile(initQOSStrategy, helper)

			reconciler.calculateAndUpdateResources(createNodeSLOWithQOSStrategy(tt.qosStrategy))
			got := gotQOSStrategyFromFile(helper)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestCgroupResourceReconcile_calculateResources(t *testing.T) {
	testingPodLS := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	podParentDirLS := testingPodLS.CgroupDir
	containerDirLS, _ := koordletutil.GetContainerCgroupParentDir(testingPodLS.CgroupDir, &testingPodLS.Pod.Status.ContainerStatuses[0])
	containerDirLS1, _ := koordletutil.GetContainerCgroupParentDir(testingPodLS.CgroupDir, &testingPodLS.Pod.Status.ContainerStatuses[1])
	testingPodBEWithMemQOS := createPodWithMemoryQOS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQOSConfig{
		Policy: slov1alpha1.PodMemoryQOSPolicyAuto,
		MemoryQOS: slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64(100),
			LowLimitPercent:   pointer.Int64(0),
			ThrottlingPercent: pointer.Int64(80),
			WmarkRatio:        pointer.Int64(95),
			WmarkScalePermill: pointer.Int64(20),
			WmarkMinAdj:       pointer.Int64(50),
		},
	})
	testingPodBEWithMemQoS1 := createPodWithMemoryQOS(corev1.PodQOSBestEffort, apiext.QoSBE, &slov1alpha1.PodMemoryQOSConfig{
		Policy: slov1alpha1.PodMemoryQOSPolicyAuto,
		MemoryQOS: slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64(50),
			LowLimitPercent:   pointer.Int64(0),
			ThrottlingPercent: pointer.Int64(40),
			WmarkRatio:        pointer.Int64(95),
			WmarkScalePermill: pointer.Int64(20),
			WmarkMinAdj:       pointer.Int64(50),
		},
	})
	podParentDirBE := testingPodBEWithMemQOS.CgroupDir
	containerDirBE, _ := koordletutil.GetContainerCgroupParentDir(testingPodBEWithMemQOS.CgroupDir, &testingPodBEWithMemQOS.Pod.Status.ContainerStatuses[0])
	containerDirBE1, _ := koordletutil.GetContainerCgroupParentDir(testingPodBEWithMemQOS.CgroupDir, &testingPodBEWithMemQOS.Pod.Status.ContainerStatuses[1])
	type fields struct {
		opt *framework.Options
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
		want   []resourceexecutor.ResourceUpdater // qosLevelResources
		want1  []resourceexecutor.ResourceUpdater // podLevelResources
		want2  []resourceexecutor.ResourceUpdater // containerLevelResources
	}{
		{
			name:   "not panic when no pods exists",
			fields: fields{opt: &framework.Options{Config: framework.NewDefaultConfig()}},
			// no resourceUpdater generated
		},
		{
			name:   "not panic when no pods exists with a valid resourceQoS config",
			fields: fields{opt: &framework.Options{Config: framework.NewDefaultConfig()}},
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
			fields: fields{opt: &framework.Options{Config: framework.NewDefaultConfig()}},
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
			fields: fields{opt: &framework.Options{Config: framework.NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: testutil.DefaultQOSStrategy().LSRClass,
					LSClass:  testutil.DefaultQOSStrategy().LSClass,
					BEClass:  testutil.DefaultQOSStrategy().BEClass,
				},
				podMetas: []*statesinformer.PodMeta{
					testingPodLS,
				},
			},
			want: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", true),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", false),
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), "0", true),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), "0", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), "0", false),
			},
			want1: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, podParentDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, podParentDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, podParentDirLS, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, podParentDirLS, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, podParentDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, podParentDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, podParentDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, podParentDirLS, "0", false),
			},
			want2: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirLS, strconv.FormatInt(math.MaxInt64, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirLS, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirLS, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirLS1, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirLS1, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirLS1, strconv.FormatInt(math.MaxInt64, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirLS1, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirLS1, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirLS1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirLS1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirLS1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirLS1, "0", false),
			},
		},
		{
			name:   "single pod using pod-level config",
			fields: fields{opt: &framework.Options{Config: framework.NewDefaultConfig()}},
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
			want: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", true),
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), "0", true),
			},
			want1: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, podParentDirBE, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, podParentDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, podParentDirBE, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, podParentDirBE, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, podParentDirBE, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, podParentDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, podParentDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, podParentDirBE, "0", false),
			},
			want2: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirBE, strconv.FormatInt(((testingPodMemRequestLimitBytes*80/100)/system.PageSize)*system.PageSize, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirBE, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirBE, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirBE, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirBE1, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirBE1, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirBE1, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirBE1, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirBE1, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirBE1, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirBE1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirBE1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirBE1, "0", false),
			},
		},
		{
			name:   "multiple pods",
			fields: fields{opt: &framework.Options{Config: framework.NewDefaultConfig()}},
			args: args{
				nodeCfg: &slov1alpha1.ResourceQOSStrategy{
					LSRClass: &slov1alpha1.ResourceQOS{},
					LSClass: &slov1alpha1.ResourceQOS{
						MemoryQOS: &slov1alpha1.MemoryQOSCfg{
							Enable: pointer.Bool(false),
							MemoryQOS: slov1alpha1.MemoryQOS{
								MinLimitPercent:   pointer.Int64(0),
								LowLimitPercent:   pointer.Int64(0),
								ThrottlingPercent: pointer.Int64(0),
								WmarkRatio:        pointer.Int64(0),
								WmarkScalePermill: pointer.Int64(50),
								WmarkMinAdj:       pointer.Int64(0),
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
			want: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", true),
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), "0", true),
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), "0", true),
			},
			want1: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, podParentDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, podParentDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, podParentDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, podParentDirLS, "50", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, podParentDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryMinName, podParentDirBE, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, podParentDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, podParentDirBE, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, podParentDirBE, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, podParentDirBE, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, podParentDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, podParentDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, podParentDirBE, "0", false),
			},
			want2: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirLS, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirLS, strconv.FormatInt(math.MaxInt64, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirLS, "50", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirLS, "0", false),
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirLS1, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirLS1, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirLS1, strconv.FormatInt(math.MaxInt64, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirLS1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirLS1, "50", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirLS1, "0", false),

				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirBE, strconv.FormatInt(((testingPodMemRequestLimitBytes*80/100)/system.PageSize)*system.PageSize, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirBE, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirBE, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirBE, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirBE1, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirBE1, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirBE1, strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirBE1, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirBE1, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirBE1, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirBE1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirBE1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirBE1, "0", false),
			},
		},
		{
			name:   "single pod with memory.high is no less than memory.min",
			fields: fields{opt: &framework.Options{Config: framework.NewDefaultConfig()}},
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
			want: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "0", true),
				createCgroupResourceUpdater(t, system.MemoryMinName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), "0", true),
			},
			want1: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, podParentDirBE, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, podParentDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, podParentDirBE, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, podParentDirBE, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, podParentDirBE, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, podParentDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, podParentDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, podParentDirBE, "0", false),
			},
			want2: []resourceexecutor.ResourceUpdater{
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirBE, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirBE, strconv.FormatInt(((testingPodMemRequestLimitBytes*40/100)/system.PageSize)*system.PageSize, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirBE, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirBE, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirBE, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirBE, "0", false),
				createCgroupResourceUpdater(t, system.MemoryMinName, containerDirBE1, strconv.FormatInt(testingPodMemRequestLimitBytes*50/100, 10), true),
				createCgroupResourceUpdater(t, system.MemoryLowName, containerDirBE1, "0", true),
				createCgroupResourceUpdater(t, system.MemoryHighName, containerDirBE1, strconv.FormatInt(((testingPodMemRequestLimitBytes)/system.PageSize)*system.PageSize, 10), true),
				createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, containerDirBE1, "95", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, containerDirBE1, "20", false),
				createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, containerDirBE1, "50", false),
				createCgroupResourceUpdater(t, system.MemoryPriorityName, containerDirBE1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryUsePriorityOomName, containerDirBE1, "0", false),
				createCgroupResourceUpdater(t, system.MemoryOomGroupName, containerDirBE1, "0", false),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			helper.SetCgroupsV2(false)
			defer helper.Cleanup()

			m := newTestCgroupResourcesReconcile(tt.fields.opt)
			stop := make(chan struct{})
			assert.NotPanics(t, func() {
				m.init(stop)
			})
			defer func() { stop <- struct{}{} }()

			got, got1, got2 := m.calculateResources(tt.args.nodeCfg, tt.args.node, tt.args.podMetas)
			assertCgroupResourceEqual(t, tt.want, got)
			assertCgroupResourceEqual(t, tt.want1, got1)
			assertCgroupResourceEqual(t, tt.want2, got2)
		})
	}
}

func TestCgroupResourcesReconcile_getMergedPodResourceQoS(t *testing.T) {
	testingNodeNoneResourceQoS := sloconfig.NoneResourceQOSStrategy().BEClass
	testingMemoryQoSEnableResourceQoS := sloconfig.DefaultResourceQOSStrategy().BEClass // qos enable
	testingMemoryQoSEnableResourceQoS.MemoryQOS.Enable = pointer.Bool(true)
	testingMemoryQoSNoneResourceQoS := sloconfig.NoneResourceQOSStrategy().BEClass // qos disable
	testingMemoryQoSNoneResourceQoS.MemoryQOS = sloconfig.NoneResourceQOSStrategy().BEClass.MemoryQOS
	testingMemoryQoSNoneResourceQoS1 := sloconfig.DefaultResourceQOSStrategy().BEClass // qos partially disable
	testingMemoryQoSNoneResourceQoS1.MemoryQOS = sloconfig.NoneResourceQOSStrategy().BEClass.MemoryQOS
	testingMemoryQoSAutoResourceQoS := sloconfig.NoneResourceQOSStrategy().BEClass
	testingMemoryQoSAutoResourceQoS.MemoryQOS.MemoryQOS = *sloconfig.DefaultMemoryQOS(apiext.QoSBE)
	testingMemoryQoSAutoResourceQoS1 := sloconfig.DefaultResourceQOSStrategy().BEClass
	testingMemoryQoSAutoResourceQoS1.MemoryQOS.ThrottlingPercent = pointer.Int64(90)
	testingMemoryQoSAutoResourceQoS2 := &slov1alpha1.ResourceQOS{
		MemoryQOS: &slov1alpha1.MemoryQOSCfg{
			MemoryQOS: *sloconfig.DefaultMemoryQOS(apiext.QoSBE),
		},
	}
	testingMemoryQoSAutoResourceQoS2.MemoryQOS.ThrottlingPercent = pointer.Int64(90)
	type args struct {
		pod *corev1.Pod
		cfg *slov1alpha1.ResourceQOS
	}
	type fields struct {
		opt *framework.Options
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
				opt: &framework.Options{Config: framework.NewDefaultConfig()},
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
				cfg: testutil.DefaultQOSStrategy().BEClass,
			},
			want: testutil.DefaultQOSStrategy().BEClass,
		},
		{
			name: "pod policy is None, use pod config",
			fields: fields{
				opt: &framework.Options{Config: framework.NewDefaultConfig()},
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
							slov1alpha1.AnnotationPodMemoryQoS: `{"policy":"none"}`,
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
				cfg: sloconfig.DefaultResourceQOSStrategy().BEClass,
			},
			want: testingMemoryQoSNoneResourceQoS1,
		},
		{
			name: "pod policy is Auto, use pod config even if node disabled",
			fields: fields{
				opt: &framework.Options{Config: framework.NewDefaultConfig()},
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
							slov1alpha1.AnnotationPodMemoryQoS: `{"policy":"auto"}`,
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
				opt: &framework.Options{Config: framework.NewDefaultConfig()},
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
							slov1alpha1.AnnotationPodMemoryQoS: `{"policy":"auto","throttlingPercent":90}`,
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
				cfg: sloconfig.DefaultResourceQOSStrategy().BEClass,
			},
			want: testingMemoryQoSAutoResourceQoS1,
		},
		{
			name: "pod policy is Auto, use merged pod config when qos=None, kubeQoS=Besteffort",
			fields: fields{
				opt: &framework.Options{Config: framework.NewDefaultConfig()},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Annotations: map[string]string{
							// qosNone
							slov1alpha1.AnnotationPodMemoryQoS: `{"policy":"auto","throttlingPercent":90}`,
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
			ci := New(tt.fields.opt)
			c := ci.(*cgroupResourcesReconcile)
			got, gotErr := c.getMergedPodResourceQoS(tt.args.pod, tt.args.cfg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func Test_makeCgroupResources(t *testing.T) {
	type fields struct {
		notAnolisOS bool
		useCgroupV2 bool
	}
	type args struct {
		parentDir string
		summary   *cgroupResourceSummary
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   func() []resourceexecutor.ResourceUpdater // for generating cgroups-v2 resources
	}{
		{
			name: "make qos resources",
			args: args{
				parentDir: "burstable",
				summary: &cgroupResourceSummary{
					memoryWmarkRatio: pointer.Int64(90),
				},
			},
			want: func() []resourceexecutor.ResourceUpdater {
				return []resourceexecutor.ResourceUpdater{
					createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, "burstable", "90", false),
				}
			},
		},
		{
			name: "make pod resources",
			args: args{
				parentDir: "pod0",
				summary: &cgroupResourceSummary{
					memoryMin:              pointer.Int64(testingPodMemRequestLimitBytes),
					memoryWmarkRatio:       pointer.Int64(95),
					memoryWmarkScaleFactor: pointer.Int64(20),
					memoryWmarkMinAdj:      pointer.Int64(-25),
				},
			},
			want: func() []resourceexecutor.ResourceUpdater {
				return []resourceexecutor.ResourceUpdater{
					createCgroupResourceUpdater(t, system.MemoryMinName, "pod0", strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
					createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, "pod0", "95", false),
					createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, "pod0", "20", false),
					createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, "pod0", "-25", false),
				}
			},
		},
		{
			name: "make container resources",
			args: args{
				parentDir: "pod0/container1",
				summary: &cgroupResourceSummary{
					memoryHigh:             pointer.Int64(testingPodMemRequestLimitBytes * 80 / 100),
					memoryWmarkRatio:       pointer.Int64(95),
					memoryWmarkScaleFactor: pointer.Int64(20),
					memoryWmarkMinAdj:      pointer.Int64(50),
				},
			},
			want: func() []resourceexecutor.ResourceUpdater {
				return []resourceexecutor.ResourceUpdater{
					createCgroupResourceUpdater(t, system.MemoryHighName, "pod0/container1", strconv.FormatInt(testingPodMemRequestLimitBytes*80/100, 10), true),
					createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, "pod0/container1", "95", false),
					createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, "pod0/container1", "20", false),
					createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, "pod0/container1", "50", false),
				}
			},
		},
		{
			name: "make container resources on cgroups v2",
			fields: fields{
				useCgroupV2: true,
			},
			args: args{
				parentDir: "pod1/container0",
				summary: &cgroupResourceSummary{
					memoryMin:              pointer.Int64(testingPodMemRequestLimitBytes),
					memoryWmarkRatio:       pointer.Int64(95),
					memoryWmarkScaleFactor: pointer.Int64(20),
					memoryWmarkMinAdj:      pointer.Int64(-25),
				},
			},
			want: func() []resourceexecutor.ResourceUpdater {
				return []resourceexecutor.ResourceUpdater{
					createCgroupResourceUpdater(t, system.MemoryMinName, "pod1/container0", strconv.FormatInt(testingPodMemRequestLimitBytes, 10), true),
					createCgroupResourceUpdater(t, system.MemoryWmarkRatioName, "pod1/container0", "95", false),
					createCgroupResourceUpdater(t, system.MemoryWmarkScaleFactorName, "pod1/container0", "20", false),
					createCgroupResourceUpdater(t, system.MemoryWmarkMinAdjName, "pod1/container0", "-25", false),
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			helper.SetCgroupsV2(tt.fields.useCgroupV2)
			defer helper.Cleanup()
			oldIsAnolisOS := system.HostSystemInfo.IsAnolisOS
			system.HostSystemInfo.IsAnolisOS = !tt.fields.notAnolisOS
			defer func() {
				system.HostSystemInfo.IsAnolisOS = oldIsAnolisOS
			}()

			got := makeCgroupResources(tt.args.parentDir, tt.args.summary)
			want := tt.want()
			assertCgroupResourceEqual(t, want, got)
		})
	}
}

func newTestCgroupResourcesReconcile(opt *framework.Options) *cgroupResourcesReconcile {
	return &cgroupResourcesReconcile{
		reconcileInterval: time.Duration(opt.Config.ReconcileIntervalSeconds) * time.Second,
		statesInformer:    opt.StatesInformer,
		executor: &resourceexecutor.ResourceUpdateExecutorImpl{
			Config:        resourceexecutor.NewDefaultConfig(),
			ResourceCache: cache.NewCacheDefault(),
		},
	}
}

func newValidQOSStrategy() *slov1alpha1.ResourceQOSStrategy {
	return &slov1alpha1.ResourceQOSStrategy{
		LSRClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(96),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					OomKillGroup:      pointer.Int64(0),
					Priority:          pointer.Int64(12),
					PriorityEnable:    pointer.Int64(1),
				},
			},
		},
		LSClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(95),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(-25),
					OomKillGroup:      pointer.Int64(0),
					Priority:          pointer.Int64(6),
					PriorityEnable:    pointer.Int64(1),
				},
			},
		},
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					MinLimitPercent:   pointer.Int64(0),
					LowLimitPercent:   pointer.Int64(0),
					ThrottlingPercent: pointer.Int64(0),
					WmarkRatio:        pointer.Int64(85),
					WmarkScalePermill: pointer.Int64(20),
					WmarkMinAdj:       pointer.Int64(50),
					OomKillGroup:      pointer.Int64(1),
					Priority:          pointer.Int64(0),
					PriorityEnable:    pointer.Int64(1),
				},
			},
		},
	}
}

func mergeWithDefaultQOSStrategy(cfg *slov1alpha1.ResourceQOSStrategy) *slov1alpha1.ResourceQOSStrategy {
	defaultCfg := testutil.DefaultQOSStrategy()
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

func createPodWithMemoryQOS(kubeQosClass corev1.PodQOSClass, qosClass apiext.QoSClass,
	memQoS *slov1alpha1.PodMemoryQOSConfig) *statesinformer.PodMeta {
	podMeta := testutil.MockTestPodWithQOS(kubeQosClass, qosClass)

	memQoSConfigBytes, _ := json.Marshal(memQoS)
	if podMeta.Pod.Annotations == nil {
		podMeta.Pod.Annotations = map[string]string{}
	}
	podMeta.Pod.Annotations[slov1alpha1.AnnotationPodMemoryQoS] = string(memQoSConfigBytes)
	return podMeta
}

func createNodeSLOWithQOSStrategy(qosStrategy *slov1alpha1.ResourceQOSStrategy) *slov1alpha1.NodeSLO {
	return &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceQOSStrategy: qosStrategy,
		},
	}
}

func createCgroupResourceUpdater(t *testing.T, resourceType system.ResourceType, parentDir string, value string, isMergeable bool) resourceexecutor.ResourceUpdater {
	var u resourceexecutor.ResourceUpdater
	var err error
	if isMergeable {
		u, err = resourceexecutor.NewMergeableCgroupUpdaterIfValueLarger(resourceType, parentDir, value, nil)
	} else {
		u, err = resourceexecutor.NewCommonCgroupUpdater(resourceType, parentDir, value, nil)
	}
	assert.NoError(t, err)
	return u
}

func assertCgroupResourceEqual(t *testing.T, expect, got []resourceexecutor.ResourceUpdater) {
	assert.Equal(t, len(expect), len(got))
	for i := range expect {
		if i >= len(got) {
			t.Errorf("index %v of expect exceeds size of got (%v)", i, len(got))
			return
		}
		e, ok := expect[i].(*resourceexecutor.CgroupResourceUpdater)
		assert.Equal(t, true, ok, fmt.Sprintf("check for index %v", i))
		g, ok := got[i].(*resourceexecutor.CgroupResourceUpdater)
		assert.Equal(t, true, ok, fmt.Sprintf("check for index %v", i))
		// assert not support func arguments
		e.SetUpdateFunc(nil, nil)
		g.SetUpdateFunc(nil, nil)
		// set expect eventhelper like reality
		e.SeteventHelper(g.GeteventHelper())
		assert.Equal(t, e, g, fmt.Sprintf("check for index %v", i))
	}
}

func gotQOSStrategyFromFile(helper *system.FileTestUtil) *slov1alpha1.ResourceQOSStrategy {
	strategy := &slov1alpha1.ResourceQOSStrategy{}
	strategy.LSRClass = readMemFromCgroupFile(koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), helper)
	strategy.LSClass = readMemFromCgroupFile(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), helper)
	strategy.BEClass = readMemFromCgroupFile(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), helper)
	return strategy
}

func initQOSCgroupFile(qos *slov1alpha1.ResourceQOSStrategy, helper *system.FileTestUtil) {
	writeMemToCgroupFile(koordletutil.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), qos.LSRClass, helper)
	writeMemToCgroupFile(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBurstable), qos.LSClass, helper)
	writeMemToCgroupFile(koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort), qos.BEClass, helper)
}

func readMemFromCgroupFile(parentDir string, helper *system.FileTestUtil) *slov1alpha1.ResourceQOS {
	resourceQoS := &slov1alpha1.ResourceQOS{
		MemoryQOS: &slov1alpha1.MemoryQOSCfg{},
	}

	// dynamic resources, calculate with pod request/limit=1GiB
	// testingPodMemRequestLimitBytes = 1073741824
	minLimitPercent, _ := cgroupFileReadIntforTest(parentDir, system.MemoryMin)
	if minLimitPercent != nil {
		resourceQoS.MemoryQOS.MinLimitPercent = pointer.Int64((*minLimitPercent) * 100 / testingPodMemRequestLimitBytes)
	}
	lowLimitPercent, _ := cgroupFileReadIntforTest(parentDir, system.MemoryLow)
	if lowLimitPercent != nil {
		resourceQoS.MemoryQOS.LowLimitPercent = pointer.Int64((*lowLimitPercent) * 100 / testingPodMemRequestLimitBytes)
	}
	throttlingPercent, _ := cgroupFileReadIntforTest(parentDir, system.MemoryHigh)
	if throttlingPercent != nil {
		resourceQoS.MemoryQOS.ThrottlingPercent = pointer.Int64(0) // assert test setting disabled
	}
	// static resources
	resourceQoS.MemoryQOS.WmarkRatio, _ = cgroupFileReadIntforTest(parentDir, system.MemoryWmarkRatio)
	resourceQoS.MemoryQOS.WmarkScalePermill, _ = cgroupFileReadIntforTest(parentDir, system.MemoryWmarkScaleFactor)
	resourceQoS.MemoryQOS.WmarkMinAdj, _ = cgroupFileReadIntforTest(parentDir, system.MemoryWmarkMinAdj)
	resourceQoS.MemoryQOS.PriorityEnable, _ = cgroupFileReadIntforTest(parentDir, system.MemoryUsePriorityOom)
	resourceQoS.MemoryQOS.Priority, _ = cgroupFileReadIntforTest(parentDir, system.MemoryPriority)
	resourceQoS.MemoryQOS.OomKillGroup, _ = cgroupFileReadIntforTest(parentDir, system.MemoryOomGroup)

	// assume NONE cfg equals to disabled
	memoryQoSDisabled := reflect.DeepEqual(sloconfig.NoneMemoryQOS(), &resourceQoS.MemoryQOS)
	resourceQoS.MemoryQOS.Enable = pointer.Bool(!memoryQoSDisabled)

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

// This function is only intended for testing functions within the current file. For specific read/write functionalities, please refer to the executor package.
func cgroupFileReadIntforTest(cgroupTaskDir string, r system.Resource) (*int64, error) {
	if supported, msg := r.IsSupported(cgroupTaskDir); !supported {
		return nil, system.ResourceUnsupportedErr(fmt.Sprintf("read cgroup %s failed, msg: %s", r.ResourceType(), msg))
	}
	if exist, msg := resourceexecutor.IsCgroupPathExist(cgroupTaskDir, r); !exist {
		return nil, resourceexecutor.ResourceCgroupDirErr(fmt.Sprintf("read cgroup %s failed, msg: %s", r.ResourceType(), msg))
	}

	filePath := r.Path(cgroupTaskDir)
	klog.V(5).Infof("read %s", filePath)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	dataStr := strings.Trim(string(data), "\n ")

	if dataStr == "" {
		return nil, fmt.Errorf("EmptyValueError")
	}
	if dataStr == "max" {
		// compatible with cgroup valued "max"
		data := int64(math.MaxInt64)
		klog.V(6).Infof("read %s and got str value, considered as MaxInt64", r.Path(cgroupTaskDir))
		return &data, nil
	}
	intData, err := strconv.ParseInt((dataStr), 10, 64)
	if err != nil {
		return nil, err
	}
	return &intData, nil
}

type memoryQOSGreyCtrlPlugin struct{}

func (p *memoryQOSGreyCtrlPlugin) Setup(kubeClient clientset.Interface) error {
	return nil
}

func (p *memoryQOSGreyCtrlPlugin) Run(stopCh <-chan struct{}) {
	return
}

func (p *memoryQOSGreyCtrlPlugin) InjectPodPolicy(pod *corev1.Pod, policyType framework.QOSPolicyType, greyCtlCfgIf *interface{}) (bool, error) {
	injected := false
	greyCtlCfg := &slov1alpha1.PodMemoryQOSConfig{}
	if pod.Namespace == "allow-ns" {
		greyCtlCfg.Policy = slov1alpha1.PodMemoryQOSPolicyAuto
		injected = true
	} else if pod.Namespace == "block-ns" {
		greyCtlCfg.Policy = slov1alpha1.PodMemoryQOSPolicyNone
		injected = true
	}
	if injected {
		*greyCtlCfgIf = greyCtlCfg
	}
	return injected, nil
}

func (p *memoryQOSGreyCtrlPlugin) name() string {
	return "memory-qos-test-plugin"
}

func TestCgroupResourcesReconcile_mergePodResourceQoSForMemoryQoS(t *testing.T) {
	type args struct {
		pod *corev1.Pod
		cfg *slov1alpha1.ResourceQOS
	}
	type wants struct {
		memoryQOSCfg *slov1alpha1.MemoryQOSCfg
	}
	tests := []struct {
		name  string
		args  args
		wants wants
	}{
		{
			name: "inject by allow ns list",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "allow-ns",
						Labels: map[string]string{
							apiext.LabelPodQoS: string(apiext.QoSLS),
						},
					},
				},
				cfg: &slov1alpha1.ResourceQOS{},
			},
			wants: wants{
				memoryQOSCfg: &slov1alpha1.MemoryQOSCfg{
					MemoryQOS: *sloconfig.DefaultMemoryQOS(apiext.QoSLS),
				},
			},
		},
	}
	p := &memoryQOSGreyCtrlPlugin{}
	framework.ClearQOSGreyCtrlPlugin()
	framework.RegisterQOSGreyCtrlPlugin(p.name(), p)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &cgroupResourcesReconcile{}
			m.mergePodResourceQoSForMemoryQoS(tt.args.pod, tt.args.cfg)
			assert.Equal(t, tt.wants.memoryQOSCfg, tt.args.cfg.MemoryQOS)
		})
	}
	framework.UnregisterQOSGreyCtrlPlugin(p.name())
}

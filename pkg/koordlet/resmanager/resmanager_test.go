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
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/component-base/featuregate"

	"gitlab.alibaba-inc.com/cos/slo-manager/pkg/agent/collector"
	"gitlab.alibaba-inc.com/cos/slo-manager/pkg/agent/metaservice/mockmetaservice"
	mock_metriccache "gitlab.alibaba-inc.com/cos/slo-manager/pkg/agent/metriccache/mockmetriccache"
	"gitlab.alibaba-inc.com/cos/slo-manager/pkg/features"
	"gitlab.alibaba-inc.com/cos/slo-manager/pkg/tools/cache"
	"gitlab.alibaba-inc.com/cos/slo-manager/pkg/util"
	uniapiext "gitlab.alibaba-inc.com/cos/unified-resource-api/apis/extension"
	nodesv1beta1 "gitlab.alibaba-inc.com/cos/unified-resource-api/apis/nodes/v1beta1"
	clientsetbeta1 "gitlab.alibaba-inc.com/cos/unified-resource-api/client/clientset/versioned"
)

func TestNewResManager(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		scheme := apiruntime.NewScheme()
		kubeClient := &clientset.Clientset{}
		crdClient := &clientsetbeta1.Clientset{}
		nodeName := "test-node"
		metaService := mock_metaservice.NewMockMetaService(ctrl)
		metricCache := mock_metriccache.NewMockMetricCache(ctrl)

		_ = NewResManager(NewDefaultConfig(), scheme, kubeClient, crdClient, nodeName, metaService, metricCache, int64(collector.NewDefaultConfig().CollectResUsedIntervalSeconds))
	})
}

func Test_mergeSLOSpecResourceUsedThresholdWithBE(t *testing.T) {
	testingDefaultSpec := util.DefaultNodeSLOSpecConfig().ResourceUsedThresholdWithBE.DeepCopy()
	testingNewSpec := &nodesv1beta1.ResourceThresholdStrategy{
		Enable:                      util.BoolPtr(true),
		CPUSuppressThresholdPercent: util.Int64Ptr(80),
		CPUEvictThresholdPercent:    util.Int64Ptr(50),
		MemoryEvictThresholdPercent: util.Int64Ptr(75),
	}
	testingNewSpec1 := &nodesv1beta1.ResourceThresholdStrategy{
		CPUSuppressThresholdPercent: util.Int64Ptr(80),
	}
	testingMergedSpec := &nodesv1beta1.ResourceThresholdStrategy{
		Enable:                      util.BoolPtr(true),
		CPUSuppressThresholdPercent: util.Int64Ptr(80),
		CPUEvictThresholdPercent:    util.Int64Ptr(70),
		MemoryEvictThresholdPercent: util.Int64Ptr(70),
		CPUEvictTimeWindowSeconds:   util.Int64Ptr(60),
		CPUSuppressPolicy:           nodesv1beta1.CPUSetPolicy,
	}
	type args struct {
		defaultSpec *nodesv1beta1.ResourceThresholdStrategy
		newSpec     *nodesv1beta1.ResourceThresholdStrategy
	}
	tests := []struct {
		name string
		args args
		want *nodesv1beta1.ResourceThresholdStrategy
	}{
		{
			name: "both empty",
			args: args{
				defaultSpec: &nodesv1beta1.ResourceThresholdStrategy{},
				newSpec:     &nodesv1beta1.ResourceThresholdStrategy{},
			},
			want: &nodesv1beta1.ResourceThresholdStrategy{},
		},
		{
			name: "totally use new",
			args: args{
				defaultSpec: &nodesv1beta1.ResourceThresholdStrategy{},
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "totally use new 1",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec,
			},
			want: &nodesv1beta1.ResourceThresholdStrategy{
				Enable:                      util.BoolPtr(true),
				CPUSuppressThresholdPercent: util.Int64Ptr(80),
				CPUEvictThresholdPercent:    util.Int64Ptr(50),
				MemoryEvictThresholdPercent: util.Int64Ptr(75),
				CPUEvictTimeWindowSeconds:   util.Int64Ptr(60),
				CPUSuppressPolicy:           nodesv1beta1.CPUSetPolicy,
			},
		},
		{
			name: "partially use new, merging with the default",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec1,
			},
			want: testingMergedSpec,
		},
		{
			name: "new overwrite a nil",
			args: args{
				defaultSpec: testingDefaultSpec,
			},
			want: testingDefaultSpec,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeSLOSpecResourceUsedThresholdWithBE(tt.args.defaultSpec, tt.args.newSpec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_mergeSLOSpecResourceQOSStrategy(t *testing.T) {
	testingDefaultSpec := util.DefaultNodeSLOSpecConfig().ResourceQOSStrategy.DeepCopy()

	testingNewSpec := testingDefaultSpec.DeepCopy()
	testingNewSpec.BEClass.CATRangeEndPercent = util.Int64Ptr(30)

	testingNewSpec1 := &nodesv1beta1.ResourceQOSStrategy{
		BEClass: &nodesv1beta1.ResourceQOS{
			ResctrlQOS: &nodesv1beta1.ResctrlQOSCfg{
				ResctrlQOS: nodesv1beta1.ResctrlQOS{
					CATRangeEndPercent: util.Int64Ptr(40),
				},
			},
		},
	}

	testingMergedSpec := testingDefaultSpec.DeepCopy()
	testingMergedSpec.BEClass.ResctrlQOS.CATRangeEndPercent = util.Int64Ptr(40)

	type args struct {
		defaultSpec *nodesv1beta1.ResourceQOSStrategy
		newSpec     *nodesv1beta1.ResourceQOSStrategy
	}
	tests := []struct {
		name string
		args args
		want *nodesv1beta1.ResourceQOSStrategy
	}{
		{
			name: "both empty",
			args: args{
				defaultSpec: &nodesv1beta1.ResourceQOSStrategy{},
				newSpec:     &nodesv1beta1.ResourceQOSStrategy{},
			},
			want: &nodesv1beta1.ResourceQOSStrategy{},
		},
		{
			name: "totally use new",
			args: args{
				defaultSpec: &nodesv1beta1.ResourceQOSStrategy{},
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "totally use new 1",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "partially use new, merging with the default",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec1,
			},
			want: testingMergedSpec,
		},
		{
			name: "new overwrite a nil",
			args: args{
				defaultSpec: testingDefaultSpec,
			},
			want: testingDefaultSpec,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeSLOSpecResourceQOSStrategy(tt.args.defaultSpec, tt.args.newSpec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_mergeSLOSpecSystemStrategy(t *testing.T) {
	testingDefaultSpec := util.DefaultNodeSLOSpecConfig().SystemStrategy.DeepCopy()

	testingNewSpec := testingDefaultSpec.DeepCopy()
	testingNewSpec.WatermarkScaleFactor = util.Int64Ptr(120)

	testingNewSpec1 := &nodesv1beta1.SystemStrategy{
		MinFreeKbytesFactor: util.Int64Ptr(130),
	}

	testingMergedSpec1 := testingDefaultSpec.DeepCopy()
	testingMergedSpec1.MinFreeKbytesFactor = util.Int64Ptr(130)

	type args struct {
		defaultSpec *nodesv1beta1.SystemStrategy
		newSpec     *nodesv1beta1.SystemStrategy
	}
	tests := []struct {
		name string
		args args
		want *nodesv1beta1.SystemStrategy
	}{
		{
			name: "both empty",
			args: args{
				defaultSpec: &nodesv1beta1.SystemStrategy{},
				newSpec:     &nodesv1beta1.SystemStrategy{},
			},
			want: &nodesv1beta1.SystemStrategy{},
		},
		{
			name: "totally use new",
			args: args{
				defaultSpec: &nodesv1beta1.SystemStrategy{},
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "totally use new 1",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "partially use new, merging with the default",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec1,
			},
			want: testingMergedSpec1,
		},
		{
			name: "new overwrite a nil",
			args: args{
				defaultSpec: testingDefaultSpec,
			},
			want: testingDefaultSpec,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeSLOSpecSystemStrategy(tt.args.defaultSpec, tt.args.newSpec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_mergeDefaultNodeSLO(t *testing.T) {
	testingCustomNodeSLOSpec := nodesv1beta1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
			CPUSuppressThresholdPercent: util.Int64Ptr(80),
		},
		ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
			BEClass: &nodesv1beta1.ResourceQOS{
				ResctrlQOS: &nodesv1beta1.ResctrlQOSCfg{
					ResctrlQOS: nodesv1beta1.ResctrlQOS{
						CATRangeEndPercent: util.Int64Ptr(50),
					},
				},
			},
		},
		CPUBurstStrategy: &nodesv1beta1.CPUBurstStrategy{
			CPUBurstConfig:            nodesv1beta1.CPUBurstConfig{},
			SharePoolThresholdPercent: nil,
		},
		SystemStrategy: &nodesv1beta1.SystemStrategy{
			WatermarkScaleFactor: util.Int64Ptr(200),
		},
	}
	testingMergedNodeSLOSpec := util.DefaultNodeSLOSpecConfig()
	mergedInterface, err := util.MergeCfg(&testingMergedNodeSLOSpec, &testingCustomNodeSLOSpec)
	assert.NoError(t, err)
	testingMergedNodeSLOSpec = *mergedInterface.(*nodesv1beta1.NodeSLOSpec)
	type args struct {
		nodeSLO *nodesv1beta1.NodeSLO
	}
	type field struct {
		nodeSLO *nodesv1beta1.NodeSLO
	}
	tests := []struct {
		name  string
		args  args
		field field
		want  *nodesv1beta1.NodeSLO
	}{
		{
			name: "skip the merge if the old one is nil",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{},
			},
			field: field{nodeSLO: nil},
			want:  nil,
		},
		{
			name: "skip the merge if the new one is nil",
			field: field{
				nodeSLO: &nodesv1beta1.NodeSLO{},
			},
			want: &nodesv1beta1.NodeSLO{},
		},
		{
			name: "use default and do not panic if the new is nil",
			field: field{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: util.DefaultNodeSLOSpecConfig(),
				},
			},
			want: &nodesv1beta1.NodeSLO{
				Spec: util.DefaultNodeSLOSpecConfig(),
			},
		},
		{
			name: "merge with the default",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: testingCustomNodeSLOSpec,
				},
			},
			field: field{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: nodesv1beta1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
							CPUSuppressThresholdPercent: util.Int64Ptr(100),
							MemoryEvictThresholdPercent: util.Int64Ptr(100),
						},
						ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
							Enable: util.BoolPtr(true),
							LSRClass: &nodesv1beta1.ResourceQOS{
								CPUQOS: &nodesv1beta1.CPUQOSCfg{
									CPUQOS: nodesv1beta1.CPUQOS{
										GroupIdentity: util.Int64Ptr(2),
									},
								},
								ResctrlQOS: &nodesv1beta1.ResctrlQOSCfg{
									ResctrlQOS: nodesv1beta1.ResctrlQOS{
										CATRangeStartPercent: util.Int64Ptr(0),
										CATRangeEndPercent:   util.Int64Ptr(100),
									},
								},
							},
							LSClass: &nodesv1beta1.ResourceQOS{
								CPUQOS: &nodesv1beta1.CPUQOSCfg{
									CPUQOS: nodesv1beta1.CPUQOS{
										GroupIdentity: util.Int64Ptr(2),
									},
								},
								ResctrlQOS: &nodesv1beta1.ResctrlQOSCfg{
									ResctrlQOS: nodesv1beta1.ResctrlQOS{
										CATRangeStartPercent: util.Int64Ptr(0),
										CATRangeEndPercent:   util.Int64Ptr(100),
									},
								},
							},
							BEClass: &nodesv1beta1.ResourceQOS{
								CPUQOS: &nodesv1beta1.CPUQOSCfg{
									CPUQOS: nodesv1beta1.CPUQOS{
										GroupIdentity: util.Int64Ptr(-1),
									},
								},
								ResctrlQOS: &nodesv1beta1.ResctrlQOSCfg{
									ResctrlQOS: nodesv1beta1.ResctrlQOS{
										CATRangeStartPercent: util.Int64Ptr(0),
										CATRangeEndPercent:   util.Int64Ptr(40),
									},
								},
							},
						},
					},
				},
			},
			want: &nodesv1beta1.NodeSLO{
				Spec: testingMergedNodeSLOSpec,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resmanager{nodeSLO: tt.field.nodeSLO}
			r.mergeDefaultNodeSLO(tt.args.nodeSLO)
			assert.Equal(t, tt.want, r.nodeSLO)
		})
	}
}

func Test_createNodeSLO(t *testing.T) {
	testingNewNodeSLO := &nodesv1beta1.NodeSLO{
		Spec: nodesv1beta1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
				CPUSuppressThresholdPercent: util.Int64Ptr(80),
			},
			ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
				LSClass: &nodesv1beta1.ResourceQOS{
					CPUQOS: &nodesv1beta1.CPUQOSCfg{
						CPUQOS: nodesv1beta1.CPUQOS{
							GroupIdentity: util.Int64Ptr(1),
						},
					},
				},
				BEClass: &nodesv1beta1.ResourceQOS{
					ResctrlQOS: &nodesv1beta1.ResctrlQOSCfg{
						ResctrlQOS: nodesv1beta1.ResctrlQOS{
							CATRangeStartPercent: util.Int64Ptr(0),
							CATRangeEndPercent:   util.Int64Ptr(20),
						},
					},
				},
			},
		},
	}

	testingCreatedNodeSLO := &nodesv1beta1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = util.Int64Ptr(80)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.LSClass.CPUQOS.GroupIdentity = util.Int64Ptr(1)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeStartPercent = util.Int64Ptr(0)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeEndPercent = util.Int64Ptr(20)

	r := resmanager{nodeSLO: nil}

	r.createNodeSLO(testingNewNodeSLO)
	assert.Equal(t, testingCreatedNodeSLO, r.nodeSLO)
}

func Test_updateNodeSLOSpec(t *testing.T) {
	testingNewNodeSLO := &nodesv1beta1.NodeSLO{
		Spec: nodesv1beta1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
				CPUSuppressThresholdPercent: util.Int64Ptr(80),
			},
			ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
				LSClass: &nodesv1beta1.ResourceQOS{
					CPUQOS: &nodesv1beta1.CPUQOSCfg{
						CPUQOS: nodesv1beta1.CPUQOS{
							GroupIdentity: util.Int64Ptr(1),
						},
					},
				},
				BEClass: &nodesv1beta1.ResourceQOS{
					ResctrlQOS: &nodesv1beta1.ResctrlQOSCfg{
						ResctrlQOS: nodesv1beta1.ResctrlQOS{
							CATRangeStartPercent: util.Int64Ptr(0),
							CATRangeEndPercent:   util.Int64Ptr(20),
						},
					},
				},
			},
		},
	}
	testingUpdatedNodeSLO := &nodesv1beta1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = util.Int64Ptr(80)
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.LSClass.CPUQOS.GroupIdentity = util.Int64Ptr(1)
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeStartPercent = util.Int64Ptr(0)
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeEndPercent = util.Int64Ptr(20)

	r := resmanager{
		nodeSLO: &nodesv1beta1.NodeSLO{
			Spec: nodesv1beta1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: util.Int64Ptr(90),
					MemoryEvictThresholdPercent: util.Int64Ptr(90),
				},
			},
		},
	}

	r.updateNodeSLOSpec(testingNewNodeSLO)
	assert.Equal(t, testingUpdatedNodeSLO, r.nodeSLO)
}

func Test_isFeatureDisabled(t *testing.T) {
	type args struct {
		nodeSLO *nodesv1beta1.NodeSLO
		feature featuregate.Feature
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name:    "throw an error for nil nodeSLO",
			wantErr: true,
		},
		{
			name: "throw an error for config field is nil",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{},
				feature: features.BECPUSuppress,
			},
			wantErr: true,
		},
		{
			name: "throw an error for config field is nil 1",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: nodesv1beta1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
							Enable: util.BoolPtr(true),
						},
					},
				},
				feature: features.RdtResctrl,
			},
			wantErr: true,
		},
		{
			name: "throw an error for config field is nil",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: nodesv1beta1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
							Enable: util.BoolPtr(false),
						},
						ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
							Enable: util.BoolPtr(false),
						},
					},
				},
				feature: featuregate.Feature("unknown_feature"),
			},
			wantErr: true,
		},
		{
			name: "use default config for nil switch",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: nodesv1beta1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{},
						ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
							Enable: util.BoolPtr(false),
						},
					},
				},
				feature: features.BECPUSuppress,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "use default config for nil switch 1",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: nodesv1beta1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{},
						ResourceQOSStrategy:         &nodesv1beta1.ResourceQOSStrategy{},
					},
				},
				feature: features.RdtResctrl,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "parse config successfully",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: nodesv1beta1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
							Enable: util.BoolPtr(false),
						},
						ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
							Enable: util.BoolPtr(false),
						},
					},
				},
				feature: features.BECPUSuppress,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "parse config successfully 1",
			args: args{
				nodeSLO: &nodesv1beta1.NodeSLO{
					Spec: nodesv1beta1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &nodesv1beta1.ResourceThresholdStrategy{
							Enable: util.BoolPtr(true),
						},
						ResourceQOSStrategy: &nodesv1beta1.ResourceQOSStrategy{
							Enable: util.BoolPtr(false),
						},
					},
				},
				feature: features.CgroupReconcile,
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := isFeatureDisabled(tt.args.nodeSLO, tt.args.feature)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func Test_EvictPodsIfNotEvicted(t *testing.T) {
	//test data
	pod := createTestPod(uniapiext.QoSBE, "test_be_pod")
	node := getNode("80", "120G")
	//env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	fakeRecorder := &FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()
	resmanager := &resmanager{eventRecorder: fakeRecorder, kubeClient: client, podsEvicted: cache.NewCacheDefault()}
	stop := make(chan struct{})
	resmanager.podsEvicted.Run(stop)
	defer func() { stop <- struct{}{} }()

	//create pod
	client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})

	//evict success
	resmanager.evictPodsIfNotEvicted([]*corev1.Pod{pod}, node, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(podsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)
	assert.Equal(t, evictPodSuccess, fakeRecorder.eventReason, "expect evict sucess event! but got %s", fakeRecorder.eventReason)

	_, found := resmanager.podsEvicted.Get(string(pod.UID))
	assert.True(t, found, "check PodEvicted cached")

	//evict duplication
	fakeRecorder.eventReason = ""
	resmanager.evictPodsIfNotEvicted([]*corev1.Pod{pod}, node, "evict pod duplication", "")
	assert.Equal(t, "", fakeRecorder.eventReason, "check evict duplication, no event send!")

}

func Test_evictPod(t *testing.T) {
	//test data
	pod := createTestPod(uniapiext.QoSBE, "test_be_pod")
	node := getNode("80", "120G")
	//env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockMetaService := mock_metaservice.NewMockMetaService(ctl)
	mockMetaService.EXPECT().GetAllPods().Return(getPodMetas([]*corev1.Pod{pod})).AnyTimes()
	mockMetaService.EXPECT().GetNode().Return(node).AnyTimes()

	fakeRecorder := &FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()
	resmanager := &resmanager{metaService: mockMetaService, eventRecorder: fakeRecorder, kubeClient: client}

	//create pod
	client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	//check pod
	existPod, err := client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	assert.NotNil(t, existPod, "pod exist in k8s!", err)

	//evict success
	resmanager.evictPod(pod, node, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(podsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)

	assert.Equal(t, evictPodSuccess, fakeRecorder.eventReason, "expect evict sucess event! but got %s", fakeRecorder.eventReason)

}

func createTestPod(qosClass uniapiext.QoSClass, name string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(name),
			Annotations: map[string]string{
				uniapiext.AnnotationPodQOSClass: string(qosClass),
			},
		},
	}
}

func testingOldQOSConfig(qosClass uniapiext.QoSClass) *nodesv1beta1.QOSConfig {
	qosConfig := &nodesv1beta1.QOSConfig{}
	switch qosClass {
	case uniapiext.QoSLSR, uniapiext.QoSLS:
		qosConfig = &nodesv1beta1.QOSConfig{
			GroupIdentity:           util.Int64Ptr(2),
			MemoryQOSEnable:         util.BoolPtr(false),
			MemoryMinLimitPercent:   util.Int64Ptr(0),
			MemoryLowLimitPercent:   util.Int64Ptr(0),
			MemoryThrottlingPercent: util.Int64Ptr(0),
			MemoryWmarkRatio:        util.Int64Ptr(95),
			MemoryWmarkScalePermill: util.Int64Ptr(20),
			MemoryWmarkMinAdj:       util.Int64Ptr(-25),
			MemoryPriorityEnable:    util.Int64Ptr(0),
			MemoryPriority:          util.Int64Ptr(0),
			MemoryOomKillGroup:      util.Int64Ptr(0),
			CATRangeStartPercent:    util.Int64Ptr(0),
			CATRangeEndPercent:      util.Int64Ptr(100),
		}
	case uniapiext.QoSBE:
		qosConfig = &nodesv1beta1.QOSConfig{
			GroupIdentity:           util.Int64Ptr(2),
			MemoryQOSEnable:         util.BoolPtr(false),
			MemoryMinLimitPercent:   util.Int64Ptr(0),
			MemoryLowLimitPercent:   util.Int64Ptr(0),
			MemoryThrottlingPercent: util.Int64Ptr(0),
			MemoryWmarkRatio:        util.Int64Ptr(95),
			MemoryWmarkScalePermill: util.Int64Ptr(20),
			MemoryWmarkMinAdj:       util.Int64Ptr(50),
			MemoryPriorityEnable:    util.Int64Ptr(0),
			MemoryPriority:          util.Int64Ptr(0),
			MemoryOomKillGroup:      util.Int64Ptr(0),
			CATRangeStartPercent:    util.Int64Ptr(0),
			CATRangeEndPercent:      util.Int64Ptr(100),
		}
	}
	return qosConfig
}

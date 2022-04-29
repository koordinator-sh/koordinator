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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/component-base/featuregate"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	clientsetalpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/pkg/features"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/tools/cache"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var podsResource = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

func TestNewResManager(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		scheme := apiruntime.NewScheme()
		kubeClient := &kubernetes.Clientset{}
		crdClient := &clientsetalpha1.Clientset{}
		nodeName := "test-node"
		metaService := mock_statesinformer.NewMockStatesInformer(ctrl)
		metricCache := mock_metriccache.NewMockMetricCache(ctrl)

		_ = NewResManager(NewDefaultConfig(), scheme, kubeClient, crdClient, nodeName, metaService, metricCache, int64(metricsadvisor.NewDefaultConfig().CollectResUsedIntervalSeconds))
	})
}

func Test_mergeNodeSLOSpec(t *testing.T) {
	testingCustomNodeSLOSpec := slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
			CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
		},
		ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
			LSR: util.NoneResourceQoS(apiext.QoSLSR),
			LS:  util.NoneResourceQoS(apiext.QoSLS),
			BE: &slov1alpha1.ResourceQoS{

				MemoryQoS: &slov1alpha1.MemoryQoSCfg{
					Enable: pointer.BoolPtr(true),
				},
				ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
					Enable: pointer.BoolPtr(true),
					ResctrlQoS: slov1alpha1.ResctrlQoS{
						CATRangeEndPercent: pointer.Int64Ptr(50),
					},
				},
			},
		},
		CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig:            slov1alpha1.CPUBurstConfig{},
			SharePoolThresholdPercent: nil,
		},
	}
	testingMergedNodeSLOSpec := util.DefaultNodeSLOSpecConfig()
	mergedInterface, err := util.MergeCfg(&testingMergedNodeSLOSpec, &testingCustomNodeSLOSpec)
	assert.NoError(t, err)
	testingMergedNodeSLOSpec = *mergedInterface.(*slov1alpha1.NodeSLOSpec)
	type args struct {
		nodeSLO *slov1alpha1.NodeSLO
	}
	type field struct {
		nodeSLO *slov1alpha1.NodeSLO
	}
	tests := []struct {
		name  string
		args  args
		field field
		want  *slov1alpha1.NodeSLO
	}{
		{
			name: "skip the merge if the old one is nil",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			field: field{nodeSLO: nil},
			want:  nil,
		},
		{
			name: "skip the merge if the new one is nil",
			field: field{
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			want: &slov1alpha1.NodeSLO{},
		},
		{
			name: "use default and do not panic if the new is nil",
			field: field{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: util.DefaultNodeSLOSpecConfig(),
				},
			},
			want: &slov1alpha1.NodeSLO{
				Spec: util.DefaultNodeSLOSpecConfig(),
			},
		},
		{
			name: "merge with the default",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: testingCustomNodeSLOSpec,
				},
			},
			field: field{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							CPUSuppressThresholdPercent: pointer.Int64Ptr(100),
							MemoryEvictThresholdPercent: pointer.Int64Ptr(100),
						},
						ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
							LSR: &slov1alpha1.ResourceQoS{
								ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
									ResctrlQoS: slov1alpha1.ResctrlQoS{
										CATRangeStartPercent: pointer.Int64Ptr(0),
										CATRangeEndPercent:   pointer.Int64Ptr(100),
									},
								},
							},
							LS: &slov1alpha1.ResourceQoS{
								ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
									ResctrlQoS: slov1alpha1.ResctrlQoS{
										CATRangeStartPercent: pointer.Int64Ptr(0),
										CATRangeEndPercent:   pointer.Int64Ptr(100),
									},
								},
							},
							BE: &slov1alpha1.ResourceQoS{
								ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
									ResctrlQoS: slov1alpha1.ResctrlQoS{
										CATRangeStartPercent: pointer.Int64Ptr(0),
										CATRangeEndPercent:   pointer.Int64Ptr(40),
									},
								},
							},
						},
					},
				},
			},
			want: &slov1alpha1.NodeSLO{
				Spec: testingMergedNodeSLOSpec,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resmanager{nodeSLO: tt.field.nodeSLO}
			r.mergeNodeSLOSpec(tt.args.nodeSLO)
			assert.Equal(t, tt.want, r.nodeSLO)
		})
	}
}

func Test_createNodeSLO(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingNewNodeSLO.Spec.ResourceUsedThresholdWithBE = &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.BoolPtr(true),
		CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
	}

	testingNewNodeSLO.Spec.ResourceQoSStrategy.BE = &slov1alpha1.ResourceQoS{
		ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
			Enable: pointer.BoolPtr(true),
			ResctrlQoS: slov1alpha1.ResctrlQoS{
				CATRangeStartPercent: pointer.Int64Ptr(0),
				CATRangeEndPercent:   pointer.Int64Ptr(20),
			},
		},
	}

	testingCreatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.Enable = pointer.BoolPtr(true)
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = pointer.Int64Ptr(80)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.LSR = util.NoneResourceQoS(apiext.QoSLSR)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.LS = util.NoneResourceQoS(apiext.QoSLS)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE = util.NoneResourceQoS(apiext.QoSBE)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.Enable = pointer.BoolPtr(true)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeStartPercent = pointer.Int64Ptr(0)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeEndPercent = pointer.Int64Ptr(20)

	r := resmanager{nodeSLO: nil}

	r.createNodeSLO(testingNewNodeSLO)
	assert.Equal(t, testingCreatedNodeSLO, r.nodeSLO)
}

func Test_updateNodeSLOSpec(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.BoolPtr(true),
				CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
			},
			ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
				BE: &slov1alpha1.ResourceQoS{
					ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
						Enable: pointer.BoolPtr(true),
						ResctrlQoS: slov1alpha1.ResctrlQoS{
							CATRangeStartPercent: pointer.Int64Ptr(0),
							CATRangeEndPercent:   pointer.Int64Ptr(20),
						},
					},
				},
			},
		},
	}
	testingUpdatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.Enable = pointer.BoolPtr(true)
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = pointer.Int64Ptr(80)
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LSR.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LSR.ResctrlQoS.ResctrlQoS = *util.NoneResctrlQoS()

	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LS.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LS.ResctrlQoS.ResctrlQoS = *util.NoneResctrlQoS()

	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.Enable = pointer.BoolPtr(true)
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeStartPercent = pointer.Int64Ptr(0)
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeEndPercent = pointer.Int64Ptr(20)

	r := resmanager{
		nodeSLO: &slov1alpha1.NodeSLO{
			Spec: slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64Ptr(90),
					MemoryEvictThresholdPercent: pointer.Int64Ptr(90),
				},
			},
		},
	}

	r.updateNodeSLOSpec(testingNewNodeSLO)
	assert.Equal(t, testingUpdatedNodeSLO, r.nodeSLO)
}

func Test_isFeatureDisabled(t *testing.T) {
	type args struct {
		nodeSLO *slov1alpha1.NodeSLO
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
			want:    true,
			wantErr: true,
		},
		{
			name: "throw an error for config field is nil",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{},
				feature: features.BECPUSuppress,
			},
			want:    true,
			wantErr: true,
		},
		{
			name: "throw an error for unknown feature",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							Enable: pointer.BoolPtr(false),
						},
					},
				},
				feature: featuregate.Feature("unknown_feature"),
			},
			want:    true,
			wantErr: true,
		},
		{
			name: "use default config for nil switch",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{},
					},
				},
				feature: features.BECPUSuppress,
			},
			want:    true,
			wantErr: true,
		},
		{
			name: "parse config successfully",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							Enable: pointer.BoolPtr(false),
						},
					},
				},
				feature: features.BECPUSuppress,
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
	// test data
	pod := createTestPod(apiext.QoSBE, "test_be_pod")
	node := getNode("80", "120G")
	// env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	fakeRecorder := &FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()
	resmanager := &resmanager{eventRecorder: fakeRecorder, kubeClient: client, podsEvicted: cache.NewCacheDefault()}
	stop := make(chan struct{})
	resmanager.podsEvicted.Run(stop)
	defer func() { stop <- struct{}{} }()

	// create pod
	client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})

	// evict success
	resmanager.evictPodsIfNotEvicted([]*corev1.Pod{pod}, node, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(podsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)
	assert.Equal(t, evictPodSuccess, fakeRecorder.eventReason, "expect evict success event! but got %s", fakeRecorder.eventReason)

	_, found := resmanager.podsEvicted.Get(string(pod.UID))
	assert.True(t, found, "check PodEvicted cached")

	// evict duplication
	fakeRecorder.eventReason = ""
	resmanager.evictPodsIfNotEvicted([]*corev1.Pod{pod}, node, "evict pod duplication", "")
	assert.Equal(t, "", fakeRecorder.eventReason, "check evict duplication, no event send!")

}

func Test_evictPod(t *testing.T) {
	// test data
	pod := createTestPod(apiext.QoSBE, "test_be_pod")
	node := getNode("80", "120G")
	// env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
	mockStatesInformer.EXPECT().GetAllPods().Return(getPodMetas([]*corev1.Pod{pod})).AnyTimes()
	mockStatesInformer.EXPECT().GetNode().Return(node).AnyTimes()

	fakeRecorder := &FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()
	resmanager := &resmanager{statesInformer: mockStatesInformer, eventRecorder: fakeRecorder, kubeClient: client}

	// create pod
	client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	// check pod
	existPod, err := client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	assert.NotNil(t, existPod, "pod exist in k8s!", err)

	// evict success
	resmanager.evictPod(pod, node, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(podsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)

	assert.Equal(t, evictPodSuccess, fakeRecorder.eventReason, "expect evict success event! but got %s", fakeRecorder.eventReason)

}

func createTestPod(qosClass apiext.QoSClass, name string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(name),
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qosClass),
			},
		},
	}
}

func getNode(cpu, memory string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-node",
			Namespace: "default",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
		},
	}
}

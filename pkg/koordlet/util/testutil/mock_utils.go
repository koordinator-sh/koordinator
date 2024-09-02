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

package testutil

import (
	"fmt"

	"github.com/golang/mock/gomock"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/pointer"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

type FakeRecorder struct {
	EventReason string
}

func (f *FakeRecorder) Event(object runtime.Object, eventType, reason, message string) {
	f.EventReason = reason
	fmt.Printf("send event:eventType:%s,reason:%s,message:%s", eventType, reason, message)
}

func (f *FakeRecorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	f.EventReason = reason
	fmt.Printf("send event:eventType:%s,reason:%s,message:%s", eventType, reason, messageFmt)
}

func (f *FakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventType, reason, messageFmt string, args ...interface{}) {
	f.Eventf(object, eventType, reason, messageFmt, args...)
}

func MockTestNode(cpu, memory string) *corev1.Node {
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

func MockTestPod(qosClass apiext.QoSClass, name string) *corev1.Pod {
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

var PodsResource = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

var EvictionResource = schema.GroupVersionResource{Group: "policy", Version: "v1beta1", Resource: "evictions"}

var EvictionKind = schema.GroupVersionKind{Group: "policy", Version: "v1beta1", Kind: "Eviction"}

func BuildMockQueryResult(ctrl *gomock.Controller, querier *mock_metriccache.MockQuerier, factory *mock_metriccache.MockAggregateResultFactory,
	queryMeta metriccache.MetricMeta, value float64) {
	result := mock_metriccache.NewMockAggregateResult(ctrl)
	result.EXPECT().Value(gomock.Any()).Return(value, nil).AnyTimes()
	result.EXPECT().Count().Return(1).AnyTimes()
	factory.EXPECT().New(queryMeta).Return(result).AnyTimes()
	querier.EXPECT().QueryAndClose(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().Query(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().Close().AnyTimes()
}

func BuildMockQueryResultAndCount(ctrl *gomock.Controller, querier *mock_metriccache.MockQuerier, factory *mock_metriccache.MockAggregateResultFactory,
	queryMeta metriccache.MetricMeta) *mock_metriccache.MockAggregateResult {
	result := mock_metriccache.NewMockAggregateResult(ctrl)
	factory.EXPECT().New(queryMeta).Return(result).AnyTimes()
	querier.EXPECT().Query(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()

	return result
}

func GetPodMetas(pods []*corev1.Pod) []*statesinformer.PodMeta {
	podMetas := make([]*statesinformer.PodMeta, len(pods))

	for index, pod := range pods {
		cgroupDir := util.GetPodCgroupParentDir(pod)
		podMeta := &statesinformer.PodMeta{CgroupDir: cgroupDir, Pod: pod.DeepCopy()}
		podMetas[index] = podMeta
	}

	return podMetas
}

func GetNodeSLOByThreshold(thresholdConfig *slov1alpha1.ResourceThresholdStrategy) *slov1alpha1.NodeSLO {
	return &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: thresholdConfig,
		},
	}
}

func MockTestPodWithQOS(kubeQosClass corev1.PodQOSClass, qosClass apiext.QoSClass) *statesinformer.PodMeta {
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
		CgroupDir: util.GetPodCgroupParentDir(pod),
		Pod:       pod,
	}
}

func DefaultQOSStrategy() *slov1alpha1.ResourceQOSStrategy {
	return &slov1alpha1.ResourceQOSStrategy{
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
					Priority:          pointer.Int64(0),
					PriorityEnable:    pointer.Int64(0),
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
					OomKillGroup:      pointer.Int64(0),
					Priority:          pointer.Int64(0),
					PriorityEnable:    pointer.Int64(0),
				},
			},
		},
	}
}

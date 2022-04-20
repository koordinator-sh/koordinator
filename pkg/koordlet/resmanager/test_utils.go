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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type FakeRecorder struct {
	eventReason string
}

func (f *FakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	f.eventReason = reason
	fmt.Printf("send event:eventType:%s,reason:%s,message:%s", eventtype, reason, message)
}

func (f *FakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	f.eventReason = reason
	fmt.Printf("send event:eventType:%s,reason:%s,message:%s", eventtype, reason, messageFmt)
}

func (f *FakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	f.Eventf(object, eventtype, reason, messageFmt, args...)
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

func getPodMetas(pods []*corev1.Pod) []*statesinformer.PodMeta {
	podMetas := make([]*statesinformer.PodMeta, len(pods))

	for index, pod := range pods {
		cgroupDir := util.GetPodKubeRelativePath(pod)
		podMeta := &statesinformer.PodMeta{CgroupDir: cgroupDir, Pod: pod.DeepCopy()}
		podMetas[index] = podMeta
	}

	return podMetas
}

func getNodeSLOByThreshold(thresholdConfig *v1alpha1.ResourceThresholdStrategy) *v1alpha1.NodeSLO {
	return &v1alpha1.NodeSLO{
		Spec: v1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: thresholdConfig,
		},
	}
}

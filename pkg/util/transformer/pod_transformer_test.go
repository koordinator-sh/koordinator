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

package transformer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestTransformPod(t *testing.T) {
	tests := []struct {
		name      string
		prepareFn func() func()
		pod       *corev1.Pod
		wantPod   *corev1.Pod
	}{
		{
			name: "normal pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("2Gi"),
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "/",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("2Gi"),
					},
				},
			},
		},
		{
			name: "pod with deprecated resources",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"kubernetes.io/gpu-core":"60","kubernetes.io/gpu-memory":"8Gi","kubernetes.io/gpu-memory-ratio":"50"}}]}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.KoordBatchCPU:            resource.MustParse("1000"),
									apiext.KoordBatchMemory:         resource.MustParse("1Gi"),
									apiext.DeprecatedGPUCore:        resource.MustParse("100"),
									apiext.DeprecatedGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.KoordBatchCPU:            resource.MustParse("1000"),
									apiext.KoordBatchMemory:         resource.MustParse("1Gi"),
									apiext.DeprecatedGPUCore:        resource.MustParse("100"),
									apiext.DeprecatedGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.KoordBatchCPU:    resource.MustParse("500"),
						apiext.KoordBatchMemory: resource.MustParse("2Gi"),
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "/",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("2Gi"),
					},
				},
			},
		},
		{
			name: "pod transform priority and preemption policy",
			prepareFn: func() func() {
				cleanFn := utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.PriorityTransformer, true)
				cleanFn1 := utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.PreemptionPolicyTransformer, true)
				return func() {
					cleanFn1()
					cleanFn()
				}
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS:              string(apiext.QoSLSR),
						apiext.LabelPodPreemptionPolicy: string(corev1.PreemptNever),
					},
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("2Gi"),
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS:              string(apiext.QoSLSR),
						apiext.LabelPodPreemptionPolicy: string(corev1.PreemptNever),
						apiext.LabelQuestionedObjectKey: "/",
					},
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("2Gi"),
					},
					Priority:         ptr.To[int32](apiext.PriorityProdValueDefault),
					PreemptionPolicy: apiext.GetPreemptionPolicyPtr(corev1.PreemptNever),
				},
			},
		},
		{
			name: "pod disable priority transform and use default preemption policy",
			prepareFn: func() func() {
				cleanFn := utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.PriorityTransformer, false)
				cleanFn1 := utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.PreemptionPolicyTransformer, true)
				return func() {
					cleanFn1()
					cleanFn()
				}
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSLSR),
					},
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("2Gi"),
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS:              string(apiext.QoSLSR),
						apiext.LabelQuestionedObjectKey: "/",
					},
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:               resource.MustParse("1000"),
									apiext.BatchMemory:            resource.MustParse("1Gi"),
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("2Gi"),
					},
					PreemptionPolicy: apiext.DefaultPreemptionPolicy,
				},
			},
		},
		{
			name: "enable replace-resources transformer to erase and replace resources",
			prepareFn: func() func() {
				return utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
					features.ReplaceResourcesTransformer, true)
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodReplaceResources: "cpu:,memory:example.io/memory",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("3"),
									corev1.ResourceMemory: resource.MustParse("6Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init-container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1.5"),
									corev1.ResourceMemory: resource.MustParse("3Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodReplaceResources: "cpu:,memory:example.io/memory",
					},
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "/",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"example.io/memory": resource.MustParse("4Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									"example.io/memory": resource.MustParse("6Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init-container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"example.io/memory": resource.MustParse("2Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									"example.io/memory": resource.MustParse("3Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						"example.io/memory": resource.MustParse("1Gi"),
					},
				},
			},
		},
		{
			name: "enable replace-resources transformer for scheduler",
			prepareFn: func() func() {
				return utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate,
					features.ReplaceResourcesTransformer, true)
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodReplaceResources: "cpu:,memory:example.io/memory",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("3"),
									corev1.ResourceMemory: resource.MustParse("6Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init-container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1.5"),
									corev1.ResourceMemory: resource.MustParse("3Gi"),
									"example.io/gpu":      resource.MustParse("1"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodReplaceResources: "cpu:,memory:example.io/memory",
					},
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "/",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"example.io/memory": resource.MustParse("4Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									"example.io/memory": resource.MustParse("6Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init-container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"example.io/memory": resource.MustParse("2Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									"example.io/memory": resource.MustParse("3Gi"),
									"example.io/gpu":    resource.MustParse("1"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						"example.io/memory": resource.MustParse("1Gi"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFn != nil {
				defer tt.prepareFn()()
			}
			obj, err := TransformPodFactory()(tt.pod)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantPod, obj)
		})
	}
}

func TestTransformScheduleExplanationObjectKey(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		wantPod *corev1.Pod
	}{
		{
			name: "gang-master",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-master-0",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: "gang-master",
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-master-0",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel:          "gang-master",
						apiext.LabelQuestionedObjectKey: "gang",
					},
				},
			},
		},
		{
			name: "gang-worker",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-worker-0",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: "gang-worker",
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-worker-0",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel:          "gang-worker",
						apiext.LabelQuestionedObjectKey: "gang",
					},
				},
			},
		},
		{
			name: "annotation-gang",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-worker-0",
					Annotations: map[string]string{
						apiext.AnnotationGangName: "annotation-gang",
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-worker-0",
					Annotations: map[string]string{
						apiext.AnnotationGangName: "annotation-gang",
					},
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "annotation-gang",
					},
				},
			},
		},
		{
			name: "annotation-gang-with-gangGroup",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-worker-0",
					Annotations: map[string]string{
						apiext.AnnotationGangName:   "gangA",
						apiext.AnnotationGangGroups: "[default/gangA, default/gangB]",
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "prefix-worker-0",
					Annotations: map[string]string{
						apiext.AnnotationGangName:   "gangA",
						apiext.AnnotationGangGroups: "[default/gangA, default/gangB]",
					},
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "[default/gangA, default/gangB]",
					},
				},
			},
		},
		{
			name: "bare pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prefix-worker-0",
					Namespace: "default",
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prefix-worker-0",
					Namespace: "default",
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "default/prefix-worker-0",
					},
				},
			},
		},
		{
			name: "already exists key",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prefix-worker-0",
					Namespace: "default",
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "default/prefix-worker-c",
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prefix-worker-0",
					Namespace: "default",
					Labels: map[string]string{
						apiext.LabelQuestionedObjectKey: "default/prefix-worker-c",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformScheduleExplanationObjectKey()(tt.pod)
			assert.Equal(t, tt.wantPod, tt.pod)
		})
	}
}

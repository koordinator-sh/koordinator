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

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func TestTransformPod(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		wantPod *corev1.Pod
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := TransformPod(tt.pod)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantPod, obj)
		})
	}
}

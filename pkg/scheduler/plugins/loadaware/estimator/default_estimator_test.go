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

package estimator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta3"
)

func TestDefaultEstimatorEstimatePod(t *testing.T) {
	tests := []struct {
		name          string
		pod           *corev1.Pod
		scalarFactors map[corev1.ResourceName]int64
		want          map[corev1.ResourceName]int64
	}{
		{
			name: "estimate empty pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:      "main",
							Resources: corev1.ResourceRequirements{},
						},
					},
				},
			},
			want: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    DefaultMilliCPURequest,
				corev1.ResourceMemory: DefaultMemoryRequest,
			},
		},
		{
			name: "estimate guaranteed pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			want: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    3400,
				corev1.ResourceMemory: 6012954214, // 5.6Gi
			},
		},
		{
			name: "estimate burstable pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			want: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    6800,
				corev1.ResourceMemory: 6012954214, // 5.6Gi
			},
		},
		{
			name: "estimate guaranteed pod and zoomed cpu factors",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			scalarFactors: map[corev1.ResourceName]int64{
				corev1.ResourceCPU: 110,
			},
			want: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    4000,
				corev1.ResourceMemory: 6012954214, // 5.6Gi
			},
		},
		{
			name: "estimate guaranteed pod and zoomed memory factors",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			scalarFactors: map[corev1.ResourceName]int64{
				corev1.ResourceMemory: 110,
			},
			want: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    3400,
				corev1.ResourceMemory: 8589934592, // 5.6Gi
			},
		},
		{
			name: "estimate Batch pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									extension.BatchCPU:    resource.MustParse("4000"),
									extension.BatchMemory: resource.MustParse("8Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									extension.BatchCPU:    resource.MustParse("4000"),
									extension.BatchMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			want: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    3400,
				corev1.ResourceMemory: 6012954214, // 5.6Gi
			},
		},
		{
			name: "estimate pod only has request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			scalarFactors: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    80,
				corev1.ResourceMemory: 80,
			},
			want: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    3200,
				corev1.ResourceMemory: 6871947674,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v1beta3args v1beta3.LoadAwareSchedulingArgs
			v1beta3args.EstimatedScalingFactors = tt.scalarFactors
			v1beta3.SetDefaults_LoadAwareSchedulingArgs(&v1beta3args)
			var loadAwareSchedulingArgs config.LoadAwareSchedulingArgs
			err := v1beta3.Convert_v1beta3_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1beta3args, &loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)
			estimator, err := NewDefaultEstimator(&loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)
			assert.NotNil(t, estimator)
			assert.Equal(t, defaultEstimatorName, estimator.Name())

			got, err := estimator.EstimatePod(tt.pod)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDefaultEstimatorEstimateNode(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want corev1.ResourceList
	}{
		{
			name: "estimate empty node",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("32"),
					},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("32"),
			},
		},
		{
			name: "estimate node with original allocatable",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNodeRawAllocatable: `{"cpu":28,"memory":"32Gi"}`,
					},
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("32"),
						corev1.ResourceMemory: resource.MustParse("42Gi"),
					},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("28"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
		},
		{
			name: "estimate node with original allocatable and sames",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNodeRawAllocatable: `{"cpu":32,"memory":"42Gi"}`,
					},
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("32"),
						corev1.ResourceMemory: resource.MustParse("42Gi"),
					},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("42Gi"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v1beta3args v1beta3.LoadAwareSchedulingArgs
			v1beta3.SetDefaults_LoadAwareSchedulingArgs(&v1beta3args)
			var loadAwareSchedulingArgs config.LoadAwareSchedulingArgs
			err := v1beta3.Convert_v1beta3_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1beta3args, &loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)
			estimator, err := NewDefaultEstimator(&loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)
			assert.NotNil(t, estimator)

			got, err := estimator.EstimateNode(tt.node)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

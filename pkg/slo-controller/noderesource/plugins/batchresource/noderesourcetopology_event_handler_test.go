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

package batchresource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
)

func Test_isNRTResourcesCreated(t *testing.T) {
	tests := []struct {
		name string
		arg  *topologyv1alpha1.NodeResourceTopology
		want bool
	}{
		{
			name: "ignore empty zoneList",
			arg:  &topologyv1alpha1.NodeResourceTopology{},
			want: false,
		},
		{
			name: "ignore zoneList which have no target resource",
			arg: &topologyv1alpha1.NodeResourceTopology{
				Zones: topologyv1alpha1.ZoneList{
					{
						Name: "node-0",
						Resources: topologyv1alpha1.ResourceInfoList{
							{
								Name:        "other-resource",
								Allocatable: resource.MustParse("1"),
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "true when zoneList have target resources",
			arg: &topologyv1alpha1.NodeResourceTopology{
				Zones: topologyv1alpha1.ZoneList{
					{
						Name: "node-0",
						Resources: topologyv1alpha1.ResourceInfoList{
							{
								Name:        string(corev1.ResourceCPU),
								Allocatable: resource.MustParse("10"),
							},
							{
								Name:        "other-resource",
								Allocatable: resource.MustParse("1"),
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "true when zoneList have target resources 1",
			arg: &topologyv1alpha1.NodeResourceTopology{
				Zones: topologyv1alpha1.ZoneList{
					{
						Name: "node-0",
						Resources: topologyv1alpha1.ResourceInfoList{
							{
								Name:        "other-resource",
								Allocatable: resource.MustParse("1"),
							},
						},
					},
					{
						Name: "node-1",
						Resources: topologyv1alpha1.ResourceInfoList{
							{
								Name:        "other-resource",
								Allocatable: resource.MustParse("1"),
							},
							{
								Name:        string(corev1.ResourceCPU),
								Allocatable: resource.MustParse("10"),
							},
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNRTResourcesCreated(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_isNRTResourcesChanged(t *testing.T) {
	type args struct {
		nrtOld *topologyv1alpha1.NodeResourceTopology
		nrtNew *topologyv1alpha1.NodeResourceTopology
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "false when objects are equal",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "false when target resources unchanged",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "true when target resources added",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "true when target resources removed",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "true when target resources changed",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("15Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "true when zone with target resources added",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "true when zone with target resources removed",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "true when resources changed",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("20"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topologyv1alpha1.ZoneList{
						{
							Name: "node-0",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("20"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "node-1",
							Resources: topologyv1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Allocatable: resource.MustParse("10"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Allocatable: resource.MustParse("20Gi"),
								},
								{
									Name:        "other-resource",
									Allocatable: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNRTResourcesChanged(tt.args.nrtOld, tt.args.nrtNew)
			assert.Equal(t, tt.want, got)
		})
	}
}

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

package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNodeResourceAmplificationRatios(t *testing.T) {
	tests := []struct {
		name    string
		node    *corev1.Node
		want    map[corev1.ResourceName]Ratio
		wantErr bool
	}{
		{
			name:    "node has no annotation",
			node:    &corev1.Node{},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has no resource amplification ratio annotation",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx": "yyy",
					},
				},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has valid resource amplification ratio annotation",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                                    "yyy",
						AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
			want:    map[corev1.ResourceName]Ratio{corev1.ResourceCPU: 1.22},
			wantErr: false,
		},
		{
			name: "node has invalid resource amplification ratio annotation",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                                    "yyy",
						AnnotationNodeResourceAmplificationRatio: "invalid",
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetNodeResourceAmplificationRatios(tt.node.Annotations)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestGetNodeResourceAmplificationRatio(t *testing.T) {
	type args struct {
		node     *corev1.Node
		resource corev1.ResourceName
	}
	tests := []struct {
		name    string
		args    args
		want    Ratio
		wantErr bool
	}{
		{
			name: "node has no annotation",
			args: args{
				node:     &corev1.Node{},
				resource: corev1.ResourceCPU,
			},
			want:    -1,
			wantErr: false,
		},
		{
			name: "node has no resource amplification ratio annotation",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				resource: corev1.ResourceCPU,
			},
			want:    -1,
			wantErr: false,
		},
		{
			name: "node has valid resource amplification ratio annotation with specific resource set",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"xxx":                                    "yyy",
							AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
						},
					},
				},
				resource: corev1.ResourceCPU,
			},
			want:    1.22,
			wantErr: false,
		},
		{
			name: "node has valid resource amplification ratio annotation with specific resource unset",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"xxx":                                    "yyy",
							AnnotationNodeResourceAmplificationRatio: `{"memory":1.22}`,
						},
					},
				},
				resource: corev1.ResourceCPU,
			},
			want:    -1,
			wantErr: false,
		},
		{
			name: "node has invalid resource amplification ratio annotation",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"xxx":                                    "yyy",
							AnnotationNodeResourceAmplificationRatio: "invalid",
						},
					},
				},
				resource: corev1.ResourceCPU,
			},
			want:    -1,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetNodeResourceAmplificationRatio(tt.args.node, tt.args.resource)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestSetNodeResourceAmplificationRatios(t *testing.T) {
	type args struct {
		node   *corev1.Node
		ratios map[corev1.ResourceName]Ratio
	}
	tests := []struct {
		name      string
		args      args
		wantField *corev1.Node
	}{
		{
			name: "set for node with no annotation, keep precision 2",
			args: args{
				node: &corev1.Node{},
				ratios: map[corev1.ResourceName]Ratio{
					corev1.ResourceCPU: 1.222,
				},
			},
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
		},
		{
			name: "set for node with old annotation",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationNodeResourceAmplificationRatio: `{"cpu":1.33}`,
						},
					},
				},
				ratios: map[corev1.ResourceName]Ratio{
					corev1.ResourceCPU: 1.22,
				},
			},
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetNodeResourceAmplificationRatios(tt.args.node, tt.args.ratios)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

func TestSetNodeResourceAmplificationRatio(t *testing.T) {
	type args struct {
		node     *corev1.Node
		resource corev1.ResourceName
		ratio    Ratio
	}
	tests := []struct {
		name      string
		args      args
		want      bool
		wantErr   bool
		wantField *corev1.Node
	}{
		{
			name: "set for node with no annotation, keep precision 2",
			args: args{
				node:     &corev1.Node{},
				resource: corev1.ResourceCPU,
				ratio:    1.222,
			},
			want:    true,
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
		},
		{
			name: "set for node with old value changed",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationNodeResourceAmplificationRatio: `{"cpu":1.33}`,
						},
					},
				},
				resource: corev1.ResourceCPU,
				ratio:    1.22,
			},
			want:    true,
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
		},
		{
			name: "set for node with new value set",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationNodeResourceAmplificationRatio: `{"memory":1.22}`,
						},
					},
				},
				resource: corev1.ResourceCPU,
				ratio:    1.22,
			},
			want:    true,
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22,"memory":1.22}`,
					},
				},
			},
		},
		{
			name: "set for node with old value unchanged",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
						},
					},
				},
				resource: corev1.ResourceCPU,
				ratio:    1.22,
			},
			want:    false,
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
		},
		{
			name: "set for node with invalid resource amplification ratio annotation",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationNodeResourceAmplificationRatio: "invalid",
						},
					},
				},
				resource: corev1.ResourceCPU,
				ratio:    1.22,
			},
			want:    false,
			wantErr: true,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeResourceAmplificationRatio: "invalid",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := SetNodeResourceAmplificationRatio(tt.args.node, tt.args.resource, tt.args.ratio)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

func TestGetNodeRawCapacity(t *testing.T) {
	tests := []struct {
		name    string
		arg     *corev1.Node
		want    corev1.ResourceList
		wantErr bool
	}{
		{
			name:    "node has no annotation",
			arg:     &corev1.Node{},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has no raw capacity annotation",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx": "yyy",
					},
				},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has valid raw capacity annotation",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                     "yyy",
						AnnotationNodeRawCapacity: `{"cpu":"1"}`,
					},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
			wantErr: false,
		},
		{
			name: "node has invalid raw capacity annotation",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                     "yyy",
						AnnotationNodeRawCapacity: "invalid",
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetNodeRawCapacity(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestSetNodeRawCapacity(t *testing.T) {
	type args struct {
		node     *corev1.Node
		capacity corev1.ResourceList
	}
	tests := []struct {
		name      string
		args      args
		wantField *corev1.Node
	}{
		{
			name: "set for node with no annotation",
			args: args{
				node: &corev1.Node{},
				capacity: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1"),
				},
			},
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeRawCapacity: `{"cpu":"1"}`,
					},
				},
			},
		},
		{
			name: "set for node with old annotation",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationNodeRawCapacity: `{"cpu":"2"}`,
						},
					},
				},
				capacity: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1"),
				},
			},
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeRawCapacity: `{"cpu":"1"}`,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetNodeRawCapacity(tt.args.node, tt.args.capacity)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

func TestGetNodeRawAllocatable(t *testing.T) {
	tests := []struct {
		name    string
		arg     *corev1.Node
		want    corev1.ResourceList
		wantErr bool
	}{
		{
			name:    "node has no annotation",
			arg:     &corev1.Node{},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has no raw allocatable annotation",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx": "yyy",
					},
				},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has valid raw allocatable annotation",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                        "yyy",
						AnnotationNodeRawAllocatable: `{"cpu":"1"}`,
					},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
			wantErr: false,
		},
		{
			name: "node has invalid raw allocatable annotation",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                        "yyy",
						AnnotationNodeRawAllocatable: "invalid",
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetNodeRawAllocatable(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestSetNodeRawAllocatable(t *testing.T) {
	type args struct {
		node        *corev1.Node
		allocatable corev1.ResourceList
	}
	tests := []struct {
		name      string
		args      args
		wantField *corev1.Node
	}{
		{
			name: "set for node with no annotation",
			args: args{
				node: &corev1.Node{},
				allocatable: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1"),
				},
			},
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeRawAllocatable: `{"cpu":"1"}`,
					},
				},
			},
		},
		{
			name: "set for node with old annotation",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationNodeRawAllocatable: `{"cpu":"2"}`,
						},
					},
				},
				allocatable: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1"),
				},
			},
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeRawAllocatable: `{"cpu":"1"}`,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetNodeRawAllocatable(tt.args.node, tt.args.allocatable)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNetworkTopologySpec(t *testing.T) {
	type args struct {
		obj v1.Object
	}
	tests := []struct {
		name    string
		args    args
		want    *NetworkTopologySpec
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "Annotation not present",
			args: args{
				obj: &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
			},
			want:    nil,
			wantErr: assert.NoError,
		},
		{
			name: "Empty annotation value",
			args: args{
				obj: &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangNetworkTopologySpec: "",
						},
					},
				},
			},
			want:    nil,
			wantErr: assert.NoError,
		},
		{
			name: "Invalid annotation value",
			args: args{
				obj: &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangNetworkTopologySpec: "invalid",
						},
					},
				},
			},
			want:    nil,
			wantErr: assert.Error,
		},
		{
			name: "Valid annotation value",
			args: args{
				obj: &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangNetworkTopologySpec: `{"gatherStrategy":[{"layer":"Node","strategy":"MustGather"}]}`,
						},
					},
				},
			},
			want: &NetworkTopologySpec{
				GatherStrategy: []NetworkTopologyGatherRule{
					{
						Layer:    "Node",
						Strategy: NetworkTopologyGatherStrategyMustGather,
					},
				},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetNetworkTopologySpec(tt.args.obj)
			if !tt.wantErr(t, err, fmt.Sprintf("GetNetworkTopologySpec(%v)", tt.args.obj)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetNetworkTopologySpec(%v)", tt.args.obj)
		})
	}
}

func TestGetPodIndex(t *testing.T) {
	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name    string
		args    args
		want    int
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "pod is nil",
			args: args{
				pod: nil,
			},
			want:    -1,
			wantErr: assert.NoError,
		},
		{
			name: "Empty annotations handling",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
			},
			want:    -1,
			wantErr: assert.NoError,
		},
		{
			name: "Invalid numeric format",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangPodNetworkTopologyIndex: "invalid",
						},
					},
				},
			},
			want:    -1,
			wantErr: assert.Error,
		},
		{
			name: "Valid positive number",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangPodNetworkTopologyIndex: "1",
						},
					},
				},
			},
			want:    1,
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetPodIndex(tt.args.pod)
			if !tt.wantErr(t, err, fmt.Sprintf("GetPodIndex(%v)", tt.args.pod)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetPodIndex(%v)", tt.args.pod)
		})
	}
}

func TestSortPodsByIndex(t *testing.T) {
	tests := []struct {
		name string
		pods []*corev1.Pod
		want []*corev1.Pod
	}{
		{
			name: "Sort pods by name",
			pods: []*corev1.Pod{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pod2",
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pod1",
					},
				},
			},
			want: []*corev1.Pod{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pod1",
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pod2",
					},
				},
			},
		},
		{
			name: "Sort pods by index",
			pods: []*corev1.Pod{
				{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangPodNetworkTopologyIndex: "2",
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangPodNetworkTopologyIndex: "1",
						},
					},
				},
			},
			want: []*corev1.Pod{
				{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangPodNetworkTopologyIndex: "1",
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationGangPodNetworkTopologyIndex: "2",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SortPodsByIndex(tt.pods)
		})
	}
}

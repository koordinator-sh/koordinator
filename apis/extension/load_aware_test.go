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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestGetIgnoreLoadAwareResources(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want sets.Set[corev1.ResourceName]
	}{
		{
			name: "no annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			want: nil,
		},
		{
			name: "empty annotation value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationIgnoreLoadAwareResources: "",
					},
				},
			},
			want: nil,
		},
		{
			name: "valid json array with cpu and memory",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationIgnoreLoadAwareResources: `["cpu","memory"]`,
					},
				},
			},
			want: sets.New(corev1.ResourceCPU, corev1.ResourceMemory),
		},
		{
			name: "valid json array with single resource",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationIgnoreLoadAwareResources: `["memory"]`,
					},
				},
			},
			want: sets.New(corev1.ResourceMemory),
		},
		{
			name: "empty json array",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationIgnoreLoadAwareResources: `[]`,
					},
				},
			},
			want: nil,
		},
		{
			name: "invalid json falls back to nil",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationIgnoreLoadAwareResources: `{"cpu", "memory"}`,
					},
				},
			},
			want: nil,
		},
		{
			name: "malformed json falls back to nil",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationIgnoreLoadAwareResources: `not-json`,
					},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetIgnoreLoadAwareResources(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

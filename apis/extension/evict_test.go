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
)

func Test_PodEvictDisabled(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name:     "nil pod",
			pod:      nil,
			expected: false,
		},
		{
			name: "nil lables",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: nil,
				},
			},
			expected: false,
		},
		{
			name: "no that label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"xxx": "xxx",
					},
				},
			},
			expected: false,
		},
		{
			name: "that label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodEvictEnabled: "false",
					},
				},
			},
			expected: false,
		},
		{
			name: "invalid value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodEvictEnabled: "xxx",
					},
				},
			},
			expected: false,
		},
		{
			name: "evict disabled",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodEvictEnabled: "true",
					},
				},
			},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := PodEvictEnabled(tt.pod)
			assert.Equal(t, res, tt.expected)
		})
	}
}

func Test_GetPodEvictionPriority(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected int32
		wantErr  bool
	}{
		{
			name:     "nil pod",
			pod:      nil,
			expected: 0,
		},
		{
			name: "no annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx": "xxx",
					},
				},
			},
			expected: 0,
		},
		{
			name: "invalid value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationPodEvictionPriority: "xxx",
					},
				},
			},
			expected: 0,
			wantErr:  true,
		},
		{
			name: "positive value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationPodEvictionPriority: "100",
					},
				},
			},
			expected: 100,
		},
		{
			name: "negative value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationPodEvictionPriority: "-10",
					},
				},
			},
			expected: -10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetPodEvictionPriority(tt.pod)
			assert.Equal(t, tt.expected, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

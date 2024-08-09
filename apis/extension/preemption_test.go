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

func TestGetPodKoordPreemptionPolicy(t *testing.T) {
	policyNever := corev1.PreemptNever
	policyPreemptLowerPriority := corev1.PreemptLowerPriority
	tests := []struct {
		name string
		arg  *corev1.Pod
		want *corev1.PreemptionPolicy
	}{
		{
			name: "pod is nil",
			arg:  nil,
			want: nil,
		},
		{
			name: "pod labels is empty",
			arg:  &corev1.Pod{},
			want: nil,
		},
		{
			name: "pod label is not set",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
			want: nil,
		},
		{
			name: "never preempt",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodPreemptionPolicy: string(corev1.PreemptNever),
					},
				},
			},
			want: &policyNever,
		},
		{
			name: "preempt lower priority",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodPreemptionPolicy: string(corev1.PreemptLowerPriority),
					},
				},
			},
			want: &policyPreemptLowerPriority,
		},
		{
			name: "policy unknown",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodPreemptionPolicy: "unknownPolicy",
					},
				},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPodKoordPreemptionPolicy(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsPodPreemptible(t *testing.T) {
	tests := []struct {
		name string
		arg  *corev1.Pod
		want bool
	}{
		{
			name: "pod is nil",
			arg:  nil,
			want: true,
		},
		{
			name: "pod has no label",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			want: true,
		},
		{
			name: "pod is marked as non-preemptible",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						LabelDisablePreemptible: "true",
					},
				},
			},
			want: false,
		},
		{
			name: "pod is marked as preemptible",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						LabelDisablePreemptible: "false",
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPodPreemptible(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

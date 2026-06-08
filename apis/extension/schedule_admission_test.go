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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHasScheduleAdmissionLabels(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "nil pod",
			pod:  nil,
			want: false,
		},
		{
			name: "pod with no labels",
			pod:  &corev1.Pod{},
			want: false,
		},
		{
			name: "pod with unrelated labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
			},
			want: false,
		},
		{
			name: "pod with one schedule-admission label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelScheduleAdmissionPrefix + "quota-check": "true",
					},
				},
			},
			want: true,
		},
		{
			name: "pod with multiple schedule-admission labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelScheduleAdmissionPrefix + "quota-check":    "true",
						LabelScheduleAdmissionPrefix + "resource-ready": "true",
					},
				},
			},
			want: true,
		},
		{
			name: "pod with mixed labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
						LabelScheduleAdmissionPrefix + "quota-check": "true",
						"version": "v1",
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasScheduleAdmissionLabels(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetScheduleAdmissionGates(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want []string
	}{
		{
			name: "nil pod",
			pod:  nil,
			want: nil,
		},
		{
			name: "pod with no schedule-admission labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
			},
			want: nil,
		},
		{
			name: "pod with one gate",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelScheduleAdmissionPrefix + "quota-check": "true",
					},
				},
			},
			want: []string{"quota-check"},
		},
		{
			name: "pod with multiple gates",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelScheduleAdmissionPrefix + "quota-check":    "true",
						LabelScheduleAdmissionPrefix + "resource-ready": "true",
						"app": "test",
					},
				},
			},
			want: []string{"quota-check", "resource-ready"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetScheduleAdmissionGates(tt.pod)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				sort.Strings(got)
				sort.Strings(tt.want)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

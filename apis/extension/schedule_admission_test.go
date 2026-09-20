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

func podWithLabels(labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
	}
}

func TestHasScheduleAdmissionLabels(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		prefixMatch bool
		want        bool
	}{
		{
			name:        "nil pod",
			pod:         nil,
			prefixMatch: true,
			want:        false,
		},
		{
			name:        "pod with no labels",
			pod:         &corev1.Pod{},
			prefixMatch: true,
			want:        false,
		},
		{
			name:        "pod with unrelated labels",
			pod:         podWithLabels(map[string]string{"app": "test"}),
			prefixMatch: true,
			want:        false,
		},
		{
			name:        "exact-match mode: fixed label present",
			pod:         podWithLabels(map[string]string{LabelScheduleAdmission: "true"}),
			prefixMatch: false,
			want:        true,
		},
		{
			name:        "exact-match mode: only prefixed label present",
			pod:         podWithLabels(map[string]string{LabelScheduleAdmissionPrefix + "quota-check": "true"}),
			prefixMatch: false,
			want:        false,
		},
		{
			name:        "prefix mode: fixed label present",
			pod:         podWithLabels(map[string]string{LabelScheduleAdmission: "true"}),
			prefixMatch: true,
			want:        true,
		},
		{
			name:        "prefix mode: one prefixed label present",
			pod:         podWithLabels(map[string]string{LabelScheduleAdmissionPrefix + "quota-check": "true"}),
			prefixMatch: true,
			want:        true,
		},
		{
			name: "prefix mode: multiple prefixed labels present",
			pod: podWithLabels(map[string]string{
				LabelScheduleAdmissionPrefix + "quota-check":    "true",
				LabelScheduleAdmissionPrefix + "resource-ready": "true",
			}),
			prefixMatch: true,
			want:        true,
		},
		{
			name: "prefix mode: mixed labels",
			pod: podWithLabels(map[string]string{
				"app": "test",
				LabelScheduleAdmissionPrefix + "quota-check": "true",
				"version": "v1",
			}),
			prefixMatch: true,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasScheduleAdmissionLabels(tt.pod, tt.prefixMatch)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetScheduleAdmissionGates(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		prefixMatch bool
		want        []string
	}{
		{
			name:        "nil pod",
			pod:         nil,
			prefixMatch: true,
			want:        nil,
		},
		{
			name:        "pod with no schedule-admission labels",
			pod:         podWithLabels(map[string]string{"app": "test"}),
			prefixMatch: true,
			want:        nil,
		},
		{
			name:        "exact-match mode: fixed label only",
			pod:         podWithLabels(map[string]string{LabelScheduleAdmission: "true"}),
			prefixMatch: false,
			want:        []string{LabelScheduleAdmission},
		},
		{
			name:        "exact-match mode: ignores prefixed labels",
			pod:         podWithLabels(map[string]string{LabelScheduleAdmissionPrefix + "quota-check": "true"}),
			prefixMatch: false,
			want:        nil,
		},
		{
			name:        "prefix mode: one gate",
			pod:         podWithLabels(map[string]string{LabelScheduleAdmissionPrefix + "quota-check": "true"}),
			prefixMatch: true,
			want:        []string{"quota-check"},
		},
		{
			name: "prefix mode: multiple gates sorted deterministically",
			pod: podWithLabels(map[string]string{
				LabelScheduleAdmissionPrefix + "resource-ready": "true",
				LabelScheduleAdmissionPrefix + "quota-check":    "true",
				"app": "test",
			}),
			prefixMatch: true,
			want:        []string{"quota-check", "resource-ready"},
		},
		{
			name: "prefix mode: fixed label and prefixed gates sorted together",
			pod: podWithLabels(map[string]string{
				LabelScheduleAdmission:                          "true",
				LabelScheduleAdmissionPrefix + "resource-ready": "true",
			}),
			prefixMatch: true,
			want:        []string{"resource-ready", LabelScheduleAdmission},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetScheduleAdmissionGates(tt.pod, tt.prefixMatch)
			assert.Equal(t, tt.want, got)
		})
	}
}

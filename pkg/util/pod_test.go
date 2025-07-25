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

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func Test_GetCPUSetFromPod(t *testing.T) {
	type args struct {
		podAnnotations map[string]string
		podAlloc       *apiext.ResourceStatus
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "get cpuset from annotation",
			args: args{
				podAnnotations: map[string]string{},
				podAlloc: &apiext.ResourceStatus{
					CPUSet: "2-4",
				},
			},
			want:    "2-4",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.args.podAlloc != nil {
				podAllocJson := DumpJSON(tt.args.podAlloc)
				tt.args.podAnnotations[apiext.AnnotationResourceStatus] = podAllocJson
			}
			got, err := GetCPUSetFromPod(tt.args.podAnnotations)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsPodInactive(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name:     "Pod is nil",
			pod:      nil,
			expected: true,
		},
		{
			name: "Pod is Pending",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-pending"},
				Status:     corev1.PodStatus{Phase: corev1.PodPending},
			},
			expected: false,
		},
		{
			name: "Pod is Running",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-running"},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			},
			expected: false,
		},
		{
			name: "Pod is Succeeded",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-succeeded"},
				Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
			},
			expected: true,
		},
		{
			name: "Pod is Failed",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-failed"},
				Status:     corev1.PodStatus{Phase: corev1.PodFailed},
			},
			expected: true,
		},
		{
			name: "Pod is Unknown",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-unknown"},
				Status:     corev1.PodStatus{Phase: "Unknown"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPodInactive(tt.pod)
			assert.Equal(t, tt.expected, result)
		})
	}
}

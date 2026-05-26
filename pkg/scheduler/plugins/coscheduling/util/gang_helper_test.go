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
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

func TestGetGangGroupId(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{
			name:  "nil slice",
			input: nil,
			want:  "",
		},
		{
			name:  "empty slice",
			input: []string{},
			want:  "",
		},
		{
			name:  "single element",
			input: []string{"nsA/gangA"},
			want:  "nsA/gangA",
		},
		{
			name:  "already sorted",
			input: []string{"nsA/gangA", "nsB/gangB"},
			want:  "nsA/gangA,nsB/gangB",
		},
		{
			name:  "unsorted — sorts before joining",
			input: []string{"nsB/gangB", "nsA/gangA"},
			want:  "nsA/gangA,nsB/gangB",
		},
		{
			name:  "three elements unsorted",
			input: []string{"nsC/gangC", "nsA/gangA", "nsB/gangB"},
			want:  "nsA/gangA,nsB/gangB,nsC/gangC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetGangGroupId(tt.input))
		})
	}
}

func TestGetId(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		podName   string
		want      string
	}{
		{
			name:      "normal namespace and name",
			namespace: "default",
			podName:   "mypod",
			want:      "default/mypod",
		},
		{
			name:      "empty namespace",
			namespace: "",
			podName:   "mypod",
			want:      "/mypod",
		},
		{
			name:      "empty name",
			namespace: "default",
			podName:   "",
			want:      "default/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetId(tt.namespace, tt.podName))
		})
	}
}

func TestGetGangNameByPod(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{
			name: "nil pod",
			pod:  nil,
			want: "",
		},
		{
			name: "pod with no gang label",
			pod:  &corev1.Pod{},
			want: "",
		},
		{
			name: "pod with PodGroupLabel",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: "my-gang",
					},
				},
			},
			want: "my-gang",
		},
		{
			name: "pod with gang annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationGangName: "my-gang",
					},
				},
			},
			want: "my-gang",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetGangNameByPod(tt.pod))
		})
	}
}

func TestGetGangMinNumFromPod(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		want    int
		wantErr bool
	}{
		{
			name:    "no label or annotation",
			pod:     &corev1.Pod{},
			want:    0,
			wantErr: true,
		},
		{
			name: "deprecated label path",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						// nolint:staticcheck
						extension.LabelLightweightCoschedulingPodGroupMinAvailable: "3",
					},
				},
			},
			want:    3,
			wantErr: false,
		},
		{
			name: "deprecated label with invalid value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						// nolint:staticcheck
						extension.LabelLightweightCoschedulingPodGroupMinAvailable: "notanumber",
					},
				},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "annotation path",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationGangMinNum: "5",
					},
				},
			},
			want:    5,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetGangMinNumFromPod(tt.pod)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestIsPodNeedGang(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "pod with no gang",
			pod:  &corev1.Pod{},
			want: false,
		},
		{
			name: "pod with gang label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: "my-gang",
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsPodNeedGang(tt.pod))
		})
	}
}

func TestGetWaitTimeDuration(t *testing.T) {
	defaultTimeout := 10 * time.Minute
	tests := []struct {
		name string
		pg   *v1alpha1.PodGroup
		want time.Duration
	}{
		{
			name: "nil PodGroup uses default",
			pg:   nil,
			want: defaultTimeout,
		},
		{
			name: "nil ScheduleTimeoutSeconds uses default",
			pg:   &v1alpha1.PodGroup{},
			want: defaultTimeout,
		},
		{
			name: "zero ScheduleTimeoutSeconds uses default",
			pg: &v1alpha1.PodGroup{
				Spec: v1alpha1.PodGroupSpec{
					ScheduleTimeoutSeconds: ptr.To[int32](0),
				},
			},
			want: defaultTimeout,
		},
		{
			name: "positive ScheduleTimeoutSeconds overrides default",
			pg: &v1alpha1.PodGroup{
				Spec: v1alpha1.PodGroupSpec{
					ScheduleTimeoutSeconds: ptr.To[int32](30),
				},
			},
			want: 30 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetWaitTimeDuration(tt.pg, defaultTimeout))
		})
	}
}

func TestStringToGangGroupSlice(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:    "empty string returns nil",
			input:   "",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "valid JSON array with two elements",
			input:   `["nsA/gangA","nsB/gangB"]`,
			want:    []string{"nsA/gangA", "nsB/gangB"},
			wantErr: false,
		},
		{
			name:    "valid JSON array with single element",
			input:   `["nsA/gangA"]`,
			want:    []string{"nsA/gangA"},
			wantErr: false,
		},
		{
			name:    "empty JSON array",
			input:   `[]`,
			want:    []string{},
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			input:   `not-json`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "JSON object instead of array",
			input:   `{"key":"value"}`,
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StringToGangGroupSlice(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestCreateMergePatch(t *testing.T) {
	t.Run("no change produces empty patch", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
		}
		patch, err := CreateMergePatch(pod, pod)
		assert.NoError(t, err)
		assert.Equal(t, "{}", string(patch))
	})

	t.Run("label addition produces non-empty patch", func(t *testing.T) {
		original := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
		}
		modified := original.DeepCopy()
		modified.Labels = map[string]string{"app": "myapp"}
		patch, err := CreateMergePatch(original, modified)
		assert.NoError(t, err)
		assert.Contains(t, string(patch), "myapp")
	})
}

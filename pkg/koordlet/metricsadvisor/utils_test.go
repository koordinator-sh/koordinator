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

package metricsadvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func Test_getRuntimeClassName(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want *string
	}{
		{
			name: "empty RuntimeClassName",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
				},
			},
			want: nil,
		},
		{
			name: "kata RuntimeClassName",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-2",
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: pointer.StringPtr("kata"),
				},
			},
			want: pointer.StringPtr("kata"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getRuntimeClassName(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_ignoreNonRuncPod(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-2",
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: pointer.StringPtr("kata"),
		},
	}
	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-3",
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: pointer.StringPtr("runc"),
		},
	}
	assert.False(t, ignoreNonRuncPod(pod1))
	assert.False(t, ignoreNonRuncPod(pod2))
	assert.False(t, ignoreNonRuncPod(pod3))
}

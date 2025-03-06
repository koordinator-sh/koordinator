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

package protocol

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestContainerReconcileIgnoreFilter(t *testing.T) {
	type args struct {
		pod           *corev1.Pod
		container     *corev1.Container
		containerStat *corev1.ContainerStatus
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "ignore kata container",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{
						RuntimeClassName: func() *string {
							s := "kata"
							return &s
						}(),
					},
				},
				container: &corev1.Container{},
				containerStat: &corev1.ContainerStatus{
					Name: "test",
				},
			},
			want: true,
		},
		{
			name: "reconcile normal container",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{},
				},
				container: &corev1.Container{},
				containerStat: &corev1.ContainerStatus{
					Name: "test",
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, ContainerReconcileIgnoreFilter(tt.args.pod, tt.args.container, tt.args.containerStat), "ContainerReconcileIgnoreFilter(%v, %v, %v)", tt.args.pod, tt.args.container, tt.args.containerStat)
		})
	}
}

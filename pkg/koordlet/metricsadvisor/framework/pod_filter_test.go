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

package framework

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

func TestNothingPodFilter(t *testing.T) {
	tests := []struct {
		name  string
		args  *statesinformer.PodMeta
		want  bool
		want1 string
	}{
		{
			name:  "always allow",
			args:  &statesinformer.PodMeta{},
			want:  false,
			want1: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &NothingPodFilter{}
			got, got1 := f.FilterPod(tt.args)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestTerminatedPodFilter(t *testing.T) {
	tests := []struct {
		name  string
		args  *statesinformer.PodMeta
		want  bool
		want1 string
	}{
		{
			name:  "filter invalid pod meta",
			args:  &statesinformer.PodMeta{},
			want:  true,
			want1: "invalid pod meta",
		},
		{
			name: "filter terminated pod",
			args: &statesinformer.PodMeta{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						Phase: corev1.PodFailed,
					},
				},
			},
			want:  true,
			want1: fmt.Sprintf("pod phase %s is terminated", corev1.PodFailed),
		},
		{
			name: "allow running pod",
			args: &statesinformer.PodMeta{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
			},
			want:  false,
			want1: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &TerminatedPodFilter{}
			got, got1 := f.FilterPod(tt.args)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestAndPodFilter(t *testing.T) {
	tests := []struct {
		name   string
		fields []PodFilter
		args   *statesinformer.PodMeta
		want   bool
		want1  string
	}{
		{
			name:  "allow with none filter",
			args:  &statesinformer.PodMeta{},
			want:  false,
			want1: "",
		},
		{
			name: "filter with one filter",
			fields: []PodFilter{
				&TerminatedPodFilter{},
			},
			args:  &statesinformer.PodMeta{},
			want:  true,
			want1: "invalid pod meta",
		},
		{
			name: "allow with multiple filters",
			fields: []PodFilter{
				&TerminatedPodFilter{},
				&NothingPodFilter{},
			},
			args: &statesinformer.PodMeta{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
			},
			want:  false,
			want1: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewAndPodFilter(tt.fields...)
			got, got1 := f.FilterPod(tt.args)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

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

package cri

import (
	"testing"

	"github.com/stretchr/testify/assert"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
)

func Test_updateResource(t *testing.T) {
	type args struct {
		a *v1alpha1.LinuxContainerResources
		b *v1alpha1.LinuxContainerResources
	}
	tests := []struct {
		name string
		args args
		want *v1alpha1.LinuxContainerResources
	}{
		{
			name: "a and b are both nil",
			args: args{
				a: nil,
				b: nil,
			},
			want: nil,
		},
		{
			name: "normal case",
			args: args{
				a: &v1alpha1.LinuxContainerResources{
					CpuPeriod:   1000,
					CpuShares:   500,
					OomScoreAdj: 10,
					Unified: map[string]string{
						"resourceA": "resource A",
					},
				},
				b: &v1alpha1.LinuxContainerResources{
					CpuPeriod:   2000,
					CpuShares:   1000,
					OomScoreAdj: 20,
					Unified: map[string]string{
						"resourceB": "resource B",
					},
				},
			},
			want: &v1alpha1.LinuxContainerResources{
				CpuPeriod:   2000,
				CpuShares:   1000,
				OomScoreAdj: 20,
				Unified: map[string]string{
					"resourceA": "resource A",
					"resourceB": "resource B",
				},
			},
		},
	}
	for _, tt := range tests {
		gotResources := updateResource(tt.args.a, tt.args.b)
		assert.Equal(t, tt.want, gotResources)
	}
}

func Test_transferToKoordResources(t *testing.T) {
	type args struct {
		r *runtimeapi.LinuxContainerResources
	}
	tests := []struct {
		name string
		args args
		want *v1alpha1.LinuxContainerResources
	}{
		{
			name: "normal case",
			args: args{
				r: &runtimeapi.LinuxContainerResources{
					CpuPeriod:   1000,
					CpuShares:   500,
					OomScoreAdj: 10,
					Unified: map[string]string{
						"resourceA": "resource A",
					},
				},
			},
			want: &v1alpha1.LinuxContainerResources{
				CpuPeriod:   1000,
				CpuShares:   500,
				OomScoreAdj: 10,
				Unified: map[string]string{
					"resourceA": "resource A",
				},
			},
		},
	}
	for _, tt := range tests {
		gotResources := transferToKoordResources(tt.args.r)
		assert.Equal(t, tt.want, gotResources)
	}
}

func Test_transferToCRIResources(t *testing.T) {
	type args struct {
		r *v1alpha1.LinuxContainerResources
	}
	tests := []struct {
		name string
		args args
		want *runtimeapi.LinuxContainerResources
	}{
		{
			name: "normal case",
			args: args{
				r: &v1alpha1.LinuxContainerResources{
					CpuPeriod:   1000,
					CpuShares:   500,
					OomScoreAdj: 10,
					Unified: map[string]string{
						"resourceA": "resource A",
					},
				},
			},
			want: &runtimeapi.LinuxContainerResources{
				CpuPeriod:   1000,
				CpuShares:   500,
				OomScoreAdj: 10,
				Unified: map[string]string{
					"resourceA": "resource A",
				},
			},
		},
	}
	for _, tt := range tests {
		gotResources := transferToCRIResources(tt.args.r)
		assert.Equal(t, tt.want, gotResources)
	}
}

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

package cpunormalization

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
)

func TestPlugin(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		p := Object()
		assert.NotNil(t, p)
	})
}

func TestPlugin_Register(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		p := newPlugin()
		p.Register(hooks.Options{})
	})
}

func TestPluginAdjustPodCFSQuota(t *testing.T) {
	type fields struct {
		rule *Rule
	}
	tests := []struct {
		name      string
		fields    fields
		arg       protocol.HooksProtocol
		wantErr   bool
		wantField protocol.HooksProtocol
	}{
		{
			name:      "nil input",
			arg:       (*protocol.PodContext)(nil),
			wantErr:   true,
			wantField: (*protocol.PodContext)(nil),
		},
		{
			name: "no resources to adjust",
			arg: &protocol.PodContext{
				Request: protocol.PodRequest{},
			},
			wantErr: false,
			wantField: &protocol.PodContext{
				Request: protocol.PodRequest{},
			},
		},
		{
			name: "skip non-cpushare pod",
			arg: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
			wantErr: false,
			wantField: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
		},
		{
			name: "rule disabled",
			fields: fields{
				rule: &Rule{
					enable:   false,
					curRatio: -1,
				},
			},
			arg: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
			wantErr: false,
			wantField: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
		},
		{
			name: "adjust correctly",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.2,
				},
			},
			arg: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.PodResponse{
					Resources: protocol.Resources{},
				},
			},
			wantErr: false,
			wantField: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.PodResponse{
					Resources: protocol.Resources{
						CFSQuota: pointer.Int64(83334),
					},
				},
			},
		},
		{
			name: "adjust correctly 1",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.0,
				},
			},
			arg: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.PodResponse{
					Resources: protocol.Resources{},
				},
			},
			wantErr: false,
			wantField: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.PodResponse{
					Resources: protocol.Resources{
						CFSQuota: pointer.Int64(100000),
					},
				},
			},
		},
		{
			name: "skip illegal ratio",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 0,
				},
			},
			arg: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.PodResponse{
					Resources: protocol.Resources{},
				},
			},
			wantErr: false,
			wantField: &protocol.PodContext{
				Request: protocol.PodRequest{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.PodResponse{
					Resources: protocol.Resources{
						CFSQuota: pointer.Int64(100000),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			p.rule = tt.fields.rule
			gotErr := p.AdjustPodCFSQuota(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.wantField, tt.arg)
		})
	}
}

func TestPluginAdjustContainerCFSQuota(t *testing.T) {
	type fields struct {
		rule *Rule
	}
	tests := []struct {
		name      string
		fields    fields
		arg       protocol.HooksProtocol
		wantErr   bool
		wantField protocol.HooksProtocol
	}{
		{
			name:      "nil input",
			arg:       (*protocol.ContainerContext)(nil),
			wantErr:   true,
			wantField: (*protocol.ContainerContext)(nil),
		},
		{
			name: "no resources to adjust",
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{},
			},
			wantErr: false,
			wantField: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{},
			},
		},
		{
			name: "skip non-cpushare container",
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
			wantErr: false,
			wantField: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
		},
		{
			name: "rule disabled",
			fields: fields{
				rule: &Rule{
					enable:   false,
					curRatio: -1,
				},
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
			wantErr: false,
			wantField: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares: pointer.Int64(1024),
						CFSQuota:  pointer.Int64(100000),
					},
				},
			},
		},
		{
			name: "adjust correctly",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.2,
				},
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.ContainerResponse{
					Resources: protocol.Resources{},
				},
			},
			wantErr: false,
			wantField: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.ContainerResponse{
					Resources: protocol.Resources{
						CFSQuota: pointer.Int64(83334),
					},
				},
			},
		},
		{
			name: "adjust correctly 1",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.0,
				},
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.ContainerResponse{
					Resources: protocol.Resources{},
				},
			},
			wantErr: false,
			wantField: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.ContainerResponse{
					Resources: protocol.Resources{
						CFSQuota: pointer.Int64(100000),
					},
				},
			},
		},
		{
			name: "skip illegal ratio",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 0,
				},
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.ContainerResponse{
					Resources: protocol.Resources{},
				},
			},
			wantErr: false,
			wantField: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					Resources: &protocol.Resources{
						CPUShares:   pointer.Int64(1024),
						CFSQuota:    pointer.Int64(100000),
						MemoryLimit: pointer.Int64(2147483648),
					},
				},
				Response: protocol.ContainerResponse{
					Resources: protocol.Resources{
						CFSQuota: pointer.Int64(100000),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			p.rule = tt.fields.rule
			gotErr := p.AdjustContainerCFSQuota(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.wantField, tt.arg)
		})
	}
}

func Test_isPodCPUShare(t *testing.T) {
	type args struct {
		labels      map[string]string
		annotations map[string]string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "none pod is cpushare",
			args: args{
				labels:      nil,
				annotations: nil,
			},
			want: true,
		},
		{
			name: "none pod is cpushare",
			args: args{
				labels:      map[string]string{},
				annotations: map[string]string{},
			},
			want: true,
		},
		{
			name: "ls pod is cpushare",
			args: args{
				labels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLS),
				},
			},
			want: true,
		},
		{
			name: "lse pod is not cpushare",
			args: args{
				labels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLSE),
				},
			},
			want: false,
		},
		{
			name: "be pod is not cpushare",
			args: args{
				labels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLSE),
				},
			},
			want: false,
		},
		{
			name: "cpuset pod is considered not cpushare",
			args: args{
				labels: map[string]string{},
				annotations: map[string]string{
					extension.AnnotationResourceStatus: `{"cpuset": "2-3,34-35"}`,
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPodCPUShare(tt.args.labels, tt.args.annotations)
			assert.Equal(t, tt.want, got)
		})
	}
}

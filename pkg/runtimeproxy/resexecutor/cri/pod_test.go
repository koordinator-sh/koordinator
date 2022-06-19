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
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/store"
)

func TestPodResourceExecutor_UpdateRequestForRunPodSandboxRequest(t *testing.T) {
	type fields struct {
		PodSandboxInfo store.PodSandboxInfo
	}
	type args struct {
		rsp interface{}
		req interface{}
	}
	tests := []struct {
		name             string
		fields           fields
		args             args
		wantAnnotations  map[string]string
		wantLabels       map[string]string
		wantCgroupParent string
		wantErr          bool
	}{
		{
			name: "not compatible rsp type",
			args: args{
				rsp: &v1alpha1.ContainerResourceHookResponse{},
				req: &runtimeapi.RunPodSandboxRequest{},
			},
			wantAnnotations:  nil,
			wantLabels:       nil,
			wantCgroupParent: "",
			wantErr:          true,
		},
		{
			name: "normal case",
			fields: fields{
				PodSandboxInfo: store.PodSandboxInfo{
					PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
						Annotations: map[string]string{
							"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
						},
						Labels: map[string]string{
							"label.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
						},
						CgroupParent: "/kubepods/besteffort",
					},
				},
			},
			args: args{
				req: &runtimeapi.RunPodSandboxRequest{
					Config: &runtimeapi.PodSandboxConfig{
						Linux: &runtimeapi.LinuxPodSandboxConfig{},
					},
				},
				rsp: &v1alpha1.PodSandboxHookResponse{
					Annotations: map[string]string{
						"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
					},
					Labels: map[string]string{
						"label.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
					},
					CgroupParent: "/offline/besteffort",
				},
			},
			wantErr: false,
			wantAnnotations: map[string]string{
				"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
				"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
			},
			wantLabels: map[string]string{
				"label.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
				"label.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
			},

			wantCgroupParent: "/offline/besteffort",
		},
	}
	for _, tt := range tests {
		p := &PodResourceExecutor{
			PodSandboxInfo: tt.fields.PodSandboxInfo,
		}
		err := p.UpdateRequest(tt.args.rsp, tt.args.req)
		assert.Equal(t, tt.wantErr, err != nil, err)
		assert.Equal(t, tt.wantAnnotations, tt.args.req.(*runtimeapi.RunPodSandboxRequest).GetConfig().GetAnnotations())
		assert.Equal(t, tt.wantLabels, tt.args.req.(*runtimeapi.RunPodSandboxRequest).GetConfig().GetLabels())
		assert.Equal(t, tt.wantCgroupParent, tt.args.req.(*runtimeapi.RunPodSandboxRequest).GetConfig().GetLinux().GetCgroupParent())
	}
}

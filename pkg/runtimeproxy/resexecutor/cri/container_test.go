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

func TestContainerResourceExecutor_UpdateRequestForCreateContainerRequest(t *testing.T) {
	type fields struct {
		ContainerInfo store.ContainerInfo
	}
	type args struct {
		rsp interface{}
		req interface{}
	}
	tests := []struct {
		name                string
		fields              fields
		args                args
		wantAnnotations     map[string]string
		wantResource        *runtimeapi.LinuxContainerResources
		wantPodCgroupParent string
		wantErr             bool
	}{
		{
			name: "not compatible rsp type",
			args: args{
				rsp: &v1alpha1.PodSandboxHookResponse{},
				req: &runtimeapi.CreateContainerRequest{},
			},
			wantAnnotations:     nil,
			wantResource:        nil,
			wantPodCgroupParent: "",
			wantErr:             true,
		},
		{
			name: "normal case",
			fields: fields{
				ContainerInfo: store.ContainerInfo{
					ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
						ContainerAnnotations: map[string]string{
							"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
						},
						PodCgroupParent: "/kubepods/besteffort",
						ContainerResources: &v1alpha1.LinuxContainerResources{
							CpuPeriod:   1000,
							CpuShares:   500,
							OomScoreAdj: 10,
							Unified: map[string]string{
								"resourceA": "resource A",
							},
						},
					},
				},
			},
			args: args{
				req: &runtimeapi.CreateContainerRequest{
					Config: &runtimeapi.ContainerConfig{
						Linux: &runtimeapi.LinuxContainerConfig{},
					},
					SandboxConfig: &runtimeapi.PodSandboxConfig{
						Linux: &runtimeapi.LinuxPodSandboxConfig{},
					},
				},
				rsp: &v1alpha1.ContainerResourceHookResponse{
					ContainerAnnotations: map[string]string{
						"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
					},
					ContainerResources: &v1alpha1.LinuxContainerResources{
						CpuPeriod:   2000,
						CpuShares:   100,
						OomScoreAdj: 20,
						Unified: map[string]string{
							"resourceB": "resource B",
						},
					},
					PodCgroupParent: "/offline/besteffort",
				},
			},
			wantErr: false,
			wantResource: &runtimeapi.LinuxContainerResources{
				CpuPeriod:   2000,
				CpuShares:   100,
				OomScoreAdj: 20,
				Unified: map[string]string{
					"resourceA": "resource A",
					"resourceB": "resource B",
				},
			},
			wantAnnotations: map[string]string{
				"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
				"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
			},
			wantPodCgroupParent: "/offline/besteffort",
		},
	}
	for _, tt := range tests {
		c := &ContainerResourceExecutor{
			ContainerInfo: tt.fields.ContainerInfo,
		}
		err := c.UpdateRequest(tt.args.rsp, tt.args.req)
		assert.Equal(t, tt.wantErr, err != nil, err)
		assert.Equal(t, tt.wantResource, tt.args.req.(*runtimeapi.CreateContainerRequest).GetConfig().GetLinux().GetResources())
		assert.Equal(t, tt.wantAnnotations, tt.args.req.(*runtimeapi.CreateContainerRequest).GetConfig().GetAnnotations())
		assert.Equal(t, tt.wantPodCgroupParent, tt.args.req.(*runtimeapi.CreateContainerRequest).GetSandboxConfig().GetLinux().GetCgroupParent())
	}
}

func TestContainerResourceExecutor_UpdateRequestForUpdateContainerResourcesRequest(t *testing.T) {
	type fields struct {
		ContainerInfo store.ContainerInfo
	}
	type args struct {
		rsp interface{}
		req interface{}
	}
	tests := []struct {
		name            string
		fields          fields
		args            args
		wantAnnotations map[string]string
		wantResource    *runtimeapi.LinuxContainerResources
		wantErr         bool
	}{
		{
			name: "not compatible rsp type",
			args: args{
				rsp: &v1alpha1.PodSandboxHookResponse{},
				req: &runtimeapi.UpdateContainerResourcesRequest{},
			},
			wantAnnotations: nil,
			wantResource:    nil,
			wantErr:         true,
		},
		{
			name: "normal case",
			fields: fields{
				ContainerInfo: store.ContainerInfo{
					ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
						ContainerAnnotations: map[string]string{
							"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
						},
						PodCgroupParent: "/kubepods/besteffort",
						ContainerResources: &v1alpha1.LinuxContainerResources{
							CpuPeriod:   1000,
							CpuShares:   500,
							OomScoreAdj: 10,
							Unified: map[string]string{
								"resourceA": "resource A",
							},
						},
					},
				},
			},
			args: args{
				req: &runtimeapi.UpdateContainerResourcesRequest{
					Linux: &runtimeapi.LinuxContainerResources{},
				},
				rsp: &v1alpha1.ContainerResourceHookResponse{
					ContainerAnnotations: map[string]string{
						"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
					},
					ContainerResources: &v1alpha1.LinuxContainerResources{
						CpuPeriod:   2000,
						CpuShares:   100,
						OomScoreAdj: 20,
						Unified: map[string]string{
							"resourceB": "resource B",
						},
					},
					PodCgroupParent: "/offline/besteffort",
				},
			},
			wantErr: false,
			wantResource: &runtimeapi.LinuxContainerResources{
				CpuPeriod:   2000,
				CpuShares:   100,
				OomScoreAdj: 20,
				Unified: map[string]string{
					"resourceA": "resource A",
					"resourceB": "resource B",
				},
			},
			wantAnnotations: map[string]string{
				"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_A": "true",
				"annotation.dummy.koordinator.sh/TestContainerResourceExecutor_UpdateRequest_B": "true",
			},
		},
	}
	for _, tt := range tests {
		c := &ContainerResourceExecutor{
			ContainerInfo: tt.fields.ContainerInfo,
		}
		err := c.UpdateRequest(tt.args.rsp, tt.args.req)
		assert.Equal(t, tt.wantErr, err != nil, err)
		assert.Equal(t, tt.wantResource, tt.args.req.(*runtimeapi.UpdateContainerResourcesRequest).GetLinux())
		assert.Equal(t, tt.wantAnnotations, tt.args.req.(*runtimeapi.UpdateContainerResourcesRequest).GetAnnotations())
	}
}

func TestContainerResourceExecutor_ResourceCheckPoint(t *testing.T) {
	type fields struct {
		ContainerInfo store.ContainerInfo
	}
	type args struct {
		rsp interface{}
	}
	tests := []struct {
		name          string
		fields        fields
		args          args
		wantErr       bool
		wantStoreInfo *store.ContainerInfo
	}{
		{
			name: "normal case - CreateContainerResponse - Set Container id successfully",
			args: args{
				rsp: &runtimeapi.CreateContainerResponse{
					ContainerId: "111111",
				},
			},
			fields: fields{
				ContainerInfo: store.ContainerInfo{
					ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
						ContainerMata: &v1alpha1.ContainerMetadata{},
					},
				},
			},
			wantErr: false,
			wantStoreInfo: &store.ContainerInfo{
				ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
					ContainerMata: &v1alpha1.ContainerMetadata{
						Id: "111111",
					},
				}},
		},
	}
	for _, tt := range tests {
		c := &ContainerResourceExecutor{
			ContainerInfo: tt.fields.ContainerInfo,
		}
		err := c.ResourceCheckPoint(tt.args.rsp)
		containerInfo := store.GetContainerInfo(c.ContainerInfo.ContainerMata.GetId())
		assert.Equal(t, tt.wantErr, err != nil, err)
		assert.Equal(t, tt.wantStoreInfo, containerInfo)
	}
}

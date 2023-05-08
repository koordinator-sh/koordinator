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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"

	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
)

func TestResources_IsOriginResSet(t *testing.T) {
	type fields struct {
		CPUShares *int64
		CFSQuota  *int64
		CPUSet    *string
		CPUBvt    *int64
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "some origin resource field is not nil",
			fields: fields{
				CPUSet: pointer.String("0-2"),
			},
			want: true,
		},
		{
			name: "all origin resource filed is nil",
			fields: fields{
				CPUBvt: pointer.Int64(-1),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Resources{
				CPUShares: tt.fields.CPUShares,
				CFSQuota:  tt.fields.CFSQuota,
				CPUSet:    tt.fields.CPUSet,
				CPUBvt:    tt.fields.CPUBvt,
			}
			if got := r.IsOriginResSet(); got != tt.want {
				t.Errorf("IsOriginResSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainerResponse_ProxyDone(t *testing.T) {
	type fields struct {
		Resources     Resources
		ContainerEnvs map[string]string
	}
	type args struct {
		resp *runtimeapi.ContainerResourceHookResponse
	}
	type wants struct {
		CPUSet      *string
		CPUShares   *int64
		CFSQuota    *int64
		MemoryLimit *int64
		CPUBvt      *int64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		wants  wants
	}{
		{
			name: "origin resource is not nil",
			fields: fields{
				Resources: Resources{
					CPUShares:   pointer.Int64(15),
					CFSQuota:    pointer.Int64(1000),
					CPUSet:      pointer.String("0,1,2"),
					MemoryLimit: pointer.Int64(1048576),
					CPUBvt:      pointer.Int64(10),
				},
				ContainerEnvs: make(map[string]string, 0),
			},
			args: args{
				resp: &runtimeapi.ContainerResourceHookResponse{
					ContainerResources: nil,
				},
			},
			wants: wants{
				CPUSet:      pointer.String("0,1,2"),
				CPUShares:   pointer.Int64(15),
				CFSQuota:    pointer.Int64(1000),
				MemoryLimit: pointer.Int64(1048576),
				CPUBvt:      pointer.Int64(10),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ContainerResponse{
				Resources:        tt.fields.Resources,
				AddContainerEnvs: tt.fields.ContainerEnvs,
			}
			c.ProxyDone(tt.args.resp)
			assert.Equal(t, tt.wants.CPUSet, c.Resources.CPUSet, "cpu set equal")
			assert.Equal(t, tt.wants.CPUBvt, c.Resources.CPUBvt, "cpu bvt equal")
			assert.Equal(t, tt.wants.CPUShares, c.Resources.CPUShares, "cpu shares equal")
			assert.Equal(t, tt.wants.CFSQuota, c.Resources.CFSQuota, "cfs quota equal")
			assert.Equal(t, tt.wants.MemoryLimit, c.Resources.MemoryLimit, "memory limit equal")
		})
	}
}

func TestPodResponse_ProxyDone(t *testing.T) {
	type fields struct {
		Resources Resources
	}
	type args struct {
		resp *runtimeapi.PodSandboxHookResponse
	}
	type wants struct {
		CPUSet *string
	}
	var tests = []struct {
		name   string
		fields fields
		args   args
		wants  wants
	}{
		{
			name: "origin response resource is nil",
			fields: fields{
				Resources: Resources{
					CPUSet: pointer.String("0,1,2"),
				},
			},
			args: args{
				resp: &runtimeapi.PodSandboxHookResponse{
					Resources: nil,
				},
			},
			wants: wants{
				CPUSet: pointer.String("0,1,2"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodResponse{
				Resources: tt.fields.Resources,
			}
			p.ProxyDone(tt.args.resp)
			assert.Equal(t, tt.wants.CPUSet, p.Resources.CPUSet, "cpu set equal")
		})
	}
}

func TestPodResponse_ReconcilerDone(t *testing.T) {

	type wants struct {
		CPUSet      *string
		CPUShares   *int64
		CFSQuota    *int64
		MemoryLimit *int64
		CPUBvt      *int64
	}
	var tests = []struct {
		name  string
		ex    *resourceexecutor.ResourceUpdateExecutorImpl
		req   ContainerRequest
		res   ContainerResponse
		wants wants
	}{
		{
			name: "req's parent is nil",
			ex: &resourceexecutor.ResourceUpdateExecutorImpl{
				LeveledUpdateLock: sync.Mutex{},
				ResourceCache:     nil,
				Config:            nil,
			},
			req: ContainerRequest{},
		},
		{
			name: "test injectForExt nil",
			ex: &resourceexecutor.ResourceUpdateExecutorImpl{
				LeveledUpdateLock: sync.Mutex{},
				ResourceCache:     nil,
				Config:            nil,
			},
			req: ContainerRequest{
				CgroupParent: "test",
			},
			res: ContainerResponse{
				Resources:        Resources{},
				AddContainerEnvs: nil,
			},
		},
		{
			name: "test injectForExt",
			ex: &resourceexecutor.ResourceUpdateExecutorImpl{
				LeveledUpdateLock: sync.Mutex{},
				ResourceCache:     nil,
				Config:            nil,
			},
			req: ContainerRequest{
				CgroupParent: "test",
			},
			res: ContainerResponse{
				Resources: Resources{
					CPUSet:      pointer.String(""),
					CPUShares:   pointer.Int64(15),
					CFSQuota:    pointer.Int64(1000),
					MemoryLimit: pointer.Int64(1048576),
					CPUBvt:      pointer.Int64(10),
				},
				AddContainerEnvs: nil,
			},
			wants: wants{
				CPUSet:      pointer.String(""),
				CPUShares:   pointer.Int64(15),
				CFSQuota:    pointer.Int64(1000),
				MemoryLimit: pointer.Int64(1048576),
				CPUBvt:      pointer.Int64(10),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			c := ContainerContext{
				Request:  tt.req,
				Response: tt.res,
				executor: nil,
			}
			c.ReconcilerDone(tt.ex)

		})
	}
}

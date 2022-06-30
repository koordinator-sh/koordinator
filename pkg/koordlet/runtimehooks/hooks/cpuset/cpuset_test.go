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

package cpuset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func initCPUSet(dirWithKube string, value string, helper *system.FileTestUtil) {
	helper.WriteCgroupFileContents(dirWithKube, system.CPUSet, value)
}

func getCPUSet(dirWithKube string, helper *system.FileTestUtil) string {
	return helper.ReadCgroupFileContents(dirWithKube, system.CPUSet)
}

func Test_cpusetPlugin_SetContainerCPUSet(t *testing.T) {
	type fields struct {
		rule *cpusetRule
	}
	type args struct {
		podAlloc *ext.ResourceStatus
		proto    protocol.HooksProtocol
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		wantErr    bool
		wantCPUSet *string
	}{
		{
			name: "set cpu with nil protocol",
			fields: fields{
				rule: nil,
			},
			args: args{
				proto: nil,
			},
			wantErr:    true,
			wantCPUSet: nil,
		},
		{
			name: "set cpu by bad pod allocated format",
			fields: fields{
				rule: nil,
			},
			args: args{
				proto: &protocol.ContainerContext{
					Request: protocol.ContainerRequest{
						CgroupParent: "kubepods/test-pod/test-container/",
						PodAnnotations: map[string]string{
							ext.AnnotationResourceStatus: "bad-format",
						},
					},
				},
			},
			wantErr:    true,
			wantCPUSet: nil,
		},
		{
			name: "set cpu by pod allocated",
			fields: fields{
				rule: nil,
			},
			args: args{
				podAlloc: &ext.ResourceStatus{
					CPUSet: "2-4",
				},
				proto: &protocol.ContainerContext{
					Request: protocol.ContainerRequest{
						CgroupParent: "kubepods/test-pod/test-container/",
					},
				},
			},
			wantErr:    false,
			wantCPUSet: pointer.StringPtr("2-4"),
		},
		{
			name: "set cpu by pod allocated share pool with nil rule",
			fields: fields{
				rule: nil,
			},
			args: args{
				podAlloc: &ext.ResourceStatus{
					CPUSharedPools: []ext.CPUSharedPool{
						{
							Socket: 0,
							Node:   0,
						},
					},
				},
				proto: &protocol.ContainerContext{
					Request: protocol.ContainerRequest{
						CgroupParent: "kubepods/test-pod/test-container/",
					},
				},
			},
			wantErr:    false,
			wantCPUSet: nil,
		},
		{
			name: "set cpu by pod allocated share pool",
			fields: fields{
				rule: &cpusetRule{
					sharePools: []ext.CPUSharedPool{
						{
							Socket: 0,
							Node:   0,
							CPUSet: "0-7",
						},
						{
							Socket: 1,
							Node:   0,
							CPUSet: "8-15",
						},
					},
				},
			},
			args: args{
				podAlloc: &ext.ResourceStatus{
					CPUSharedPools: []ext.CPUSharedPool{
						{
							Socket: 0,
							Node:   0,
						},
					},
				},
				proto: &protocol.ContainerContext{
					Request: protocol.ContainerRequest{
						CgroupParent: "kubepods/test-pod/test-container/",
					},
				},
			},
			wantErr:    false,
			wantCPUSet: pointer.StringPtr("0-7"),
		},
		{
			name: "set cpu for origin besteffort pod",
			fields: fields{
				rule: &cpusetRule{
					sharePools: []ext.CPUSharedPool{
						{
							Socket: 0,
							Node:   0,
							CPUSet: "0-7",
						},
						{
							Socket: 1,
							Node:   0,
							CPUSet: "8-15",
						},
					},
				},
			},
			args: args{
				proto: &protocol.ContainerContext{
					Request: protocol.ContainerRequest{
						CgroupParent: "kubepods/besteffort/test-pod/test-container/",
					},
				},
			},
			wantErr:    false,
			wantCPUSet: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)
			var containerCtx *protocol.ContainerContext

			p := &cpusetPlugin{
				rule: tt.fields.rule,
			}
			if tt.args.proto != nil {
				containerCtx = tt.args.proto.(*protocol.ContainerContext)
				initCPUSet(containerCtx.Request.CgroupParent, "", testHelper)
				if tt.args.podAlloc != nil {
					podAllocJson := util.DumpJSON(tt.args.podAlloc)
					containerCtx.Request.PodAnnotations = map[string]string{
						ext.AnnotationResourceStatus: podAllocJson,
					}
				}
			}

			err := p.SetContainerCPUSet(containerCtx)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetContainerCPUSet() error = %v, wantErr %v", err, tt.wantErr)
			}

			if containerCtx == nil {
				return
			}
			if tt.wantCPUSet == nil {
				assert.Nil(t, containerCtx.Response.Resources.CPUSet, "cpuset value should be nil")
			} else {
				containerCtx.ReconcilerDone()
				assert.Equal(t, *tt.wantCPUSet, *containerCtx.Response.Resources.CPUSet, "container cpuset should be equal")
				gotCPUSet := getCPUSet(containerCtx.Request.CgroupParent, testHelper)
				assert.Equal(t, *tt.wantCPUSet, gotCPUSet, "container cpuset should be equal")
			}
		})
	}
}

func Test_getCPUSetFromPod(t *testing.T) {
	type args struct {
		podAnnotations map[string]string
		podAlloc       *ext.ResourceStatus
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
				podAlloc: &ext.ResourceStatus{
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
				podAllocJson := util.DumpJSON(tt.args.podAlloc)
				tt.args.podAnnotations[ext.AnnotationResourceStatus] = podAllocJson
			}
			got, err := getCPUSetFromPod(tt.args.podAnnotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("getCPUSetFromPod() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getCPUSetFromPod() got = %v, want %v", got, tt.want)
			}
		})
	}
}

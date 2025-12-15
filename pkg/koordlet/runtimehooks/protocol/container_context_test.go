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
	"encoding/json"
	"reflect"
	"testing"

	"github.com/containerd/nri/pkg/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestContainerContext_FromNri(t *testing.T) {
	type fields struct {
		Request  ContainerRequest
		Response ContainerResponse
		executor resourceexecutor.ResourceUpdateExecutor
	}
	type args struct {
		pod       *api.PodSandbox
		container *api.Container
	}
	testSpec := &extension.ExtendedResourceSpec{
		Containers: map[string]extension.ExtendedResourceContainerSpec{
			"test-container-1": {
				Requests: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("1Gi"),
				},
				Limits: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("1Gi"),
				},
			},
			"test-container-2": {
				Requests: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("2Gi"),
				},
				Limits: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	testBytes, _ := json.Marshal(testSpec)
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name:   "Update Container Context FromNri and GetExtendedResourceSpec failed",
			fields: fields{},
			args: args{
				pod: &api.PodSandbox{
					Linux: &api.LinuxPodSandbox{},
					Annotations: map[string]string{
						"node.koordinator.sh/extended-resource-spec": "test",
					},
				},
				container: &api.Container{
					Env: []string{"test=test"},
				},
			},
		},
		{
			name:   "Update Container Context FromNri and GetExtendedResourceSpec success",
			fields: fields{},
			args: args{
				pod: &api.PodSandbox{
					Linux: &api.LinuxPodSandbox{},
					Annotations: map[string]string{
						"node.koordinator.sh/extended-resource-spec": string(testBytes),
					},
				},
				container: &api.Container{
					Name: "test-container-1",
					Env:  []string{"test=test"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ContainerContext{
				Request:  tt.fields.Request,
				Response: tt.fields.Response,
				executor: tt.fields.executor,
			}
			c.FromNri(tt.args.pod, tt.args.container)
		})
	}
}

func TestContainerContext_NriDone(t *testing.T) {
	type fields struct {
		Request  ContainerRequest
		Response ContainerResponse
		executor resourceexecutor.ResourceUpdateExecutor
	}
	tests := []struct {
		name    string
		fields  fields
		want    *api.ContainerAdjustment
		want1   *api.ContainerUpdate
		wantErr bool
	}{
		{
			name: "NriDone success with resources",
			fields: fields{
				Response: ContainerResponse{
					Resources: Resources{
						CPUShares:   ptr.To[int64](1024 * 500 / 1000),
						CFSQuota:    ptr.To[int64](1024 * 500 / 1000),
						CPUSet:      ptr.To[string]("0,1,2"),
						MemoryLimit: ptr.To[int64](2 * 1024 * 1024 * 1024),
					},
					AddContainerEnvs: map[string]string{"test": "test"},
				},
				executor: resourceexecutor.NewTestResourceExecutor(),
			},
			want: &api.ContainerAdjustment{
				Linux: &api.LinuxContainerAdjustment{
					Resources: &api.LinuxResources{
						Memory: &api.LinuxMemory{
							Limit: &api.OptionalInt64{
								Value: 2147483648,
							},
						},
						Cpu: &api.LinuxCPU{
							Shares: &api.OptionalUInt64{
								Value: 512,
							},
							Quota: &api.OptionalInt64{
								Value: 512,
							},
							Cpus: "0,1,2",
						},
					},
				},
			},
			want1: &api.ContainerUpdate{
				Linux: &api.LinuxContainerUpdate{
					Resources: &api.LinuxResources{
						Memory: &api.LinuxMemory{
							Limit: &api.OptionalInt64{
								Value: 2147483648,
							},
						},
						Cpu: &api.LinuxCPU{
							Shares: &api.OptionalUInt64{
								Value: 512,
							},
							Quota: &api.OptionalInt64{
								Value: 512,
							},
							Cpus: "0,1,2",
						},
					},
				},
				IgnoreFailure: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ContainerContext{
				Request:  tt.fields.Request,
				Response: tt.fields.Response,
				executor: tt.fields.executor,
			}
			got, got1, err := c.NriDone(tt.fields.executor)
			if (err != nil) != tt.wantErr {
				t.Errorf("Protocol2NRI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != nil && !reflect.DeepEqual(got.Linux, tt.want.Linux) {
				t.Errorf("Protocol2NRI() got = %v, want %v", got1, tt.want1)
			}

			if got != nil && got.Env != nil && got.Env[0].GetKey() != "test" && got.Env[0].GetValue() != "test" {
				t.Errorf("Protocol2NRI() got env = %v, want env = test: test", got.Env[0])
			}
			if !reflect.DeepEqual(got1, tt.want1) {
				t.Errorf("Protocol2NRI() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func Test_getContainerID(t *testing.T) {
	type args struct {
		podAnnotations           map[string]string
		containerUID             string
		systemConfDefaultRuntime string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "default is containerd",
			args: args{
				podAnnotations:           nil,
				containerUID:             "testContainerID",
				systemConfDefaultRuntime: system.RuntimeTypeContainerd,
			},
			want: "containerd://testContainerID",
		},
		{
			name: "default is pouch",
			args: args{
				podAnnotations:           nil,
				containerUID:             "testContainerID",
				systemConfDefaultRuntime: system.RuntimeTypePouch,
			},
			want: "pouch://testContainerID",
		},
		{
			name: "default is docker",
			args: args{
				podAnnotations:           nil,
				containerUID:             "testContainerID",
				systemConfDefaultRuntime: system.RuntimeTypeDocker,
			},
			want: "docker://testContainerID",
		},
		{
			name: "docker in pod annotation",
			args: args{
				podAnnotations: map[string]string{
					"io.kubernetes.docker.type": "true",
				},
				containerUID:             "testContainerID",
				systemConfDefaultRuntime: system.RuntimeTypePouch,
			},
			want: "docker://testContainerID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system.Conf = &system.Config{
				DefaultRuntimeType: tt.args.systemConfDefaultRuntime,
			}
			if got := getContainerID(tt.args.podAnnotations, tt.args.containerUID); got != tt.want {
				t.Errorf("getContainerID() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
	"testing"

	"github.com/containerd/nri/pkg/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
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

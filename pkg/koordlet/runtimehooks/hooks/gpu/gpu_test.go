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

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
)

func Test_InjectContainerGPUEnv(t *testing.T) {
	tests := []struct {
		name             string
		expectedAllocStr string
		expectedError    bool
		proto            protocol.HooksProtocol
		expectedMounts   []*protocol.Mount
		expectedEnvs     map[string]string
	}{
		{
			name:             "test empty proto",
			expectedAllocStr: "",
			expectedError:    true,
			proto:            nil,
		},
		{
			name:             "test normal gpu alloc",
			expectedAllocStr: "0,1",
			expectedError:    false,
			proto: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodAnnotations: map[string]string{
						ext.AnnotationDeviceAllocated: "{\"gpu\": [{\"minor\": 0},{\"minor\": 1}]}",
					},
				},
			},
			expectedEnvs: map[string]string{GpuAllocEnv: "0,1"},
		},
		{
			name:             "test empty gpu alloc",
			expectedAllocStr: "",
			expectedError:    false,
			proto: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodAnnotations: map[string]string{
						ext.AnnotationDeviceAllocated: "{\"fpga\": [{\"minor\": 0},{\"minor\": 1}]}",
					},
				},
			},
		},
		{
			name:             "gpu share with HAMi",
			expectedAllocStr: "1",
			expectedError:    false,
			proto: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodLabels: map[string]string{
						ext.LabelGPUIsolationProvider: string(ext.GPUIsolationProviderHAMICore),
					},
					PodAnnotations: map[string]string{
						ext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"50","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
				},
			},
			expectedEnvs: map[string]string{
				GpuAllocEnv:                "1",
				"CUDA_DEVICE_MEMORY_LIMIT": "17179869184",
				"CUDA_DEVICE_SM_LIMIT":     "50",
				"LD_PRELOAD":               "/usr/local/vgpu/libvgpu.so",
			},
			expectedMounts: []*protocol.Mount{
				{
					Destination: "/usr/local/vgpu/libvgpu.so",
					Type:        "bind",
					Source:      "/usr/local/vgpu/libvgpu.so",
					Options:     []string{"rbind"},
				},
				{
					Destination: "/tmp/vgpulock",
					Type:        "bind",
					Source:      "/tmp/vgpulock",
					Options:     []string{"rbind"},
				},
			},
		},
	}
	plugin := gpuPlugin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var containerCtx *protocol.ContainerContext
			if tt.proto != nil {
				containerCtx = tt.proto.(*protocol.ContainerContext)
			}
			err := plugin.InjectContainerGPUEnv(containerCtx)
			assert.Equal(t, tt.expectedError, err != nil, tt.name)
			if tt.proto != nil {
				containerCtx := tt.proto.(*protocol.ContainerContext)
				assert.Equal(t, containerCtx.Response.AddContainerEnvs[GpuAllocEnv], tt.expectedAllocStr, tt.name)
				assert.Equal(t, containerCtx.Response.AddContainerEnvs, tt.expectedEnvs, tt.name)
				assert.Equal(t, containerCtx.Response.AddContainerMounts, tt.expectedMounts, tt.name)
			}
		})
	}
}

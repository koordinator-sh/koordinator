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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
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
					PodMeta: protocol.PodMeta{
						UID: "pod-uid",
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "container-name",
					},
					PodLabels: map[string]string{
						ext.LabelGPUIsolationProvider: string(ext.GPUIsolationProviderHAMICore),
					},
					PodAnnotations: map[string]string{
						ext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"50","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
					},
				},
			},
			expectedEnvs: map[string]string{
				GpuAllocEnv:                       "1",
				"CUDA_DEVICE_MEMORY_LIMIT":        "17179869184",
				"CUDA_DEVICE_SM_LIMIT":            "50",
				"LD_PRELOAD":                      "/usr/local/vgpu/libvgpu.so",
				"CUDA_DEVICE_MEMORY_SHARED_CACHE": "/usr/local/vgpu/pod-uid_container-name.cache",
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
				{
					Destination: "/usr/local/vgpu",
					Type:        "bind",
					Source:      "/usr/local/vgpu/containers/pod-uid_container-name",
					Options:     []string{"rbind"},
				},
			},
		},
	}
	plugin := gpuPlugin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFeatureGates := map[string]bool{string(features.HamiCoreVGPUMonitor): true}
			err := features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
			assert.NoError(t, err)
			defer func() {
				err = features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
				assert.NoError(t, err)
			}()

			var containerCtx *protocol.ContainerContext
			if tt.proto != nil {
				containerCtx = tt.proto.(*protocol.ContainerContext)
			}
			err = plugin.InjectContainerGPUEnv(containerCtx)
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

func Test_injectGPUEnv(t *testing.T) {
	tests := []struct {
		name         string
		devices      []*ext.DeviceAllocation
		expectedEnvs map[string]string
	}{
		{
			name: "single gpu",
			devices: []*ext.DeviceAllocation{
				{Minor: 0},
			},
			expectedEnvs: map[string]string{GpuAllocEnv: "0"},
		},
		{
			name: "multiple gpus",
			devices: []*ext.DeviceAllocation{
				{Minor: 0},
				{Minor: 1},
				{Minor: 3},
			},
			expectedEnvs: map[string]string{GpuAllocEnv: "0,1,3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.Response.AddContainerEnvs = make(map[string]string)
			injectGPUEnv(containerCtx, tt.devices)
			assert.Equal(t, tt.expectedEnvs, containerCtx.Response.AddContainerEnvs)
		})
	}
}

func mockGetDeviceNumbers(path string) ([]int64, error) {
	// Simulate real device numbers for known NVIDIA device paths.
	knownDevices := map[string][]int64{
		"/dev/nvidia0":          {195, 0},
		"/dev/nvidia1":          {195, 1},
		"/dev/nvidiactl":        {195, 255},
		"/dev/nvidia-uvm":       {510, 0},
		"/dev/nvidia-uvm-tools": {510, 1},
	}
	if info, ok := knownDevices[path]; ok {
		return info, nil
	}
	return nil, fmt.Errorf("device not found: %s", path)
}

func Test_injectGPUDevices(t *testing.T) {
	original := getDeviceNumbers
	getDeviceNumbers = mockGetDeviceNumbers
	defer func() { getDeviceNumbers = original }()

	tests := []struct {
		name            string
		devices         []*ext.DeviceAllocation
		expectedDevices []*protocol.LinuxDevice
	}{
		{
			name: "single gpu",
			devices: []*ext.DeviceAllocation{
				{Minor: 0},
			},
			expectedDevices: []*protocol.LinuxDevice{
				{Path: "/dev/nvidia0", Type: "c", Major: 195, Minor: 0, FileModeValue: 0666},
				{Path: nvidiaCtlDevice, Type: "c", Major: 195, Minor: 255, FileModeValue: 0666},
				{Path: nvidiaUVMDevice, Type: "c", Major: 510, Minor: 0, FileModeValue: 0666},
				{Path: nvidiaUVMToolsDevice, Type: "c", Major: 510, Minor: 1, FileModeValue: 0666},
			},
		},
		{
			name: "multiple gpus",
			devices: []*ext.DeviceAllocation{
				{Minor: 0},
				{Minor: 1},
			},
			expectedDevices: []*protocol.LinuxDevice{
				{Path: "/dev/nvidia0", Type: "c", Major: 195, Minor: 0, FileModeValue: 0666},
				{Path: "/dev/nvidia1", Type: "c", Major: 195, Minor: 1, FileModeValue: 0666},
				{Path: nvidiaCtlDevice, Type: "c", Major: 195, Minor: 255, FileModeValue: 0666},
				{Path: nvidiaUVMDevice, Type: "c", Major: 510, Minor: 0, FileModeValue: 0666},
				{Path: nvidiaUVMToolsDevice, Type: "c", Major: 510, Minor: 1, FileModeValue: 0666},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerCtx := &protocol.ContainerContext{}
			err := injectGPUDevices(containerCtx, tt.devices)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedDevices, containerCtx.Response.AddContainerDevices)
		})
	}
}

func Test_injectGPUDevices_deviceNotFound(t *testing.T) {
	original := getDeviceNumbers
	getDeviceNumbers = func(path string) ([]int64, error) {
		return nil, fmt.Errorf("device not found: %s", path)
	}
	defer func() { getDeviceNumbers = original }()

	containerCtx := &protocol.ContainerContext{}
	err := injectGPUDevices(containerCtx, []*ext.DeviceAllocation{{Minor: 0}})
	assert.NoError(t, err)
	assert.Nil(t, containerCtx.Response.AddContainerDevices)
}

func Test_injectGPUDevices_partialFailure(t *testing.T) {
	original := getDeviceNumbers
	getDeviceNumbers = func(path string) ([]int64, error) {
		// GPU0 exists, GPU1 does not.
		if path == "/dev/nvidia0" {
			return []int64{195, 0}, nil
		}
		return nil, fmt.Errorf("device not found: %s", path)
	}
	defer func() { getDeviceNumbers = original }()

	containerCtx := &protocol.ContainerContext{}
	err := injectGPUDevices(containerCtx, []*ext.DeviceAllocation{{Minor: 0}, {Minor: 1}})
	assert.NoError(t, err)
	// On partial failure, no devices should be injected (atomic all-or-nothing).
	assert.Nil(t, containerCtx.Response.AddContainerDevices)
}

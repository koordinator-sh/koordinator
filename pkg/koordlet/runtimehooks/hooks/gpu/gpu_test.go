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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_InjectContainerGPUEnv(t *testing.T) {
	// Point the HAMi-core library path at a temp dir so the hook can create the
	// host ld.so.preload file and the vgpu monitor cache dir hermetically, without
	// depending on /usr/local/vgpu being writable on the test host.
	tmpDir := t.TempDir()
	lib := filepath.Join(tmpDir, "libvgpu.so")
	preloadHostPath := filepath.Join(tmpDir, "ld.so.preload")
	cacheHostDir := filepath.Join(tmpDir, "containers", "pod-uid_container-name")
	origConf := *system.Conf
	defer system.SetConf(origConf)
	testConf := origConf
	testConf.HAMICoreLibraryDirectoryPath = lib
	system.SetConf(testConf)

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
				"CUDA_DEVICE_MEMORY_SHARED_CACHE": tmpDir + "/pod-uid_container-name.cache",
			},
			expectedMounts: []*protocol.Mount{
				{
					Destination: "/etc/ld.so.preload",
					Type:        "bind",
					Source:      preloadHostPath,
					Options:     []string{"rbind", "ro"},
				},
				{
					Destination: lib,
					Type:        "bind",
					Source:      lib,
					Options:     []string{"rbind", "ro"},
				},
				{
					Destination: "/tmp/vgpulock",
					Type:        "bind",
					Source:      "/tmp/vgpulock",
					Options:     []string{"rbind"},
				},
				{
					Destination: tmpDir,
					Type:        "bind",
					Source:      cacheHostDir,
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

func Test_ensureHAMICorePreloadFile(t *testing.T) {
	t.Run("create when missing", func(t *testing.T) {
		tmpDir := t.TempDir()
		lib := filepath.Join(tmpDir, "libvgpu.so")
		hostPath, err := ensureHAMICorePreloadFile(lib)
		assert.NoError(t, err)
		assert.Equal(t, filepath.Join(tmpDir, "ld.so.preload"), hostPath)
		content, err := os.ReadFile(hostPath)
		assert.NoError(t, err)
		assert.Equal(t, lib+"\n", string(content))
	})

	t.Run("skip rewrite when content matches", func(t *testing.T) {
		tmpDir := t.TempDir()
		lib := filepath.Join(tmpDir, "libvgpu.so")
		preloadPath := filepath.Join(tmpDir, "ld.so.preload")
		assert.NoError(t, os.WriteFile(preloadPath, []byte(lib+"\n"), 0644))
		// Make the file unwritable so any attempt to rewrite it would fail;
		// a successful return proves the idempotent early-return was taken.
		if os.Geteuid() != 0 {
			assert.NoError(t, os.Chmod(preloadPath, 0444))
		}
		hostPath, err := ensureHAMICorePreloadFile(lib)
		assert.NoError(t, err)
		assert.Equal(t, preloadPath, hostPath)
	})

	t.Run("rewrite when content differs", func(t *testing.T) {
		tmpDir := t.TempDir()
		lib := filepath.Join(tmpDir, "libvgpu.so")
		preloadPath := filepath.Join(tmpDir, "ld.so.preload")
		assert.NoError(t, os.WriteFile(preloadPath, []byte("/stale/path/libvgpu.so\n"), 0644))
		hostPath, err := ensureHAMICorePreloadFile(lib)
		assert.NoError(t, err)
		content, err := os.ReadFile(hostPath)
		assert.NoError(t, err)
		assert.Equal(t, lib+"\n", string(content))
	})

	t.Run("fail when parent dir is not a directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		blocker := filepath.Join(tmpDir, "blocker")
		assert.NoError(t, os.WriteFile(blocker, nil, 0644))
		_, err := ensureHAMICorePreloadFile(filepath.Join(blocker, "sub", "libvgpu.so"))
		assert.Error(t, err)
	})

	t.Run("fail when dir is not writable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root, permission bits are not enforced")
		}
		tmpDir := t.TempDir()
		readonlyDir := filepath.Join(tmpDir, "readonly")
		assert.NoError(t, os.MkdirAll(readonlyDir, 0555))
		_, err := ensureHAMICorePreloadFile(filepath.Join(readonlyDir, "libvgpu.so"))
		assert.Error(t, err)
	})
}

func Test_InjectContainerGPUEnv_PreloadFailClosed(t *testing.T) {
	tmpDir := t.TempDir()
	blocker := filepath.Join(tmpDir, "blocker")
	assert.NoError(t, os.WriteFile(blocker, nil, 0644))
	origConf := *system.Conf
	defer system.SetConf(origConf)
	testConf := origConf
	// The parent of the library path is a regular file, so the preload file
	// cannot be created and the hook must fail closed.
	testConf.HAMICoreLibraryDirectoryPath = filepath.Join(blocker, "sub", "libvgpu.so")
	system.SetConf(testConf)

	plugin := gpuPlugin{}
	containerCtx := &protocol.ContainerContext{
		Request: protocol.ContainerRequest{
			PodLabels: map[string]string{
				ext.LabelGPUIsolationProvider: string(ext.GPUIsolationProviderHAMICore),
			},
			PodAnnotations: map[string]string{
				ext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"50","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"50"}}]}`,
			},
		},
	}
	err := plugin.InjectContainerGPUEnv(containerCtx)
	assert.Error(t, err)
	// A failed hook must not leave a half-injected container behind: neither
	// the preload mount nor the LD_PRELOAD-style silent fallback env.
	assert.Empty(t, containerCtx.Response.AddContainerMounts)
}

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

package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_NewDsModeConfig(t *testing.T) {
	expectConfig := &Config{
		CgroupRootDir:                "/host-cgroup/",
		ProcRootDir:                  "/proc/",
		SysRootDir:                   "/host-sys/",
		SysFSRootDir:                 "/host-sys-fs/",
		VarRunRootDir:                "/host-var-run/",
		VarLibKubeletRootDir:         "/var/lib/kubelet/",
		RunRootDir:                   "/host-run/",
		RuntimeHooksConfigDir:        "/host-etc-hookserver/",
		DefaultRuntimeType:           "containerd",
		HAMICoreLibraryDirectoryPath: "/usr/local/vgpu/libvgpu.so",
	}
	defaultConfig := NewDsModeConfig()
	assert.Equal(t, expectConfig, defaultConfig)
}

func Test_NewHostModeConfig(t *testing.T) {
	expectConfig := &Config{
		CgroupRootDir:                "/sys/fs/cgroup/",
		ProcRootDir:                  "/proc/",
		SysRootDir:                   "/sys/",
		SysFSRootDir:                 "/sys/fs/",
		VarRunRootDir:                "/var/run/",
		VarLibKubeletRootDir:         "/var/lib/kubelet/",
		RunRootDir:                   "/run/",
		RuntimeHooksConfigDir:        "/etc/runtime/hookserver.d",
		DefaultRuntimeType:           "containerd",
		HAMICoreLibraryDirectoryPath: "/usr/local/vgpu/libvgpu.so",
	}
	defaultConfig := NewHostModeConfig()
	assert.Equal(t, expectConfig, defaultConfig)
}

func Test_InitFlags(t *testing.T) {
	t.Run("", func(t *testing.T) {
		cfg := NewDsModeConfig()
		assert.NotNil(t, cfg)
	})
}

func Test_InitSupportConfigs(t *testing.T) {
	type fields struct {
		prepareFn func(helper *FileTestUtil)
	}
	type expects struct {
		hostSystemInfo VersionInfo
	}
	tests := []struct {
		name    string
		fields  fields
		expects expects
	}{
		{
			name: "not anolis os, not support resctrl",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {},
			},
			expects: expects{
				hostSystemInfo: VersionInfo{
					IsAnolisOS: false,
				},
			},
		},
		{
			name: "anolis os, not support resctrl",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					bvtResource, _ := GetCgroupResource(CPUBVTWarpNsName)
					helper.WriteCgroupFileContents("", bvtResource, "0")
				},
			},
			expects: expects{
				hostSystemInfo: VersionInfo{
					IsAnolisOS: true,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			InitSupportConfigs()
			assert.Equal(t, tt.expects.hostSystemInfo, HostSystemInfo)
		})
	}
}

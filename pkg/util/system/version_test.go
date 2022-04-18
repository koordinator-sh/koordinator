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
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAliOS(t *testing.T) {
	tests := []struct {
		name       string
		fs         string
		cgroupFile string
		expect     bool
	}{
		{
			name:       "alios_bvt_test",
			fs:         CgroupCPUDir,
			cgroupFile: path.Join(CgroupCPUDir, CPUBVTWarpNsName),
			expect:     true,
		},
		{
			name:       "alios_wmark_ratio_systemd_test",
			fs:         CgroupMemDir,
			cgroupFile: path.Join(CgroupMemDir, KubeRootNameSystemd, MemWmarkRatioFileName),
			expect:     true,
		},
		{
			name:       "alios_wmark_ratio_cgroupfs_test",
			fs:         CgroupMemDir,
			cgroupFile: path.Join(CgroupMemDir, KubeRootNameCgroupfs, MemWmarkRatioFileName),
			expect:     true,
		},
		{
			name:       "not_alios_test",
			fs:         CgroupCPUDir,
			cgroupFile: path.Join(CgroupCPUDir, CPUCFSQuotaName),
			expect:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.MkDirAll(tt.fs)
			helper.CreateFile(tt.cgroupFile)
			assert.Equal(t, tt.expect, isSupportBvtOrWmarRatio())

		})
	}

}

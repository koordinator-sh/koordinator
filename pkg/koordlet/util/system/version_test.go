package system

import (
	"github.com/stretchr/testify/assert"
	"path"
	"testing"
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

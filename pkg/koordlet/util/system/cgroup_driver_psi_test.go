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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// psiSetSubfsExpect resets a shared package-level PSI resource's Subfs to a known value before
// the test runs and restores it afterward, so its mutation does not leak across subtests.
func psiSetSubfsExpect(t *testing.T, res *CgroupResource, resetSubfs string) {
	t.Helper()
	original := res.Subfs
	res.Subfs = resetSubfs
	t.Cleanup(func() { res.Subfs = original })
}

// TestSetupCgroupV1PSIPathSubsysAutoDetect exercises SetupCgroupV1PSIPathSubsysAutoDetect
// against a temporary cgroup v1 filesystem (Conf.CgroupRootDir is pointed at the temp dir by
// NewFileTestUtil, and the kubepods parent dir comes from SetupCgroupPathFormatter).
func TestSetupCgroupV1PSIPathSubsysAutoDetect(t *testing.T) {
	pressureFileNames := []string{CPUAcctCPUPressureName, CPUAcctMemoryPressureName, CPUAcctIOPressureName}

	// candidate subsystems in the exact probe priority order used by the production function.
	candidateSubsys := []string{CgroupCPUAcctDir, CgroupCPUDir, CgroupMemDir, CgroupBlkioDir}

	// createPressureFile writes <cgroupRoot>/<subsys>/kubepods/<pressureName>.
	createPressureFile := func(cgroupRoot, subsys, pressureName string) {
		dir := filepath.Join(cgroupRoot, subsys, KubeRootNameCgroupfs)
		assert.NoError(t, os.MkdirAll(dir, 0755))
		assert.NoError(t, os.WriteFile(filepath.Join(dir, pressureName), []byte("some 0 0"), 0644))
	}

	tests := []struct {
		name       string
		setup      func(cgroupRoot string)
		wantCPU    string
		wantMemory string
		wantIO     string
	}{
		{
			name: "alinux default layout, all pressure files under cpuacct",
			setup: func(cgroupRoot string) {
				for _, n := range pressureFileNames {
					createPressureFile(cgroupRoot, CgroupCPUAcctDir, n)
				}
			},
			wantCPU:    CgroupCPUAcctDir,
			wantMemory: CgroupCPUAcctDir,
			wantIO:     CgroupCPUAcctDir,
		},
		{
			name: "tencentos split layout with per-controller pressure files",
			setup: func(cgroupRoot string) {
				createPressureFile(cgroupRoot, CgroupCPUAcctDir, CPUAcctCPUPressureName)
				createPressureFile(cgroupRoot, CgroupMemDir, CPUAcctMemoryPressureName)
				createPressureFile(cgroupRoot, CgroupBlkioDir, CPUAcctIOPressureName)
			},
			wantCPU:    CgroupCPUAcctDir,
			wantMemory: CgroupMemDir,
			wantIO:     CgroupBlkioDir,
		},
		{
			name:       "no pressure files anywhere, all fall back to cpuacct",
			setup:      func(cgroupRoot string) {},
			wantCPU:    CgroupCPUAcctDir,
			wantMemory: CgroupCPUAcctDir,
			wantIO:     CgroupCPUAcctDir,
		},
		{
			name: "multiple candidates hold a pressure file, first in probe order wins",
			setup: func(cgroupRoot string) {
				// cpu.pressure in cpu, memory, blkio -> cpu (second candidate, cpuacct has none) wins
				createPressureFile(cgroupRoot, CgroupCPUDir, CPUAcctCPUPressureName)
				createPressureFile(cgroupRoot, CgroupMemDir, CPUAcctCPUPressureName)
				createPressureFile(cgroupRoot, CgroupBlkioDir, CPUAcctCPUPressureName)
				// memory.pressure in cpuacct and blkio -> cpuacct (first candidate) wins
				createPressureFile(cgroupRoot, CgroupCPUAcctDir, CPUAcctMemoryPressureName)
				createPressureFile(cgroupRoot, CgroupBlkioDir, CPUAcctMemoryPressureName)
				// io.pressure only in blkio -> blkio wins
				createPressureFile(cgroupRoot, CgroupBlkioDir, CPUAcctIOPressureName)
			},
			wantCPU:    CgroupCPUDir,
			wantMemory: CgroupCPUAcctDir,
			wantIO:     CgroupBlkioDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(false)

			// point the probe parentDir at the kubepods cgroup (cgroupfs layout => kubepods/)
			SetupCgroupPathFormatter(Cgroupfs)

			// reset each shared resource to the default before mutating, restore after the test
			psiSetSubfsExpect(t, CPUAcctCPUPressure.(*CgroupResource), CgroupCPUAcctDir)
			psiSetSubfsExpect(t, CPUAcctMemoryPressure.(*CgroupResource), CgroupCPUAcctDir)
			psiSetSubfsExpect(t, CPUAcctIOPressure.(*CgroupResource), CgroupCPUAcctDir)

			tt.setup(helper.TempDir)

			SetupCgroupV1PSIPathSubsysAutoDetect()

			assert.Equal(t, tt.wantCPU, CPUAcctCPUPressure.(*CgroupResource).Subfs)
			assert.Equal(t, tt.wantMemory, CPUAcctMemoryPressure.(*CgroupResource).Subfs)
			assert.Equal(t, tt.wantIO, CPUAcctIOPressure.(*CgroupResource).Subfs)
		})
	}

	// sanity: constant set used across the test matches the production candidate order.
	assert.Equal(t, candidateSubsys, []string{CgroupCPUAcctDir, CgroupCPUDir, CgroupMemDir, CgroupBlkioDir})
}
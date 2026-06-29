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

package cgroup

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestReadCpuMax(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUCFSQuotaV2, "200000 100000")

	got, err := ReadCpuMax("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, &CpuQuota{Quota: 200000, Period: 100000}, got)
}

func TestReadCpuStat(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUStatV2, "usage_usec 3000\nuser_usec 2000\nsystem_usec 1000\nnr_periods 10\nnr_throttled 1\nthrottled_usec 100\n")

	got, err := ReadCpuStat("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, &CpuStat{UsageUsec: 3000, UserUsec: 2000, SystemUsec: 1000}, got)
}

func TestReadAndWriteCpuWeight(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUSharesV2, "100")

	got, err := ReadCpuWeight("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, uint64(100), got)

	err = WriteCpuWeight("/test-pod", 200)
	assert.NoError(t, err)
	assert.Equal(t, "200", helper.ReadCgroupFileContents("/test-pod", sysutil.CPUSharesV2))
}

func TestGetMilliCPU(t *testing.T) {
	tests := []struct {
		name   string
		weight uint64
	}{
		{
			name:   "minimum weight",
			weight: 1,
		},
		{
			name:   "kube default weight",
			weight: 39,
		},
		{
			name:   "maximum weight",
			weight: 10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shares, err := sysutil.ConvertCPUWeightToShares(int64(tt.weight))
			assert.NoError(t, err)

			got := GetMilliCPU(tt.weight)
			assert.Equal(t, sharesToMilliCPU(shares), got)
		})
	}
}

func TestReadAndWriteMemoryResources(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	helper.WriteCgroupFileContents("/test-pod", sysutil.MemoryUsageV2, "4096")
	helper.WriteCgroupFileContents("/test-pod", sysutil.MemoryHighV2, "max")
	helper.WriteCgroupFileContents("/test-pod", sysutil.MemoryMinV2, "1024")

	current, err := ReadMemoryCurrent("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, int64(4096), current)

	high, err := ReadMemoryHigh("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, int64(math.MaxInt64), high)

	min, err := ReadMemoryMin("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, int64(1024), min)

	err = WriteMemoryHigh("/test-pod", 2048)
	assert.NoError(t, err)
	assert.Equal(t, "2048", helper.ReadCgroupFileContents("/test-pod", sysutil.MemoryHighV2))

	err = WriteMemoryMin("/test-pod", 512)
	assert.NoError(t, err)
	assert.Equal(t, "512", helper.ReadCgroupFileContents("/test-pod", sysutil.MemoryMinV2))
}

func TestReadAndWriteIOResources(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	helper.WriteCgroupFileContents("/test-pod", sysutil.IOStatV2, "8:0 rbytes=1024 wbytes=2048 rios=3 wios=4\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.IOMaxV2, "8:0 rbps=max wbps=4096 riops=7 wiops=8\n")

	stat, err := ReadIOStat("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, IOStat{Dev: "8:0", Rbytes: 1024, Wbytes: 2048, Rios: 3, Wios: 4}, stat["8:0"])

	max, err := ReadIOMax("/test-pod")
	assert.NoError(t, err)
	assert.Equal(t, IOMax{Dev: "8:0", Rbps: Max, Wbps: 4096, Riops: 7, Wiops: 8}, max["8:0"])

	err = WriteIOMax("/test-pod", &IOMax{Dev: "8:0", Rbps: 1024, Wbps: 2048, Riops: 3, Wiops: 4})
	assert.NoError(t, err)
	assert.Equal(t, "8:0 rbps=1024 wbps=2048 riops=3 wiops=4", helper.ReadCgroupFileContents("/test-pod", sysutil.IOMaxV2))
}

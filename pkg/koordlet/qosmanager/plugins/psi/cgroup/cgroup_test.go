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
	"testing"

	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestLoadIOSkipsDeviceStatsWhenMultipleDevicesAreUnknown(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	sysutil.CPUAcctCPUPressureV2.WithSupported(true, "supported for test")
	sysutil.CPUAcctMemoryPressureV2.WithSupported(true, "supported for test")
	sysutil.CPUAcctIOPressureV2.WithSupported(true, "supported for test")
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUAcctCPUPressureV2, "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUAcctMemoryPressureV2, "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUAcctIOPressureV2, "some avg10=1.00 avg60=2.00 avg300=3.00 total=1000000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.IOStatV2, "8:0 rbytes=1024 wbytes=2048 rios=3 wios=4\n8:16 rbytes=4096 wbytes=8192 rios=7 wios=8\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.IOMaxV2, "8:0 rbps=4096 wbps=8192 riops=7 wiops=8\n8:16 rbps=4096 wbps=8192 riops=7 wiops=8\n")
	cg := NewCgroup("/test-pod", InvalidDevice)

	err := cg.LoadIO()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cg.Bps.Current().Int64(); got != 0 {
		t.Fatalf("expected unknown device to skip BPS stats, got %d", got)
	}
	if got := cg.Iops.Current().Int64(); got != 0 {
		t.Fatalf("expected unknown device to skip IOPS stats, got %d", got)
	}
	if got := cg.Iops.Pressure10(); got != 1 {
		t.Fatalf("expected IO pressure to be loaded, got %v", got)
	}
}

func TestLoadIOInfersSingleDeviceStats(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	sysutil.CPUAcctCPUPressureV2.WithSupported(true, "supported for test")
	sysutil.CPUAcctMemoryPressureV2.WithSupported(true, "supported for test")
	sysutil.CPUAcctIOPressureV2.WithSupported(true, "supported for test")
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUAcctCPUPressureV2, "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUAcctMemoryPressureV2, "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.CPUAcctIOPressureV2, "some avg10=1.00 avg60=2.00 avg300=3.00 total=1000000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.IOStatV2, "8:0 rbytes=1024 wbytes=2048 rios=3 wios=4\n")
	helper.WriteCgroupFileContents("/test-pod", sysutil.IOMaxV2, "8:0 rbps=4096 wbps=8192 riops=7 wiops=8\n")
	cg := NewCgroup("/test-pod", InvalidDevice)

	err := cg.LoadIO()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cg.Bps.Throttle().Int64(); got != 8192 {
		t.Fatalf("expected BPS throttle from the only device, got %d", got)
	}
	if got := cg.Iops.Throttle().Int64(); got != 8 {
		t.Fatalf("expected IOPS throttle from the only device, got %d", got)
	}
}

func TestLoadReadsPSIOnce(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(true)
	writeLoadAllFiles(helper, "/test-pod")
	reader := &countingPSIReader{
		psi: &sysutil.PSIByResource{
			CPU: psiStats(1),
			Mem: psiStats(2),
			IO:  psiStats(3),
		},
	}
	restore := setCgroupReaderFactoryForTest(func() resourceexecutor.CgroupReader {
		return reader
	})
	defer restore()
	cg := NewCgroup("/test-pod", InvalidDevice)

	err := cg.Load(ResourceMaskAll)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reader.reads != 1 {
		t.Fatalf("expected PSI to be read once, got %d", reader.reads)
	}
	if got := cg.Cpu.Pressure10(); got != 1 {
		t.Fatalf("expected CPU pressure from shared PSI sample, got %v", got)
	}
	if got := cg.Memory.Pressure10(); got != 2 {
		t.Fatalf("expected memory pressure from shared PSI sample, got %v", got)
	}
	if got := cg.Iops.Pressure10(); got != 3 {
		t.Fatalf("expected IO pressure from shared PSI sample, got %v", got)
	}
}

func writeLoadAllFiles(helper *sysutil.FileTestUtil, cgroupPath string) {
	helper.WriteCgroupFileContents(cgroupPath, sysutil.CPUStatV2, "usage_usec 3000\nuser_usec 2000\nsystem_usec 1000\nnr_periods 10\nnr_throttled 1\nthrottled_usec 100\n")
	helper.WriteCgroupFileContents(cgroupPath, sysutil.CPUCFSQuotaV2, "200000 100000")
	helper.WriteCgroupFileContents(cgroupPath, sysutil.CPUSharesV2, "100")
	helper.WriteCgroupFileContents(cgroupPath, sysutil.MemoryUsageV2, "4096")
	helper.WriteCgroupFileContents(cgroupPath, sysutil.MemoryHighV2, "max")
	helper.WriteCgroupFileContents(cgroupPath, sysutil.MemoryMinV2, "1024")
	helper.WriteCgroupFileContents(cgroupPath, sysutil.IOStatV2, "8:0 rbytes=1024 wbytes=2048 rios=3 wios=4\n")
	helper.WriteCgroupFileContents(cgroupPath, sysutil.IOMaxV2, "8:0 rbps=4096 wbps=8192 riops=7 wiops=8\n")
}

func psiStats(avg10 float64) sysutil.PSIStats {
	return sysutil.PSIStats{
		Some: &sysutil.PSILine{Avg10: avg10, Avg60: avg10, Avg300: avg10, Total: 1000000},
		Full: &sysutil.PSILine{},
	}
}

type countingPSIReader struct {
	psi   *sysutil.PSIByResource
	reads int
}

func (c *countingPSIReader) ReadCPUQuota(string) (int64, error)              { return 0, nil }
func (c *countingPSIReader) ReadCPUPeriod(string) (int64, error)             { return 0, nil }
func (c *countingPSIReader) ReadCPUShares(string) (int64, error)             { return 0, nil }
func (c *countingPSIReader) ReadCPUBurst(string) (int64, error)              { return 0, nil }
func (c *countingPSIReader) ReadCPUSet(string) (*cpuset.CPUSet, error)       { return nil, nil }
func (c *countingPSIReader) ReadCPUAcctUsage(string) (uint64, error)         { return 0, nil }
func (c *countingPSIReader) ReadCPUStat(string) (*sysutil.CPUStatRaw, error) { return nil, nil }
func (c *countingPSIReader) ReadMemoryUsage(string) (uint64, error)          { return 0, nil }
func (c *countingPSIReader) ReadMemoryLimit(string) (int64, error)           { return 0, nil }
func (c *countingPSIReader) ReadMemoryStat(string) (*sysutil.MemoryStatRaw, error) {
	return nil, nil
}
func (c *countingPSIReader) ReadMemoryNumaStat(string) ([]sysutil.NumaMemoryPages, error) {
	return nil, nil
}
func (c *countingPSIReader) ReadCPUTasks(string) ([]int32, error)  { return nil, nil }
func (c *countingPSIReader) ReadCPUProcs(string) ([]uint32, error) { return nil, nil }
func (c *countingPSIReader) ReadPSI(string) (*sysutil.PSIByResource, error) {
	c.reads++
	return c.psi, nil
}
func (c *countingPSIReader) ReadMemoryColdPageUsage(string) (uint64, error) { return 0, nil }
func (c *countingPSIReader) ReadNetClsId(string) (uint32, error)            { return 0, nil }

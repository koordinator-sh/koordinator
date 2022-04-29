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
	"math"

	"k8s.io/klog/v2"
)

const (
	// subfs name
	CgroupCPUDir     string = "cpu/"
	CgroupCPUSetDir  string = "cpuset/"
	CgroupCPUacctDir string = "cpuacct/"
	CgroupMemDir     string = "memory/"
)

const (
	CFSBasePeriodValue int64 = 100000

	CPUStatFileName   = "cpu.stat"
	CPUSharesFileName = "cpu.shares"
	CPUCFSQuotaName   = "cpu.cfs_quota_us"
	CPUCFSPeriodName  = "cpu.cfs_period_us"
	CPUBVTWarpNsName  = "cpu.bvt_warp_ns"
	CPUBurstName      = "cpu.cfs_burst_us"
	CPUSFileName      = "cpuset.cpus"

	CpuacctStatFileName = "cpuacct.stat"

	MemWmarkRatioFileName       = "memory.wmark_ratio"
	MemWmarkScaleFactorFileName = "memory.wmark_scale_factor"
	MemPriorityFileName         = "memory.priority"
	MemUsePriorityOomFileName   = "memory.use_priority_oom"
	MemOomGroupFileName         = "memory.oom.group"
	MemWmarkMinAdjFileName      = "memory.wmark_min_adj"
	MemMinFileName              = "memory.min"
	MemLowFileName              = "memory.low"
	MemHighFileName             = "memory.high"
	MemoryLimitFileName         = "memory.limit_in_bytes"
	MemStatFileName             = "memory.stat"
)

var (
	CPUBurstValidator = &RangeValidator{name: CPUBurstName, min: 0, max: 100 * 10 * 100000}

	MemWmarkRatioValidator               = &RangeValidator{name: MemWmarkRatioFileName, min: 0, max: 100}
	MemPriorityValidator                 = &RangeValidator{name: MemPriorityFileName, min: 0, max: 12}
	MemOomGroupValidator                 = &RangeValidator{name: MemOomGroupFileName, min: 0, max: 1}
	MemUsePriorityOomValidator           = &RangeValidator{name: MemUsePriorityOomFileName, min: 0, max: 1}
	MemWmarkMinAdjValidator              = &RangeValidator{name: MemWmarkMinAdjFileName, min: -50, max: 50}
	MemWmarkScaleFactorFileNameValidator = &RangeValidator{name: MemWmarkScaleFactorFileName, min: 1, max: 1000}
	MemMinValidator                      = &RangeValidator{name: MemMinFileName, min: 0, max: math.MaxInt64}
	MemLowValidator                      = &RangeValidator{name: MemLowFileName, min: 0, max: math.MaxInt64}
	MemHighValidator                     = &RangeValidator{name: MemHighFileName, min: 0, max: math.MaxInt64} // write value(>node.total) -> read "max"
)

var (
	CPUStat      = CgroupFile{ResourceFileName: CPUStatFileName, Subfs: CgroupCPUDir, IsAnolisOS: false}
	CPUShares    = CgroupFile{ResourceFileName: CPUSharesFileName, Subfs: CgroupCPUDir, IsAnolisOS: false}
	CPUCFSQuota  = CgroupFile{ResourceFileName: CPUCFSQuotaName, Subfs: CgroupCPUDir, IsAnolisOS: false}
	CPUCFSPeriod = CgroupFile{ResourceFileName: CPUCFSPeriodName, Subfs: CgroupCPUDir, IsAnolisOS: false}
	CPUBurst     = CgroupFile{ResourceFileName: CPUBurstName, Subfs: CgroupCPUDir, IsAnolisOS: true, Validator: CPUBurstValidator}

	CPUSet = CgroupFile{ResourceFileName: CPUSFileName, Subfs: CgroupCPUSetDir, IsAnolisOS: false}

	CpuacctStat = CgroupFile{ResourceFileName: CpuacctStatFileName, Subfs: CgroupCPUacctDir, IsAnolisOS: false}

	MemStat             = CgroupFile{ResourceFileName: MemStatFileName, Subfs: CgroupMemDir, IsAnolisOS: false}
	MemoryLimit         = CgroupFile{ResourceFileName: MemoryLimitFileName, Subfs: CgroupMemDir, IsAnolisOS: false}
	MemWmarkRatio       = CgroupFile{ResourceFileName: MemWmarkRatioFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemWmarkRatioValidator}
	MemPriority         = CgroupFile{ResourceFileName: MemPriorityFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemPriorityValidator}
	MemUsePriorityOom   = CgroupFile{ResourceFileName: MemUsePriorityOomFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemUsePriorityOomValidator}
	MemOomGroup         = CgroupFile{ResourceFileName: MemOomGroupFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemOomGroupValidator}
	MemWmarkMinAdj      = CgroupFile{ResourceFileName: MemWmarkMinAdjFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemWmarkMinAdjValidator}
	MemWmarkScaleFactor = CgroupFile{ResourceFileName: MemWmarkScaleFactorFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemWmarkScaleFactorFileNameValidator}
	MemMin              = CgroupFile{ResourceFileName: MemMinFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemMinValidator}
	MemLow              = CgroupFile{ResourceFileName: MemLowFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemLowValidator}
	MemHigh             = CgroupFile{ResourceFileName: MemHighFileName, Subfs: CgroupMemDir, IsAnolisOS: true, Validator: MemHighValidator}
)

type CgroupFile struct {
	ResourceFileName string
	Subfs            string
	IsAnolisOS       bool
	Validator        Validate
}

func ValidateCgroupValue(value *int64, parentDir string, file CgroupFile) bool {
	if value == nil {
		klog.V(5).Infof("validate fail, dir:%s, file:%s, value is nil!", parentDir, file.ResourceFileName)
		return false
	}
	if file.Validator != nil {
		valid, msg := file.Validator.Validate(value)
		if !valid {
			klog.Warningf("validate fail! dir:%s, msg:%s", parentDir, msg)
		}
		return valid
	}
	return true
}

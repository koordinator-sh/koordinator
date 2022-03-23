package system

import (
	"k8s.io/klog/v2"
	"math"
)

const (
	/** subfs name */
	CgroupCPUDir     string = "cpu/"
	CgroupCPUSetDir  string = "cpuset/"
	CgroupCPUacctDir string = "cpuacct/"
	CgroupMemDir     string = "memory/"
	CgroupBlkioDir   string = "blkio/"
	CgroupNetClsDir  string = "net_cls/"
	Devices          string = "devices/"
)

const (
	CFSBasePeriodValue int64 = 100000
	MemoryUnlimitValue int64 = -1

	CPUStatFileName   = "cpu.stat"
	CPUSharesFileName = "cpu.shares"
	CPUCFSQuotaName   = "cpu.cfs_quota_us"
	CPUCFSPeriodName  = "cpu.cfs_period_us"
	CPUBVTWarpNsName  = "cpu.bvt_warp_ns"
	CPUBurstName      = "cpu.cfs_burst_us"
	CPUSFileName      = "cpuset.cpus"
	CPUTaskFileName   = "tasks"

	CpuacctStatFileName       = "cpuacct.stat"
	CpuacctProcStatFileName   = "cpuacct.proc_stat"
	CpuacctProcStatV2FileName = "cpuacct.proc_stat_v2"

	MemWmarkRatioFileName       = "memory.wmark_ratio"
	MemWmarkScaleFactorFileName = "memory.wmark_scale_factor"
	MemDroppableFileName        = "memory.droppable"
	MemFlagsFileName            = "memory.coldpgs.flags"
	MemPriorityFileName         = "memory.priority"
	MemUsePriorityOomFileName   = "memory.use_priority_oom"
	MemOomGroupFileName         = "memory.oom.group"
	MemWmarkMinAdjFileName      = "memory.wmark_min_adj"
	MemMinFileName              = "memory.min"
	MemLowFileName              = "memory.low"
	MemHighFileName             = "memory.high"
	MemoryLimitFileName         = "memory.limit_in_bytes"
	MemorySWLimitFileName       = "memory.memsw.limit_in_bytes"
	MemStatFileName             = "memory.stat"

	NetClsIDFileName = "net_cls.classid"

	BlkioTRIopsFileName = "blkio.throttle.read_iops_device"
	BlkioTRBpsFileName  = "blkio.throttle.read_bps_device"
	BlkioTWIopsFileName = "blkio.throttle.write_iops_device"
	BlkioTWBpsFileName  = "blkio.throttle.write_bps_device"
)

var (
	MemWmarkRatioValidator               = &RangeValidator{name: MemWmarkRatioFileName, min: 0, max: 100}
	MemPriorityValidator                 = &RangeValidator{name: MemPriorityFileName, min: 0, max: 12}
	MemOomGroupValidator                 = &RangeValidator{name: MemOomGroupFileName, min: 0, max: 1}
	MemUsePriorityOomValidator           = &RangeValidator{name: MemUsePriorityOomFileName, min: 0, max: 1}
	MemWmarkMinAdjValidator              = &RangeValidator{name: MemWmarkMinAdjFileName, min: -50, max: 50}
	MemWmarkScaleFactorFileNameValidator = &RangeValidator{name: MemWmarkScaleFactorFileName, min: 1, max: 1000}
	MemDroppableValidator                = &RangeValidator{name: MemDroppableFileName, min: 0, max: 1}
	MemMinValidator                      = &RangeValidator{name: MemMinFileName, min: 0, max: math.MaxInt64}
	MemLowValidator                      = &RangeValidator{name: MemLowFileName, min: 0, max: math.MaxInt64}
	MemHighValidator                     = &RangeValidator{name: MemHighFileName, min: 0, max: math.MaxInt64} // write value(>node.total) -> read "max"
	CPUBvtWarpNsValidator                = &RangeValidator{name: CPUBVTWarpNsName, min: -1, max: 2}
	CPUBurstValidator                    = &RangeValidator{name: CPUBurstName, min: 0, max: 100 * 10 * 100000}
)

var (
	CPUStat      = CgroupFile{ResourceFileName: CPUStatFileName, Subfs: CgroupCPUDir, IsAliOS: false}
	CPUShares    = CgroupFile{ResourceFileName: CPUSharesFileName, Subfs: CgroupCPUDir, IsAliOS: false}
	CPUCFSQuota  = CgroupFile{ResourceFileName: CPUCFSQuotaName, Subfs: CgroupCPUDir, IsAliOS: false}
	CPUCFSPeriod = CgroupFile{ResourceFileName: CPUCFSPeriodName, Subfs: CgroupCPUDir, IsAliOS: false}
	CPUTask      = CgroupFile{ResourceFileName: CPUTaskFileName, Subfs: CgroupCPUDir, IsAliOS: false}
	CPUBVTWarpNs = CgroupFile{ResourceFileName: CPUBVTWarpNsName, Subfs: CgroupCPUDir, IsAliOS: true, Validator: CPUBvtWarpNsValidator}
	CPUBurst     = CgroupFile{ResourceFileName: CPUBurstName, Subfs: CgroupCPUDir, IsAliOS: true, Validator: CPUBurstValidator}

	CPUSet = CgroupFile{ResourceFileName: CPUSFileName, Subfs: CgroupCPUSetDir, IsAliOS: false}

	CpuacctStat       = CgroupFile{ResourceFileName: CpuacctStatFileName, Subfs: CgroupCPUacctDir, IsAliOS: false}
	CpuacctProcStat   = CgroupFile{ResourceFileName: CpuacctProcStatFileName, Subfs: CgroupCPUacctDir, IsAliOS: true}
	CpuacctProcStatV2 = CgroupFile{ResourceFileName: CpuacctProcStatV2FileName, Subfs: CgroupCPUacctDir, IsAliOS: true}

	MemWmarkRatio       = CgroupFile{ResourceFileName: MemWmarkRatioFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemWmarkRatioValidator}
	MemDroppable        = CgroupFile{ResourceFileName: MemDroppableFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemDroppableValidator}
	MemFlags            = CgroupFile{ResourceFileName: MemFlagsFileName, Subfs: CgroupMemDir, IsAliOS: true}
	MemPriority         = CgroupFile{ResourceFileName: MemPriorityFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemPriorityValidator}
	MemUsePriorityOom   = CgroupFile{ResourceFileName: MemUsePriorityOomFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemUsePriorityOomValidator}
	MemOomGroup         = CgroupFile{ResourceFileName: MemOomGroupFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemOomGroupValidator}
	MemWmarkMinAdj      = CgroupFile{ResourceFileName: MemWmarkMinAdjFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemWmarkMinAdjValidator}
	MemWmarkScaleFactor = CgroupFile{ResourceFileName: MemWmarkScaleFactorFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemWmarkScaleFactorFileNameValidator}
	MemMin              = CgroupFile{ResourceFileName: MemMinFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemMinValidator}
	MemLow              = CgroupFile{ResourceFileName: MemLowFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemLowValidator}
	MemHigh             = CgroupFile{ResourceFileName: MemHighFileName, Subfs: CgroupMemDir, IsAliOS: true, Validator: MemHighValidator}
	MemoryLimit         = CgroupFile{ResourceFileName: MemoryLimitFileName, Subfs: CgroupMemDir, IsAliOS: false}
	MemorySWLimit       = CgroupFile{ResourceFileName: MemorySWLimitFileName, Subfs: CgroupMemDir, IsAliOS: false}
	MemStat             = CgroupFile{ResourceFileName: MemStatFileName, Subfs: CgroupMemDir, IsAliOS: false}

	NetClsID = CgroupFile{ResourceFileName: NetClsIDFileName, Subfs: CgroupNetClsDir, IsAliOS: false}

	BlkioReadIops  = CgroupFile{ResourceFileName: BlkioTRIopsFileName, Subfs: CgroupBlkioDir, IsAliOS: false}
	BlkioReadBps   = CgroupFile{ResourceFileName: BlkioTRBpsFileName, Subfs: CgroupBlkioDir, IsAliOS: false}
	BlkioWriteIops = CgroupFile{ResourceFileName: BlkioTWIopsFileName, Subfs: CgroupBlkioDir, IsAliOS: false}
	BlkioWriteBps  = CgroupFile{ResourceFileName: BlkioTWBpsFileName, Subfs: CgroupBlkioDir, IsAliOS: false}
)

type CgroupFile struct {
	ResourceFileName string
	Subfs            string
	IsAliOS          bool
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

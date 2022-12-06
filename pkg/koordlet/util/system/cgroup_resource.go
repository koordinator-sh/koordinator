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
	"path/filepath"
	"sync"

	"k8s.io/utils/pointer"
)

func init() {
	DefaultRegistry.Add(CgroupVersionV1, knownCgroupResources...)
	DefaultRegistry.Add(CgroupVersionV2, knownCgroupV2Resources...)
}

var DefaultRegistry = NewCgroupResourceRegistry()

type CgroupVersion int32

const (
	CgroupVersionV1 CgroupVersion = 1
	CgroupVersionV2 CgroupVersion = 2
)

type CgroupResourceRegistry interface {
	Add(v CgroupVersion, s ...Resource)
	Get(v CgroupVersion, t ResourceType) (Resource, bool)
}

type CgroupResourceRegistryImpl struct {
	lock sync.RWMutex
	v1   map[ResourceType]Resource
	v2   map[ResourceType]Resource
}

func NewCgroupResourceRegistry() CgroupResourceRegistry {
	return &CgroupResourceRegistryImpl{
		v1: map[ResourceType]Resource{},
		v2: map[ResourceType]Resource{},
	}
}

func (r *CgroupResourceRegistryImpl) Add(v CgroupVersion, s ...Resource) {
	r.lock.Lock()
	defer r.lock.Unlock()
	m := r.v1
	if v == CgroupVersionV2 {
		m = r.v2
	}
	for i := range s {
		m[s[i].ResourceType()] = s[i]
	}
}

func (r *CgroupResourceRegistryImpl) Get(v CgroupVersion, key ResourceType) (Resource, bool) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	m := r.v1
	if v == CgroupVersionV2 {
		m = r.v2
	}
	s, ok := m[key]
	return s, ok
}

func GetCurrentCgroupVersion() CgroupVersion {
	if UseCgroupsV2 {
		return CgroupVersionV2
	}
	return CgroupVersionV1
}

const ( // subsystems
	CgroupCPUDir     string = "cpu/"
	CgroupCPUSetDir  string = "cpuset/"
	CgroupCPUAcctDir string = "cpuacct/"
	CgroupMemDir     string = "memory/"
	CgroupBlkioDir   string = "blkio/"

	CgroupV2Dir = ""
)

const (
	CFSBasePeriodValue int64 = 100000
	CFSQuotaMinValue   int64 = 1000 // min value except `-1`
	CPUSharesMinValue  int64 = 2

	CPUStatName      = "cpu.stat"
	CPUSharesName    = "cpu.shares"
	CPUCFSQuotaName  = "cpu.cfs_quota_us"
	CPUCFSPeriodName = "cpu.cfs_period_us"
	CPUBVTWarpNsName = "cpu.bvt_warp_ns"
	CPUBurstName     = "cpu.cfs_burst_us"
	CPUTasksName     = "tasks"
	CPUProcsName     = "cgroup.procs"
	CPUMaxName       = "cpu.max"
	CPUWeightName    = "cpu.weight"

	CPUSetCPUSName          = "cpuset.cpus"
	CPUSetCPUSEffectiveName = "cpuset.cpus.effective"

	CPUAcctStatName           = "cpuacct.stat"
	CPUAcctUsageName          = "cpuacct.usage"
	CPUAcctCPUPressureName    = "cpu.pressure"
	CPUAcctMemoryPressureName = "memory.pressure"
	CPUAcctIOPressureName     = "io.pressure"

	MemoryLimitName            = "memory.limit_in_bytes"
	MemoryUsageName            = "memory.usage_in_bytes"
	MemoryStatName             = "memory.stat"
	MemoryWmarkRatioName       = "memory.wmark_ratio"
	MemoryWmarkScaleFactorName = "memory.wmark_scale_factor"
	MemoryWmarkMinAdjName      = "memory.wmark_min_adj"
	MemoryMinName              = "memory.min"  // anolis os or cgroups-v2
	MemoryLowName              = "memory.low"  // anolis os or cgroups-v2
	MemoryHighName             = "memory.high" // anolis os or cgroups-v2
	MemoryMaxName              = "memory.max"
	MemoryCurrentName          = "memory.current"
	MemoryPriorityName         = "memory.priority"
	MemoryUsePriorityOomName   = "memory.use_priority_oom"
	MemoryOomGroupName         = "memory.oom.group"

	BlkioTRIopsName = "blkio.throttle.read_iops_device"
	BlkioTRBpsName  = "blkio.throttle.read_bps_device"
	BlkioTWIopsName = "blkio.throttle.write_iops_device"
	BlkioTWBpsName  = "blkio.throttle.write_bps_device"
)

var (
	NaturalInt64Validator = &RangeValidator{min: 0, max: math.MaxInt64}

	CPUSharesValidator                      = &RangeValidator{min: CPUSharesMinValue, max: math.MaxInt64}
	CPUBurstValidator                       = &RangeValidator{min: 0, max: 100 * 10 * 100000}
	CPUBvtWarpNsValidator                   = &RangeValidator{min: -1, max: 2}
	CPUWeightValidator                      = &RangeValidator{min: 1, max: 10000}
	MemoryWmarkRatioValidator               = &RangeValidator{min: 0, max: 100}
	MemoryPriorityValidator                 = &RangeValidator{min: 0, max: 12}
	MemoryOomGroupValidator                 = &RangeValidator{min: 0, max: 1}
	MemoryUsePriorityOomValidator           = &RangeValidator{min: 0, max: 1}
	MemoryWmarkMinAdjValidator              = &RangeValidator{min: -25, max: 50}
	MemoryWmarkScaleFactorFileNameValidator = &RangeValidator{min: 1, max: 1000}

	CPUSetCPUSValidator = &CPUSetStrValidator{}
)

// for cgroup resources, we use the corresponding cgroups-v1 filename as its resource type
var (
	DefaultFactory = NewCgroupResourceFactory()

	CPUStat      = DefaultFactory.New(CPUStatName, CgroupCPUDir)
	CPUShares    = DefaultFactory.New(CPUSharesName, CgroupCPUDir).WithValidator(CPUSharesValidator)
	CPUCFSQuota  = DefaultFactory.New(CPUCFSQuotaName, CgroupCPUDir)
	CPUCFSPeriod = DefaultFactory.New(CPUCFSPeriodName, CgroupCPUDir)
	CPUBurst     = DefaultFactory.New(CPUBurstName, CgroupCPUDir).WithValidator(CPUBurstValidator).WithCheckSupported(SupportedIfFileExists)
	CPUBVTWarpNs = DefaultFactory.New(CPUBVTWarpNsName, CgroupCPUDir).WithValidator(CPUBvtWarpNsValidator).WithCheckSupported(SupportedIfFileExists)
	CPUTasks     = DefaultFactory.New(CPUTasksName, CgroupCPUDir)
	CPUProcs     = DefaultFactory.New(CPUProcsName, CgroupCPUDir)

	CPUSet = DefaultFactory.New(CPUSetCPUSName, CgroupCPUSetDir).WithValidator(CPUSetCPUSValidator)

	CPUAcctStat           = DefaultFactory.New(CPUAcctStatName, CgroupCPUAcctDir)
	CPUAcctUsage          = DefaultFactory.New(CPUAcctUsageName, CgroupCPUAcctDir)
	CPUAcctCPUPressure    = DefaultFactory.New(CPUAcctCPUPressureName, CgroupCPUAcctDir)
	CPUAcctMemoryPressure = DefaultFactory.New(CPUAcctMemoryPressureName, CgroupCPUAcctDir)
	CPUAcctIOPressure     = DefaultFactory.New(CPUAcctIOPressureName, CgroupCPUAcctDir)

	MemoryLimit            = DefaultFactory.New(MemoryLimitName, CgroupMemDir)
	MemoryUsage            = DefaultFactory.New(MemoryUsageName, CgroupMemDir)
	MemoryStat             = DefaultFactory.New(MemoryStatName, CgroupMemDir)
	MemoryWmarkRatio       = DefaultFactory.New(MemoryWmarkRatioName, CgroupMemDir).WithValidator(MemoryWmarkRatioValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryWmarkScaleFactor = DefaultFactory.New(MemoryWmarkScaleFactorName, CgroupMemDir).WithValidator(MemoryWmarkScaleFactorFileNameValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryWmarkMinAdj      = DefaultFactory.New(MemoryWmarkMinAdjName, CgroupMemDir).WithValidator(MemoryWmarkMinAdjValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryMin              = DefaultFactory.New(MemoryMinName, CgroupMemDir).WithValidator(NaturalInt64Validator).WithCheckSupported(SupportedIfFileExists)
	MemoryLow              = DefaultFactory.New(MemoryLowName, CgroupMemDir).WithValidator(NaturalInt64Validator).WithCheckSupported(SupportedIfFileExists)
	MemoryHigh             = DefaultFactory.New(MemoryHighName, CgroupMemDir).WithValidator(NaturalInt64Validator).WithCheckSupported(SupportedIfFileExists)
	MemoryPriority         = DefaultFactory.New(MemoryPriorityName, CgroupMemDir).WithValidator(MemoryPriorityValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryUsePriorityOom   = DefaultFactory.New(MemoryUsePriorityOomName, CgroupMemDir).WithValidator(MemoryUsePriorityOomValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryOomGroup         = DefaultFactory.New(MemoryOomGroupName, CgroupMemDir).WithValidator(MemoryOomGroupValidator).WithCheckSupported(SupportedIfFileExists)

	BlkioReadIops  = DefaultFactory.New(BlkioTRIopsName, CgroupBlkioDir).WithValidator(NaturalInt64Validator)
	BlkioReadBps   = DefaultFactory.New(BlkioTRBpsName, CgroupBlkioDir).WithValidator(NaturalInt64Validator)
	BlkioWriteIops = DefaultFactory.New(BlkioTWIopsName, CgroupBlkioDir).WithValidator(NaturalInt64Validator)
	BlkioWriteBps  = DefaultFactory.New(BlkioTWBpsName, CgroupBlkioDir).WithValidator(NaturalInt64Validator)

	knownCgroupResources = []Resource{
		CPUStat,
		CPUShares,
		CPUCFSQuota,
		CPUCFSPeriod,
		CPUBurst,
		CPUTasks,
		CPUBVTWarpNs,
		CPUSet,
		CPUAcctStat,
		CPUAcctUsage,
		CPUAcctCPUPressure,
		CPUAcctMemoryPressure,
		CPUAcctIOPressure,
		MemoryLimit,
		MemoryUsage,
		MemoryStat,
		MemoryWmarkRatio,
		MemoryWmarkScaleFactor,
		MemoryWmarkMinAdj,
		MemoryMin,
		MemoryLow,
		MemoryHigh,
		MemoryPriority,
		MemoryUsePriorityOom,
		MemoryOomGroup,
		BlkioReadIops,
		BlkioReadBps,
		BlkioWriteIops,
		BlkioWriteBps,
	}

	CPUCFSQuotaV2     = DefaultFactory.NewV2(CPUCFSQuotaName, CPUMaxName)
	CPUCFSPeriodV2    = DefaultFactory.NewV2(CPUCFSPeriodName, CPUMaxName)
	CPUSharesV2       = DefaultFactory.NewV2(CPUSharesName, CPUWeightName).WithValidator(CPUWeightValidator)
	CPUStatV2         = DefaultFactory.NewV2(CPUStatName, CPUStatName)
	CPUAcctStatV2     = DefaultFactory.NewV2(CPUAcctStatName, CPUStatName)
	CPUAcctUsageV2    = DefaultFactory.NewV2(CPUAcctUsageName, CPUStatName)
	CPUSetV2          = DefaultFactory.NewV2(CPUSetCPUSName, CPUSetCPUSName).WithValidator(CPUSetCPUSValidator)
	CPUSetEffectiveV2 = DefaultFactory.NewV2(CPUSetCPUSEffectiveName, CPUSetCPUSEffectiveName) // TODO: unify the R/W
	MemoryLimitV2     = DefaultFactory.NewV2(MemoryLimitName, MemoryMaxName)
	MemoryUsageV2     = DefaultFactory.NewV2(MemoryUsageName, MemoryCurrentName)
	MemoryStatV2      = DefaultFactory.NewV2(MemoryStatName, MemoryStatName)
	MemoryMinV2       = DefaultFactory.NewV2(MemoryMinName, MemoryMinName)
	MemoryLowV2       = DefaultFactory.NewV2(MemoryLowName, MemoryLowName)
	MemoryHighV2      = DefaultFactory.NewV2(MemoryHighName, MemoryHighName)

	knownCgroupV2Resources = []Resource{
		CPUCFSQuotaV2,
		CPUCFSPeriodV2,
		CPUSharesV2,
		CPUStatV2,
		CPUAcctStatV2,
		CPUAcctUsageV2,
		CPUSetV2,
		CPUSetEffectiveV2,
		MemoryLimitV2,
		MemoryUsageV2,
		MemoryStatV2,
		MemoryMinV2,
		MemoryLowV2,
		MemoryHighV2,
	}
)

var _ Resource = &CgroupResource{}

type CgroupResource struct {
	Type           ResourceType
	FileName       string
	Subfs          string
	Supported      *bool
	SupportMsg     string
	CheckSupported func(r Resource, parentDir string) (*bool, string)
	Validator      ResourceValidator
}

func (c *CgroupResource) ResourceType() ResourceType {
	if len(c.Type) > 0 {
		return c.Type
	}
	return GetDefaultResourceType(c.Subfs, c.FileName)
}

func (c *CgroupResource) Path(parentDir string) string {
	// get cgroup path
	return filepath.Join(Conf.CgroupRootDir, c.Subfs, parentDir, c.FileName)
}

func (c *CgroupResource) IsSupported(parentDir string) (bool, string) {
	if c.Supported == nil {
		if c.CheckSupported == nil {
			return false, "unknown support status"
		}
		c.Supported, c.SupportMsg = c.CheckSupported(c, parentDir)
	}
	return *c.Supported, c.SupportMsg
}

func (c *CgroupResource) IsValid(v string) (bool, string) {
	if c.Validator == nil {
		return true, ""
	}
	return c.Validator.Validate(v)
}

func (c *CgroupResource) WithValidator(validator ResourceValidator) Resource {
	c.Validator = validator
	return c
}

func (c *CgroupResource) WithCheckSupported(checkSupportedFn func(r Resource, parentDir string) (*bool, string)) Resource {
	c.Supported = nil
	c.CheckSupported = checkSupportedFn
	return c
}

type CgroupResourceFactory interface {
	New(filename string, subfs string) Resource // cgroup-v1 filename represents the resource type
	NewV2(t ResourceType, filename string) Resource
}

type cgroupResourceFactoryImpl struct{}

func NewCgroupResourceFactory() CgroupResourceFactory {
	return &cgroupResourceFactoryImpl{}
}

func (f *cgroupResourceFactoryImpl) New(filename string, subfs string) Resource {
	return &CgroupResource{Type: ResourceType(filename), FileName: filename, Subfs: subfs, Supported: pointer.Bool(true)}
}

func (f *cgroupResourceFactoryImpl) NewV2(t ResourceType, filename string) Resource {
	return &CgroupResource{Type: t, FileName: filename, Subfs: CgroupV2Dir, Supported: pointer.Bool(true)}
}

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
	"fmt"
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
		if conv, ok := s[i].(*CgroupResource); ok {
			conv.SetCgroupVersion(v)
		}
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
	if UseCgroupsV2.Load() {
		return CgroupVersionV2
	}
	return CgroupVersionV1
}

func GetCgroupResource(resourceType ResourceType) (Resource, error) {
	r, ok := DefaultRegistry.Get(GetCurrentCgroupVersion(), resourceType)
	if !ok {
		return nil, fmt.Errorf("%s not found in cgroup registry", resourceType)
	}
	return r, nil
}

func IsCgroupV2Resource(r Resource) bool {
	if conv, ok := r.(*CgroupResource); ok {
		return conv.GetCgroupVersion() == CgroupVersionV2
	}
	return false
}

const ( // subsystems
	CgroupCPUDir     string = "cpu/"
	CgroupCPUSetDir  string = "cpuset/"
	CgroupCPUAcctDir string = "cpuacct/"
	CgroupMemDir     string = "memory/"
	CgroupBlkioDir   string = "blkio/"
	CgroupNetClsDir  string = "net_cls/"

	CgroupV2Dir = ""
)

const (
	CFSBasePeriodValue int64 = 100000
	CFSQuotaMinValue   int64 = 1000 // min value except `-1`
	CPUSharesMinValue  int64 = 2
	CPUSharesMaxValue  int64 = 262144
	CPUWeightMinValue  int64 = 1
	CPUWeightMaxValue  int64 = 10000

	CPUStatName      = "cpu.stat"
	CPUSharesName    = "cpu.shares"
	CPUCFSQuotaName  = "cpu.cfs_quota_us"
	CPUCFSPeriodName = "cpu.cfs_period_us"
	CPUBVTWarpNsName = "cpu.bvt_warp_ns"
	CPUBurstName     = "cpu.cfs_burst_us"
	CPUTasksName     = "tasks"
	CPUProcsName     = "cgroup.procs"
	CPUThreadsName   = "cgroup.threads"
	CPUMaxName       = "cpu.max"
	CPUMaxBurstName  = "cpu.max.burst"
	CPUWeightName    = "cpu.weight"
	CPUIdleName      = "cpu.idle"

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
	MemoryNumaStatName         = "memory.numa_stat"
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
	MemoryIdlePageStatsName    = "memory.idle_page_stats"

	BlkioTRIopsName   = "blkio.throttle.read_iops_device"
	BlkioTRBpsName    = "blkio.throttle.read_bps_device"
	BlkioTWIopsName   = "blkio.throttle.write_iops_device"
	BlkioTWBpsName    = "blkio.throttle.write_bps_device"
	BlkioIOWeightName = "blkio.cost.weight"
	BlkioIOQoSName    = "blkio.cost.qos"
	BlkioIOModelName  = "blkio.cost.model"

	NetClsClassIdName = "net_cls.classid"
)

var (
	NaturalInt64Validator = &RangeValidator{min: 0, max: math.MaxInt64}

	CPUSharesValidator                      = &RangeValidator{min: CPUSharesMinValue, max: CPUSharesMaxValue}
	CPUBurstValidator                       = &RangeValidator{min: 0, max: 100 * 10 * 100000}
	CPUBvtWarpNsValidator                   = &RangeValidator{min: -1, max: 2}
	CPUWeightValidator                      = &RangeValidator{min: CPUWeightMinValue, max: CPUWeightMaxValue}
	CPUMaxBurstValidator                    = &RangeValidator{min: 0, max: math.MaxInt64}
	CPUIdleValidator                        = &RangeValidator{min: 0, max: 1}
	MemoryWmarkRatioValidator               = &RangeValidator{min: 0, max: 100}
	MemoryPriorityValidator                 = &RangeValidator{min: 0, max: 12}
	MemoryOomGroupValidator                 = &RangeValidator{min: 0, max: 1}
	MemoryUsePriorityOomValidator           = &RangeValidator{min: 0, max: 1}
	MemoryWmarkMinAdjValidator              = &RangeValidator{min: -25, max: 50}
	MemoryWmarkScaleFactorFileNameValidator = &RangeValidator{min: 1, max: 1000}
	BlkioTRIopsValidator                    = &BlkIORangeValidator{min: 0, max: math.MaxInt64, resource: BlkioTRIopsName}
	BlkioTRBpsValidator                     = &BlkIORangeValidator{min: 0, max: math.MaxInt64, resource: BlkioTRBpsName}
	BlkioTWIopsValidator                    = &BlkIORangeValidator{min: 0, max: math.MaxInt64, resource: BlkioTWIopsName}
	BlkioTWBpsValidator                     = &BlkIORangeValidator{min: 0, max: math.MaxInt64, resource: BlkioTWBpsName}
	BlkioIOWeightValidator                  = &BlkIORangeValidator{min: 1, max: 100, resource: BlkioIOWeightName}
	BlkioIOQoSValidator                     = &BlkIORangeValidator{min: 0, max: math.MaxInt64, resource: BlkioIOQoSName}
	BlkioIOModelValidator                   = &BlkIORangeValidator{min: 1, max: math.MaxInt64, resource: BlkioIOModelName}

	NetClsClassIdValidator = &NetClsRangeValidator{resource: NetClsClassIdName}

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
	CPUIdle      = DefaultFactory.New(CPUIdleName, CgroupCPUDir).WithValidator(CPUIdleValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	CPUTasks     = DefaultFactory.New(CPUTasksName, CgroupCPUDir)
	CPUProcs     = DefaultFactory.New(CPUProcsName, CgroupCPUDir)

	CPUSet = DefaultFactory.New(CPUSetCPUSName, CgroupCPUSetDir).WithValidator(CPUSetCPUSValidator)

	CPUAcctStat           = DefaultFactory.New(CPUAcctStatName, CgroupCPUAcctDir)
	CPUAcctUsage          = DefaultFactory.New(CPUAcctUsageName, CgroupCPUAcctDir)
	CPUAcctCPUPressure    = DefaultFactory.New(CPUAcctCPUPressureName, CgroupCPUAcctDir).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	CPUAcctMemoryPressure = DefaultFactory.New(CPUAcctMemoryPressureName, CgroupCPUAcctDir).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	CPUAcctIOPressure     = DefaultFactory.New(CPUAcctIOPressureName, CgroupCPUAcctDir).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)

	MemoryLimit            = DefaultFactory.New(MemoryLimitName, CgroupMemDir)
	MemoryUsage            = DefaultFactory.New(MemoryUsageName, CgroupMemDir)
	MemoryStat             = DefaultFactory.New(MemoryStatName, CgroupMemDir)
	MemoryNumaStat         = DefaultFactory.New(MemoryNumaStatName, CgroupMemDir)
	MemoryWmarkRatio       = DefaultFactory.New(MemoryWmarkRatioName, CgroupMemDir).WithValidator(MemoryWmarkRatioValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryWmarkScaleFactor = DefaultFactory.New(MemoryWmarkScaleFactorName, CgroupMemDir).WithValidator(MemoryWmarkScaleFactorFileNameValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryWmarkMinAdj      = DefaultFactory.New(MemoryWmarkMinAdjName, CgroupMemDir).WithValidator(MemoryWmarkMinAdjValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryMin              = DefaultFactory.New(MemoryMinName, CgroupMemDir).WithValidator(NaturalInt64Validator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryLow              = DefaultFactory.New(MemoryLowName, CgroupMemDir).WithValidator(NaturalInt64Validator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryHigh             = DefaultFactory.New(MemoryHighName, CgroupMemDir).WithValidator(NaturalInt64Validator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryPriority         = DefaultFactory.New(MemoryPriorityName, CgroupMemDir).WithValidator(MemoryPriorityValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryUsePriorityOom   = DefaultFactory.New(MemoryUsePriorityOomName, CgroupMemDir).WithValidator(MemoryUsePriorityOomValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryOomGroup         = DefaultFactory.New(MemoryOomGroupName, CgroupMemDir).WithValidator(MemoryOomGroupValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	MemoryIdlePageStats    = DefaultFactory.New(MemoryIdlePageStatsName, CgroupMemDir).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)

	BlkioReadIops  = DefaultFactory.New(BlkioTRIopsName, CgroupBlkioDir).WithValidator(BlkioTRIopsValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	BlkioReadBps   = DefaultFactory.New(BlkioTRBpsName, CgroupBlkioDir).WithValidator(BlkioTRBpsValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	BlkioWriteIops = DefaultFactory.New(BlkioTWIopsName, CgroupBlkioDir).WithValidator(BlkioTWIopsValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	BlkioWriteBps  = DefaultFactory.New(BlkioTWBpsName, CgroupBlkioDir).WithValidator(BlkioTWBpsValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	BlkioIOWeight  = DefaultFactory.New(BlkioIOWeightName, CgroupBlkioDir).WithValidator(BlkioIOWeightValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	BlkioIOQoS     = DefaultFactory.New(BlkioIOQoSName, CgroupBlkioDir).WithValidator(BlkioIOQoSValidator).WithSupported(SupportedIfFileExistsInRootCgroup(BlkioIOQoSName, CgroupBlkioDir))
	BlkioIOModel   = DefaultFactory.New(BlkioIOModelName, CgroupBlkioDir).WithValidator(BlkioIOModelValidator).WithSupported(SupportedIfFileExistsInRootCgroup(BlkioIOModelName, CgroupBlkioDir))

	NetClsClassId = DefaultFactory.New(NetClsClassIdName, CgroupNetClsDir).WithValidator(NetClsClassIdValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)

	knownCgroupResources = []Resource{
		CPUStat,
		CPUShares,
		CPUCFSQuota,
		CPUCFSPeriod,
		CPUBurst,
		CPUTasks,
		CPUBVTWarpNs,
		CPUIdle,
		CPUSet,
		CPUAcctStat,
		CPUAcctUsage,
		CPUAcctCPUPressure,
		CPUAcctMemoryPressure,
		CPUAcctIOPressure,
		CPUProcs,
		MemoryLimit,
		MemoryUsage,
		MemoryStat,
		MemoryNumaStat,
		MemoryWmarkRatio,
		MemoryWmarkScaleFactor,
		MemoryWmarkMinAdj,
		MemoryMin,
		MemoryLow,
		MemoryHigh,
		MemoryPriority,
		MemoryUsePriorityOom,
		MemoryOomGroup,
		MemoryIdlePageStats,
		BlkioReadIops,
		BlkioReadBps,
		BlkioWriteIops,
		BlkioWriteBps,
		BlkioIOWeight,
		BlkioIOQoS,
		BlkioIOModel,
		NetClsClassId,
	}

	CPUCFSQuotaV2  = DefaultFactory.NewV2(CPUCFSQuotaName, CPUMaxName)
	CPUCFSPeriodV2 = DefaultFactory.NewV2(CPUCFSPeriodName, CPUMaxName)
	CPUSharesV2    = DefaultFactory.NewV2(CPUSharesName, CPUWeightName).WithValidator(CPUWeightValidator)
	CPUStatV2      = DefaultFactory.NewV2(CPUStatName, CPUStatName)
	CPUAcctStatV2  = DefaultFactory.NewV2(CPUAcctStatName, CPUStatName)
	CPUAcctUsageV2 = DefaultFactory.NewV2(CPUAcctUsageName, CPUStatName)
	CPUBurstV2     = DefaultFactory.NewV2(CPUBurstName, CPUMaxBurstName).WithValidator(CPUMaxBurstValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	CPUBVTWarpNsV2 = DefaultFactory.NewV2(CPUBVTWarpNsName, CPUBVTWarpNsName).WithValidator(CPUBvtWarpNsValidator).WithCheckSupported(SupportedIfFileExists)
	CPUIdleV2      = DefaultFactory.NewV2(CPUIdleName, CPUIdleName).WithValidator(CPUIdleValidator).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)

	CPUAcctCPUPressureV2    = DefaultFactory.NewV2(CPUAcctCPUPressureName, CPUAcctCPUPressureName).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	CPUAcctMemoryPressureV2 = DefaultFactory.NewV2(CPUAcctMemoryPressureName, CPUAcctMemoryPressureName).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)
	CPUAcctIOPressureV2     = DefaultFactory.NewV2(CPUAcctIOPressureName, CPUAcctIOPressureName).WithCheckSupported(SupportedIfFileExistsInKubepods).WithCheckOnce(true)

	CPUSetV2                 = DefaultFactory.NewV2(CPUSetCPUSName, CPUSetCPUSName).WithValidator(CPUSetCPUSValidator)
	CPUSetEffectiveV2        = DefaultFactory.NewV2(CPUSetCPUSEffectiveName, CPUSetCPUSEffectiveName) // TODO: unify the R/W
	CPUTasksV2               = DefaultFactory.NewV2(CPUTasksName, CPUThreadsName)
	CPUProcsV2               = DefaultFactory.NewV2(CPUProcsName, CPUProcsName)
	MemoryLimitV2            = DefaultFactory.NewV2(MemoryLimitName, MemoryMaxName)
	MemoryUsageV2            = DefaultFactory.NewV2(MemoryUsageName, MemoryCurrentName)
	MemoryStatV2             = DefaultFactory.NewV2(MemoryStatName, MemoryStatName)
	MemoryNumaStatV2         = DefaultFactory.NewV2(MemoryNumaStatName, MemoryNumaStatName)
	MemoryMinV2              = DefaultFactory.NewV2(MemoryMinName, MemoryMinName).WithValidator(NaturalInt64Validator)
	MemoryLowV2              = DefaultFactory.NewV2(MemoryLowName, MemoryLowName).WithValidator(NaturalInt64Validator)
	MemoryHighV2             = DefaultFactory.NewV2(MemoryHighName, MemoryHighName).WithValidator(NaturalInt64Validator)
	MemoryWmarkRatioV2       = DefaultFactory.NewV2(MemoryWmarkRatioName, MemoryWmarkRatioName).WithValidator(MemoryWmarkRatioValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryWmarkScaleFactorV2 = DefaultFactory.NewV2(MemoryWmarkScaleFactorName, MemoryWmarkScaleFactorName).WithValidator(MemoryWmarkScaleFactorFileNameValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryWmarkMinAdjV2      = DefaultFactory.NewV2(MemoryWmarkMinAdjName, MemoryWmarkMinAdjName).WithValidator(MemoryWmarkMinAdjValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryPriorityV2         = DefaultFactory.NewV2(MemoryPriorityName, MemoryPriorityName).WithValidator(MemoryPriorityValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryUsePriorityOomV2   = DefaultFactory.NewV2(MemoryUsePriorityOomName, MemoryUsePriorityOomName).WithValidator(MemoryUsePriorityOomValidator).WithCheckSupported(SupportedIfFileExists)
	MemoryOomGroupV2         = DefaultFactory.NewV2(MemoryOomGroupName, MemoryOomGroupName).WithValidator(MemoryOomGroupValidator).WithCheckSupported(SupportedIfFileExists)

	knownCgroupV2Resources = []Resource{
		CPUCFSQuotaV2,
		CPUCFSPeriodV2,
		CPUSharesV2,
		CPUStatV2,
		CPUAcctStatV2,
		CPUAcctUsageV2,
		CPUBurstV2,
		CPUBVTWarpNsV2,
		CPUIdleV2,
		CPUAcctCPUPressureV2,
		CPUAcctMemoryPressureV2,
		CPUAcctIOPressureV2,
		CPUSetV2,
		CPUSetEffectiveV2,
		CPUTasksV2,
		CPUProcsV2,
		MemoryLimitV2,
		MemoryUsageV2,
		MemoryStatV2,
		MemoryNumaStatV2,
		MemoryMinV2,
		MemoryLowV2,
		MemoryHighV2,
		MemoryWmarkRatioV2,
		MemoryWmarkScaleFactorV2,
		MemoryWmarkMinAdjV2,
		MemoryPriorityV2,
		MemoryUsePriorityOomV2,
		MemoryOomGroupV2,
		// TODO: register BlkioIOWeight, BlkioIOQoS and BlkioIOModel

		NetClsClassId,
	}
)

var _ Resource = &CgroupResource{}

type CgroupResource struct {
	Type           ResourceType
	FileName       string
	Subfs          string
	Supported      *bool
	SupportMsg     string
	CheckSupported func(r Resource, parentDir string) (isSupported bool, msg string)
	CheckOnce      bool
	Validator      ResourceValidator
	CgroupVersion  CgroupVersion
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
	if c.Supported != nil {
		return *c.Supported, c.SupportMsg
	}
	if c.CheckSupported == nil {
		return false, "unknown support status"
	}
	isSupported, msg := c.CheckSupported(c, parentDir)
	if c.CheckOnce {
		c.Supported = &isSupported
		c.SupportMsg = msg
	}

	return isSupported, msg
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

func (c *CgroupResource) WithSupported(isSupported bool, msg string) Resource {
	c.Supported = pointer.Bool(isSupported)
	c.SupportMsg = msg
	return c
}

func (c *CgroupResource) WithCheckOnce(isCheckOnce bool) Resource {
	c.CheckOnce = isCheckOnce
	return c
}

func (c *CgroupResource) WithCheckSupported(checkSupportedFn func(r Resource, parentDir string) (isSupported bool, msg string)) Resource {
	c.Supported = nil
	c.CheckSupported = checkSupportedFn
	return c
}

func (c *CgroupResource) SetCgroupVersion(cv CgroupVersion) {
	c.CgroupVersion = cv
}

func (c *CgroupResource) GetCgroupVersion() CgroupVersion {
	return c.CgroupVersion
}

func NewCommonCgroupResource(resourceType ResourceType, filename string, subfs string) Resource {
	return &CgroupResource{Type: resourceType, FileName: filename, Subfs: subfs, Supported: pointer.Bool(true)}
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
	return NewCommonCgroupResource(ResourceType(filename), filename, subfs)
}

func (f *cgroupResourceFactoryImpl) NewV2(t ResourceType, filename string) Resource {
	return NewCommonCgroupResource(t, filename, CgroupV2Dir)
}

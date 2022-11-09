---
title: Koordlet Support Cgroups V2
authors:
  - "@saintube"
reviewers:
  - "@eahydra"
  - "@FillZpp"
  - "@hormes"
  - "@jasonliu747"
  - "@stormgbs"
  - "@zwzhang0107"
creation-date: 2022-10-18
last-updated: 2022-11-08
---
# Koordlet Support Cgroups V2

## Table of Contents

<!--ts-->
* [Koordlet Support Cgroups V2](#koordlet-support-cgroups-v2)
   * [Table of Contents](#table-of-contents)
   * [Glossary](#glossary)
   * [Summary](#summary)
   * [Motivation](#motivation)
      * [Goals](#goals)
      * [Non-Goals/Future Work](#non-goalsfuture-work)
   * [Proposal](#proposal)
      * [User Stories](#user-stories)
         * [Story 1](#story-1)
         * [Story 2](#story-2)
      * [Design Principles](#design-principles)
      * [API](#api)
         * [Cgroup Resource](#cgroup-resource)
         * [Resource Updater](#resource-updater)
            * [Resource Update Executor](#resource-update-executor)
         * [Cgroup Read](#cgroup-read)
   * [Alternatives](#alternatives)
   * [Implementation History](#implementation-history)
<!--te-->

## Glossary

Control Group v2 (cgroup v2): https://docs.kernel.org/admin-guide/cgroup-v2.html

## Summary

To support cgroups-v2 in koordlet, a module for operating system files is proposed for collecting resource utilization and applying resource management strategies on various system environments.

## Motivation

The Linux cgroups-v2 mechanism provided by the 5.x kernels tends to mature, provides memcg QoS, PSI, iocost, and other capabilities in container resource isolation and monitoring, and unifies the mapping of containers to different cgroup subsystems. Kubernetes has enabled cgroups-v2 by default since v1.25, providing Kubelet MemoryQoS and other functional support.
In the Koordinator <= v0.7, koordlet implements resource utilization collection, container resource management and dynamic QoS strategies based on Linux cgroups-v1 common subsystems such as cpu, cpuacct, cpuset and memcg. To exploit the kernel's high-version features and get compatible with Kubelet (>=v1.25), the koordlet should be able to run on the cgroups-v2 environment. On the other hand, the koordlet should keep the resource collection and management functionalities between cgroups-v1 and cgroups-v2 consistent.
Therefore, the module Resource Executor is provided to cover up the operations on system files on different environments. Other modules can simply call the resource executor when they want to read or write any cgroup resource, no longer perceiving the underlying differences of various systems (e.g. cgroups-v1 vs. cgroups-v2).

### Goals

- Define the APIs in Resource Executor module.
- Encapsulate all cgroups modifications into this module.
- Give examples about how to operate cgroups-v1 and v2.

### Non-Goals/Future Work

- Design new resource management strategies based on cgroups-v2.

## Proposal

### User Stories

#### Story 1

As a cluster administrator, I want to deploy the koordinator across different versions of kubernetes which may use cgroups-v2 by default.

#### Story 2

As a koordinator developer, I want to run the koordlet on the linux kernel 5.x that enables cgroups-v2.

### Design Principles

How we expect to operate cgroups:

1. Derive cgroup path from the pod meta.
2. To reduce IO cost, cacheable update since many updates take the same values.
3. Uniformly operate both the cgroups-v1 files and the similar cgroups-v2 files.
4. Extensible to operate VM-based container cgroups in guest OS.

How we achieve the cgroups-v2 support:

1. Refactor current cgroup-related data structures (e.g. `CgroupFile`, `ResourceUpdater`) to unify the operations on system files (e.g. cgroups, resctrl).
2. Clarify the new implementations for supporting cgroups-v2.

### API

#### System Resource

![image](/docs/images/system-resource.svg)

**SystemResource** is the interface of a system resource.

Here the old `CgroupFile` struct is refactored from cgroup-targeted (e.g. the field `Subfs`) to general defined for system resources.
For system files like `kubepods-slice/memory.limit_in_bytes`, `/proc/meminfo`, `resctrl/BE/schemata`, their parent directories usually represents the managed processes/hardware.
Their file reading and writing usually vary on the filenames but has the same pattern on different parent directories. So we extract the types of system resources from parent directories.
Besides, cgroups v1 and v2 can implement the same resource on different files. e.g. cgroups-v1's `cpu.cfs_quota_us` and `cpu.cfs_period_us` are both implemented in `cpu.max` on cgroups-v2.
Thus, the `ResourceType` is added to identify the finer-grained types of system resources.
One cgroup system resource is defined for one resource type, and one cgroup file may get mapped into multiple system resources. e.g. `cpu.max` is mapped into two resource types `cpu.cfs_quota_us` and `cpu.cfs_period_us`.
In addition, some system resources are limited to particular platforms. e.g. the cgroup `memory.min` is supported on upstream linux cgroups-v2 and Anolis cgroups-v1, but not compatible with upstream linux cgroups-v1.
The `IsSupported()` method is defined for checking the compatibility of system resources when trying to modify the files.

```go
type ResourceType string

type SystemResource interface {
	// the type of system resource. e.g. "cpu.cfs_quota_us", "cpu.cfs_period_us", "schemata"
	ResourceType() ResourceType
	// the generated system file path according to the given parent directory. e.g. "/host-cgroup/kubepods/kubepods-podxxx/cpu.shares"
	Path(parentDir string) string
	// whether the system resource is supported in current platform
	IsSupported() bool
	// whether the given value is valid for the system resource's content
	IsValid(v string) (bool, string)
	WithValidator(validator ResourceValidator) SystemResource
}

type ResourceValidator interface {
	Validate(value string) (isValid bool, msg string)
}
```

The implementation for cgroup resources are shown below, which trim the old `CgroupFile`.

```go
type CgroupResource struct {
	Type           ResourceType
	FileName       string
	Subfs          string
	Supported      *bool
	CheckSupported func() bool
	Validator      ResourceValidator
}

func (c *CgroupResource) ResourceType() ResourceType {
	if len(c.Type) > 0 {
		return c.Type
	}
	return GetDefaultSystemResourceType(c.Subfs, c.FileName)
}

func (c *CgroupResource) Path(parentDir string) string {
	return filepath.Join(sysutil.Conf.CgroupRootDir, c.Subfs, parentDir, c.FileName)
}

func (c *CgroupResource) IsSupported() bool {
	if c.Supported == nil {
		supported := c.CheckSupported()
		c.Supported = pointer.Int64(supported)
	}
	return *c.Supported
}

func (c *CgroupResource) IsValid(v string) (bool, string) {
	if c.Validator == nil {
		return true, ""
	}
	return c.Validator.Validate(v)
}

func (c *CgroupResource) WithValidator(validator ResourceValidator) SystemResource {
	c.Validator = validator
	return c
}

func GetDefaultSystemResourceType(subfs string, filename string) ResourceType {
	return ResourceType(filepath.Join(subfs, filename))
}
```

Registry of CgroupResource is defined as following. It is for generating and retrieving the various resource types of cgroups.

```go
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

func GetCurrentCgroupVersion() CgroupVersion {
	if sysutil.Conf.UseCgroupV2 {
		return CgroupVersionV2
	}
	return CgroupVersionV1
}

type CgroupResourceRegistry interface {
	Add(v CgroupVersion, s ...SystemResource)
	Get(v CgroupVersion, t ResourceType) (SystemResource, bool)
}

var DefaultFactory = NewCgroupResourceFactory()

type CgroupResourceFactory interface {
	New(t ResourceType, filename string, subfs string) SystemResource
	NewV2(t ResourceType, filename string) SystemResource
}

var (
	CPUStat          = DefaultFactory.New(CPUStatName, CPUStatName, CgroupCPUDir)
	CPUCFSQuota      = DefaultFactory.New(CPUCFSQuotaName, CPUCFSQuotaName, CgroupCPUDir)
	CPUCFSPeriod     = DefaultFactory.New(CPUCFSPeriodName, CPUCFSPeriodName, CgroupCPUDir)
	MemoryLimit      = DefaultFactory.New(MemoryLimitFileName, MemoryLimitFileName, CgroupMemDir)
	MemoryMin        = DefaultFactory.New(MemMinFileName, MemMinFileName, CgroupMemDir)

	knownCgroupResources = []SystemResource{
		CPUStat,
		CPUCFSQuota,
		CPUCFSPeriod,
		MemoryLimit,
		MemoryMin,
	}

	CPUQuotaV2  = DefaultFactory.NewV2(CPUCFSQuotaName, CPUMaxName)
	CPUPeriodV2 = DefaultFactory.NewV2(CPUCFSPeriodName, CPUMaxName)
	CPUStatV2   = DefaultFactory.NewV2(CPUStatName, CPUStatName)
	MemoryMinV2 = DefaultFactory.NewV2(MemMinFileName, MemMinFileName)

	knownCgroupV2Resources = []SystemResource{
		CPUQuotaV2,
		CPUPeriodV2,
		CPUStatV2,
		MemoryMinV2,
	}
)
```

#### Resource Updater

![image](/docs/images/resource-executor.svg)

**ResourceUpdater** defines an updatable system resource and can be implemented for updating cgroups.

The old `ResourceUpdater` is refactored to adapt `SystemResource`. In the old interface, a method `Key()` is defined to identify the updates for different system files.
However, the implementations of the `Key()` are always the file paths in practice. So we replace the method `Key()` with `Path()` which is more concise.
Moreover, the old `MergeableResourceUpdater` is removed, and its method `MergeUpdate()` is added into the new `ResourceUpdater`.
Non-mergeable updaters can simply return empty result to skip the merge update and keep almost the same as the old `ResourceUpdater`.

```go
type UpdateFunc func(resource ResourceUpdater) error

type MergeUpdateFunc func(resource ResourceUpdater) (ResourceUpdater, error)

type ResourceUpdater interface {
	ResourceType() systemresource.ResourceType
	Path() string
	Value() string
	Update() error
	MergeUpdate() (ResourceUpdater, error)
	GetLastUpdateTimestamp() time.Time
	UpdateLastUpdateTimestamp(time time.Time)
}
```

The implementation of CgroupResourceUpdater is shown below. It is compatible to both cgroups-v1 and cgroups-v2.

```go
type CgroupResourceUpdater struct {
	file      systemresource.SystemResource
	parentDir string
	value     string

	lastUpdateTimestamp time.Time
	updateFunc          UpdateFunc
	mergeUpdateFunc     MergeUpdateFunc // optional: only for merge updates
}

func (u *CgroupResourceUpdater) ResourceType() systemresource.ResourceType {
	return u.file.ResourceType()
}

func (u *CgroupResourceUpdater) Path() string {
	return u.file.Path(u.parentDir)
}

func (u *CgroupResourceUpdater) Value() string {
	return u.value
}

func (u *CgroupResourceUpdater) Update() error {
	return u.updateFunc(u)
}

func (u *CgroupResourceUpdater) MergeUpdate() (ResourceUpdater, error) {
	if u.mergeUpdateFunc == nil {
		return nil, u.updateFunc(u)
	}
	return u.mergeUpdateFunc(u)
}
```

Other Implementations:

```go
type DefaultResourceUpdater struct {
	value               string
	dir                 string
	file                string
	lastUpdateTimestamp time.Time
	updateFunc          UpdateFunc
}

type GuestCgroupResourceUpdater struct {
	*CgroupResourceUpdater
	sandboxID string // for execute the file operation inside the sandbox
}
```

Factory of CgroupResourceUpdater:

```go
func init() {
	DefaultCgroupUpdaterFactory.Register(systemresource.CPUBurst.ResourceType(), NewCommonCgroupUpdater)
}

type NewResourceUpdaterFunc func(resourceType systemresource.ResourceType, parentDir string, value string) (ResourceUpdater, error)

type ResourceUpdaterFactory interface {
	Register(resourceType systemresource.ResourceType, g NewResourceUpdaterFunc)
	New(resourceType systemresource.ResourceType, parentDir string, value string) (ResourceUpdater, error)
}

var DefaultCgroupUpdaterFactory = NewCgroupUpdaterFactory()

type CgroupUpdaterFactoryImpl struct {
	lock     sync.RWMutex
	registry map[systemresource.ResourceType]NewResourceUpdaterFunc
}

func NewCommonCgroupUpdater(resourceType systemresource.ResourceType, parentDir string, value string) (ResourceUpdater, error) {
	r, ok := systemresource.DefaultRegistry.Get(systemresource.GetCurrentCgroupVersion(), resourceType)
	if !ok {
		return nil, fmt.Errorf("%s not found in cgroup registry", resourceType)
	}
	return &CgroupResourceUpdater{
		file:       r,
		parentDir:  parentDir,
		value:      value,
		updateFunc: CommonCgroupUpdateFunc,
	}, nil
}
```

##### Resource Update Executor

**ResourceUpdateExecutor** does cacheable updates for system resources.

Other modules can invoke `Update()` or `UpdateBatch()` for general updates, either cacheable or non-cacheable.
The workers of executor merge duplicated updates asynchronously.
For some special cgroup files, e.g. `cpuset.cpus` and `memory.min`, which require the current cgroup's value is always a smaller or subset of the upper's, the executor allows to merge update their ancestor cgroups in the level order.

e.g. To update the `cpuset.cpus` of a BE pod into "4-5", we should check the cgroups of qos-level, pod-level and container-level.
0. Initially a BE pod's `cpuset.cpus` is "1-4", and the kubepods-besteffort's is "1-4".
1. update qos-level cgroups with merged value "1-5" (merge "1-4" with "4-5").
2. update pod-level cgroups with merged value "1-5" (merge "1-4" with "4-5").
3. update container-level cgroups with new value "4-5" (update with "4-5").
4. update pod-level cgroups with new value "4-5" (update with "4-5").
5. update qos-level cgroups with new value "4-5" (update with "4-5").

```go
type ResourceUpdateExecutor interface {
	Update(cacheable bool, resource ResourceUpdater) (updated bool, err error)
	UpdateBatch(cacheable bool, resources ...ResourceUpdater)
	// Leveled Update:
	// For cgroup files like `cpuset.cpus` and `memory.min`, the ancestor cgroup should have larger/broader value
	// than the lower. Thus a merge function is used for updating these cgroups by the level order:
	//  1. from upper to lower, merge the new cgroup value with the old value and update the merged value
	//  2. from lower to upper, update the new cgroup value
	// Compatibility: if mergeFunc is nil, skip the merge and do the general update
	LeveledUpdateBatch(cachable bool, resources [][]ResourceUpdater)
	Run(stopCh <-chan struct{})
}
```

Implementation of ResourceUpdateExecutor:

```go
var singleton ResourceUpdateExecutor

type UpdateRequest struct {
	Updater   ResourceUpdater
	Cacheable bool
}

type ResourceUpdateExecutorImpl struct {
	lock               sync.Mutex // used for leveled update
	resourceCache      *cache.Cache
	forceUpdateSeconds int
	workers            int
	updates            chan *UpdateRequest
}

func NewResourceUpdateExecutor() ResourceUpdateExecutor {
	if singleton == nil {
		singleton = newResourceUpdateExecutor()
	}
	return singleton
}

func (e *ResourceUpdateExecutorImpl) Update(cacheable bool, resource ResourceUpdater) (updated bool, err error) {
	if cacheable {
		return e.updateByCache(resource)
	}
	return true, e.update(resource)
}

func (e *ResourceUpdateExecutorImpl) Run(stopCh <-chan struct{}) {
	go e.resourceCache.Run(stopCh)
	for i := 0; i < e.workers; i++ {
		go e.process(stopCh)
	}
	<-stopCh
	close(e.updates)
}
```

#### Cgroup Read

**CgroupReader** defines unified cgroup reads operations.

```go
type CgroupReader interface {
	ReadCPUQuota(parentDir string) (int64, error)
	ReadCPUAcctStat(parentDir string) (*CPUAcctStat, error)
	ReadCPUStat(parentDir string) (*CPUStat, error)
	ReadMemoryLimit(parentDir string) (int64, error)
	ReadMemoryStat(parentDir string) (*MemoryStat, error)
}
```

CgroupReader Implementations:

```go
func NewCgroupReader() CgroupReader {
	if sysutil.Conf.UseCgroupV2 {
		return &CgroupV2Reader{}
	}
	return &CgroupV1Reader{}
}

// for cgroups-v1
type CgroupV1Reader struct{}

func (r *CgroupV1Reader) ReadCPUQuota(parentDir string) (int64, error) {
	s, err := sysutil.CgroupFileRead(parentDir, systemresource.CPUCFSQuota)
	if err != nil {
		return -1, fmt.Errorf("cannot read cgroup file, err: %v", err)
	}
	// content: "-1", "100000", ...
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("cannot parse cgroup value %s, err: %v", s, err)
	}
	return v, nil
}

// for cgroups-v2
type CgroupV2Reader struct{}

func (r *CgroupV2Reader) ReadCPUQuota(parentDir string) (int64, error) {
	s, err := sysutil.CgroupFileRead(parentDir, systemresource.CPUMaxV2)
	if err != nil {
		return -1, fmt.Errorf("cannot read cgroup file, err: %v", err)
	}
	// content: "max 100000", "100000 100000"
	ss := strings.Fields(s)
	if len(ss) != 2 {
		return -1, fmt.Errorf("cannot parse cgroup value %s, err: invalid pattern", s)
	}
	if ss[0] == sysutil.CgroupMaxSymbolStr {
		return -1, nil
	}
	v, err := strconv.ParseInt(ss[0], 10, 64)
	if err != nil {
		return -1, fmt.Errorf("cannot parse cgroup value %s, err: %v", s, err)
	}
	return v, nil
}
```

#### Example: QoS Manager

Here the CPU Burst Plugin scales container's cfs quota via the Resource Executor module:

```go
// CPUBurst Plugin
func (p *Plugin) applyContainerCFSQuota(podMeta *statesinformer.PodMeta, containerStat *corev1.ContainerStatus,
	curContainerCFS, deltaContainerCFS int64) error {
	// get current pod-level cfs quota
	curPodCFS, err := p.Executor().ReadCPUQuota(podMeta.CgroupDir)
	if err != nil {
		return err
	}

	if deltaContainerCFS > 0 { // cfs scale up, order: pod->container
		if curPodCFS > 0 { // for pod-level; no need to adjust pod cpu.cfs_quota if it is already -1
			targetPodCFS := curPodCFS + deltaContainerCFS
			podCFSValStr := strconv.FormatInt(targetPodCFS, 10)
			// use resource type `CPUCFSQuotaName` to retrieve the cgroup updater compatible to current system.
			// i.e. get updater of `cpu.cfs_quota_us` when cgroups-v1, or get updater of `cpu.max` when cgroups-v2
			updater, err := executor.DefaultCgroupUpdaterFactory.New(systemresource.CPUCFSQuotaName, podDir, podCFSValStr)
			if err != nil {
				return err
			}
			_, err := p.Executor().Update(true, updater)
			if err != nil {
				return err
			}
		}

		// for container-level
		containerDir, err := util.GetContainerCgroupPathWithKube(podMeta.CgroupDir, containerStat)
		if err != nil {
			return err
		}
		targetContainerCFS := curContainerCFS + deltaContainerCFS
		containerCFSValStr := strconv.FormatInt(targetContainerCFS, 10)
		updater, err := executor.DefaultCgroupUpdaterFactory.New(systemresource.CPUCFSQuotaName, containerDir, containerCFSValStr)
		if err != nil {
			return err
		}
		_, err := p.Executor().Update(true, updater)
		if err != nil {
			return err
		}
	}
	// ...
}
```

## Alternatives

1. Just add new *CgroupFile*s for cgroups-v2, let each module (collector, resmanager) adopts cgroups-v2.

To support cgroups-v2, the collector and the resmanager modules would implement a new set of methods for interface conversion and numerical calculation individually.

## Implementation History

- [X]  10/20/2022: Open PR for initial draft
- [ ]  11/08/2022: Update design details to resolve comments

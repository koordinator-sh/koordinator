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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CPUQOS enables cpu qos features.
type CPUQOS struct {
	// group identity value for pods, default = 0
	// NOTE: It takes effect if cpuPolicy = "groupIdentity".
	GroupIdentity *int64 `json:"groupIdentity,omitempty" validate:"omitempty,min=-1,max=2"`
	// cpu.idle value for pods, default = 0.
	// `1` means using SCHED_IDLE.
	// CGroup Idle (introduced since mainline Linux 5.15): https://lore.kernel.org/lkml/162971078674.25758.15464079371945307825.tip-bot2@tip-bot2/#r
	// NOTE: It takes effect if cpuPolicy = "coreSched".
	SchedIdle *int64 `json:"schedIdle,omitempty" validate:"omitempty,min=0,max=1"`
	// whether pods of the QoS class can expel the cgroup idle pods at the SMT-level. default = false
	// If set to true, pods of this QoS will use a dedicated core sched group for noise clean with the SchedIdle pods.
	// NOTE: It takes effect if cpuPolicy = "coreSched".
	CoreExpeller *bool `json:"coreExpeller,omitempty"`
}

type CPUQOSPolicy string

const (
	// CPUQOSPolicyGroupIdentity indicates the Group Identity is applied to ensure the CPU QoS.
	CPUQOSPolicyGroupIdentity CPUQOSPolicy = "groupIdentity"
	// CPUQOSPolicyCoreSched indicates the Linux Core Scheduling and CGroup Idle is applied to ensure the CPU QoS.
	CPUQOSPolicyCoreSched CPUQOSPolicy = "coreSched"
)

type NETQOSPolicy string

const (
	// NETQOSPolicyTC indicates implement netqos by tc.
	NETQOSPolicyTC NETQOSPolicy = "tc"
	// NETQOSPolicyTerwayQos indicates implement netqos by terway-qos.
	NETQOSPolicyTerwayQos NETQOSPolicy = "terway-qos"
)

// MemoryQOS enables memory qos features.
type MemoryQOS struct {
	// memcg qos
	// If enabled, memcg qos will be set by the agent, where some fields are implicitly calculated from pod spec.
	// 1. `memory.min` := spec.requests.memory * minLimitFactor / 100 (use 0 if requests.memory is not set)
	// 2. `memory.low` := spec.requests.memory * lowLimitFactor / 100 (use 0 if requests.memory is not set)
	// 3. `memory.limit_in_bytes` := spec.limits.memory (set $node.allocatable.memory if limits.memory is not set)
	// 4. `memory.high` := floor[(spec.requests.memory + throttlingFactor / 100 * (memory.limit_in_bytes or node allocatable memory - spec.requests.memory))/pageSize] * pageSize
	// MinLimitPercent specifies the minLimitFactor percentage to calculate `memory.min`, which protects memory
	// from global reclamation when memory usage does not exceed the min limit.
	// Close: 0.
	// +kubebuilder:validation:Minimum=0
	MinLimitPercent *int64 `json:"minLimitPercent,omitempty" validate:"omitempty,min=0,max=100"`
	// LowLimitPercent specifies the lowLimitFactor percentage to calculate `memory.low`, which TRIES BEST
	// protecting memory from global reclamation when memory usage does not exceed the low limit unless no unprotected
	// memcg can be reclaimed.
	// NOTE: `memory.low` should be larger than `memory.min`. If spec.requests.memory == spec.limits.memory,
	// pod `memory.low` and `memory.high` become invalid, while `memory.wmark_ratio` is still in effect.
	// Close: 0.
	// +kubebuilder:validation:Minimum=0
	LowLimitPercent *int64 `json:"lowLimitPercent,omitempty" validate:"omitempty,min=0,max=100"`
	// ThrottlingPercent specifies the throttlingFactor percentage to calculate `memory.high` with pod
	// memory.limits or node allocatable memory, which triggers memcg direct reclamation when memory usage exceeds.
	// Lower the factor brings more heavier reclaim pressure.
	// Close: 0.
	// +kubebuilder:validation:Minimum=0
	ThrottlingPercent *int64 `json:"throttlingPercent,omitempty" validate:"omitempty,min=0,max=100"`

	// wmark_ratio (Anolis OS required)
	// Async memory reclamation is triggered when cgroup memory usage exceeds `memory.wmark_high` and the reclamation
	// stops when usage is below `memory.wmark_low`. Basically,
	// `memory.wmark_high` := min(memory.high, memory.limit_in_bytes) * memory.memory.wmark_ratio
	// `memory.wmark_low` := min(memory.high, memory.limit_in_bytes) * (memory.wmark_ratio - memory.wmark_scale_factor)
	// WmarkRatio specifies `memory.wmark_ratio` that help calculate `memory.wmark_high`, which triggers async
	// memory reclamation when memory usage exceeds.
	// Close: 0. Recommended: 95.
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=0
	WmarkRatio *int64 `json:"wmarkRatio,omitempty" validate:"omitempty,min=0,max=100"`
	// WmarkScalePermill specifies `memory.wmark_scale_factor` that helps calculate `memory.wmark_low`, which
	// stops async memory reclamation when memory usage belows.
	// Close: 50. Recommended: 20.
	// +kubebuilder:validation:Maximum=1000
	// +kubebuilder:validation:Minimum=1
	WmarkScalePermill *int64 `json:"wmarkScalePermill,omitempty" validate:"omitempty,min=1,max=1000"`

	// wmark_min_adj (Anolis OS required)
	// WmarkMinAdj specifies `memory.wmark_min_adj` which adjusts per-memcg threshold for global memory
	// reclamation. Lower the factor brings later reclamation.
	// The adjustment uses different formula for different value range.
	// [-25, 0)：global_wmark_min' = global_wmark_min + (global_wmark_min - 0) * wmarkMinAdj
	// (0, 50]：global_wmark_min' = global_wmark_min + (global_wmark_low - global_wmark_min) * wmarkMinAdj
	// Close: [LSR:0, LS:0, BE:0]. Recommended: [LSR:-25, LS:-25, BE:50].
	// +kubebuilder:validation:Maximum=50
	// +kubebuilder:validation:Minimum=-25
	WmarkMinAdj *int64 `json:"wmarkMinAdj,omitempty" validate:"omitempty,min=-25,max=50"`

	// TODO: enhance the usages of oom priority and oom kill group
	PriorityEnable *int64 `json:"priorityEnable,omitempty" validate:"omitempty,min=0,max=1"`
	Priority       *int64 `json:"priority,omitempty" validate:"omitempty,min=0,max=12"`
	OomKillGroup   *int64 `json:"oomKillGroup,omitempty" validate:"omitempty,min=0,max=1"`
}

type PodMemoryQOSPolicy string

const (
	// PodMemoryQOSPolicyDefault indicates pod inherits node-level config
	PodMemoryQOSPolicyDefault PodMemoryQOSPolicy = "default"
	// PodMemoryQOSPolicyNone indicates pod disables memory qos
	PodMemoryQOSPolicyNone PodMemoryQOSPolicy = "none"
	// PodMemoryQOSPolicyAuto indicates pod uses a recommended config
	PodMemoryQOSPolicyAuto PodMemoryQOSPolicy = "auto"
)

type PodMemoryQOSConfig struct {
	// Policy indicates the qos plan; use "default" if empty
	Policy    PodMemoryQOSPolicy `json:"policy,omitempty"`
	MemoryQOS `json:",inline"`
}

// CPUQOSCfg stores node-level config of cpu qos
type CPUQOSCfg struct {
	// Enable indicates whether the cpu qos is enabled.
	Enable *bool `json:"enable,omitempty"`
	CPUQOS `json:",inline"`
}

// MemoryQOSCfg stores node-level config of memory qos
type MemoryQOSCfg struct {
	// Enable indicates whether the memory qos is enabled (default: false).
	// This field is used for node-level control, while pod-level configuration is done with MemoryQOS and `Policy`
	// instead of an `Enable` option. Please view the differences between MemoryQOSCfg and PodMemoryQOSConfig structs.
	Enable    *bool `json:"enable,omitempty"`
	MemoryQOS `json:",inline"`
}

type BlockType string

const (
	// Device, such as /dev/sdb
	// Only used for RootClass blk-iocost configuration
	BlockTypeDevice BlockType = "device"
	// LVM volume group
	BlockTypeVolumeGroup BlockType = "volumegroup"
	// Pod volume
	BlockTypePodVolume BlockType = "podvolume"
)

type IOCfg struct {
	// Throttling of IOPS
	// The value is set to 0, which indicates that the feature is disabled.
	// +kubebuilder:validation:Minimum=0
	ReadIOPS *int64 `json:"readIOPS,omitempty"`
	// +kubebuilder:validation:Minimum=0
	WriteIOPS *int64 `json:"writeIOPS,omitempty"`
	// Throttling of throughput
	// The value is set to 0, which indicates that the feature is disabled.
	// +kubebuilder:validation:Minimum=0
	ReadBPS *int64 `json:"readBPS,omitempty"`
	// +kubebuilder:validation:Minimum=0
	WriteBPS *int64 `json:"writeBPS,omitempty"`
	// This field is used to set the weight of a sub-group. Default value: 100. Valid values: 1 to 100.
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=1
	IOWeightPercent *int64 `json:"ioWeightPercent,omitempty"`
	// Configure the weight-based throttling feature of blk-iocost
	// Only used for RootClass
	// After blk-iocost is enabled, the kernel calculates the proportion of requests that exceed the read or write latency threshold out of all requests. When the proportion is greater than the read or write latency percentile (95%), the kernel considers the disk to be saturated and reduces the rate at which requests are sent to the disk.
	// the read latency threshold. Unit: microseconds.
	ReadLatency *int64 `json:"readLatency,omitempty"`
	// the write latency threshold. Unit: microseconds.
	WriteLatency *int64 `json:"writeLatency,omitempty"`
	// the read latency percentile
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=0
	ReadLatencyPercent *int64 `json:"readLatencyPercent,omitempty"`
	// the write latency percentile
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=0
	WriteLatencyPercent *int64 `json:"writeLatencyPercent,omitempty"`
	// configure the cost model of blkio-cost manually
	// whether the user model is enabled. Default value: false
	EnableUserModel *bool `json:"enableUserModel,omitempty"`
	// the read BPS of user model
	// +kubebuilder:validation:Minimum=1
	ModelReadBPS *int64 `json:"modelReadBPS,omitempty"`
	// the write BPS of user model
	// +kubebuilder:validation:Minimum=1
	ModelWriteBPS *int64 `json:"modelWriteBPS,omitempty"`
	// the sequential read iops of user model
	// +kubebuilder:validation:Minimum=1
	ModelReadSeqIOPS *int64 `json:"modelReadSeqIOPS,omitempty"`
	// the sequential write iops of user model
	// +kubebuilder:validation:Minimum=1
	ModelWriteSeqIOPS *int64 `json:"modelWriteSeqIOPS,omitempty"`
	// the random read iops of user model
	// +kubebuilder:validation:Minimum=1
	ModelReadRandIOPS *int64 `json:"modelReadRandIOPS,omitempty"`
	// the random write iops of user model
	// +kubebuilder:validation:Minimum=1
	ModelWriteRandIOPS *int64 `json:"modelWriteRandIOPS,omitempty"`
}

type BlockCfg struct {
	Name      string    `json:"name,omitempty"`
	BlockType BlockType `json:"type,omitempty"`
	IOCfg     IOCfg     `json:"ioCfg,omitempty"`
}

type BlkIOQOS struct {
	Blocks []*BlockCfg `json:"blocks,omitempty"`
}

type BlkIOQOSCfg struct {
	Enable   *bool `json:"enable,omitempty"`
	BlkIOQOS `json:",inline"`
}

type ResourceQOS struct {
	CPUQOS     *CPUQOSCfg     `json:"cpuQOS,omitempty"`
	MemoryQOS  *MemoryQOSCfg  `json:"memoryQOS,omitempty"`
	BlkIOQOS   *BlkIOQOSCfg   `json:"blkioQOS,omitempty"`
	ResctrlQOS *ResctrlQOSCfg `json:"resctrlQOS,omitempty"`
	NetworkQOS *NetworkQOSCfg `json:"networkQOS,omitempty"`
}

type NetworkQOSCfg struct {
	Enable     *bool `json:"enable,omitempty"`
	NetworkQOS `json:",inline"`
}

type NetworkQOS struct {
	// IngressRequest describes the minimum network bandwidth guaranteed in the ingress direction.
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=0
	IngressRequest *intstr.IntOrString `json:"ingressRequest,omitempty"`
	// IngressLimit describes the maximum network bandwidth can be used in the ingress direction,
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=100
	IngressLimit *intstr.IntOrString `json:"ingressLimit,omitempty"`

	// EgressRequest describes the minimum network bandwidth guaranteed in the egress direction.
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=0
	EgressRequest *intstr.IntOrString `json:"egressRequest,omitempty"`
	// EgressLimit describes the maximum network bandwidth can be used in the egress direction,
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=100
	EgressLimit *intstr.IntOrString `json:"egressLimit,omitempty"`
}

type ResourceQOSPolicies struct {
	// applied policy for the CPU QoS, default = "groupIdentity"
	CPUPolicy *CPUQOSPolicy `json:"cpuPolicy,omitempty"`

	// applied policy for the Net QoS, default = "tc"
	NETQOSPolicy *NETQOSPolicy `json:"netQOSPolicy,omitempty"`
}

type ResourceQOSStrategy struct {
	// Policies of pod QoS.
	Policies *ResourceQOSPolicies `json:"policies,omitempty"`

	// ResourceQOS for LSR pods.
	LSRClass *ResourceQOS `json:"lsrClass,omitempty"`

	// ResourceQOS for LS pods.
	LSClass *ResourceQOS `json:"lsClass,omitempty"`

	// ResourceQOS for BE pods.
	BEClass *ResourceQOS `json:"beClass,omitempty"`

	// ResourceQOS for system pods
	SystemClass *ResourceQOS `json:"systemClass,omitempty"`

	// ResourceQOS for root cgroup.
	CgroupRoot *ResourceQOS `json:"cgroupRoot,omitempty"`
}

type CPUSuppressPolicy string

const (
	CPUSetPolicy      CPUSuppressPolicy = "cpuset"
	CPUCfsQuotaPolicy CPUSuppressPolicy = "cfsQuota"
)

type CPUEvictPolicy string

const (
	EvictByRealLimitPolicy   CPUEvictPolicy = "evictByRealLimit"
	EvictByAllocatablePolicy CPUEvictPolicy = "evictByAllocatable"
)

type ResourceThresholdStrategy struct {
	// whether the strategy is enabled, default = false
	Enable *bool `json:"enable,omitempty"`

	// cpu suppress threshold percentage (0,100), default = 65
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=0
	CPUSuppressThresholdPercent *int64 `json:"cpuSuppressThresholdPercent,omitempty" validate:"omitempty,min=0,max=100"`
	// cpu suppress min percentage (0,100)
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=0
	CPUSuppressMinPercent *int64 `json:"cpuSuppressMinPercent,omitempty" validate:"omitempty,min=0,max=100"`
	// CPUSuppressPolicy
	CPUSuppressPolicy CPUSuppressPolicy `json:"cpuSuppressPolicy,omitempty"`

	// upper: memory evict threshold percentage (0,100), default = 70
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=0
	MemoryEvictThresholdPercent *int64 `json:"memoryEvictThresholdPercent,omitempty" validate:"omitempty,min=0,max=100,gtfield=MemoryEvictLowerPercent"`
	// lower: memory release util usage under MemoryEvictLowerPercent, default = MemoryEvictThresholdPercent - 2
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Minimum=0
	MemoryEvictLowerPercent *int64 `json:"memoryEvictLowerPercent,omitempty" validate:"omitempty,min=0,max=100,ltfield=MemoryEvictThresholdPercent"`

	// be.satisfactionRate = be.CPURealLimit/be.CPURequest
	// if be.satisfactionRate > CPUEvictBESatisfactionUpperPercent/100, then stop to evict.
	CPUEvictBESatisfactionUpperPercent *int64 `json:"cpuEvictBESatisfactionUpperPercent,omitempty" validate:"omitempty,min=0,max=100,gtfield=CPUEvictBESatisfactionLowerPercent"`
	// be.satisfactionRate = be.CPURealLimit/be.CPURequest; be.cpuUsage = be.CPUUsed/be.CPURealLimit
	// if be.satisfactionRate < CPUEvictBESatisfactionLowerPercent/100 && be.usage >= CPUEvictBEUsageThresholdPercent/100,
	// then start to evict pod, and will evict to ${CPUEvictBESatisfactionUpperPercent}
	CPUEvictBESatisfactionLowerPercent *int64 `json:"cpuEvictBESatisfactionLowerPercent,omitempty" validate:"omitempty,min=0,max=100,ltfield=CPUEvictBESatisfactionUpperPercent"`
	// if be.cpuUsage >= CPUEvictBEUsageThresholdPercent/100, then start to calculate the resources need to be released.
	CPUEvictBEUsageThresholdPercent *int64 `json:"cpuEvictBEUsageThresholdPercent,omitempty" validate:"omitempty,min=0,max=100"`
	// when avg(cpuusage) > CPUEvictThresholdPercent, will start to evict pod by cpu,
	// and avg(cpuusage) is calculated based on the most recent CPUEvictTimeWindowSeconds data
	CPUEvictTimeWindowSeconds *int64 `json:"cpuEvictTimeWindowSeconds,omitempty" validate:"omitempty,gt=0"`
	// CPUEvictPolicy defines the policy for the BECPUEvict feature.
	// Default: `evictByRealLimit`.
	CPUEvictPolicy CPUEvictPolicy `json:"cpuEvictPolicy,omitempty"`
}

// ResctrlQOSCfg stores node-level config of resctrl qos
type ResctrlQOSCfg struct {
	// Enable indicates whether the resctrl qos is enabled.
	Enable     *bool `json:"enable,omitempty"`
	ResctrlQOS `json:",inline"`
}

type ResctrlQOS struct {
	// LLC available range start for pods by percentage
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	CATRangeStartPercent *int64 `json:"catRangeStartPercent,omitempty" validate:"omitempty,min=0,max=100,ltfield=CATRangeEndPercent"`
	// LLC available range end for pods by percentage
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	CATRangeEndPercent *int64 `json:"catRangeEndPercent,omitempty" validate:"omitempty,min=0,max=100,gtfield=CATRangeStartPercent"`
	// MBA percent
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	MBAPercent *int64 `json:"mbaPercent,omitempty" validate:"omitempty,min=0,max=100"`
}

type CPUBurstPolicy string

const (
	// CPUBurstNone disables cpu burst policy
	CPUBurstNone CPUBurstPolicy = "none"
	// CPUBurstOnly enables cpu burst policy by setting cpu.cfs_burst_us
	CPUBurstOnly CPUBurstPolicy = "cpuBurstOnly"
	// CFSQuotaBurstOnly enables cfs quota burst policy by scale up cpu.cfs_quota_us if pod throttled
	CFSQuotaBurstOnly CPUBurstPolicy = "cfsQuotaBurstOnly"
	// CPUBurstAuto enables both
	CPUBurstAuto CPUBurstPolicy = "auto"
)

type CPUBurstConfig struct {
	Policy CPUBurstPolicy `json:"policy,omitempty"`
	// cpu burst percentage for setting cpu.cfs_burst_us, legal range: [0, 10000], default as 1000 (1000%)
	// +kubebuilder:validation:Maximum=10000
	// +kubebuilder:validation:Minimum=0
	CPUBurstPercent *int64 `json:"cpuBurstPercent,omitempty" validate:"omitempty,min=1,max=10000"`
	// pod cfs quota scale up ceil percentage, default = 300 (300%)
	CFSQuotaBurstPercent *int64 `json:"cfsQuotaBurstPercent,omitempty" validate:"omitempty,min=100"`
	// specifies a period of time for pod can use at burst, default = -1 (unlimited)
	CFSQuotaBurstPeriodSeconds *int64 `json:"cfsQuotaBurstPeriodSeconds,omitempty" validate:"omitempty,min=-1"`
}

type CPUBurstStrategy struct {
	CPUBurstConfig `json:",inline"`
	// scale down cfs quota if node cpu overload, default = 50
	SharePoolThresholdPercent *int64 `json:"sharePoolThresholdPercent,omitempty" validate:"omitempty,min=0,max=100"`
}

type SystemStrategy struct {
	// for /proc/sys/vm/min_free_kbytes, min_free_kbytes = minFreeKbytesFactor * nodeTotalMemory /10000
	MinFreeKbytesFactor *int64 `json:"minFreeKbytesFactor,omitempty" validate:"omitempty,gt=0"`
	// /proc/sys/vm/watermark_scale_factor
	WatermarkScaleFactor *int64 `json:"watermarkScaleFactor,omitempty" validate:"omitempty,gt=0,max=400"`
	// /sys/kernel/mm/memcg_reaper/reap_background
	MemcgReapBackGround *int64 `json:"memcgReapBackGround,omitempty" validate:"omitempty,min=0,max=1"`

	// TotalNetworkBandwidth indicates the overall network bandwidth, cluster manager can set this field, and default value taken from /sys/class/net/${NIC_NAME}/speed, unit: Mbps
	TotalNetworkBandwidth resource.Quantity `json:"totalNetworkBandwidth,omitempty"`
}

type PSIStrategy struct {
	PSIExport      *PSIExportConfig      `json:"psiExport,omitempty"`
	MemorySuppress *MemorySuppressConfig `json:"memorySuppress,omitempty"`
	GroupShare     *GroupShareConfig     `json:"groupShare,omitempty"`
	BudgetBalance  *BudgetBalanceConfig  `json:"budgetBalance,omitempty"`
}

type PSIExportConfig struct {
	// Enable indicates whether the psi exporter is enabled.
	Enable *bool `json:"enable,omitempty"`
	// Threshold indicates the report threshold for PSI, default is 2000 (20%) for each resource.
	Threshold *PSIExporterThresholdConfig `json:"threshold,omitempty"`
}

type PSIExporterThresholdConfig struct {
	// CPU PSI threshold
	CPU *PSIThreshold `json:"cpu,omitempty"`
	// Memory PSI threshold
	Memory *PSIThreshold `json:"memory,omitempty"`
	// IO PSI threshold
	IO *PSIThreshold `json:"io,omitempty"`
}

type PSIThreshold struct {
	// Avg10 indicates the average 10-second PSI threshold, range [0,10000] indicating [0%,100%].
	Avg10 int64 `json:"avg10,omitempty" validate:"min=0,max=10000"`
	// Avg60 indicates the average 60-second PSI threshold, range [0,10000] indicating [0%,100%].
	Avg60 int64 `json:"avg60,omitempty" validate:"min=0,max=10000"`
	// Avg300 indicates the average 300-second PSI threshold, range [0,10000] indicating [0%,100%].
	Avg300 int64 `json:"avg300,omitempty" validate:"min=0,max=10000"`
}

type MemorySuppressConfig struct {
	// Enable indicates whether the memory suppress is enabled.
	Enable *bool `json:"enable,omitempty"`
	// MinSpot indicates the pressure spot (ratio [0,10000] indicating [request,limit]) at which a pod begins to bear memory pressure, default is 5000 (0.5)
	MinSpot *int64 `json:"minSpot,omitempty" validate:"min=0,max=10000"`
	// MaxSpot indicates the pressure spot (ratio [0,10000] indicating [request,limit]) at which a pod bears max memory pressure, default is 9000 (0.9)
	MaxSpot *int64 `json:"maxSpot,omitempty" validate:"min=0,max=10000"`
	// GrowPeriods indicates the number of periods to grow memory from MinSpot to MaxSpot, default is 10
	GrowPeriods *int64 `json:"growPeriods,omitempty" validate:"min=1"`
	// KillPeriods indicates the number of periods to kill memory after MaxSpot is reached, default is 60
	KillPeriods *int64 `json:"killPeriods,omitempty" validate:"min=1"`
}

type GroupShareConfig struct {
	// Enable indicates whether the group share is enabled.
	Enable *bool `json:"enable,omitempty"`
	// GroupingAnnotationKey indicates the annotation key used to group pods, default is "koordinator.sh/grouping-hash"
	GroupingAnnotationKey *string `json:"groupingAnnotationKey,omitempty"`
	// LowerBound indicates the lower bound of weight a pod will keep from group share, range [0,10000] indicating [0%,100%], default is 5000 (0.5)
	LowerBound *int64 `json:"lowerBound,omitempty" validate:"min=0,max=10000"`
}

type BudgetBalanceConfig struct {
	// Enable indicates whether the budget balance is enabled.
	Enable *bool `json:"enable,omitempty"`
	// BasePrice indicates the base price of budget balance, range [0,10000] indicating [0,100], default is 50 (0.5)
	BasePrice *int64 `json:"basePrice,omitempty" validate:"min=0,max=10000"`
	// LowerBound indicates the lower bound of weight a pod will keep from budget balance, range [0,10000] indicating [0%,100%], default is 5000 (0.5)
	LowerBound *int64 `json:"lowerBound,omitempty" validate:"min=0,max=10000"`
}

// NodeSLOSpec defines the desired state of NodeSLO
type NodeSLOSpec struct {
	// BE pods will be limited if node resource usage overload
	ResourceUsedThresholdWithBE *ResourceThresholdStrategy `json:"resourceUsedThresholdWithBE,omitempty"`
	// QoS config strategy for pods of different qos-class
	ResourceQOSStrategy *ResourceQOSStrategy `json:"resourceQOSStrategy,omitempty"`
	// CPU Burst Strategy
	CPUBurstStrategy *CPUBurstStrategy `json:"cpuBurstStrategy,omitempty"`
	//node global system config
	SystemStrategy *SystemStrategy `json:"systemStrategy,omitempty"`
	// PSI config strategy
	PSIStrategy *PSIStrategy `json:"psiStrategy,omitempty"`
	// Third party extensions for NodeSLO
	Extensions *ExtensionsMap `json:"extensions,omitempty"`
	// QoS management for out-of-band applications
	HostApplications []HostApplicationSpec `json:"hostApplications,omitempty"`
}

// NodeSLOStatus defines the observed state of NodeSLO
type NodeSLOStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status

// NodeSLO is the Schema for the nodeslos API
type NodeSLO struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSLOSpec   `json:"spec,omitempty"`
	Status NodeSLOStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeSLOList contains a list of NodeSLO
type NodeSLOList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeSLO `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeSLO{}, &NodeSLOList{})
}

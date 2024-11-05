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

package sloconfig

import (
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

var defautExtensions = &slov1alpha1.ExtensionsMap{}

func getDefaultExtensionsMap() *slov1alpha1.ExtensionsMap {
	return defautExtensions.DeepCopy()
}

func RegisterDefaultExtensionsMap(extKey string, extCfg interface{}) {
	if defautExtensions.Object == nil {
		defautExtensions.Object = make(map[string]interface{})
	}
	defautExtensions.Object[extKey] = extCfg
}

// DefaultNodeSLOSpecConfig defines the default config of the nodeSLOSpec, which would be used by the resmgr
func DefaultNodeSLOSpecConfig() slov1alpha1.NodeSLOSpec {
	return slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: DefaultResourceThresholdStrategy(),
		ResourceQOSStrategy:         DefaultResourceQOSStrategy(),
		CPUBurstStrategy:            DefaultCPUBurstStrategy(),
		SystemStrategy:              DefaultSystemStrategy(),
		Extensions:                  DefaultExtensions(),
	}
}

func DefaultResourceThresholdStrategy() *slov1alpha1.ResourceThresholdStrategy {
	return &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.Bool(false),
		CPUSuppressThresholdPercent: pointer.Int64(65),
		CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
		MemoryEvictThresholdPercent: pointer.Int64(70),
		CPUEvictPolicy:              slov1alpha1.EvictByRealLimitPolicy,
	}
}

func DefaultCPUQOS(qos apiext.QoSClass) *slov1alpha1.CPUQOS {
	var cpuQOS *slov1alpha1.CPUQOS
	switch qos {
	case apiext.QoSLSR:
		cpuQOS = &slov1alpha1.CPUQOS{
			GroupIdentity: pointer.Int64(2),
			SchedIdle:     pointer.Int64(0),
			CoreExpeller:  pointer.Bool(true),
		}
	case apiext.QoSLS:
		cpuQOS = &slov1alpha1.CPUQOS{
			GroupIdentity: pointer.Int64(2),
			SchedIdle:     pointer.Int64(0),
			CoreExpeller:  pointer.Bool(true),
		}
	case apiext.QoSBE:
		cpuQOS = &slov1alpha1.CPUQOS{
			GroupIdentity: pointer.Int64(-1),
			// NOTE: Be careful to enable CPU Idle since it overrides and lock the cpu.shares/cpu.weight of the same
			// cgroup to a minimal value. This can affect other components like Kubelet which wants to write
			// cpu.shares/cpu.weight to other values.
			// https://git.kernel.org/pub/scm/linux/kernel/git/tip/tip.git/commit/?id=304000390f88d049c85e9a0958ac5567f38816ee
			SchedIdle:    pointer.Int64(0),
			CoreExpeller: pointer.Bool(false),
		}
	case apiext.QoSSystem:
		cpuQOS = &slov1alpha1.CPUQOS{
			GroupIdentity: pointer.Int64(0),
			SchedIdle:     pointer.Int64(0),
			CoreExpeller:  pointer.Bool(false),
		}
	default:
		klog.Infof("cpu qos has no auto config for qos %s", qos)
	}
	return cpuQOS
}

// TODO https://github.com/koordinator-sh/koordinator/pull/94#discussion_r858786733
func DefaultResctrlQOS(qos apiext.QoSClass) *slov1alpha1.ResctrlQOS {
	var resctrlQOS *slov1alpha1.ResctrlQOS
	switch qos {
	case apiext.QoSLSR:
		resctrlQOS = &slov1alpha1.ResctrlQOS{
			CATRangeStartPercent: pointer.Int64(0),
			CATRangeEndPercent:   pointer.Int64(100),
			MBAPercent:           pointer.Int64(100),
		}
	case apiext.QoSLS:
		resctrlQOS = &slov1alpha1.ResctrlQOS{
			CATRangeStartPercent: pointer.Int64(0),
			CATRangeEndPercent:   pointer.Int64(100),
			MBAPercent:           pointer.Int64(100),
		}
	case apiext.QoSBE:
		resctrlQOS = &slov1alpha1.ResctrlQOS{
			CATRangeStartPercent: pointer.Int64(0),
			CATRangeEndPercent:   pointer.Int64(30),
			MBAPercent:           pointer.Int64(100),
		}
	case apiext.QoSSystem:
		resctrlQOS = &slov1alpha1.ResctrlQOS{
			CATRangeStartPercent: pointer.Int64(0),
			CATRangeEndPercent:   pointer.Int64(100),
			MBAPercent:           pointer.Int64(100),
		}
	default:
		klog.Infof("resctrl qos has no auto config for qos %s", qos)
	}
	return resctrlQOS
}

// DefaultMemoryQOS returns the recommended configuration for memory qos strategy.
// Please refer to `apis/slo/v1alpha1` for the definition of each field.
// In the recommended configuration, all abilities of memcg qos are disable, including `MinLimitPercent`,
// `LowLimitPercent`, `ThrottlingPercent` since they are not fully beneficial to all scenarios. Whereas, they are still
// useful when the use case is determined. e.g. lock some memory to improve file read performance.
// Asynchronous memory reclaim is enabled by default to alleviate the direct reclaim pressure, including `WmarkRatio`
// and `WmarkScalePermill`. The watermark of async reclaim is not recommended to set too low, since lower the watermark
// the more excess reclamations.
// Memory min watermark grading corresponding to `WmarkMinAdj` is enabled. It benefits high-priority pods by postponing
// global reclaim when machine's free memory is below than `/proc/sys/vm/min_free_kbytes`.
func DefaultMemoryQOS(qos apiext.QoSClass) *slov1alpha1.MemoryQOS {
	var memoryQOS *slov1alpha1.MemoryQOS
	switch qos {
	case apiext.QoSLSR:
		memoryQOS = &slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64(0),
			LowLimitPercent:   pointer.Int64(0),
			ThrottlingPercent: pointer.Int64(0),
			WmarkRatio:        pointer.Int64(95),
			WmarkScalePermill: pointer.Int64(20),
			WmarkMinAdj:       pointer.Int64(-25),
			PriorityEnable:    pointer.Int64(0),
			Priority:          pointer.Int64(0),
			OomKillGroup:      pointer.Int64(0),
		}
	case apiext.QoSLS:
		memoryQOS = &slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64(0),
			LowLimitPercent:   pointer.Int64(0),
			ThrottlingPercent: pointer.Int64(0),
			WmarkRatio:        pointer.Int64(95),
			WmarkScalePermill: pointer.Int64(20),
			WmarkMinAdj:       pointer.Int64(-25),
			PriorityEnable:    pointer.Int64(0),
			Priority:          pointer.Int64(0),
			OomKillGroup:      pointer.Int64(0),
		}
	case apiext.QoSBE:
		memoryQOS = &slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64(0),
			LowLimitPercent:   pointer.Int64(0),
			ThrottlingPercent: pointer.Int64(0),
			WmarkRatio:        pointer.Int64(95),
			WmarkScalePermill: pointer.Int64(20),
			WmarkMinAdj:       pointer.Int64(50),
			PriorityEnable:    pointer.Int64(0),
			Priority:          pointer.Int64(0),
			OomKillGroup:      pointer.Int64(0),
		}
	case apiext.QoSSystem:
		memoryQOS = &slov1alpha1.MemoryQOS{
			MinLimitPercent:   pointer.Int64(0),
			LowLimitPercent:   pointer.Int64(0),
			ThrottlingPercent: pointer.Int64(0),
			WmarkRatio:        pointer.Int64(0),
			WmarkScalePermill: pointer.Int64(50),
			WmarkMinAdj:       pointer.Int64(0),
			PriorityEnable:    pointer.Int64(0),
			Priority:          pointer.Int64(0),
			OomKillGroup:      pointer.Int64(0),
		}
	default:
		klog.V(5).Infof("memory qos has no auto config for qos %s", qos)
	}
	return memoryQOS
}

func DefaultResourceQOSPolicies() *slov1alpha1.ResourceQOSPolicies {
	defaultCPUPolicy := slov1alpha1.CPUQOSPolicyGroupIdentity
	defaultNetQoSPolicy := slov1alpha1.NETQOSPolicyTC
	return &slov1alpha1.ResourceQOSPolicies{
		CPUPolicy:    &defaultCPUPolicy,
		NETQOSPolicy: &defaultNetQoSPolicy,
	}
}

func DefaultResourceQOSStrategy() *slov1alpha1.ResourceQOSStrategy {
	return &slov1alpha1.ResourceQOSStrategy{
		Policies: DefaultResourceQOSPolicies(),
		LSRClass: &slov1alpha1.ResourceQOS{
			CPUQOS: &slov1alpha1.CPUQOSCfg{
				Enable: pointer.Bool(false),
				CPUQOS: *DefaultCPUQOS(apiext.QoSLSR),
			},
			ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
				Enable:     pointer.Bool(false),
				ResctrlQOS: *DefaultResctrlQOS(apiext.QoSLSR),
			},
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable:    pointer.Bool(false),
				MemoryQOS: *DefaultMemoryQOS(apiext.QoSLSR),
			},
			BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
				Enable:   pointer.Bool(false),
				BlkIOQOS: *NoneBlkIOQOS(),
			},
			NetworkQOS: &slov1alpha1.NetworkQOSCfg{
				Enable:     pointer.Bool(false),
				NetworkQOS: *NoneNetworkQOS(),
			},
		},
		LSClass: &slov1alpha1.ResourceQOS{
			CPUQOS: &slov1alpha1.CPUQOSCfg{
				Enable: pointer.Bool(false),
				CPUQOS: *DefaultCPUQOS(apiext.QoSLS),
			},
			ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
				Enable:     pointer.Bool(false),
				ResctrlQOS: *DefaultResctrlQOS(apiext.QoSLS),
			},
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable:    pointer.Bool(false),
				MemoryQOS: *DefaultMemoryQOS(apiext.QoSLS),
			},
			BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
				Enable:   pointer.Bool(false),
				BlkIOQOS: *NoneBlkIOQOS(),
			},
			NetworkQOS: &slov1alpha1.NetworkQOSCfg{
				Enable:     pointer.Bool(false),
				NetworkQOS: *NoneNetworkQOS(),
			},
		},
		BEClass: &slov1alpha1.ResourceQOS{
			CPUQOS: &slov1alpha1.CPUQOSCfg{
				Enable: pointer.Bool(false),
				CPUQOS: *DefaultCPUQOS(apiext.QoSBE),
			},
			ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
				Enable:     pointer.Bool(false),
				ResctrlQOS: *DefaultResctrlQOS(apiext.QoSBE),
			},
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable:    pointer.Bool(false),
				MemoryQOS: *DefaultMemoryQOS(apiext.QoSBE),
			},
			BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
				Enable:   pointer.Bool(false),
				BlkIOQOS: *NoneBlkIOQOS(),
			},
			NetworkQOS: &slov1alpha1.NetworkQOSCfg{
				Enable:     pointer.Bool(false),
				NetworkQOS: *NoneNetworkQOS(),
			},
		},
		SystemClass: &slov1alpha1.ResourceQOS{
			CPUQOS: &slov1alpha1.CPUQOSCfg{
				Enable: pointer.Bool(false),
				CPUQOS: *DefaultCPUQOS(apiext.QoSSystem),
			},
			ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
				Enable:     pointer.Bool(false),
				ResctrlQOS: *DefaultResctrlQOS(apiext.QoSSystem),
			},
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable:    pointer.Bool(false),
				MemoryQOS: *DefaultMemoryQOS(apiext.QoSSystem),
			},
			BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
				Enable:   pointer.Bool(false),
				BlkIOQOS: *NoneBlkIOQOS(),
			},
			NetworkQOS: &slov1alpha1.NetworkQOSCfg{
				Enable:     pointer.Bool(false),
				NetworkQOS: *NoneNetworkQOS(),
			},
		},
		CgroupRoot: &slov1alpha1.ResourceQOS{
			BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
				Enable:   pointer.Bool(false),
				BlkIOQOS: *NoneBlkIOQOS(),
			},
		},
	}
}

func NoneResourceQOS(qos apiext.QoSClass) *slov1alpha1.ResourceQOS {
	// cgroup root case: only used by blkio qos
	if qos == apiext.QoSNone {
		return &slov1alpha1.ResourceQOS{
			BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
				Enable:   pointer.Bool(false),
				BlkIOQOS: *NoneBlkIOQOS(),
			},
		}
	}
	return &slov1alpha1.ResourceQOS{
		CPUQOS: &slov1alpha1.CPUQOSCfg{
			Enable: pointer.Bool(false),
			CPUQOS: *NoneCPUQOS(),
		},
		ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
			Enable:     pointer.Bool(false),
			ResctrlQOS: *NoneResctrlQOS(),
		},
		MemoryQOS: &slov1alpha1.MemoryQOSCfg{
			Enable:    pointer.Bool(false),
			MemoryQOS: *NoneMemoryQOS(),
		},
		BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
			Enable:   pointer.Bool(false),
			BlkIOQOS: *NoneBlkIOQOS(),
		},
		NetworkQOS: &slov1alpha1.NetworkQOSCfg{
			Enable:     pointer.Bool(false),
			NetworkQOS: *NoneNetworkQOS(),
		},
	}
}

func NoneCPUQOS() *slov1alpha1.CPUQOS {
	return &slov1alpha1.CPUQOS{
		GroupIdentity: pointer.Int64(0),
		SchedIdle:     pointer.Int64(0),
		CoreExpeller:  pointer.Bool(false),
	}
}

func NoneResctrlQOS() *slov1alpha1.ResctrlQOS {
	return &slov1alpha1.ResctrlQOS{
		CATRangeStartPercent: pointer.Int64(0),
		CATRangeEndPercent:   pointer.Int64(100),
		MBAPercent:           pointer.Int64(100),
	}
}

// NoneMemoryQOS returns the all-disabled configuration for memory qos strategy.
func NoneMemoryQOS() *slov1alpha1.MemoryQOS {
	return &slov1alpha1.MemoryQOS{
		MinLimitPercent:   pointer.Int64(0),
		LowLimitPercent:   pointer.Int64(0),
		ThrottlingPercent: pointer.Int64(0),
		WmarkRatio:        pointer.Int64(0),
		WmarkScalePermill: pointer.Int64(50),
		WmarkMinAdj:       pointer.Int64(0),
		PriorityEnable:    pointer.Int64(0),
		Priority:          pointer.Int64(0),
		OomKillGroup:      pointer.Int64(0),
	}
}

func NoneBlkIOQOS() *slov1alpha1.BlkIOQOS {
	return &slov1alpha1.BlkIOQOS{}
}

func NoneNetworkQOS() *slov1alpha1.NetworkQOS {
	zero := intstr.FromInt32(0)
	return &slov1alpha1.NetworkQOS{
		IngressRequest: &zero,
		IngressLimit:   &zero,
		EgressRequest:  &zero,
		EgressLimit:    &zero,
	}
}

func NoneResourceQOSPolicies() *slov1alpha1.ResourceQOSPolicies {
	noneCPUPolicy := slov1alpha1.CPUQOSPolicyGroupIdentity
	defaultNetQoSPolicy := slov1alpha1.NETQOSPolicyTC
	return &slov1alpha1.ResourceQOSPolicies{
		CPUPolicy:    &noneCPUPolicy,
		NETQOSPolicy: &defaultNetQoSPolicy,
	}
}

// NoneResourceQOSStrategy indicates the qos strategy with all qos
func NoneResourceQOSStrategy() *slov1alpha1.ResourceQOSStrategy {
	return &slov1alpha1.ResourceQOSStrategy{
		Policies:    NoneResourceQOSPolicies(),
		LSRClass:    NoneResourceQOS(apiext.QoSLSR),
		LSClass:     NoneResourceQOS(apiext.QoSLS),
		BEClass:     NoneResourceQOS(apiext.QoSBE),
		SystemClass: NoneResourceQOS(apiext.QoSSystem),
		CgroupRoot:  NoneResourceQOS(apiext.QoSNone),
	}
}

func DefaultCPUBurstStrategy() *slov1alpha1.CPUBurstStrategy {
	return &slov1alpha1.CPUBurstStrategy{
		CPUBurstConfig:            DefaultCPUBurstConfig(),
		SharePoolThresholdPercent: pointer.Int64(50),
	}
}

func DefaultCPUBurstConfig() slov1alpha1.CPUBurstConfig {
	return slov1alpha1.CPUBurstConfig{
		Policy:                     slov1alpha1.CPUBurstNone,
		CPUBurstPercent:            pointer.Int64(1000),
		CFSQuotaBurstPercent:       pointer.Int64(300),
		CFSQuotaBurstPeriodSeconds: pointer.Int64(-1),
	}
}

func DefaultSystemStrategy() *slov1alpha1.SystemStrategy {
	return &slov1alpha1.SystemStrategy{
		MinFreeKbytesFactor:   pointer.Int64(100), // 1 means 1/10000
		WatermarkScaleFactor:  pointer.Int64(150), // 1 means 1/10000
		MemcgReapBackGround:   pointer.Int64(0),
		TotalNetworkBandwidth: resource.MustParse("0"),
	}
}

func DefaultExtensions() *slov1alpha1.ExtensionsMap {
	return getDefaultExtensionsMap()
}

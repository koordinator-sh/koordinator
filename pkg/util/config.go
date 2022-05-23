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

package util

import (
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

// DefaultNodeSLOSpecConfig defines the default config of the nodeSLOSpec, which would be used by the resmgr
func DefaultNodeSLOSpecConfig() slov1alpha1.NodeSLOSpec {
	return slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: DefaultResourceThresholdStrategy(),
		ResourceQoSStrategy:         DefaultResourceQoSStrategy(),
		CPUBurstStrategy:            DefaultCPUBurstStrategy(),
	}
}

func DefaultResourceThresholdStrategy() *slov1alpha1.ResourceThresholdStrategy {
	return &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.BoolPtr(false),
		CPUSuppressThresholdPercent: pointer.Int64Ptr(65),
		CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
		MemoryEvictThresholdPercent: pointer.Int64Ptr(70),
	}
}

func DefaultCPUQoS(qos apiext.QoSClass) *slov1alpha1.CPUQoS {
	var cpuQoS *slov1alpha1.CPUQoS
	switch qos {
	case apiext.QoSLSR:
		cpuQoS = &slov1alpha1.CPUQoS{
			GroupIdentity: pointer.Int64Ptr(2),
		}
	case apiext.QoSLS:
		cpuQoS = &slov1alpha1.CPUQoS{
			GroupIdentity: pointer.Int64Ptr(2),
		}
	case apiext.QoSBE:
		cpuQoS = &slov1alpha1.CPUQoS{
			GroupIdentity: pointer.Int64Ptr(-1),
		}
	default:
		klog.Infof("cpu qos has no auto config for qos %s", qos)
	}
	return cpuQoS
}

// TODO https://github.com/koordinator-sh/koordinator/pull/94#discussion_r858786733
func DefaultResctrlQoS(qos apiext.QoSClass) *slov1alpha1.ResctrlQoS {
	var resctrlQoS *slov1alpha1.ResctrlQoS
	switch qos {
	case apiext.QoSLSR:
		resctrlQoS = &slov1alpha1.ResctrlQoS{
			CATRangeStartPercent: pointer.Int64Ptr(0),
			CATRangeEndPercent:   pointer.Int64Ptr(100),
			MBAPercent:           pointer.Int64Ptr(100),
		}
	case apiext.QoSLS:
		resctrlQoS = &slov1alpha1.ResctrlQoS{
			CATRangeStartPercent: pointer.Int64Ptr(0),
			CATRangeEndPercent:   pointer.Int64Ptr(100),
			MBAPercent:           pointer.Int64Ptr(100),
		}
	case apiext.QoSBE:
		resctrlQoS = &slov1alpha1.ResctrlQoS{
			CATRangeStartPercent: pointer.Int64Ptr(0),
			CATRangeEndPercent:   pointer.Int64Ptr(30),
			MBAPercent:           pointer.Int64Ptr(100),
		}
	default:
		klog.Infof("resctrl qos has no auto config for qos %s", qos)
	}
	return resctrlQoS
}

// DefaultMemoryQoS returns the recommended configuration for memory qos strategy.
// Please refer to `apis/slo/v1alpha1` for the definition of each field.
// In the recommended configuration, all abilities of memcg qos are disable, including `MinLimitPercent`,
// `LowLimitPercent`, `ThrottlingPercent` since they are not fully beneficial to all scenarios. Whereas, they are still
// useful when the use case is determined. e.g. lock some memory to improve file read performance.
// Asynchronous memory reclaim is enabled by default to alleviate the direct reclaim pressure, including `WmarkRatio`
// and `WmarkScalePermill`. The watermark of async reclaim is not recommended to set too low, since lower the watermark
// the more excess reclamations.
// Memory min watermark grading corresponding to `WmarkMinAdj` is enabled. It benefits high-priority pods by postponing
// global reclaim when machine's free memory is below than `/proc/sys/vm/min_free_kbytes`.
func DefaultMemoryQoS(qos apiext.QoSClass) *slov1alpha1.MemoryQoS {
	var memoryQoS *slov1alpha1.MemoryQoS
	switch qos {
	case apiext.QoSLSR:
		memoryQoS = &slov1alpha1.MemoryQoS{
			MinLimitPercent:   pointer.Int64Ptr(0),
			LowLimitPercent:   pointer.Int64Ptr(0),
			ThrottlingPercent: pointer.Int64Ptr(0),
			WmarkRatio:        pointer.Int64Ptr(95),
			WmarkScalePermill: pointer.Int64Ptr(20),
			WmarkMinAdj:       pointer.Int64Ptr(-25),
			PriorityEnable:    pointer.Int64Ptr(0),
			Priority:          pointer.Int64Ptr(0),
			OomKillGroup:      pointer.Int64Ptr(0),
		}
	case apiext.QoSLS:
		memoryQoS = &slov1alpha1.MemoryQoS{
			MinLimitPercent:   pointer.Int64Ptr(0),
			LowLimitPercent:   pointer.Int64Ptr(0),
			ThrottlingPercent: pointer.Int64Ptr(0),
			WmarkRatio:        pointer.Int64Ptr(95),
			WmarkScalePermill: pointer.Int64Ptr(20),
			WmarkMinAdj:       pointer.Int64Ptr(-25),
			PriorityEnable:    pointer.Int64Ptr(0),
			Priority:          pointer.Int64Ptr(0),
			OomKillGroup:      pointer.Int64Ptr(0),
		}
	case apiext.QoSBE:
		memoryQoS = &slov1alpha1.MemoryQoS{
			MinLimitPercent:   pointer.Int64Ptr(0),
			LowLimitPercent:   pointer.Int64Ptr(0),
			ThrottlingPercent: pointer.Int64Ptr(0),
			WmarkRatio:        pointer.Int64Ptr(95),
			WmarkScalePermill: pointer.Int64Ptr(20),
			WmarkMinAdj:       pointer.Int64Ptr(50),
			PriorityEnable:    pointer.Int64Ptr(0),
			Priority:          pointer.Int64Ptr(0),
			OomKillGroup:      pointer.Int64Ptr(0),
		}
	default:
		klog.V(5).Infof("memory qos has no auto config for qos %s", qos)
	}
	return memoryQoS
}

func DefaultResourceQoSStrategy() *slov1alpha1.ResourceQoSStrategy {
	return &slov1alpha1.ResourceQoSStrategy{
		LSR: &slov1alpha1.ResourceQoS{
			CPUQoS: &slov1alpha1.CPUQoSCfg{
				Enable: pointer.BoolPtr(false),
				CPUQoS: *DefaultCPUQoS(apiext.QoSLSR),
			},
			ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
				Enable:     pointer.BoolPtr(false),
				ResctrlQoS: *DefaultResctrlQoS(apiext.QoSLSR),
			},
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable:    pointer.BoolPtr(false),
				MemoryQoS: *DefaultMemoryQoS(apiext.QoSLSR),
			},
		},
		LS: &slov1alpha1.ResourceQoS{
			CPUQoS: &slov1alpha1.CPUQoSCfg{
				Enable: pointer.BoolPtr(false),
				CPUQoS: *DefaultCPUQoS(apiext.QoSLS),
			},
			ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
				Enable:     pointer.BoolPtr(false),
				ResctrlQoS: *DefaultResctrlQoS(apiext.QoSLS),
			},
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable:    pointer.BoolPtr(false),
				MemoryQoS: *DefaultMemoryQoS(apiext.QoSLS),
			},
		},
		BE: &slov1alpha1.ResourceQoS{
			CPUQoS: &slov1alpha1.CPUQoSCfg{
				Enable: pointer.BoolPtr(false),
				CPUQoS: *DefaultCPUQoS(apiext.QoSBE),
			},
			ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
				Enable:     pointer.BoolPtr(false),
				ResctrlQoS: *DefaultResctrlQoS(apiext.QoSBE),
			},
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable:    pointer.BoolPtr(false),
				MemoryQoS: *DefaultMemoryQoS(apiext.QoSBE),
			},
		},
	}
}

func NoneResourceQoS(qos apiext.QoSClass) *slov1alpha1.ResourceQoS {
	return &slov1alpha1.ResourceQoS{
		CPUQoS: &slov1alpha1.CPUQoSCfg{
			Enable: pointer.BoolPtr(false),
			CPUQoS: *NoneCPUQoS(),
		},
		ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
			Enable:     pointer.BoolPtr(false),
			ResctrlQoS: *NoneResctrlQoS(),
		},
		MemoryQoS: &slov1alpha1.MemoryQoSCfg{
			Enable:    pointer.BoolPtr(false),
			MemoryQoS: *NoneMemoryQoS(),
		},
	}
}

func NoneCPUQoS() *slov1alpha1.CPUQoS {
	return &slov1alpha1.CPUQoS{
		GroupIdentity: pointer.Int64(0),
	}
}

func NoneResctrlQoS() *slov1alpha1.ResctrlQoS {
	return &slov1alpha1.ResctrlQoS{
		CATRangeStartPercent: pointer.Int64Ptr(0),
		CATRangeEndPercent:   pointer.Int64Ptr(100),
		MBAPercent:           pointer.Int64Ptr(100),
	}
}

// NoneMemoryQoS returns the all-disabled configuration for memory qos strategy.
func NoneMemoryQoS() *slov1alpha1.MemoryQoS {
	return &slov1alpha1.MemoryQoS{
		MinLimitPercent:   pointer.Int64Ptr(0),
		LowLimitPercent:   pointer.Int64Ptr(0),
		ThrottlingPercent: pointer.Int64Ptr(0),
		WmarkRatio:        pointer.Int64Ptr(0),
		WmarkScalePermill: pointer.Int64Ptr(50),
		WmarkMinAdj:       pointer.Int64Ptr(0),
		PriorityEnable:    pointer.Int64Ptr(0),
		Priority:          pointer.Int64Ptr(0),
		OomKillGroup:      pointer.Int64Ptr(0),
	}
}

// NoneResourceQoSStrategy indicates the qos strategy with all qos
func NoneResourceQoSStrategy() *slov1alpha1.ResourceQoSStrategy {
	return &slov1alpha1.ResourceQoSStrategy{
		LSR: NoneResourceQoS(apiext.QoSLSR),
		LS:  NoneResourceQoS(apiext.QoSLS),
		BE:  NoneResourceQoS(apiext.QoSBE),
	}
}

func DefaultCPUBurstStrategy() *slov1alpha1.CPUBurstStrategy {
	return &slov1alpha1.CPUBurstStrategy{
		CPUBurstConfig:            DefaultCPUBurstConfig(),
		SharePoolThresholdPercent: pointer.Int64Ptr(50),
	}
}

func DefaultCPUBurstConfig() slov1alpha1.CPUBurstConfig {
	return slov1alpha1.CPUBurstConfig{
		Policy:                     slov1alpha1.CPUBurstNone,
		CPUBurstPercent:            pointer.Int64Ptr(1000),
		CFSQuotaBurstPercent:       pointer.Int64Ptr(300),
		CFSQuotaBurstPeriodSeconds: pointer.Int64Ptr(-1),
	}
}

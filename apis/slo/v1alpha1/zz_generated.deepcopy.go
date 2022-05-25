//go:build !ignore_autogenerated
// +build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	"k8s.io/api/core/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CPUBurstConfig) DeepCopyInto(out *CPUBurstConfig) {
	*out = *in
	if in.CPUBurstPercent != nil {
		in, out := &in.CPUBurstPercent, &out.CPUBurstPercent
		*out = new(int64)
		**out = **in
	}
	if in.CFSQuotaBurstPercent != nil {
		in, out := &in.CFSQuotaBurstPercent, &out.CFSQuotaBurstPercent
		*out = new(int64)
		**out = **in
	}
	if in.CFSQuotaBurstPeriodSeconds != nil {
		in, out := &in.CFSQuotaBurstPeriodSeconds, &out.CFSQuotaBurstPeriodSeconds
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CPUBurstConfig.
func (in *CPUBurstConfig) DeepCopy() *CPUBurstConfig {
	if in == nil {
		return nil
	}
	out := new(CPUBurstConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CPUBurstStrategy) DeepCopyInto(out *CPUBurstStrategy) {
	*out = *in
	in.CPUBurstConfig.DeepCopyInto(&out.CPUBurstConfig)
	if in.SharePoolThresholdPercent != nil {
		in, out := &in.SharePoolThresholdPercent, &out.SharePoolThresholdPercent
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CPUBurstStrategy.
func (in *CPUBurstStrategy) DeepCopy() *CPUBurstStrategy {
	if in == nil {
		return nil
	}
	out := new(CPUBurstStrategy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CPUQoS) DeepCopyInto(out *CPUQoS) {
	*out = *in
	if in.GroupIdentity != nil {
		in, out := &in.GroupIdentity, &out.GroupIdentity
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CPUQoS.
func (in *CPUQoS) DeepCopy() *CPUQoS {
	if in == nil {
		return nil
	}
	out := new(CPUQoS)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CPUQoSCfg) DeepCopyInto(out *CPUQoSCfg) {
	*out = *in
	if in.Enable != nil {
		in, out := &in.Enable, &out.Enable
		*out = new(bool)
		**out = **in
	}
	in.CPUQoS.DeepCopyInto(&out.CPUQoS)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CPUQoSCfg.
func (in *CPUQoSCfg) DeepCopy() *CPUQoSCfg {
	if in == nil {
		return nil
	}
	out := new(CPUQoSCfg)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MemoryQoS) DeepCopyInto(out *MemoryQoS) {
	*out = *in
	if in.MinLimitPercent != nil {
		in, out := &in.MinLimitPercent, &out.MinLimitPercent
		*out = new(int64)
		**out = **in
	}
	if in.LowLimitPercent != nil {
		in, out := &in.LowLimitPercent, &out.LowLimitPercent
		*out = new(int64)
		**out = **in
	}
	if in.ThrottlingPercent != nil {
		in, out := &in.ThrottlingPercent, &out.ThrottlingPercent
		*out = new(int64)
		**out = **in
	}
	if in.WmarkRatio != nil {
		in, out := &in.WmarkRatio, &out.WmarkRatio
		*out = new(int64)
		**out = **in
	}
	if in.WmarkScalePermill != nil {
		in, out := &in.WmarkScalePermill, &out.WmarkScalePermill
		*out = new(int64)
		**out = **in
	}
	if in.WmarkMinAdj != nil {
		in, out := &in.WmarkMinAdj, &out.WmarkMinAdj
		*out = new(int64)
		**out = **in
	}
	if in.PriorityEnable != nil {
		in, out := &in.PriorityEnable, &out.PriorityEnable
		*out = new(int64)
		**out = **in
	}
	if in.Priority != nil {
		in, out := &in.Priority, &out.Priority
		*out = new(int64)
		**out = **in
	}
	if in.OomKillGroup != nil {
		in, out := &in.OomKillGroup, &out.OomKillGroup
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MemoryQoS.
func (in *MemoryQoS) DeepCopy() *MemoryQoS {
	if in == nil {
		return nil
	}
	out := new(MemoryQoS)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MemoryQoSCfg) DeepCopyInto(out *MemoryQoSCfg) {
	*out = *in
	if in.Enable != nil {
		in, out := &in.Enable, &out.Enable
		*out = new(bool)
		**out = **in
	}
	in.MemoryQoS.DeepCopyInto(&out.MemoryQoS)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MemoryQoSCfg.
func (in *MemoryQoSCfg) DeepCopy() *MemoryQoSCfg {
	if in == nil {
		return nil
	}
	out := new(MemoryQoSCfg)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeMetric) DeepCopyInto(out *NodeMetric) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeMetric.
func (in *NodeMetric) DeepCopy() *NodeMetric {
	if in == nil {
		return nil
	}
	out := new(NodeMetric)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *NodeMetric) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeMetricCollectPolicy) DeepCopyInto(out *NodeMetricCollectPolicy) {
	*out = *in
	if in.AggregateDurationSeconds != nil {
		in, out := &in.AggregateDurationSeconds, &out.AggregateDurationSeconds
		*out = new(int64)
		**out = **in
	}
	if in.ReportIntervalSeconds != nil {
		in, out := &in.ReportIntervalSeconds, &out.ReportIntervalSeconds
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeMetricCollectPolicy.
func (in *NodeMetricCollectPolicy) DeepCopy() *NodeMetricCollectPolicy {
	if in == nil {
		return nil
	}
	out := new(NodeMetricCollectPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeMetricInfo) DeepCopyInto(out *NodeMetricInfo) {
	*out = *in
	in.NodeUsage.DeepCopyInto(&out.NodeUsage)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeMetricInfo.
func (in *NodeMetricInfo) DeepCopy() *NodeMetricInfo {
	if in == nil {
		return nil
	}
	out := new(NodeMetricInfo)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeMetricList) DeepCopyInto(out *NodeMetricList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]NodeMetric, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeMetricList.
func (in *NodeMetricList) DeepCopy() *NodeMetricList {
	if in == nil {
		return nil
	}
	out := new(NodeMetricList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *NodeMetricList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeMetricSpec) DeepCopyInto(out *NodeMetricSpec) {
	*out = *in
	if in.CollectPolicy != nil {
		in, out := &in.CollectPolicy, &out.CollectPolicy
		*out = new(NodeMetricCollectPolicy)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeMetricSpec.
func (in *NodeMetricSpec) DeepCopy() *NodeMetricSpec {
	if in == nil {
		return nil
	}
	out := new(NodeMetricSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeMetricStatus) DeepCopyInto(out *NodeMetricStatus) {
	*out = *in
	if in.UpdateTime != nil {
		in, out := &in.UpdateTime, &out.UpdateTime
		*out = (*in).DeepCopy()
	}
	if in.NodeMetric != nil {
		in, out := &in.NodeMetric, &out.NodeMetric
		*out = new(NodeMetricInfo)
		(*in).DeepCopyInto(*out)
	}
	if in.PodsMetric != nil {
		in, out := &in.PodsMetric, &out.PodsMetric
		*out = make([]*PodMetricInfo, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(PodMetricInfo)
				(*in).DeepCopyInto(*out)
			}
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeMetricStatus.
func (in *NodeMetricStatus) DeepCopy() *NodeMetricStatus {
	if in == nil {
		return nil
	}
	out := new(NodeMetricStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeSLO) DeepCopyInto(out *NodeSLO) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeSLO.
func (in *NodeSLO) DeepCopy() *NodeSLO {
	if in == nil {
		return nil
	}
	out := new(NodeSLO)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *NodeSLO) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeSLOList) DeepCopyInto(out *NodeSLOList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]NodeSLO, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeSLOList.
func (in *NodeSLOList) DeepCopy() *NodeSLOList {
	if in == nil {
		return nil
	}
	out := new(NodeSLOList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *NodeSLOList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeSLOSpec) DeepCopyInto(out *NodeSLOSpec) {
	*out = *in
	if in.ResourceUsedThresholdWithBE != nil {
		in, out := &in.ResourceUsedThresholdWithBE, &out.ResourceUsedThresholdWithBE
		*out = new(ResourceThresholdStrategy)
		(*in).DeepCopyInto(*out)
	}
	if in.ResourceQoSStrategy != nil {
		in, out := &in.ResourceQoSStrategy, &out.ResourceQoSStrategy
		*out = new(ResourceQoSStrategy)
		(*in).DeepCopyInto(*out)
	}
	if in.CPUBurstStrategy != nil {
		in, out := &in.CPUBurstStrategy, &out.CPUBurstStrategy
		*out = new(CPUBurstStrategy)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeSLOSpec.
func (in *NodeSLOSpec) DeepCopy() *NodeSLOSpec {
	if in == nil {
		return nil
	}
	out := new(NodeSLOSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeSLOStatus) DeepCopyInto(out *NodeSLOStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeSLOStatus.
func (in *NodeSLOStatus) DeepCopy() *NodeSLOStatus {
	if in == nil {
		return nil
	}
	out := new(NodeSLOStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodMemoryQoSConfig) DeepCopyInto(out *PodMemoryQoSConfig) {
	*out = *in
	in.MemoryQoS.DeepCopyInto(&out.MemoryQoS)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodMemoryQoSConfig.
func (in *PodMemoryQoSConfig) DeepCopy() *PodMemoryQoSConfig {
	if in == nil {
		return nil
	}
	out := new(PodMemoryQoSConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodMetricInfo) DeepCopyInto(out *PodMetricInfo) {
	*out = *in
	in.PodUsage.DeepCopyInto(&out.PodUsage)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodMetricInfo.
func (in *PodMetricInfo) DeepCopy() *PodMetricInfo {
	if in == nil {
		return nil
	}
	out := new(PodMetricInfo)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResctrlQoS) DeepCopyInto(out *ResctrlQoS) {
	*out = *in
	if in.CATRangeStartPercent != nil {
		in, out := &in.CATRangeStartPercent, &out.CATRangeStartPercent
		*out = new(int64)
		**out = **in
	}
	if in.CATRangeEndPercent != nil {
		in, out := &in.CATRangeEndPercent, &out.CATRangeEndPercent
		*out = new(int64)
		**out = **in
	}
	if in.MBAPercent != nil {
		in, out := &in.MBAPercent, &out.MBAPercent
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResctrlQoS.
func (in *ResctrlQoS) DeepCopy() *ResctrlQoS {
	if in == nil {
		return nil
	}
	out := new(ResctrlQoS)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResctrlQoSCfg) DeepCopyInto(out *ResctrlQoSCfg) {
	*out = *in
	if in.Enable != nil {
		in, out := &in.Enable, &out.Enable
		*out = new(bool)
		**out = **in
	}
	in.ResctrlQoS.DeepCopyInto(&out.ResctrlQoS)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResctrlQoSCfg.
func (in *ResctrlQoSCfg) DeepCopy() *ResctrlQoSCfg {
	if in == nil {
		return nil
	}
	out := new(ResctrlQoSCfg)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceMap) DeepCopyInto(out *ResourceMap) {
	*out = *in
	if in.ResourceList != nil {
		in, out := &in.ResourceList, &out.ResourceList
		*out = make(v1.ResourceList, len(*in))
		for key, val := range *in {
			(*out)[key] = val.DeepCopy()
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceMap.
func (in *ResourceMap) DeepCopy() *ResourceMap {
	if in == nil {
		return nil
	}
	out := new(ResourceMap)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceQoS) DeepCopyInto(out *ResourceQoS) {
	*out = *in
	if in.CPUQoS != nil {
		in, out := &in.CPUQoS, &out.CPUQoS
		*out = new(CPUQoSCfg)
		(*in).DeepCopyInto(*out)
	}
	if in.MemoryQoS != nil {
		in, out := &in.MemoryQoS, &out.MemoryQoS
		*out = new(MemoryQoSCfg)
		(*in).DeepCopyInto(*out)
	}
	if in.ResctrlQoS != nil {
		in, out := &in.ResctrlQoS, &out.ResctrlQoS
		*out = new(ResctrlQoSCfg)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceQoS.
func (in *ResourceQoS) DeepCopy() *ResourceQoS {
	if in == nil {
		return nil
	}
	out := new(ResourceQoS)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceQoSStrategy) DeepCopyInto(out *ResourceQoSStrategy) {
	*out = *in
	if in.LSR != nil {
		in, out := &in.LSR, &out.LSR
		*out = new(ResourceQoS)
		(*in).DeepCopyInto(*out)
	}
	if in.LS != nil {
		in, out := &in.LS, &out.LS
		*out = new(ResourceQoS)
		(*in).DeepCopyInto(*out)
	}
	if in.BE != nil {
		in, out := &in.BE, &out.BE
		*out = new(ResourceQoS)
		(*in).DeepCopyInto(*out)
	}
	if in.System != nil {
		in, out := &in.System, &out.System
		*out = new(ResourceQoS)
		(*in).DeepCopyInto(*out)
	}
	if in.CgroupRoot != nil {
		in, out := &in.CgroupRoot, &out.CgroupRoot
		*out = new(ResourceQoS)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceQoSStrategy.
func (in *ResourceQoSStrategy) DeepCopy() *ResourceQoSStrategy {
	if in == nil {
		return nil
	}
	out := new(ResourceQoSStrategy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceThresholdStrategy) DeepCopyInto(out *ResourceThresholdStrategy) {
	*out = *in
	if in.Enable != nil {
		in, out := &in.Enable, &out.Enable
		*out = new(bool)
		**out = **in
	}
	if in.CPUSuppressThresholdPercent != nil {
		in, out := &in.CPUSuppressThresholdPercent, &out.CPUSuppressThresholdPercent
		*out = new(int64)
		**out = **in
	}
	if in.MemoryEvictThresholdPercent != nil {
		in, out := &in.MemoryEvictThresholdPercent, &out.MemoryEvictThresholdPercent
		*out = new(int64)
		**out = **in
	}
	if in.MemoryEvictLowerPercent != nil {
		in, out := &in.MemoryEvictLowerPercent, &out.MemoryEvictLowerPercent
		*out = new(int64)
		**out = **in
	}
	if in.CPUEvictThresholdPercent != nil {
		in, out := &in.CPUEvictThresholdPercent, &out.CPUEvictThresholdPercent
		*out = new(int64)
		**out = **in
	}
	if in.CPUEvictBESatisfactionUpperPercent != nil {
		in, out := &in.CPUEvictBESatisfactionUpperPercent, &out.CPUEvictBESatisfactionUpperPercent
		*out = new(int64)
		**out = **in
	}
	if in.CPUEvictBESatisfactionLowerPercent != nil {
		in, out := &in.CPUEvictBESatisfactionLowerPercent, &out.CPUEvictBESatisfactionLowerPercent
		*out = new(int64)
		**out = **in
	}
	if in.CPUEvictTimeWindowSeconds != nil {
		in, out := &in.CPUEvictTimeWindowSeconds, &out.CPUEvictTimeWindowSeconds
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceThresholdStrategy.
func (in *ResourceThresholdStrategy) DeepCopy() *ResourceThresholdStrategy {
	if in == nil {
		return nil
	}
	out := new(ResourceThresholdStrategy)
	in.DeepCopyInto(out)
	return out
}

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

package extension

import "encoding/json"

const (
	// AnnotationContainerCgroupOverride carries JSON for phase-0 (annotation-driven) overrides.
	AnnotationContainerCgroupOverride = "koordinator.sh/cgroup-override"

	// AnnotationContainerCgroupOverrideSkip tells plugins / peers to leave hard limits alone.
	AnnotationContainerCgroupOverrideSkip = "koordinator.sh/cgroup-override-skip"

	// ExtensionContainerCgroupOverrides is the NodeSLO.Spec.Extensions key for phase-1 delivery.
	ExtensionContainerCgroupOverrides = "containerCgroupOverrides"

	// FinalizerContainerCgroupOverrideWriteback blocks CR delete until writeback completes.
	FinalizerContainerCgroupOverrideWriteback = "containercgroupoverride.slo.koordinator.sh/writeback"
)

// CgroupOverridePhase is reported on CR status / NodeSLO payload.
type CgroupOverridePhase string

const (
	CgroupOverridePhasePending   CgroupOverridePhase = "Pending"
	CgroupOverridePhaseApplied   CgroupOverridePhase = "Applied"
	CgroupOverridePhaseWriteback CgroupOverridePhase = "Writeback"
	CgroupOverridePhaseReleased  CgroupOverridePhase = "Released"
	CgroupOverridePhaseFailed    CgroupOverridePhase = "Failed"
)

// MemoryCgroupOverride is the memory controller hard-limit surface.
// Nested (ACK-style) so future knobs (high/min/low) can land without flattening Spec.
type MemoryCgroupOverride struct {
	// Max is a Quantity string (e.g. "512Mi") or "max".
	// Maps to memory.max (v2) / memory.limit_in_bytes (v1).
	Max string `json:"max,omitempty"`
}

// CPUCgroupOverride is the cpu / cpuset controller surface.
type CPUCgroupOverride struct {
	// Quota is a millicores Quantity string (e.g. "200m").
	// Maps to cpu.max (v2) / cpu.cfs_quota_us (v1); period from host unless Period set.
	Quota string `json:"quota,omitempty"`
	// Period is optional CFS period in microseconds; 0/empty = use host period.
	Period *int64 `json:"period,omitempty"`
	// CPUSet maps to cpuset.cpus (isolation experiments; optional).
	CPUSet string `json:"cpuset,omitempty"`
}

// BlkioCgroupOverride is reserved for device throttling (future; needs host /dev).
// Shape mirrors ACK Cgroups blkio without enabling it in v1alpha1 actuators yet.
type BlkioCgroupOverride struct {
	Weight          *int64             `json:"weight,omitempty"`
	WeightDevice    []BlkioDeviceValue `json:"weightDevice,omitempty"`
	DeviceReadBps   []BlkioDeviceValue `json:"deviceReadBps,omitempty"`
	DeviceReadIops  []BlkioDeviceValue `json:"deviceReadIops,omitempty"`
	DeviceWriteBps  []BlkioDeviceValue `json:"deviceWriteBps,omitempty"`
	DeviceWriteIops []BlkioDeviceValue `json:"deviceWriteIops,omitempty"`
}

// BlkioDeviceValue is one device throttle entry.
type BlkioDeviceValue struct {
	Device string `json:"device"`
	Value  string `json:"value"`
}

// ContainerCgroupResources is the nested desired (or baseline) cgroup hard limits.
// Prefer this over flat memoryMax/cpuQuota for extensibility.
type ContainerCgroupResources struct {
	Memory *MemoryCgroupOverride `json:"memory,omitempty"`
	CPU    *CPUCgroupOverride    `json:"cpu,omitempty"`
	// Blkio is schema-ready; koordlet ignores until explicitly implemented.
	Blkio *BlkioCgroupOverride `json:"blkio,omitempty"`
}

// Empty reports whether no controller fields are set.
func (r ContainerCgroupResources) Empty() bool {
	if r.Memory != nil && r.Memory.Max != "" {
		return false
	}
	if r.CPU != nil && (r.CPU.Quota != "" || r.CPU.CPUSet != "") {
		return false
	}
	if r.Blkio != nil {
		return false
	}
	return true
}

// MemoryMax returns resources.memory.max if set.
func (r ContainerCgroupResources) MemoryMax() string {
	if r.Memory == nil {
		return ""
	}
	return r.Memory.Max
}

// CPUQuota returns resources.cpu.quota if set.
func (r ContainerCgroupResources) CPUQuota() string {
	if r.CPU == nil {
		return ""
	}
	return r.CPU.Quota
}

// CPUSet returns resources.cpu.cpuset if set.
func (r ContainerCgroupResources) CPUSet() string {
	if r.CPU == nil {
		return ""
	}
	return r.CPU.CPUSet
}

// DeepCopyInto copies resources into out.
func (r *ContainerCgroupResources) DeepCopyInto(out *ContainerCgroupResources) {
	*out = *r
	if r.Memory != nil {
		m := *r.Memory
		out.Memory = &m
	}
	if r.CPU != nil {
		c := *r.CPU
		if r.CPU.Period != nil {
			p := *r.CPU.Period
			c.Period = &p
		}
		out.CPU = &c
	}
	if r.Blkio != nil {
		b := *r.Blkio
		if r.Blkio.Weight != nil {
			w := *r.Blkio.Weight
			b.Weight = &w
		}
		b.WeightDevice = append([]BlkioDeviceValue(nil), r.Blkio.WeightDevice...)
		b.DeviceReadBps = append([]BlkioDeviceValue(nil), r.Blkio.DeviceReadBps...)
		b.DeviceReadIops = append([]BlkioDeviceValue(nil), r.Blkio.DeviceReadIops...)
		b.DeviceWriteBps = append([]BlkioDeviceValue(nil), r.Blkio.DeviceWriteBps...)
		b.DeviceWriteIops = append([]BlkioDeviceValue(nil), r.Blkio.DeviceWriteIops...)
		out.Blkio = &b
	}
}

// DeepCopy returns a deep copy of resources.
func (r *ContainerCgroupResources) DeepCopy() *ContainerCgroupResources {
	if r == nil {
		return nil
	}
	out := new(ContainerCgroupResources)
	r.DeepCopyInto(out)
	return out
}

// NodeCgroupOverrideItem is one override delivered via NodeSLO.Extensions.
type NodeCgroupOverrideItem struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`

	PodNamespace  string `json:"podNamespace"`
	PodName       string `json:"podName"`
	PodUID        string `json:"podUID,omitempty"`
	ContainerName string `json:"containerName"`

	// Resources is the nested desired hard limits.
	Resources ContainerCgroupResources `json:"resources"`

	WritebackOnDelete bool `json:"writebackOnDelete"`

	// PendingDelete asks koordlet to writeback then acknowledge Released.
	PendingDelete bool `json:"pendingDelete,omitempty"`

	// Baseline is copied from CR.status by the aggregator (survives koordlet restart).
	Baseline *ContainerCgroupResources `json:"baseline,omitempty"`

	DesiredHash string `json:"desiredHash,omitempty"`
}

// NodeCgroupOverrides is the Extensions payload value.
type NodeCgroupOverrides struct {
	Items []NodeCgroupOverrideItem `json:"items,omitempty"`
}

// ParseNodeCgroupOverrides decodes an Extensions object value.
func ParseNodeCgroupOverrides(raw interface{}) (*NodeCgroupOverrides, error) {
	if raw == nil {
		return &NodeCgroupOverrides{}, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	out := &NodeCgroupOverrides{}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ResourcesEqual compares two nested resource trees for aggregator skip.
func ResourcesEqual(a, b *ContainerCgroupResources) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.MemoryMax() != b.MemoryMax() || a.CPUQuota() != b.CPUQuota() || a.CPUSet() != b.CPUSet() {
		return false
	}
	var ap, bp int64
	if a.CPU != nil && a.CPU.Period != nil {
		ap = *a.CPU.Period
	}
	if b.CPU != nil && b.CPU.Period != nil {
		bp = *b.CPU.Period
	}
	return ap == bp
}

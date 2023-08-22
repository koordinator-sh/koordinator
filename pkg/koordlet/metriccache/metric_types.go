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

package metriccache

import (
	"fmt"
	"time"
)

const (
	NodeCPUInfoKey          = "node_cpu_info"
	NodeNUMAInfoKey         = "node_numa_info"
	NodeLocalStorageInfoKey = "node_local_storage_info"
)

const (
	// metricLabelName is the key of metric kind saved in tsdb, e.g. { "__name__": "node_cpu_usage" } = 4.1 (cores)
	metricLabelName = "__name__"
)

// MetricKind represents all kind of metrics
type MetricKind string

const (
	NodeMetricCPUUsage     MetricKind = "node_cpu_usage"
	NodeMetricMemoryUsage  MetricKind = "node_memory_usage"
	NodeMetricGPUCoreUsage MetricKind = "node_gpu_core_usage"
	NodeMetricGPUMemUsage  MetricKind = "node_gpu_memory_usage"
	NodeMetricGPUMemTotal  MetricKind = "node_gpu_memory_total"

	SysMetricCPUUsage    MetricKind = "sys_cpu_usage"
	SysMetricMemoryUsage MetricKind = "sys_memory_usage"

	// NodeBE
	NodeMetricBE MetricKind = "node_be"

	PriorityMetricCPUUsage     MetricKind = "priority_cpu_usage"
	PriorityMetricCPURealLimit MetricKind = "priority_cpu_real_limit"
	PriorityMetricCPURequest   MetricKind = "priority_cpu_request"

	PodMetricCPUUsage     MetricKind = "pod_cpu_usage"
	PodMetricMemoryUsage  MetricKind = "pod_memory_usage"
	PodMetricGPUCoreUsage MetricKind = "pod_gpu_core_usage"
	PodMetricGPUMemUsage  MetricKind = "pod_gpu_memory_usage"
	// PodMetricGPUMemTotal       MetricKind = "pod_gpu_memory_total"

	ContainerMetricCPUUsage     MetricKind = "container_cpu_usage"
	ContainerMetricMemoryUsage  MetricKind = "container_memory_usage"
	ContainerMetricGPUCoreUsage MetricKind = "container_gpu_core_usage"
	ContainerMetricGPUMemUsage  MetricKind = "container_gpu_memory_usage"
	// ContainerMetricGPUMemTotal       MetricKind = "container_gpu_memory_total"

	PodMetricCPUThrottled       MetricKind = "pod_cpu_throttled"
	ContainerMetricCPUThrottled MetricKind = "container_cpu_throttled"

	// CPI
	ContainerMetricCPI MetricKind = "container_cpi"

	// PSI
	ContainerMetricPSI                 MetricKind = "container_psi"
	ContainerMetricPSICPUFullSupported MetricKind = "container_psi_cpu_full_supported"
	PodMetricPSI                       MetricKind = "pod_psi"
	PodMetricPSICPUFullSupported       MetricKind = "pod_psi_cpu_full_supported"

	//cold memory metircs
	NodeMemoryWithHotPageUsage      MetricKind = "node_memory_with_hot_page_uasge"
	PodMemoryWithHotPageUsage       MetricKind = "pod_memory_with_hot_page_uasge"
	ContainerMemoryWithHotPageUsage MetricKind = "container_memory_with_hot_page_uasge"
	NodeMemoryColdPageSize          MetricKind = "node_memory_cold_page_size"
	PodMemoryColdPageSize           MetricKind = "pod_memory_cold_page_size"
	ContainerMemoryColdPageSize     MetricKind = "container_memory_cold_page_size"
)

// MetricProperty is the property of metric
type MetricProperty string

const (
	MetricPropertyPodUID        MetricProperty = "pod_uid"
	MetricPropertyContainerID   MetricProperty = "container_id"
	MetricPropertyPriorityClass MetricProperty = "priority_class"
	MetricPropertyGPUMinor      MetricProperty = "gpu_minor"
	MetricPropertyGPUDeviceUUID MetricProperty = "gpu_device_uuid"

	MetricPropertyCPIResource MetricProperty = "cpi_resource"

	MetricPropertyPSIResource  MetricProperty = "psi_resource"
	MetricPropertyPSIPrecision MetricProperty = "psi_precision"
	MetricPropertyPSIDegree    MetricProperty = "psi_degree"

	MetricPropertyBEResource   MetricProperty = "be_resource"
	MetricPropertyBEAllocation MetricProperty = "be_allocation"
)

// MetricPropertyValue is the property value
type MetricPropertyValue string

const (
	CPIResourceCycle       MetricPropertyValue = "cycle"
	CPIResourceInstruction MetricPropertyValue = "instruction"

	PSIResourceCPU  MetricPropertyValue = "cpu"
	PSIResourceMem  MetricPropertyValue = "mem"
	PSIResourceIO   MetricPropertyValue = "io"
	PSIPrecision10  MetricPropertyValue = "10"
	PSIPrecision60  MetricPropertyValue = "60"
	PSIPrecision300 MetricPropertyValue = "300"
	PSIDegreeFull   MetricPropertyValue = "full"
	PSIDegreeSome   MetricPropertyValue = "some"

	BEResourceCPU                MetricPropertyValue = "cpu"
	BEResouceAllocationUsage     MetricPropertyValue = "usage"
	BEResouceAllocationRealLimit MetricPropertyValue = "real-limit"
	BEResouceAllocationRequest   MetricPropertyValue = "request"
)

// MetricPropertiesFunc is a collection of functions generating metric property k-v, for metric sample generation and query
var MetricPropertiesFunc = struct {
	Pod                 func(string) map[MetricProperty]string
	Container           func(string) map[MetricProperty]string
	GPU                 func(string, string) map[MetricProperty]string
	PSICPUFullSupported func(string, string) map[MetricProperty]string
	ContainerCPI        func(string, string, string) map[MetricProperty]string
	PodPSI              func(string, string, string, string) map[MetricProperty]string
	ContainerPSI        func(string, string, string, string, string) map[MetricProperty]string
	PodGPU              func(string, string, string) map[MetricProperty]string
	ContainerGPU        func(string, string, string) map[MetricProperty]string
	NodeBE              func(string, string) map[MetricProperty]string
}{
	Pod: func(podUID string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyPodUID: podUID}
	},
	Container: func(containerID string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyContainerID: containerID}
	},
	GPU: func(minor, uuid string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyGPUMinor: minor, MetricPropertyGPUDeviceUUID: uuid}
	},
	PSICPUFullSupported: func(podUID, containerID string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyPodUID: podUID, MetricPropertyContainerID: containerID}
	},
	ContainerCPI: func(podUID, containerID, cpiResource string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyPodUID: podUID, MetricPropertyContainerID: containerID, MetricPropertyCPIResource: cpiResource}
	},
	PodPSI: func(podUID, psiResource, psiPrecision, psiDegree string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyPodUID: podUID, MetricPropertyPSIResource: psiResource, MetricPropertyPSIPrecision: psiPrecision, MetricPropertyPSIDegree: psiDegree}
	},
	ContainerPSI: func(podUID, containerID, psiResource, psiPrecision, psiDegree string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyPodUID: podUID, MetricPropertyContainerID: containerID, MetricPropertyPSIResource: psiResource, MetricPropertyPSIPrecision: psiPrecision, MetricPropertyPSIDegree: psiDegree}
	},
	PodGPU: func(podUID, minor, uuid string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyPodUID: podUID, MetricPropertyGPUMinor: minor, MetricPropertyGPUDeviceUUID: uuid}
	},
	ContainerGPU: func(containerID, minor, uuid string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyContainerID: containerID, MetricPropertyGPUMinor: minor, MetricPropertyGPUDeviceUUID: uuid}
	},
	NodeBE: func(beResource, beResourceAllocation string) map[MetricProperty]string {
		return map[MetricProperty]string{MetricPropertyBEResource: beResource, MetricPropertyBEAllocation: beResourceAllocation}
	},
}

// point is the struct to describe metric
type Point struct {
	Timestamp time.Time
	Value     float64
}

// timestamp return the metric ts as milli-seconds
func (p *Point) timestamp() int64 {
	// use milli seconds here to follow prometheus.tsdb
	return p.Timestamp.UnixMilli()
}

// value return the exact value of point
func (p *Point) value() float64 {
	return p.Value
}

// MetricMeta is the meta info of metric
type MetricMeta interface {
	// GetKind should returns the metric kind like pod_cpu_usage, pod_cpu_throttled
	GetKind() string
	// GetProperties should return the property of metric like pod_uid, container_id, gpu_device_name
	GetProperties() map[string]string
}

var _ MetricMeta = &metricMeta{}

// metricMeta is an implementation of MetricMeta
type metricMeta struct {
	kind     MetricKind
	property map[string]string
}

func (m *metricMeta) GetKind() string {
	return string(m.kind)
}

func (m *metricMeta) GetProperties() map[string]string {
	return m.property
}

// MetricSample is a sample of specified metric, e.g. '{__name__: node_cpu_usage} = <2023-04-18:20:00:00, 4.1 core>'
type MetricSample interface {
	MetricMeta

	// timestamp returns the ts of metric
	timestamp() int64

	// value returns the metric value
	value() float64
}

// metricSample is an implementation of MetricSample
var _ MetricSample = &metricSample{}

type metricSample struct {
	metricMeta
	Point
}

// MetricResource represents the Metric struct like NodeCPUUsage, which is an abstract struct for generate sample or query meta
// All MetricResources should be init before using
type MetricResource interface {
	// withPropertySchema specifies the property schema of MetricResource
	withPropertySchema(propertySchema ...MetricProperty) MetricResource

	// GenerateSample produce the detailed sample according to the specified k-v properties, timestamp and value
	// All properties in the schema of MetricResource must be set, or it will return with error
	GenerateSample(properties map[MetricProperty]string, t time.Time, val float64) (MetricSample, error)

	// BuildQueryMeta produce the MetricMeta for query, properties for query must be already registered in schema,
	// or it will return with error
	BuildQueryMeta(properties map[MetricProperty]string) (MetricMeta, error)
}

// metricResource implements the MetricResource
var _ MetricResource = &metricResource{}

type metricResource struct {
	kind           MetricKind
	propertySchema map[MetricProperty]struct{}
}

func (m *metricResource) withPropertySchema(properties ...MetricProperty) MetricResource {
	for _, p := range properties {
		if m.propertySchema == nil {
			m.propertySchema = make(map[MetricProperty]struct{}, len(properties))
		}
		m.propertySchema[p] = struct{}{}
	}
	return m
}

func (m *metricResource) GenerateSample(properties map[MetricProperty]string, t time.Time, val float64) (MetricSample, error) {
	s := &metricSample{
		metricMeta: metricMeta{
			kind:     m.kind,
			property: make(map[string]string, len(properties)),
		},
		Point: Point{
			Timestamp: t,
			Value:     val,
		},
	}
	// all properties in schema must be set for new sample to be appended
	for k, v := range properties {
		if _, exist := m.propertySchema[k]; !exist {
			return nil, fmt.Errorf("property %v is not registered in metric %v", k, m.kind)
		}
		s.property[string(k)] = v
	}
	if len(s.property) != len(m.propertySchema) {
		return nil, fmt.Errorf("property is not fulled set, current %v, schema %v", properties, m.propertySchema)
	}
	return s, nil
}

func (m *metricResource) BuildQueryMeta(properties map[MetricProperty]string) (MetricMeta, error) {
	meta := &metricMeta{
		kind:     m.kind,
		property: make(map[string]string, len(properties)),
	}
	// property for query must be registered in schema
	for k, v := range properties {
		if _, exist := m.propertySchema[k]; !exist {
			return nil, fmt.Errorf("property %v is not registered in metric %v", k, m.kind)
		}
		meta.property[string(k)] = v
	}
	return meta, nil
}

// MetricFactory generates MetricResource by specified kind
type MetricFactory interface {
	// New generate MetricResource by giving kind
	New(metricKind MetricKind) MetricResource
}

func NewMetricFactory() MetricFactory {
	return &metricFactory{}
}

// metricFactory implements the MetricFactory
var _ MetricFactory = &metricFactory{}

type metricFactory struct{}

func (f *metricFactory) New(metricKind MetricKind) MetricResource {
	return &metricResource{
		kind:           metricKind,
		propertySchema: nil,
	}
}

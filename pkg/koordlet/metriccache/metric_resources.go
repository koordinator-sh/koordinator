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

var (
	defaultMetricFactory = NewMetricFactory()

	// define all kinds of MetricResource
	NodeCPUUsageMetric     = defaultMetricFactory.New(NodeMetricCPUUsage)
	NodeMemoryUsageMetric  = defaultMetricFactory.New(NodeMetricMemoryUsage)
	NodeGPUCoreUsageMetric = defaultMetricFactory.New(NodeMetricGPUCoreUsage).withPropertySchema(MetricPropertyGPUMinor, MetricPropertyGPUDeviceUUID)
	NodeGPUMemUsageMetric  = defaultMetricFactory.New(NodeMetricGPUMemUsage).withPropertySchema(MetricPropertyGPUMinor, MetricPropertyGPUDeviceUUID)
	NodeGPUMemTotalMetric  = defaultMetricFactory.New(NodeMetricGPUMemTotal).withPropertySchema(MetricPropertyGPUMinor, MetricPropertyGPUDeviceUUID)

	// define system resource usage as independent metric, although this can be calculate by node-sum(pod), but the time series are
	// unaligned across different type of metric, which makes it hard to aggregate.
	SystemCPUUsageMetric    = defaultMetricFactory.New(SysMetricCPUUsage)
	SystemMemoryUsageMetric = defaultMetricFactory.New(SysMetricMemoryUsage)

	PodCPUUsageMetric     = defaultMetricFactory.New(PodMetricCPUUsage).withPropertySchema(MetricPropertyPodUID)
	PodMemUsageMetric     = defaultMetricFactory.New(PodMetricMemoryUsage).withPropertySchema(MetricPropertyPodUID)
	PodCPUThrottledMetric = defaultMetricFactory.New(PodMetricCPUThrottled).withPropertySchema(MetricPropertyPodUID)
	PodGPUCoreUsageMetric = defaultMetricFactory.New(PodMetricGPUCoreUsage).withPropertySchema(MetricPropertyPodUID, MetricPropertyGPUMinor, MetricPropertyGPUDeviceUUID)
	PodGPUMemUsageMetric  = defaultMetricFactory.New(PodMetricGPUMemUsage).withPropertySchema(MetricPropertyPodUID, MetricPropertyGPUMinor, MetricPropertyGPUDeviceUUID)

	ContainerCPUUsageMetric     = defaultMetricFactory.New(ContainerMetricCPUUsage).withPropertySchema(MetricPropertyContainerID)
	ContainerMemUsageMetric     = defaultMetricFactory.New(ContainerMetricMemoryUsage).withPropertySchema(MetricPropertyContainerID)
	ContainerGPUCoreUsageMetric = defaultMetricFactory.New(ContainerMetricGPUCoreUsage).withPropertySchema(MetricPropertyContainerID, MetricPropertyGPUMinor, MetricPropertyGPUDeviceUUID)
	ContainerGPUMemUsageMetric  = defaultMetricFactory.New(ContainerMetricGPUMemUsage).withPropertySchema(MetricPropertyContainerID, MetricPropertyGPUMinor, MetricPropertyGPUDeviceUUID)
	ContainerCPUThrottledMetric = defaultMetricFactory.New(ContainerMetricCPUThrottled).withPropertySchema(MetricPropertyContainerID)
	//cold memory metrics
	NodeMemoryWithHotPageUsageMetric      = defaultMetricFactory.New(NodeMemoryWithHotPageUsage)
	PodMemoryWithHotPageUsageMetric       = defaultMetricFactory.New(PodMemoryWithHotPageUsage).withPropertySchema(MetricPropertyPodUID)
	ContainerMemoryWithHotPageUsageMetric = defaultMetricFactory.New(ContainerMemoryWithHotPageUsage).withPropertySchema(MetricPropertyContainerID)
	NodeMemoryColdPageSizeMetric          = defaultMetricFactory.New(NodeMemoryColdPageSize)
	PodMemoryColdPageSizeMetric           = defaultMetricFactory.New(PodMemoryColdPageSize).withPropertySchema(MetricPropertyPodUID)
	ContainerMemoryColdPageSizeMetric     = defaultMetricFactory.New(ContainerMemoryColdPageSize).withPropertySchema(MetricPropertyContainerID)

	// CPI
	ContainerCPI = defaultMetricFactory.New(ContainerMetricCPI).withPropertySchema(MetricPropertyPodUID, MetricPropertyContainerID, MetricPropertyCPIResource)

	// PSI
	ContainerPSIMetric                 = defaultMetricFactory.New(ContainerMetricPSI).withPropertySchema(MetricPropertyPodUID, MetricPropertyContainerID, MetricPropertyPSIResource, MetricPropertyPSIPrecision, MetricPropertyPSIDegree)
	ContainerPSICPUFullSupportedMetric = defaultMetricFactory.New(ContainerMetricPSICPUFullSupported).withPropertySchema(MetricPropertyPodUID, MetricPropertyContainerID)
	PodPSIMetric                       = defaultMetricFactory.New(PodMetricPSI).withPropertySchema(MetricPropertyPodUID, MetricPropertyPSIResource, MetricPropertyPSIPrecision, MetricPropertyPSIDegree)
	PodPSICPUFullSupportedMetric       = defaultMetricFactory.New(PodMetricPSICPUFullSupported).withPropertySchema(MetricPropertyPodUID)

	// BE
	NodeBEMetric = defaultMetricFactory.New(NodeMetricBE).withPropertySchema(MetricPropertyBEResource, MetricPropertyBEAllocation)
)

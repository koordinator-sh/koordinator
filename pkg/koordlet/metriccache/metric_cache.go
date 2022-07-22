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
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

type AggregationType string

type AggregationFunc func(interface{}, AggregateParam) (float64, error)

const (
	AggregationTypeAVG   AggregationType = "AVG"
	AggregationTypeP90   AggregationType = "P90"
	AggregationTypeLast  AggregationType = "last"
	AggregationTypeCount AggregationType = "count"
)

type QueryParam struct {
	Aggregate AggregationType
	Start     *time.Time
	End       *time.Time
}

type AggregateParam struct {
	ValueFieldName string
	TimeFieldName  string
}

type AggregateInfo struct {
	MetricsCount int64
}

type QueryResult struct {
	AggregateInfo *AggregateInfo
	Error         error
}

func (q *QueryParam) FillDefaultValue() {
	// todo, set start time as unix-zero if nil, set end as now if nil
}

type MetricCache interface {
	Run(stopCh <-chan struct{}) error
	GetNodeResourceMetric(param *QueryParam) NodeResourceQueryResult
	GetPodResourceMetric(podUID *string, param *QueryParam) PodResourceQueryResult
	GetContainerResourceMetric(containerID *string, param *QueryParam) ContainerResourceQueryResult
	GetNodeCPUInfo(param *QueryParam) (*NodeCPUInfo, error)
	GetBECPUResourceMetric(param *QueryParam) BECPUResourceQueryResult
	GetPodThrottledMetric(podUID *string, param *QueryParam) PodThrottledQueryResult
	GetContainerThrottledMetric(containerID *string, param *QueryParam) ContainerThrottledQueryResult
	InsertNodeResourceMetric(t time.Time, nodeResUsed *NodeResourceMetric) error
	InsertPodResourceMetric(t time.Time, podResUsed *PodResourceMetric) error
	InsertContainerResourceMetric(t time.Time, containerResUsed *ContainerResourceMetric) error
	InsertNodeCPUInfo(info *NodeCPUInfo) error
	InsertBECPUResourceMetric(t time.Time, metric *BECPUResourceMetric) error
	InsertPodThrottledMetrics(t time.Time, metric *PodThrottledMetric) error
	InsertContainerThrottledMetrics(t time.Time, metric *ContainerThrottledMetric) error
}

type metricCache struct {
	config *Config
	db     *storage
}

func NewMetricCache(cfg *Config) (MetricCache, error) {
	database, err := NewStorage()
	if err != nil {
		return nil, err
	}
	return &metricCache{
		config: cfg,
		db:     database,
	}, nil
}

func (m *metricCache) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	go wait.Until(func() {
		m.recycleDB()
	}, time.Duration(m.config.MetricGCIntervalSeconds)*time.Second, stopCh)

	return nil
}

func (m *metricCache) GetNodeResourceMetric(param *QueryParam) NodeResourceQueryResult {
	result := NodeResourceQueryResult{}
	if param == nil || param.Start == nil || param.End == nil {
		result.Error = fmt.Errorf("node query parameters are illegal %v", param)
		return result
	}
	metrics, err := m.db.GetNodeResourceMetric(param.Start, param.End)
	if err != nil {
		result.Error = fmt.Errorf("get node resource metric failed, query params %v, error %v", param, err)
		return result
	}
	if len(metrics) == 0 {
		result.Error = fmt.Errorf("get node resource metric not exist, query params %v", param)
		return result
	}

	aggregateFunc := getAggregateFunc(param.Aggregate)
	cpuUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPUUsedCores", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate CPUUsedCores failed, metrics %v, error %v", metrics, err)
		return result
	}
	memoryUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "MemoryUsedBytes", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate MemoryUsedBytes failed, metrics %v, error %v", metrics, err)
		return result
	}

	// gpu metrics time series.
	// m.GPUs is a slice.
	gpuUsagesByTime := make([][]gpuResourceMetric, 0)
	for _, m := range metrics {
		if len(m.GPUs) == 0 {
			continue
		}
		gpuUsagesByTime = append(gpuUsagesByTime, m.GPUs)
	}

	var aggregateGPUMetrics []GPUMetric
	if len(gpuUsagesByTime) > 0 {
		aggregateGPUMetrics, err = m.aggregateGPUUsages(gpuUsagesByTime, aggregateFunc)
		if err != nil {
			result.Error = fmt.Errorf("get node aggregate GPUMetric failed, metrics %v, error %v", metrics, err)
			return result
		}
	}

	count, err := count(metrics)
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate count failed, metrics %v, error %v", metrics, err)
		return result
	}

	result.AggregateInfo = &AggregateInfo{MetricsCount: int64(count)}
	result.Metric = &NodeResourceMetric{
		CPUUsed: CPUMetric{
			CPUUsed: *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI),
		},
		MemoryUsed: MemoryMetric{
			MemoryWithoutCache: *resource.NewQuantity(int64(memoryUsed), resource.BinarySI),
		},
		GPUs: aggregateGPUMetrics,
	}

	return result
}

func (m *metricCache) GetPodResourceMetric(podUID *string, param *QueryParam) PodResourceQueryResult {
	result := PodResourceQueryResult{}
	if podUID == nil || param == nil || param.Start == nil || param.End == nil {
		result.Error = fmt.Errorf("pod %v query parameters are illegal %v", podUID, param)
		return result
	}
	metrics, err := m.db.GetPodResourceMetric(podUID, param.Start, param.End)
	if err != nil {
		result.Error = fmt.Errorf("get pod %v resource metric failed, query params %v, error %v", *podUID, param, err)
		return result
	}
	if len(metrics) == 0 {
		result.Error = fmt.Errorf("get pod %v resource metric not exist, query params %v", *podUID, param)
		return result
	}

	aggregateFunc := getAggregateFunc(param.Aggregate)
	cpuUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPUUsedCores", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get pod %v aggregate CPUUsedCores failed, metrics %v, error %v",
			*podUID, metrics, err)
		return result
	}
	memoryUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "MemoryUsedBytes", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get pod %v aggregate MemoryUsedBytes failed, metrics %v, error %v",
			*podUID, metrics, err)
		return result
	}

	// gpu metrics time series.
	// m.GPUs is a slice.
	gpuUsagesByTime := make([][]gpuResourceMetric, 0)
	for _, m := range metrics {
		if len(m.GPUs) == 0 {
			continue
		}
		gpuUsagesByTime = append(gpuUsagesByTime, m.GPUs)
	}

	var aggregateGPUMetrics []GPUMetric
	if len(gpuUsagesByTime) > 0 {
		aggregateGPUMetrics, err = m.aggregateGPUUsages(gpuUsagesByTime, aggregateFunc)
		if err != nil {
			result.Error = fmt.Errorf("get pod aggregate GPUMetric failed, metrics %v, error %v", metrics, err)
			return result
		}
	}

	count, err := count(metrics)
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate count failed, metrics %v, error %v", metrics, err)
		return result
	}

	result.AggregateInfo = &AggregateInfo{MetricsCount: int64(count)}
	result.Metric = &PodResourceMetric{
		PodUID: *podUID,
		CPUUsed: CPUMetric{
			CPUUsed: *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI),
		},
		MemoryUsed: MemoryMetric{
			MemoryWithoutCache: *resource.NewQuantity(int64(memoryUsed), resource.BinarySI),
		},
		GPUs: aggregateGPUMetrics,
	}

	return result
}

func (m *metricCache) GetContainerResourceMetric(containerID *string, param *QueryParam) ContainerResourceQueryResult {
	result := ContainerResourceQueryResult{}
	if containerID == nil || param == nil || param.Start == nil || param.End == nil {
		result.Error = fmt.Errorf("container %v query parameters are illegal %v", containerID, param)
		return result
	}
	metrics, err := m.db.GetContainerResourceMetric(containerID, param.Start, param.End)
	if err != nil {
		result.Error = fmt.Errorf("get container %v resource metric failed, query params %v, error %v",
			containerID, param, err)
		return result
	}
	if len(metrics) == 0 {
		result.Error = fmt.Errorf("get container %v resource metric not exist, query params %v", containerID, param)
		return result
	}

	aggregateFunc := getAggregateFunc(param.Aggregate)
	cpuUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPUUsedCores", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get container %v aggregate CPUUsedCores failed, metrics %v, error %v",
			containerID, metrics, err)
		return result
	}
	memoryUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "MemoryUsedBytes", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get container %v aggregate MemoryUsedBytes failed, metrics %v, error %v",
			containerID, metrics, err)
		return result
	}

	count, err := count(metrics)
	if err != nil {
		result.Error = fmt.Errorf("get container aggregate count failed, metrics %v, error %v", metrics, err)
		return result
	}

	// gpu metrics time series.
	// m.GPUs is a slice.
	gpuUsagesByTime := make([][]gpuResourceMetric, 0)
	for _, m := range metrics {
		if len(m.GPUs) == 0 {
			continue
		}
		gpuUsagesByTime = append(gpuUsagesByTime, m.GPUs)
	}

	var aggregateGPUMetrics []GPUMetric
	if len(gpuUsagesByTime) > 0 {
		aggregateGPUMetrics, err = m.aggregateGPUUsages(gpuUsagesByTime, aggregateFunc)
		if err != nil {
			result.Error = fmt.Errorf("get container aggregate GPUMetric failed, metrics %v, error %v", metrics, err)
			return result
		}
	}

	result.AggregateInfo = &AggregateInfo{MetricsCount: int64(count)}
	result.Metric = &ContainerResourceMetric{
		ContainerID: *containerID,
		CPUUsed: CPUMetric{
			CPUUsed: *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI),
		},
		MemoryUsed: MemoryMetric{
			MemoryWithoutCache: *resource.NewQuantity(int64(memoryUsed), resource.BinarySI),
		},
		GPUs: aggregateGPUMetrics,
	}
	return result
}

func (m *metricCache) GetBECPUResourceMetric(param *QueryParam) BECPUResourceQueryResult {
	result := BECPUResourceQueryResult{}
	if param == nil || param.Start == nil || param.End == nil {
		result.Error = fmt.Errorf("BECPUResourceMetric query parameters are illegal %v", param)
		return result
	}
	metrics, err := m.db.GetBECPUResourceMetric(param.Start, param.End)
	if err != nil {
		result.Error = fmt.Errorf("get BECPUResourceMetric failed, query params %v, error %v", param, err)
		return result
	}
	if len(metrics) == 0 {
		result.Error = fmt.Errorf("get BECPUResourceMetric not exist, query params %v", param)
		return result
	}

	aggregateFunc := getAggregateFunc(param.Aggregate)
	cpuUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPUUsedCores", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate CPUUsedCores failed, metrics %v, error %v", metrics, err)
		return result
	}
	cpuLimit, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPULimitCores", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate CPULimitCores failed, metrics %v, error %v", metrics, err)
		return result
	}

	cpuRequest, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPURequestCores", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate CPURequestCores failed, metrics %v, error %v", metrics, err)
		return result
	}

	count, err := count(metrics)
	if err != nil {
		result.Error = fmt.Errorf("get node aggregate count failed, metrics %v, error %v", metrics, err)
		return result
	}

	result.AggregateInfo = &AggregateInfo{MetricsCount: int64(count)}
	result.Metric = &BECPUResourceMetric{
		CPUUsed:      *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI),
		CPURealLimit: *resource.NewMilliQuantity(int64(cpuLimit*1000), resource.DecimalSI),
		CPURequest:   *resource.NewMilliQuantity(int64(cpuRequest*1000), resource.DecimalSI),
	}
	return result
}

func (m *metricCache) GetNodeCPUInfo(param *QueryParam) (*NodeCPUInfo, error) {
	// get node cpu info from the rawRecordTable
	if param == nil {
		return nil, fmt.Errorf("node cpu info query parameters are illegal %v", param)
	}
	record, err := m.db.GetRawRecord(NodeCPUInfoRecordType)
	if err != nil {
		return nil, fmt.Errorf("get node cpu info failed, query params %v, err %v", param, err)
	}

	info := &NodeCPUInfo{}
	if err := json.Unmarshal([]byte(record.RecordStr), info); err != nil {
		return nil, fmt.Errorf("get node cpu info failed, parse recordStr %v, err %v", record.RecordStr, err)
	}
	return info, nil
}

func (m *metricCache) GetPodThrottledMetric(podUID *string, param *QueryParam) PodThrottledQueryResult {
	result := PodThrottledQueryResult{}
	if param == nil || param.Start == nil || param.End == nil {
		result.Error = fmt.Errorf("GetPodThrottledMetric %v query parameters are illegal %v", podUID, param)
		return result
	}
	metrics, err := m.db.GetPodThrottledMetric(podUID, param.Start, param.End)
	if err != nil {
		result.Error = fmt.Errorf("GetPodThrottledMetric %v failed, query params %v, error %v", podUID, param, err)
		return result
	}
	if len(metrics) == 0 {
		result.Error = fmt.Errorf("GetPodThrottledMetric %v failed, query params %v, error %v", podUID, param, err)
		return result
	}

	aggregateFunc := getAggregateFunc(param.Aggregate)
	throttledRatio, err := aggregateFunc(metrics, AggregateParam{
		ValueFieldName: "CPUThrottledRatio", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("GetPodThrottledMetric %v aggregate CPUUsedCores failed, metrics %v, error %v",
			podUID, metrics, err)
		return result
	}

	count, err := count(metrics)
	if err != nil {
		result.Error = fmt.Errorf("GetPodThrottledMetric %v aggregate CPUUsedCores failed, metrics %v, error %v",
			podUID, metrics, err)
		return result
	}

	result.AggregateInfo = &AggregateInfo{MetricsCount: int64(count)}
	result.Metric = &PodThrottledMetric{
		PodUID: *podUID,
		CPUThrottledMetric: &CPUThrottledMetric{
			ThrottledRatio: throttledRatio,
		},
	}
	return result
}

func (m *metricCache) GetContainerThrottledMetric(containerID *string, param *QueryParam) ContainerThrottledQueryResult {
	result := ContainerThrottledQueryResult{}
	if param == nil || param.Start == nil || param.End == nil {
		result.Error = fmt.Errorf("GetContainerThrottledMetric %v query parameters are illegal %v",
			containerID, param)
		return result
	}
	metrics, err := m.db.GetContainerThrottledMetric(containerID, param.Start, param.End)
	if err != nil {
		result.Error = fmt.Errorf("GetContainerThrottledMetric %v failed, query params %v, error %v",
			containerID, param, err)
		return result
	}
	if len(metrics) == 0 {
		result.Error = fmt.Errorf("GetContainerThrottledMetric %v failed, query params %v, error %v",
			containerID, param, err)
		return result
	}

	aggregateFunc := getAggregateFunc(param.Aggregate)
	throttledRatio, err := aggregateFunc(metrics, AggregateParam{
		ValueFieldName: "CPUThrottledRatio", TimeFieldName: "Timestamp"})
	if err != nil {
		result.Error = fmt.Errorf("GetContainerThrottledMetric %v aggregate CPUUsedCores failed, metrics %v, error %v",
			containerID, metrics, err)
		return result
	}

	count, err := count(metrics)
	if err != nil {
		result.Error = fmt.Errorf("GetContainerThrottledMetric %v aggregate CPUUsedCores failed, metrics %v, error %v",
			containerID, metrics, err)
		return result
	}

	result.AggregateInfo = &AggregateInfo{MetricsCount: int64(count)}
	result.Metric = &ContainerThrottledMetric{
		ContainerID: *containerID,
		CPUThrottledMetric: &CPUThrottledMetric{
			ThrottledRatio: throttledRatio,
		},
	}
	return result
}

func (m *metricCache) InsertNodeResourceMetric(t time.Time, nodeResUsed *NodeResourceMetric) error {
	gpuUsages := make([]gpuResourceMetric, len(nodeResUsed.GPUs))
	for idx, usage := range nodeResUsed.GPUs {
		gpuUsages[idx] = gpuResourceMetric{
			DeviceUUID:  usage.DeviceUUID,
			Minor:       usage.Minor,
			SMUtil:      float64(usage.SMUtil),
			MemoryUsed:  float64(usage.MemoryUsed.Value()),
			MemoryTotal: float64(usage.MemoryTotal.Value()),
			Timestamp:   t,
		}
	}

	dbItem := &nodeResourceMetric{
		CPUUsedCores:    float64(nodeResUsed.CPUUsed.CPUUsed.MilliValue()) / 1000,
		MemoryUsedBytes: float64(nodeResUsed.MemoryUsed.MemoryWithoutCache.Value()),
		GPUs:            gpuUsages,
		Timestamp:       t,
	}
	return m.db.InsertNodResourceMetric(dbItem)
}

func (m *metricCache) InsertPodResourceMetric(t time.Time, podResUsed *PodResourceMetric) error {
	gpuUsages := make([]gpuResourceMetric, len(podResUsed.GPUs))
	for idx, usage := range podResUsed.GPUs {
		gpuUsages[idx] = gpuResourceMetric{
			DeviceUUID:  usage.DeviceUUID,
			Minor:       usage.Minor,
			SMUtil:      float64(usage.SMUtil),
			MemoryUsed:  float64(usage.MemoryUsed.Value()),
			MemoryTotal: float64(usage.MemoryTotal.Value()),
			Timestamp:   t,
		}
	}

	dbItem := &podResourceMetric{
		PodUID:          podResUsed.PodUID,
		CPUUsedCores:    float64(podResUsed.CPUUsed.CPUUsed.MilliValue()) / 1000,
		MemoryUsedBytes: float64(podResUsed.MemoryUsed.MemoryWithoutCache.Value()),
		GPUs:            gpuUsages,
		Timestamp:       t,
	}
	return m.db.InsertPodResourceMetric(dbItem)
}

func (m *metricCache) InsertContainerResourceMetric(t time.Time, containerResUsed *ContainerResourceMetric) error {
	gpuUsages := make([]gpuResourceMetric, len(containerResUsed.GPUs))
	for idx, usage := range containerResUsed.GPUs {
		gpuUsages[idx] = gpuResourceMetric{
			DeviceUUID:  usage.DeviceUUID,
			Minor:       usage.Minor,
			SMUtil:      float64(usage.SMUtil),
			MemoryUsed:  float64(usage.MemoryUsed.Value()),
			MemoryTotal: float64(usage.MemoryTotal.Value()),
			Timestamp:   t,
		}
	}
	dbItem := &containerResourceMetric{
		ContainerID:     containerResUsed.ContainerID,
		CPUUsedCores:    float64(containerResUsed.CPUUsed.CPUUsed.MilliValue()) / 1000,
		MemoryUsedBytes: float64(containerResUsed.MemoryUsed.MemoryWithoutCache.Value()),
		GPUs:            gpuUsages,
		Timestamp:       t,
	}
	return m.db.InsertContainerResourceMetric(dbItem)
}

func (m *metricCache) InsertBECPUResourceMetric(t time.Time, metric *BECPUResourceMetric) error {
	dbItem := &beCPUResourceMetric{
		CPUUsedCores:    float64(metric.CPUUsed.MilliValue()) / 1000,
		CPULimitCores:   float64(metric.CPURealLimit.MilliValue()) / 1000,
		CPURequestCores: float64(metric.CPURequest.MilliValue()) / 1000,
		Timestamp:       t,
	}
	return m.db.InsertBECPUResourceMetric(dbItem)
}

func (m *metricCache) InsertNodeCPUInfo(info *NodeCPUInfo) error {
	infoBytes, err := json.Marshal(info)
	if err != nil {
		return err
	}

	record := &rawRecord{
		RecordType: NodeCPUInfoRecordType,
		RecordStr:  string(infoBytes),
	}

	return m.db.InsertRawRecord(record)
}

func (m *metricCache) InsertPodThrottledMetrics(t time.Time, metric *PodThrottledMetric) error {
	dbItem := &podThrottledMetric{
		PodUID:            metric.PodUID,
		CPUThrottledRatio: metric.CPUThrottledMetric.ThrottledRatio,
		Timestamp:         t,
	}
	return m.db.InsertPodThrottledMetric(dbItem)
}

func (m *metricCache) InsertContainerThrottledMetrics(t time.Time, metric *ContainerThrottledMetric) error {
	dbItem := &containerThrottledMetric{
		ContainerID:       metric.ContainerID,
		CPUThrottledRatio: metric.CPUThrottledMetric.ThrottledRatio,
		Timestamp:         t,
	}
	return m.db.InsertContainerThrottledMetric(dbItem)
}

func (m *metricCache) aggregateGPUUsages(gpuResourceMetricsByTime [][]gpuResourceMetric, aggregateFunc AggregationFunc) ([]GPUMetric, error) {
	if len(gpuResourceMetricsByTime) == 0 {
		return nil, nil
	}
	deviceCount := len(gpuResourceMetricsByTime[0])
	// keep order by device minor.
	gpuUsageByDevice := make([][]gpuResourceMetric, deviceCount)
	for _, deviceMetrics := range gpuResourceMetricsByTime {
		if len(deviceMetrics) != deviceCount {
			return nil, fmt.Errorf("aggregateGPUUsages %v error: inconsistent time series dimensions, deviceCount %d", deviceMetrics, deviceCount)
		}
		for devIdx, m := range deviceMetrics {
			gpuUsageByDevice[devIdx] = append(gpuUsageByDevice[devIdx], m)
		}
	}

	metrics := make([]GPUMetric, 0)
	for _, v := range gpuUsageByDevice {
		if len(v) == 0 {
			continue
		}
		smutil, err := aggregateFunc(v, AggregateParam{ValueFieldName: "SMUtil", TimeFieldName: "Timestamp"})
		if err != nil {
			return nil, err
		}

		memoryUsed, err := aggregateFunc(v, AggregateParam{ValueFieldName: "MemoryUsed", TimeFieldName: "Timestamp"})
		if err != nil {
			return nil, err
		}

		g := GPUMetric{
			DeviceUUID:  v[len(v)-1].DeviceUUID,
			Minor:       v[len(v)-1].Minor,
			SMUtil:      uint32(smutil),
			MemoryUsed:  *resource.NewQuantity(int64(memoryUsed), resource.BinarySI),
			MemoryTotal: *resource.NewQuantity(int64(v[len(v)-1].MemoryTotal), resource.BinarySI),
		}
		metrics = append(metrics, g)
	}

	return metrics, nil
}

func (m *metricCache) recycleDB() {
	now := time.Now()
	oldTime := time.Unix(0, 0)
	expiredTime := now.Add(-time.Duration(m.config.MetricExpireSeconds) * time.Second)
	if err := m.db.DeletePodResourceMetric(&oldTime, &expiredTime); err != nil {
		klog.Warningf("DeletePodResourceMetric failed during recycle, error %v", err)
	}
	if err := m.db.DeleteNodeResourceMetric(&oldTime, &expiredTime); err != nil {
		klog.Warningf("DeleteNodeResourceMetric failed during recycle, error %v", err)
	}
	if err := m.db.DeleteContainerResourceMetric(&oldTime, &expiredTime); err != nil {
		klog.Warningf("DeleteContainerResourceMetric failed during recycle, error %v", err)
	}
	if err := m.db.DeleteBECPUResourceMetric(&oldTime, &expiredTime); err != nil {
		klog.Warningf("DeleteBECPUResourceMetric failed during recycle, error %v", err)
	}
	if err := m.db.DeletePodThrottledMetric(&oldTime, &expiredTime); err != nil {
		klog.Warningf("DeletePodThrottledMetric failed during recycle, error %v", err)
	}
	if err := m.db.DeleteContainerThrottledMetric(&oldTime, &expiredTime); err != nil {
		klog.Warningf("DeleteContainerThrottledMetric failed during recycle, error %v", err)
	}
	// raw records do not need to cleanup
	klog.Infof("expired metric data before %v has been recycled", expiredTime)
}

func getAggregateFunc(aggregationType AggregationType) AggregationFunc {
	switch aggregationType {
	case AggregationTypeAVG:
		return fieldAvgOfMetricList
	case AggregationTypeP90:
		return fieldP90OfMetricList
	case AggregationTypeLast:
		return fieldLastOfMetricList
	case AggregationTypeCount:
		return fieldCountOfMetricList
	default:
		return fieldAvgOfMetricList
	}
}

func count(metrics interface{}) (float64, error) {
	aggregateFunc := getAggregateFunc(AggregationTypeCount)
	return aggregateFunc(metrics, AggregateParam{})
}

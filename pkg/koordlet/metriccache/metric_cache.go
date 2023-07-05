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
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

type InterferenceMetricName string

type QueryParam struct {
	Aggregate AggregationType
	Start     *time.Time
	End       *time.Time
}

type AggregateInfo struct {
	// TODO only support node resource metric now
	MetricStart *time.Time
	MetricEnd   *time.Time

	MetricsCount int64
}

func (a *AggregateInfo) TimeRangeDuration() time.Duration {
	if a == nil || a.MetricStart == nil || a.MetricEnd == nil {
		return time.Duration(0)
	}
	return a.MetricEnd.Sub(*a.MetricStart)

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
	TSDBStorage
	KVStorage
	// GetBECPUResourceMetric(param *QueryParam) BECPUResourceQueryResult
	// InsertBECPUResourceMetric(t time.Time, metric *BECPUResourceMetric) error
}

type metricCache struct {
	config *Config
	db     *storage
	TSDBStorage
	KVStorage
}

func NewMetricCache(cfg *Config) (MetricCache, error) {
	database, err := NewStorage()
	if err != nil {
		return nil, err
	}
	tsdb, err := NewTSDBStorage(cfg)
	if err != nil {
		return nil, err
	}
	kvdb := NewMemoryStorage()
	return &metricCache{
		config:      cfg,
		db:          database,
		TSDBStorage: tsdb,
		KVStorage:   kvdb,
	}, nil
}

func (m *metricCache) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	go wait.Until(func() {
		m.recycleDB()
	}, time.Duration(m.config.MetricGCIntervalSeconds)*time.Second, stopCh)

	return nil
}

// func (m *metricCache) GetBECPUResourceMetric(param *QueryParam) BECPUResourceQueryResult {
// 	result := BECPUResourceQueryResult{}
// 	if param == nil || param.Start == nil || param.End == nil {
// 		result.Error = fmt.Errorf("BECPUResourceMetric query parameters are illegal %v", param)
// 		return result
// 	}
// 	metrics, err := m.db.GetBECPUResourceMetric(param.Start, param.End)
// 	if err != nil {
// 		result.Error = fmt.Errorf("get BECPUResourceMetric failed, query params %v, error %v", param, err)
// 		return result
// 	}
// 	if len(metrics) == 0 {
// 		result.Error = fmt.Errorf("get BECPUResourceMetric not exist, query params %v", param)
// 		return result
// 	}

// 	aggregateFunc := getAggregateFunc(param.Aggregate)
// 	cpuUsed, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPUUsedCores", TimeFieldName: "Timestamp"})
// 	if err != nil {
// 		result.Error = fmt.Errorf("get node aggregate CPUUsedCores failed, metrics %v, error %v", metrics, err)
// 		return result
// 	}
// 	cpuLimit, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPULimitCores", TimeFieldName: "Timestamp"})
// 	if err != nil {
// 		result.Error = fmt.Errorf("get node aggregate CPULimitCores failed, metrics %v, error %v", metrics, err)
// 		return result
// 	}

// 	cpuRequest, err := aggregateFunc(metrics, AggregateParam{ValueFieldName: "CPURequestCores", TimeFieldName: "Timestamp"})
// 	if err != nil {
// 		result.Error = fmt.Errorf("get node aggregate CPURequestCores failed, metrics %v, error %v", metrics, err)
// 		return result
// 	}

// 	count, err := count(metrics)
// 	if err != nil {
// 		result.Error = fmt.Errorf("get node aggregate count failed, metrics %v, error %v", metrics, err)
// 		return result
// 	}

// 	result.AggregateInfo = &AggregateInfo{MetricsCount: int64(count)}
// 	result.Metric = &BECPUResourceMetric{
// 		CPUUsed:      *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI),
// 		CPURealLimit: *resource.NewMilliQuantity(int64(cpuLimit*1000), resource.DecimalSI),
// 		CPURequest:   *resource.NewMilliQuantity(int64(cpuRequest*1000), resource.DecimalSI),
// 	}
// 	return result
// }

// func (m *metricCache) InsertBECPUResourceMetric(t time.Time, metric *BECPUResourceMetric) error {
// 	dbItem := &beCPUResourceMetric{
// 		CPUUsedCores:    float64(metric.CPUUsed.MilliValue()) / 1000,
// 		CPULimitCores:   float64(metric.CPURealLimit.MilliValue()) / 1000,
// 		CPURequestCores: float64(metric.CPURequest.MilliValue()) / 1000,
// 		Timestamp:       t,
// 	}
// 	return m.db.InsertBECPUResourceMetric(dbItem)
// }

func (m *metricCache) recycleDB() {
	now := time.Now()
	oldTime := time.Unix(0, 0)
	expiredTime := now.Add(-time.Duration(m.config.MetricExpireSeconds) * time.Second)

	if err := m.db.DeleteBECPUResourceMetric(&oldTime, &expiredTime); err != nil {
		klog.Warningf("DeleteBECPUResourceMetric failed during recycle, error %v", err)
	}
	// raw records do not need to cleanup
	beCPUResCount, _ := m.db.CountBECPUResourceMetric()
	klog.V(4).Infof("expired metric data before %v has been recycled, remaining in db size: beCPUResCount=%v", expiredTime, beCPUResCount)
}

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

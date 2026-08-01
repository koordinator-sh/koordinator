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
	"sync"
	"time"

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

// WALCompactor is optionally implemented by TSDBStorage to support runtime WAL rotation.
type WALCompactor interface {
	// Compact triggers head compaction and WAL truncation.
	Compact() error
	// WALSize returns the current total size of the WAL directory in bytes.
	WALSize() (int64, error)
}

type metricCache struct {
	config *Config
	TSDBStorage
	KVStorage
}

func NewMetricCache(cfg *Config) (MetricCache, error) {
	tsdb, err := NewTSDBStorage(cfg)
	if err != nil {
		return nil, err
	}
	kvdb := NewMemoryStorage()
	return &metricCache{
		config:      cfg,
		TSDBStorage: tsdb,
		KVStorage:   kvdb,
	}, nil
}

func (m *metricCache) Run(stopCh <-chan struct{}) error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.runWALRotation(stopCh)
	}()
	<-stopCh
	wg.Wait()
	m.Close()
	return nil
}

// runWALRotation periodically checks the WAL size and triggers compaction
// when it exceeds TSDBWALRotationThresholdBytes (soft threshold).
// Compaction creates a checkpoint (keeping only active series and recent samples)
// and truncates old WAL segments, preventing unbounded WAL growth that could
// cause OOM on restart.
func (m *metricCache) runWALRotation(stopCh <-chan struct{}) {
	softThreshold := m.config.TSDBWALRotationThresholdBytes
	if softThreshold <= 0 {
		return
	}
	wc, ok := m.TSDBStorage.(WALCompactor)
	if !ok {
		return
	}

	interval := time.Duration(m.config.MetricGCIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			walSize, err := wc.WALSize()
			if err != nil {
				klog.V(4).Infof("failed to get WAL size: %v", err)
				continue
			}
			if walSize <= softThreshold {
				continue
			}
			klog.Infof("WAL size %d bytes exceeds soft threshold %d bytes, triggering compaction to rotate WAL",
				walSize, softThreshold)
			if err := wc.Compact(); err != nil {
				klog.Warningf("failed to compact TSDB for WAL rotation: %v", err)
				continue
			}
			newSize, err := wc.WALSize()
			if err == nil {
				klog.Infof("WAL rotation complete: %d -> %d bytes", walSize, newSize)
			}
		}
	}
}

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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/labels"
	promstorage "github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/fileutil"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
)

// TSDBStorage defines time-series type DB, providing insert and query interface
type TSDBStorage interface {
	Appendable
	Queryable

	// Close closes the storage and all its underlying resources.
	Close() error
}

// Appendable allows creating appenders.
type Appendable interface {
	// Appender returns a new appender for the storage.
	Appender() Appender
}

// Appender provides batch appends of MetricSample against a storage
type Appender interface {
	// Append adds a list of MetricSample for the given series.
	Append(s []MetricSample) error
	// Commit submits the MetricSample and purges the batch. If Commit
	// returns a non-nil error, it also rolls back all modifications made in
	// the appender so far, as Rollback would do. In any case, an Appender
	// must not be used anymore after Commit has been called.
	Commit() error
}

// Queryable handles queries against a storage.
type Queryable interface {
	// Querier returns a new querier over the data partition for the given time range.
	Querier(startTime, endTime time.Time) (Querier, error)
}

// Querier provides querying access over time series data of a fixed time range.
type Querier interface {
	// Query add series to MetricResult that matches the given meta.
	// It allows passing hints that can help in optimizing select,
	// but it's up to implementation of MetricResult how to use it.
	Query(meta MetricMeta, hints *QueryHints, result MetricResult) error

	// QueryAndClose is same with Query, but close the querier after return the result
	QueryAndClose(meta MetricMeta, hints *QueryHints, result MetricResult) error

	// Close closes the querier and releases its resources.
	Close()
}

// SelectHints specifies hints passed for query.
// It is only an option for implementation of MetricResult to use, e.g. GroupedResult
type QueryHints struct {
	// GroupBy []string
}

var _ TSDBStorage = &tsdbStorage{}

// tsdbStorage implements TSDBStorage
type tsdbStorage struct {
	db *tsdb.DB
}

func (t *tsdbStorage) Appender() Appender {
	return &tsdbAppender{
		appender: t.db.Appender(context.TODO()),
	}
}

func (t *tsdbStorage) Querier(startTime, endTime time.Time) (Querier, error) {
	klog.V(7).Infof("query start %v, end %v", startTime.UnixMilli(), endTime.UnixMilli())
	q, err := t.db.Querier(context.TODO(), startTime.UnixMilli(), endTime.UnixMilli())
	if err != nil {
		return nil, err
	}
	return &tsdbQuerier{
		querier: q,
	}, nil
}

func (t *tsdbStorage) Close() error {
	return t.db.Close()
}

// Compact triggers a head compaction which persists the head block to disk,
// creates a WAL checkpoint (dropping obsolete series and old samples),
// and truncates old WAL segments.
func (t *tsdbStorage) Compact() error {
	return t.db.Compact()
}

// WALSize returns the total size in bytes of the WAL directory.
func (t *tsdbStorage) WALSize() (int64, error) {
	walDir := filepath.Join(t.db.Dir(), "wal")
	size, err := fileutil.DirSize(walDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return size, nil
}

func NewTSDBStorage(conf *Config) (TSDBStorage, error) {
	if err := maybeRemoveWAL(conf); err != nil {
		klog.Warningf("failed to check/remove WAL: %v", err)
	}

	tsdbOpt := tsdb.DefaultOptions()
	tsdbOpt.RetentionDuration = int64(conf.TSDBRetentionDuration / time.Millisecond)
	tsdbOpt.StripeSize = conf.TSDBStripeSize
	tsdbOpt.MaxBytes = conf.TSDBMaxBytes
	tsdbOpt.WALSegmentSize = conf.TSDBWALSegmentSize
	tsdbOpt.MaxBlockChunkSegmentSize = conf.TSDBMaxBlockChunkSegmentSize
	tsdbOpt.MinBlockDuration = int64(conf.TSDBMinBlockDuration / time.Millisecond)
	tsdbOpt.MaxBlockDuration = int64(conf.TSDBMaxBlockDuration / time.Millisecond)
	tsdbOpt.HeadChunksWriteBufferSize = conf.TSDBHeadChunksWriteBufferSize
	// avoid conflicts using prometheus.tsdb v0.39 or higher
	// prometheus.tsdb(0.37) requires all sample must following the time order
	// new sample >= TSDB.MaxTime, out of order sample could not be appended until v0.39 with outOfOrderTimeWindow
	// option enabled.
	// oooTimeWindow must follow the grain of metric series
	tsdbOpt.OutOfOrderTimeWindow = int64(time.Minute / time.Millisecond)
	klog.V(5).Infof("ready to start tsdb with option %+v", tsdbOpt)

	var promReg prometheus.Registerer
	if conf.TSDBEnablePromMetrics {
		promReg = metrics.ExternalRegistry
	}
	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	db, err := tsdb.Open(conf.TSDBPath, log.With(logger, "component", "tsdb"), promReg, tsdbOpt, nil)
	if err != nil {
		return nil, err
	}
	return &tsdbStorage{
		db: db,
	}, nil
}

var _ Appender = &tsdbAppender{}

// tsdbAppender implements Appender
type tsdbAppender struct {
	appender promstorage.Appender
}

func (t *tsdbAppender) Append(samples []MetricSample) error {
	for _, s := range samples {
		l := s.GetProperties()
		l[metricLabelName] = s.GetKind()
		klog.V(7).Infof("append labels %v, ts %v, value %v", labels.FromMap(l).String(), s.timestamp(), s.value())
		// TODO cache the seriesRef to accelerate calls
		_, err := t.appender.Append(0, labels.FromMap(l), s.timestamp(), s.value())
		if err != nil {
			rollbackErr := t.appender.Rollback()
			return fmt.Errorf("append error %v, rollback error %v", err, rollbackErr)
		}
	}
	return nil
}

func (t *tsdbAppender) Commit() error {
	return t.appender.Commit()
}

var _ Querier = &tsdbQuerier{}

// tsdbQuerier implements Querier
type tsdbQuerier struct {
	querier promstorage.Querier
}

func (t *tsdbQuerier) Query(meta MetricMeta, hints *QueryHints, result MetricResult) error {
	properties := meta.GetProperties()
	labelMatchers := make([]*labels.Matcher, 0, len(properties)+1)

	nameLabelMatcher, err := labels.NewMatcher(labels.MatchEqual, metricLabelName, meta.GetKind())
	if err != nil {
		return err
	}
	labelMatchers = append(labelMatchers, nameLabelMatcher)

	for k, v := range properties {
		matcher, err := labels.NewMatcher(labels.MatchEqual, k, v)
		if err != nil {
			return err
		}
		labelMatchers = append(labelMatchers, matcher)
	}

	ss := t.querier.Select(false, nil, labelMatchers...)
	for ss.Next() {
		if ss.Err() != nil {
			return ss.Err()
		}
		series := ss.At()
		if err := result.AddSeries(series); err != nil {
			return err
		}
	}
	return nil
}

func (t *tsdbQuerier) QueryAndClose(meta MetricMeta, hints *QueryHints, result MetricResult) error {
	defer t.Close()
	return t.Query(meta, hints, result)
}

func (t *tsdbQuerier) Close() {
	if err := t.querier.Close(); err != nil {
		klog.Warningf("close querier error %v", err)
	}
}

// maybeRemoveWAL checks the total size of the WAL directory and removes it
// if it exceeds the configured threshold. This prevents OOM crash loops caused
// by replaying an oversized WAL after container restart (emptyDir persists).
func maybeRemoveWAL(conf *Config) error {
	if conf.TSDBMaxWALSizeBytes <= 0 {
		return nil
	}

	walDir := filepath.Join(conf.TSDBPath, "wal")
	walSize, err := fileutil.DirSize(walDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("get WAL dir size: %w", err)
	}

	if walSize <= conf.TSDBMaxWALSizeBytes {
		klog.V(4).Infof("WAL size %d bytes is within limit %d bytes", walSize, conf.TSDBMaxWALSizeBytes)
		return nil
	}

	klog.Warningf("WAL size %d bytes exceeds limit %d bytes, removing WAL and WBL directories to prevent OOM on replay",
		walSize, conf.TSDBMaxWALSizeBytes)

	if err := os.RemoveAll(walDir); err != nil {
		return fmt.Errorf("remove WAL dir: %w", err)
	}

	// Also remove the out-of-order WBL directory since it is replayed together with WAL.
	wblDir := filepath.Join(conf.TSDBPath, "wbl")
	if err := os.RemoveAll(wblDir); err != nil {
		return fmt.Errorf("remove WBL dir: %w", err)
	}

	return nil
}

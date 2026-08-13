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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testMetricSample struct {
	property map[MetricProperty]string
	point    Point
}

func Test_tsdbStorage_Append_And_Querier(t *testing.T) {
	pod1Property := map[MetricProperty]string{
		MetricPropertyPodUID: "test-pod-uid1",
	}
	pod1Meta, _ := PodCPUUsageMetric.BuildQueryMeta(pod1Property)
	pod2Property := map[MetricProperty]string{
		MetricPropertyPodUID: "test-pod-uid2",
	}
	pod2Meta, _ := PodCPUUsageMetric.BuildQueryMeta(pod2Property)

	now := time.UnixMilli(time.Now().UnixMilli())

	type args struct {
		metricSamples [][]testMetricSample
		startTime     time.Time
		endTime       time.Time
		aggregateType AggregationType
	}
	type queryValue struct {
		startTime time.Time
		endTime   time.Time
		queryMeta MetricMeta
		value     float64
	}
	type want struct {
		values []queryValue
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "insert pod cpu usage",
			args: args{
				metricSamples: [][]testMetricSample{
					{
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-4 * time.Second),
								Value:     4,
							},
						},
						{
							property: pod2Property,
							point: Point{
								Timestamp: now.Add(-3 * time.Second),
								Value:     300,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-2 * time.Second),
								Value:     1,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-1 * time.Second),
								Value:     1,
							},
						},
					},
				},
				startTime:     now.Add(-5 * time.Second),
				endTime:       now,
				aggregateType: AggregationTypeAVG,
			},
			want: want{
				values: []queryValue{
					{
						startTime: now.Add(-4 * time.Second),
						endTime:   now.Add(-1 * time.Second),
						queryMeta: pod1Meta,
						value:     2,
					},
					{
						startTime: now.Add(-3 * time.Second),
						endTime:   now.Add(-3 * time.Second),
						queryMeta: pod2Meta,
						value:     300,
					},
				},
			},
		},
		{
			name: "insert different pod cpu usage at same ts",
			args: args{
				metricSamples: [][]testMetricSample{
					{
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-4 * time.Second),
								Value:     4,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-2 * time.Second),
								Value:     1,
							},
						},
						{
							property: pod1Property,
							point: Point{
								Timestamp: now.Add(-1 * time.Second),
								Value:     1,
							},
						},
					},
					{
						{
							property: pod2Property,
							point: Point{
								Timestamp: now.Add(-2 * time.Second),
								Value:     300,
							},
						},
					},
				},
				startTime:     now.Add(-5 * time.Second),
				endTime:       now,
				aggregateType: AggregationTypeAVG,
			},
			want: want{
				values: []queryValue{
					{
						startTime: now.Add(-4 * time.Second),
						endTime:   now.Add(-1 * time.Second),
						queryMeta: pod1Meta,
						value:     2,
					},
					{
						startTime: now.Add(-2 * time.Second),
						endTime:   now.Add(-2 * time.Second),
						queryMeta: pod2Meta,
						value:     300,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			defer os.RemoveAll(dir)
			conf := NewDefaultConfig()
			conf.TSDBPath = dir
			conf.TSDBEnablePromMetrics = false
			db, err := NewTSDBStorage(conf)
			defer func() {
				db.Close()
			}()
			assert.NoError(t, err)

			t.Log(dir)
			for _, sampleList := range tt.args.metricSamples {
				appendSample := make([]MetricSample, 0, len(sampleList))
				for _, sample := range sampleList {
					s, err := PodCPUUsageMetric.GenerateSample(sample.property, sample.point.Timestamp, sample.point.Value)
					assert.NoError(t, err)
					appendSample = append(appendSample, s)
				}
				appender := db.Appender()
				err = appender.Append(appendSample)
				assert.NoError(t, err)

				err = appender.Commit()
				assert.NoError(t, err)
			}

			for _, want := range tt.want.values {
				querier, err := db.Querier(tt.args.startTime, tt.args.endTime)
				assert.NoError(t, err)

				aggregateResult := &aggregateResult{}
				err = querier.Query(want.queryMeta, nil, aggregateResult)
				assert.NoError(t, err)
				gotValue, err := aggregateResult.Value(tt.args.aggregateType)
				assert.NoError(t, err)

				assert.True(t, aggregateResult.metricStart.Equal(want.startTime), "metric start time should be equal, want %v, got %v",
					want.startTime, aggregateResult.metricStart)
				assert.True(t, aggregateResult.metricsEnd.Equal(want.endTime), "metric end time should be equal, want %v, got %v",
					want.endTime, aggregateResult.metricsEnd)
				assert.Equal(t, want.value, gotValue, "metric aggregate value should be equal")
			}
		})
	}
}

func Test_maybeRemoveWAL(t *testing.T) {
	tests := []struct {
		name            string
		maxWALSizeBytes int64
		walFileSize     int
		expectRemoved   bool
	}{
		{
			name:            "disabled when maxWALSizeBytes is 0",
			maxWALSizeBytes: 0,
			walFileSize:     1024,
			expectRemoved:   false,
		},
		{
			name:            "no WAL dir exists",
			maxWALSizeBytes: 100,
			walFileSize:     0,
			expectRemoved:   false,
		},
		{
			name:            "WAL within limit",
			maxWALSizeBytes: 2048,
			walFileSize:     1024,
			expectRemoved:   false,
		},
		{
			name:            "WAL exceeds limit",
			maxWALSizeBytes: 512,
			walFileSize:     1024,
			expectRemoved:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			conf := &Config{
				TSDBPath:            dir,
				TSDBMaxWALSizeBytes: tt.maxWALSizeBytes,
			}

			if tt.walFileSize > 0 {
				walDir := filepath.Join(dir, "wal")
				err := os.MkdirAll(walDir, 0o755)
				assert.NoError(t, err)
				data := make([]byte, tt.walFileSize)
				err = os.WriteFile(filepath.Join(walDir, "00000000"), data, 0o644)
				assert.NoError(t, err)

				// Also create a WBL dir to verify it gets removed together
				wblDir := filepath.Join(dir, "wbl")
				err = os.MkdirAll(wblDir, 0o755)
				assert.NoError(t, err)
				err = os.WriteFile(filepath.Join(wblDir, "00000000"), data, 0o644)
				assert.NoError(t, err)
			}

			err := maybeRemoveWAL(conf)
			assert.NoError(t, err)

			walDir := filepath.Join(dir, "wal")
			_, statErr := os.Stat(walDir)
			if tt.expectRemoved {
				assert.True(t, os.IsNotExist(statErr), "WAL dir should be removed")
				wblDir := filepath.Join(dir, "wbl")
				_, wblStatErr := os.Stat(wblDir)
				assert.True(t, os.IsNotExist(wblStatErr), "WBL dir should be removed")
			} else if tt.walFileSize > 0 {
				assert.NoError(t, statErr, "WAL dir should still exist")
			}
		})
	}
}

func Test_maybeRemoveWAL_tsdbOpensAfterRemoval(t *testing.T) {
	dir := t.TempDir()

	// Simulate a large WAL that exceeds the limit
	walDir := filepath.Join(dir, "wal")
	err := os.MkdirAll(walDir, 0o755)
	assert.NoError(t, err)
	data := make([]byte, 2048)
	err = os.WriteFile(filepath.Join(walDir, "00000000"), data, 0o644)
	assert.NoError(t, err)

	conf := NewDefaultConfig()
	conf.TSDBPath = dir
	conf.TSDBEnablePromMetrics = false
	conf.TSDBMaxWALSizeBytes = 1024 // lower than the WAL size

	// NewTSDBStorage should succeed after removing the oversized WAL
	db, err := NewTSDBStorage(conf)
	assert.NoError(t, err)
	assert.NotNil(t, db)
	defer db.Close()

	// WAL dir should have been recreated by tsdb.Open
	_, statErr := os.Stat(walDir)
	assert.NoError(t, statErr, "WAL dir should be recreated by tsdb.Open")
}

func Test_tsdbStorage_CompactAndWALSize(t *testing.T) {
	dir := t.TempDir()
	conf := NewDefaultConfig()
	conf.TSDBPath = dir
	conf.TSDBEnablePromMetrics = false

	db, err := NewTSDBStorage(conf)
	assert.NoError(t, err)
	defer db.Close()

	// tsdbStorage should implement WALCompactor
	wc, ok := db.(WALCompactor)
	assert.True(t, ok, "tsdbStorage should implement WALCompactor")

	// WALSize should return a non-negative value
	walSize, err := wc.WALSize()
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, walSize, int64(0))

	// Compact should not error on an empty DB
	err = wc.Compact()
	assert.NoError(t, err)
}

func Test_runWALRotation(t *testing.T) {
	dir := t.TempDir()
	conf := NewDefaultConfig()
	conf.TSDBPath = dir
	conf.TSDBEnablePromMetrics = false
	conf.MetricGCIntervalSeconds = 1       // 1s interval for fast test
	conf.TSDBWALRotationThresholdBytes = 1 // very low soft threshold to trigger rotation
	conf.TSDBMaxWALSizeBytes = 0           // disable hard threshold

	cache, err := NewMetricCache(conf)
	assert.NoError(t, err)

	// Append some data to create WAL content
	now := time.Now()
	sample, err := PodCPUUsageMetric.GenerateSample(
		map[MetricProperty]string{MetricPropertyPodUID: "test-uid"},
		now, 1.0,
	)
	assert.NoError(t, err)
	appender := cache.Appender()
	err = appender.Append([]MetricSample{sample})
	assert.NoError(t, err)
	err = appender.Commit()
	assert.NoError(t, err)

	// Run the cache with WAL rotation for a short period
	stopCh := make(chan struct{})
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cache.Run(stopCh)
	}()

	// Wait for at least one rotation cycle
	time.Sleep(2 * time.Second)
	close(stopCh)
	err = <-doneCh
	assert.NoError(t, err)
}

func Test_runWALRotation_disabled(t *testing.T) {
	dir := t.TempDir()
	conf := NewDefaultConfig()
	conf.TSDBPath = dir
	conf.TSDBEnablePromMetrics = false
	conf.MetricGCIntervalSeconds = 1
	conf.TSDBWALRotationThresholdBytes = 0 // disabled
	conf.TSDBMaxWALSizeBytes = 0           // disabled

	cache, err := NewMetricCache(conf)
	assert.NoError(t, err)

	stopCh := make(chan struct{})
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cache.Run(stopCh)
	}()

	// Should exit cleanly without any rotation
	time.Sleep(500 * time.Millisecond)
	close(stopCh)
	err = <-doneCh
	assert.NoError(t, err)
}

func Test_runWALRotation_notTriggeredBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	conf := NewDefaultConfig()
	conf.TSDBPath = dir
	conf.TSDBEnablePromMetrics = false
	conf.MetricGCIntervalSeconds = 1
	conf.TSDBWALRotationThresholdBytes = 100 * 1024 * 1024 // 100MB, won't be reached
	conf.TSDBMaxWALSizeBytes = 0

	cache, err := NewMetricCache(conf)
	assert.NoError(t, err)

	// Append a small amount of data
	now := time.Now()
	sample, err := PodCPUUsageMetric.GenerateSample(
		map[MetricProperty]string{MetricPropertyPodUID: "test-uid"},
		now, 1.0,
	)
	assert.NoError(t, err)
	appender := cache.Appender()
	err = appender.Append([]MetricSample{sample})
	assert.NoError(t, err)
	err = appender.Commit()
	assert.NoError(t, err)

	stopCh := make(chan struct{})
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cache.Run(stopCh)
	}()

	// WAL is small, rotation should not trigger
	time.Sleep(2 * time.Second)
	close(stopCh)
	err = <-doneCh
	assert.NoError(t, err)

	// Verify WAL still exists (not removed)
	walDir := filepath.Join(dir, "wal")
	_, statErr := os.Stat(walDir)
	assert.NoError(t, statErr, "WAL dir should still exist")
}

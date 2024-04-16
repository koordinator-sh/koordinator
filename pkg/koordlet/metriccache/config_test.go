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
	"flag"
	"testing"
	"time"

	"github.com/prometheus/prometheus/tsdb"
	"github.com/stretchr/testify/assert"
)

func Test_NewDefaultConfig(t *testing.T) {
	expectConfig := &Config{
		MetricGCIntervalSeconds: 300,
		MetricExpireSeconds:     1800,

		TSDBPath:              "/metric-data/",
		TSDBRetentionDuration: 12 * time.Hour,
		TSDBEnablePromMetrics: true,
		TSDBStripeSize:        tsdb.DefaultStripeSize,
		TSDBMaxBytes:          100 * 1024 * 1024, // 100 MB

		TSDBWALSegmentSize:            1 * 1024 * 1024,
		TSDBMaxBlockChunkSegmentSize:  5 * 1024 * 1024,
		TSDBMinBlockDuration:          10 * time.Minute,
		TSDBMaxBlockDuration:          10 * time.Minute,
		TSDBHeadChunksWriteBufferSize: 1024 * 1024,
	}
	defaultConfig := NewDefaultConfig()
	assert.Equal(t, expectConfig, defaultConfig)
}

func Test_InitFlags(t *testing.T) {
	cmdArgs := []string{
		"",
		"--metric-gc-interval-seconds=100",
		"--metric-expire-seconds=600",

		"--tsdb-path=/test-metric-path/",
		"--tsdb-retention-duration=30m",
		"--tsdb-enable-prometheus-metric=false",
		"--tsdb-stripe-size=10240",
		"--tsdb-max-bytes=65536",

		"--tsdb-wal-segment-size=2048",
		"--tsdb-max-block-chunk-segment-size=4096",
		"--tsdb-min-block-duration=10m",
		"--tsdb-max-block-duration=20m",
		"--tsdb-head-chunks-write-buffer-size=512",
	}
	fs := flag.NewFlagSet(cmdArgs[0], flag.ExitOnError)

	type fields struct {
		MetricGCIntervalSeconds int
		MetricExpireSeconds     int

		TSDBPath              string
		TSDBRetentionDuration time.Duration
		TSDBEnablePromMetrics bool
		TSDBStripeSize        int
		TSDBMaxBytes          int64

		TSDBWALSegmentSize            int
		TSDBMaxBlockChunkSegmentSize  int64
		TSDBMinBlockDuration          time.Duration
		TSDBMaxBlockDuration          time.Duration
		TSDBHeadChunksWriteBufferSize int
	}
	type args struct {
		fs *flag.FlagSet
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "not default",
			fields: fields{
				MetricGCIntervalSeconds:       100,
				MetricExpireSeconds:           600,
				TSDBPath:                      "/test-metric-path/",
				TSDBRetentionDuration:         30 * time.Minute,
				TSDBEnablePromMetrics:         false,
				TSDBStripeSize:                10240,
				TSDBMaxBytes:                  65536,
				TSDBWALSegmentSize:            2048,
				TSDBMaxBlockChunkSegmentSize:  4096,
				TSDBMinBlockDuration:          10 * time.Minute,
				TSDBMaxBlockDuration:          20 * time.Minute,
				TSDBHeadChunksWriteBufferSize: 512,
			},
			args: args{fs: fs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := &Config{
				MetricGCIntervalSeconds: tt.fields.MetricGCIntervalSeconds,
				MetricExpireSeconds:     tt.fields.MetricExpireSeconds,

				TSDBPath:              tt.fields.TSDBPath,
				TSDBRetentionDuration: tt.fields.TSDBRetentionDuration,
				TSDBEnablePromMetrics: tt.fields.TSDBEnablePromMetrics,
				TSDBStripeSize:        tt.fields.TSDBStripeSize,
				TSDBMaxBytes:          tt.fields.TSDBMaxBytes,

				TSDBWALSegmentSize:            tt.fields.TSDBWALSegmentSize,
				TSDBMaxBlockChunkSegmentSize:  tt.fields.TSDBMaxBlockChunkSegmentSize,
				TSDBMinBlockDuration:          tt.fields.TSDBMinBlockDuration,
				TSDBMaxBlockDuration:          tt.fields.TSDBMaxBlockDuration,
				TSDBHeadChunksWriteBufferSize: tt.fields.TSDBHeadChunksWriteBufferSize,
			}
			c := NewDefaultConfig()
			c.InitFlags(tt.args.fs)
			tt.args.fs.Parse(cmdArgs[1:])
			assert.Equal(t, raw, c)
		})
	}
}

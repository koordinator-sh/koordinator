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
	"time"

	"github.com/prometheus/prometheus/tsdb"
)

type Config struct {
	MetricGCIntervalSeconds int
	MetricExpireSeconds     int

	TSDBPath              string
	TSDBRetentionDuration time.Duration
	TSDBEnablePromMetrics bool
	TSDBStripeSize        int
	TSDBMaxBytes          int64

	// not necessary now since it is in-memory empty dir now
	TSDBWALSegmentSize            int
	TSDBMaxBlockChunkSegmentSize  int64
	TSDBMinBlockDuration          time.Duration
	TSDBMaxBlockDuration          time.Duration
	TSDBHeadChunksWriteBufferSize int

	// TSDBWALRotationThresholdBytes is the soft threshold for runtime WAL rotation.
	// When the WAL size exceeds this value during runtime, a head compaction is
	// triggered to create a WAL checkpoint (keeping only active series and recent
	// samples) and truncate old WAL segments. This preserves recent metric data
	// while bounding the WAL size to prevent OOM on restart.
	// The check runs every MetricGCIntervalSeconds. Set to 0 to disable.
	TSDBWALRotationThresholdBytes int64

	// TSDBMaxWALSizeBytes is the hard threshold checked only at startup before
	// opening the TSDB. If the WAL directory exceeds this size (e.g., the runtime
	// rotation never ran because the container was OOM-killed before the first
	// compaction), the entire WAL and WBL directories are removed to break the
	// OOM crash loop. This is a last-resort guard for pre-existing abnormal states;
	// buffered metric data is lost but re-collected within seconds.
	// Set to 0 to disable.
	TSDBMaxWALSizeBytes int64
}

func NewDefaultConfig() *Config {
	return &Config{
		MetricGCIntervalSeconds: 300,
		MetricExpireSeconds:     1800,

		TSDBPath:              "/metric-data/",
		TSDBRetentionDuration: 12 * time.Hour,
		TSDBEnablePromMetrics: true,
		TSDBStripeSize:        tsdb.DefaultStripeSize,
		TSDBMaxBytes:          100 * 1024 * 1024, // 100 MB

		TSDBWALSegmentSize:            1 * 1024 * 1024,  // 1 MB
		TSDBMaxBlockChunkSegmentSize:  5 * 1024 * 1024,  // 5 MB
		TSDBMinBlockDuration:          10 * time.Minute, // 10 minutes
		TSDBMaxBlockDuration:          10 * time.Minute, // 10 minutes
		TSDBHeadChunksWriteBufferSize: 1024 * 1024,      // 1 MB

		TSDBWALRotationThresholdBytes: 32 * 1024 * 1024, // 32 MB
		TSDBMaxWALSizeBytes:           64 * 1024 * 1024, // 64 MB
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.IntVar(&c.MetricGCIntervalSeconds, "metric-gc-interval-seconds", c.MetricGCIntervalSeconds, "Collect node metrics interval by seconds")
	fs.IntVar(&c.MetricExpireSeconds, "metric-expire-seconds", c.MetricExpireSeconds, "Collect pod metrics expire by seconds")

	fs.StringVar(&c.TSDBPath, "tsdb-path", c.TSDBPath, "Base path for metric data storage.")
	fs.DurationVar(&c.TSDBRetentionDuration, "tsdb-retention-duration", c.TSDBRetentionDuration, "Duration of persisted data to keep")
	fs.BoolVar(&c.TSDBEnablePromMetrics, "tsdb-enable-prometheus-metric", c.TSDBEnablePromMetrics, "Enable prometheus metric for tsdb")
	fs.IntVar(&c.TSDBStripeSize, "tsdb-stripe-size", c.TSDBStripeSize, "Size in entries of the series hash map. Reducing the size will save memory but impact performance.")
	fs.Int64Var(&c.TSDBMaxBytes, "tsdb-max-bytes", c.TSDBMaxBytes, "Maximum number of bytes in blocks to be retained.")

	fs.IntVar(&c.TSDBWALSegmentSize, "tsdb-wal-segment-size", c.TSDBWALSegmentSize, "Byte size of WAL(Write Ahead Log).")
	fs.Int64Var(&c.TSDBMaxBlockChunkSegmentSize, "tsdb-max-block-chunk-segment-size", c.TSDBMaxBlockChunkSegmentSize, "The max size of block chunk segment files.")
	fs.DurationVar(&c.TSDBMinBlockDuration, "tsdb-min-block-duration", c.TSDBMinBlockDuration, "The timestamp range of head blocks after which they get persisted, recommend >= 1h or this will cause chunks_head leak")
	fs.DurationVar(&c.TSDBMaxBlockDuration, "tsdb-max-block-duration", c.TSDBMaxBlockDuration, "The maximum timestamp range of compacted blocks, recommend >= 1h or this will cause chunks_head leak.")
	fs.IntVar(&c.TSDBHeadChunksWriteBufferSize, "tsdb-head-chunks-write-buffer-size", c.TSDBHeadChunksWriteBufferSize, "Write buffer size used by the head chunks mapper.")
	fs.Int64Var(&c.TSDBWALRotationThresholdBytes, "tsdb-wal-rotation-threshold-bytes", c.TSDBWALRotationThresholdBytes, "Soft threshold in bytes for runtime WAL rotation. Triggers compaction to checkpoint and truncate WAL when exceeded. Set 0 to disable.")
	fs.Int64Var(&c.TSDBMaxWALSizeBytes, "tsdb-max-wal-size-bytes", c.TSDBMaxWALSizeBytes, "Hard threshold in bytes checked at startup. Removes entire WAL if exceeded to break OOM crash loop. Set 0 to disable.")
}

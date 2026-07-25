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

package framework

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

func writeBaseline(t *testing.T, result types.BenchmarkResult) string {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal baseline: %v", err)
	}
	f := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(f, data, 0644); err != nil {
		t.Fatalf("failed to write baseline: %v", err)
	}
	return f
}

func TestCompareToBaseline_noBaseline(t *testing.T) {
	breached, err := CompareToBaseline(types.BenchmarkResult{}, "", types.Thresholds{})
	if err != nil || breached {
		t.Errorf("expected no breach and no error with empty baseline path, got breached=%v err=%v", breached, err)
	}
}

func TestCompareToBaseline_withinThresholds(t *testing.T) {
	baseline := types.BenchmarkResult{ThroughputPodsPerSec: 100, LatencyP99Sec: 5}
	// 5% throughput drop and 10% P99 increase — both within 10%/20% thresholds
	result := types.BenchmarkResult{ThroughputPodsPerSec: 95, LatencyP99Sec: 5.5}
	thresholds := types.Thresholds{ThroughputDropPct: 10, P99IncreasePct: 20}

	path := writeBaseline(t, baseline)
	breached, err := CompareToBaseline(result, path, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if breached {
		t.Error("expected no threshold breach")
	}
}

func TestCompareToBaseline_throughputDropBreaches(t *testing.T) {
	baseline := types.BenchmarkResult{ThroughputPodsPerSec: 100, LatencyP99Sec: 5}
	// 20% throughput drop exceeds the 10% threshold
	result := types.BenchmarkResult{ThroughputPodsPerSec: 80, LatencyP99Sec: 5}
	thresholds := types.Thresholds{ThroughputDropPct: 10, P99IncreasePct: 20}

	path := writeBaseline(t, baseline)
	breached, err := CompareToBaseline(result, path, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !breached {
		t.Error("expected threshold breach due to throughput drop")
	}
}

func TestCompareToBaseline_p99IncreaseBreaches(t *testing.T) {
	baseline := types.BenchmarkResult{ThroughputPodsPerSec: 100, LatencyP99Sec: 5}
	// 50% P99 increase exceeds the 20% threshold
	result := types.BenchmarkResult{ThroughputPodsPerSec: 100, LatencyP99Sec: 7.5}
	thresholds := types.Thresholds{ThroughputDropPct: 10, P99IncreasePct: 20}

	path := writeBaseline(t, baseline)
	breached, err := CompareToBaseline(result, path, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !breached {
		t.Error("expected threshold breach due to P99 increase")
	}
}

func TestCompareToBaseline_missingFile(t *testing.T) {
	_, err := CompareToBaseline(types.BenchmarkResult{}, "/nonexistent/baseline.json", types.Thresholds{})
	if err == nil {
		t.Error("expected error for missing baseline file")
	}
}

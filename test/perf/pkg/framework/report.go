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
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// CompareToBaseline loads the baseline JSON at baselinePath and checks whether
// result breaches the thresholds defined in cfg. Returns false with no error
// when baselinePath is empty (no comparison requested).
func CompareToBaseline(result types.BenchmarkResult, baselinePath string, thresholds types.Thresholds) (bool, error) {
	if baselinePath == "" {
		return false, nil
	}
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		return false, fmt.Errorf("reading baseline %q: %w", baselinePath, err)
	}
	var baseline types.BenchmarkResult
	if err := json.Unmarshal(data, &baseline); err != nil {
		return false, fmt.Errorf("parsing baseline %q: %w", baselinePath, err)
	}

	if baseline.ThroughputPodsPerSec > 0 {
		drop := (baseline.ThroughputPodsPerSec - result.ThroughputPodsPerSec) /
			baseline.ThroughputPodsPerSec * 100
		if drop > thresholds.ThroughputDropPct {
			klog.InfoS("Threshold breached: throughput dropped",
				"dropPct", fmt.Sprintf("%.1f%%", drop),
				"thresholdPct", fmt.Sprintf("%.1f%%", thresholds.ThroughputDropPct))
			return true, nil
		}
	}

	if baseline.LatencyP99Sec > 0 {
		increase := (result.LatencyP99Sec - baseline.LatencyP99Sec) /
			baseline.LatencyP99Sec * 100
		if increase > thresholds.P99IncreasePct {
			klog.InfoS("Threshold breached: P99 latency increased",
				"increasePct", fmt.Sprintf("%.1f%%", increase),
				"thresholdPct", fmt.Sprintf("%.1f%%", thresholds.P99IncreasePct))
			return true, nil
		}
	}

	klog.InfoS("Baseline comparison passed", "baseline", baselinePath)
	return false, nil
}

// WriteReport writes result to outputPath as formatted JSON
// and prints a human-readable summary to stdout.
func WriteReport(result types.BenchmarkResult, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}

	klog.InfoS("Benchmark result",
		"scenario", result.Name,
		"throughputPodsPerSec", fmt.Sprintf("%.2f", result.ThroughputPodsPerSec),
		"apiCreationDurationSec", fmt.Sprintf("%.2fs", result.APICreationDurationSec),
		"totalDurationSec", fmt.Sprintf("%.2fs", result.TotalDurationSec),
		"p50LatencySec", fmt.Sprintf("%.2fs", result.LatencyP50Sec),
		"p90LatencySec", fmt.Sprintf("%.2fs", result.LatencyP90Sec),
		"p99LatencySec", fmt.Sprintf("%.2fs", result.LatencyP99Sec),
		"output", outputPath,
	)

	return nil
}

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

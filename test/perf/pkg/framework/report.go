package framework

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteReport writes result to outputPath as formatted JSON and prints a summary to stdout.
func WriteReport(result BenchmarkResult, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write result file: %w", err)
	}

	fmt.Printf("\n=== Benchmark Result: %s ===\n", result.Name)
	fmt.Printf("Throughput:         %.2f pods/sec\n", result.ThroughputPodsPerSec)
	fmt.Printf("API Creation Time:  %.2fs\n", result.APICreationDurationSec)
	fmt.Printf("Total Time:         %.2fs\n", result.TotalDurationSec)
	fmt.Printf("P50 Latency:        %.2fs\n", result.LatencyP50Sec)
	fmt.Printf("P90 Latency:        %.2fs\n", result.LatencyP90Sec)
	fmt.Printf("P99 Latency:        %.2fs\n", result.LatencyP99Sec)
	fmt.Printf("Result written to:  %s\n\n", outputPath)

	return nil
}

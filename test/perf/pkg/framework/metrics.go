package framework

import (
	"sort"
	"time"
)

// ComputeLatencyPercentiles sorts latencies and returns P50, P90, P99.
func ComputeLatencyPercentiles(latencies []time.Duration) (p50, p90, p99 time.Duration) {
	if len(latencies) == 0 {
		return 0, 0, 0
	}

	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	p50 = sorted[int(float64(len(sorted))*0.50)]
	p90 = sorted[int(float64(len(sorted))*0.90)]
	p99 = sorted[int(float64(len(sorted))*0.99)]
	return
}

// ComputeThroughput returns pods scheduled per second.
func ComputeThroughput(podCount int, totalDuration time.Duration) float64 {
	if totalDuration <= 0 {
		return 0
	}
	return float64(podCount) / totalDuration.Seconds()
}

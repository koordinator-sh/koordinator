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
	"sort"
	"time"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// ComputeLatencyPercentiles sorts latencies and returns P50, P90, P99.
// Uses idx = int(n*p)-1 clamped to [0, n-1], matching the repo's existing
// percentile pattern in pkg/koordlet/metriccache/util.go.
func ComputeLatencyPercentiles(latencies []time.Duration) (p50, p90, p99 time.Duration) {
	if len(latencies) == 0 {
		return 0, 0, 0
	}

	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	n := len(sorted)
	p50 = sorted[clampIdx(int(float64(n)*0.50)-1, n)]
	p90 = sorted[clampIdx(int(float64(n)*0.90)-1, n)]
	p99 = sorted[clampIdx(int(float64(n)*0.99)-1, n)]
	return
}

// clampIdx clamps idx to [0, n-1].
func clampIdx(idx, n int) int {
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

// ComputeThroughput returns pods scheduled per second.
func ComputeThroughput(podCount int, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}
	return float64(podCount) / total.Seconds()
}

// ComputeGangCompletionPercentiles groups latencies by GangID and takes,
// per group, the latency of that group's last scheduled member — a gang is
// not "complete" until every member is placed. Percentiles are then computed
// across groups, not across individual pods.
//
// Pods with an empty GangID are ignored. Returns ok=false when no
// gang-labeled pods are present, so callers can leave
// GangCompletionP50Sec/P99Sec nil rather than reporting a false zero.
func ComputeGangCompletionPercentiles(latencies []types.PodLatency) (p50, p99 time.Duration, ok bool) {
	groupMax := map[string]time.Duration{}
	for _, l := range latencies {
		if l.GangID == "" {
			continue
		}
		if l.Latency > groupMax[l.GangID] {
			groupMax[l.GangID] = l.Latency
		}
	}
	if len(groupMax) == 0 {
		return 0, 0, false
	}

	completions := make([]time.Duration, 0, len(groupMax))
	for _, d := range groupMax {
		completions = append(completions, d)
	}
	sort.Slice(completions, func(i, j int) bool { return completions[i] < completions[j] })

	n := len(completions)
	p50 = completions[clampIdx(int(float64(n)*0.50)-1, n)]
	p99 = completions[clampIdx(int(float64(n)*0.99)-1, n)]
	return p50, p99, true
}

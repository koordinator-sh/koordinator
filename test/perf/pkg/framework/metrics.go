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

// ComputeThroughputWindow returns pods/sec measured over the actual scheduling
// window of the recorded pods: from the earliest CreatedAt to the latest
// ScheduledAt among them. Both endpoints are server-side timestamps, so the
// result is insensitive to client-side send pacing (clientQPS/clientBurst)
// and reflects only the recorded (i.e. actually scheduled) population.
//
// For basic/gang (ExpectedScheduledPodCount == PodCount), this window
// coincides with totalDuration, so reported numbers are unchanged compared to
// ComputeThroughput. The window is clamped to 1s because CreationTimestamp
// and LastTransitionTime are second-granular — a sub-second window would
// divide by near-zero and produce an implausible spike.
func ComputeThroughputWindow(pods []types.PodLatency) float64 {
	if len(pods) == 0 {
		return 0
	}
	minCreated, maxScheduled := pods[0].CreatedAt, pods[0].ScheduledAt
	for _, p := range pods[1:] {
		if p.CreatedAt.Before(minCreated) {
			minCreated = p.CreatedAt
		}
		if p.ScheduledAt.After(maxScheduled) {
			maxScheduled = p.ScheduledAt
		}
	}
	window := maxScheduled.Sub(minCreated)
	if window < time.Second {
		window = time.Second
	}
	return float64(len(pods)) / window.Seconds()
}

// ComputeGangCompletionPercentiles groups pods by GangID and computes the
// latency of the last pod scheduled in each gang (gang completion time), then
// returns P50 and P99 over all gangs. Returns ok=false when no gang pods exist.
func ComputeGangCompletionPercentiles(podLatencies []types.PodLatency) (p50, p99 time.Duration, ok bool) {
	gangMax := map[string]time.Duration{}
	for _, pl := range podLatencies {
		if pl.GangID == "" {
			continue
		}
		if pl.Latency > gangMax[pl.GangID] {
			gangMax[pl.GangID] = pl.Latency
		}
	}
	if len(gangMax) == 0 {
		return 0, 0, false
	}
	completions := make([]time.Duration, 0, len(gangMax))
	for _, d := range gangMax {
		completions = append(completions, d)
	}
	sort.Slice(completions, func(i, j int) bool { return completions[i] < completions[j] })
	n := len(completions)
	p50 = completions[clampIdx(int(float64(n)*0.50)-1, n)]
	p99 = completions[clampIdx(int(float64(n)*0.99)-1, n)]
	return p50, p99, true
}

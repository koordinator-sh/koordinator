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
	"testing"
	"time"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

func TestComputeLatencyPercentiles_empty(t *testing.T) {
	p50, p90, p99 := ComputeLatencyPercentiles(nil)
	if p50 != 0 || p90 != 0 || p99 != 0 {
		t.Errorf("expected all zeros for empty input, got %v %v %v", p50, p90, p99)
	}
}

func TestComputeLatencyPercentiles_single(t *testing.T) {
	p50, p90, p99 := ComputeLatencyPercentiles([]time.Duration{5 * time.Second})
	if p50 != 5*time.Second || p90 != 5*time.Second || p99 != 5*time.Second {
		t.Errorf("expected 5s for all percentiles on single element, got %v %v %v", p50, p90, p99)
	}
}

func TestComputeLatencyPercentiles_sorted(t *testing.T) {
	// 10 evenly spaced values: 1s, 2s, ..., 10s
	latencies := make([]time.Duration, 10)
	for i := range latencies {
		latencies[i] = time.Duration(i+1) * time.Second
	}
	p50, p90, p99 := ComputeLatencyPercentiles(latencies)

	// P50 index: int(10*0.50)-1 = 4 → 5s
	if p50 != 5*time.Second {
		t.Errorf("P50: expected 5s, got %v", p50)
	}
	// P90 index: int(10*0.90)-1 = 8 → 9s
	if p90 != 9*time.Second {
		t.Errorf("P90: expected 9s, got %v", p90)
	}
	// P99 index: int(10*0.99)-1 = 8 → 9s (clamped)
	if p99 != 9*time.Second {
		t.Errorf("P99: expected 9s, got %v", p99)
	}
}

func TestComputeLatencyPercentiles_unsorted(t *testing.T) {
	// Input out of order — function must sort internally.
	// sorted: [1s, 5s, 10s]; P50 index = int(3*0.5)-1 = 0 → 1s
	latencies := []time.Duration{10 * time.Second, 1 * time.Second, 5 * time.Second}
	p50, _, _ := ComputeLatencyPercentiles(latencies)
	if p50 != 1*time.Second {
		t.Errorf("P50: expected 1s on unsorted input, got %v", p50)
	}
}

func TestComputeThroughput(t *testing.T) {
	tput := ComputeThroughput(100, 10*time.Second)
	if tput != 10.0 {
		t.Errorf("expected 10.0 pods/sec, got %v", tput)
	}
}

func TestComputeThroughput_zeroDuration(t *testing.T) {
	tput := ComputeThroughput(100, 0)
	if tput != 0 {
		t.Errorf("expected 0 for zero duration, got %v", tput)
	}
}

func TestComputeGangCompletionPercentiles_noGangs(t *testing.T) {
	_, _, ok := ComputeGangCompletionPercentiles([]types.PodLatency{
		{PodName: "a", Latency: 1 * time.Second},
	})
	if ok {
		t.Error("expected ok=false when no pod carries a GangID")
	}
}

func TestComputeGangCompletionPercentiles_empty(t *testing.T) {
	_, _, ok := ComputeGangCompletionPercentiles(nil)
	if ok {
		t.Error("expected ok=false for nil input")
	}
}

func TestComputeGangCompletionPercentiles_takesMaxPerGroup(t *testing.T) {
	latencies := []types.PodLatency{
		{PodName: "a1", GangID: "g1", Latency: 1 * time.Second},
		{PodName: "a2", GangID: "g1", Latency: 3 * time.Second}, // last member of g1
		{PodName: "b1", GangID: "g2", Latency: 2 * time.Second}, // last member of g2
	}
	p50, p99, ok := ComputeGangCompletionPercentiles(latencies)
	if !ok {
		t.Fatal("expected ok=true")
	}
	// group completions: g1=3s, g2=2s → sorted [2s, 3s]
	// P50 index: int(2*0.50)-1 = 0 → 2s
	if p50 != 2*time.Second {
		t.Errorf("P50: expected 2s, got %v", p50)
	}
	// P99 index: int(2*0.99)-1 = 0 → 2s (clamped)
	if p99 != 2*time.Second {
		t.Errorf("P99: expected 2s (clamped), got %v", p99)
	}
}

func TestComputeGangCompletionPercentiles_singleGroup(t *testing.T) {
	latencies := []types.PodLatency{
		{PodName: "a1", GangID: "g1", Latency: 2 * time.Second},
		{PodName: "a2", GangID: "g1", Latency: 5 * time.Second},
	}
	p50, p99, ok := ComputeGangCompletionPercentiles(latencies)
	if !ok {
		t.Fatal("expected ok=true")
	}
	// one group, completion = 5s; both percentiles clamp to index 0
	if p50 != 5*time.Second {
		t.Errorf("P50: expected 5s, got %v", p50)
	}
	if p99 != 5*time.Second {
		t.Errorf("P99: expected 5s, got %v", p99)
	}
}

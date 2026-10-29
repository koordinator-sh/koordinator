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

package equivalence

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/component-base/metrics/testutil"

	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

const benchmarkNodeCount = 2000

// BenchmarkEquivalenceReuse measures a real upstream cycle with a warm class decision.
func BenchmarkEquivalenceReuse(b *testing.B) {
	for _, placement := range []PlacementPolicy{PlacementSpread, PlacementPack} {
		b.Run(string(placement), func(b *testing.B) {
			b.StopTimer()
			h := newBenchmarkScheduling(b)
			h.engine.placement = placement
			pod := h.pod("benchmark", "benchmark-hash", "1")
			key := equivalenceCacheKey(h.fwk.ProfileName(), "benchmark-hash")
			primeClass(b, h, key)
			before, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassHits.WithLabelValues(h.fwk.ProfileName()))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.StartTimer()
			for i := 0; i < b.N; i++ {
				if h.engine.equivalence.entries[key].consumed >= DefaultDriftFactor*benchmarkNodeCount {
					b.StopTimer()
					primeClass(b, h, key)
					b.StartTimer()
				}
				result, err := h.schedule(b, pod)
				if err != nil {
					b.Fatal(err)
				}
				if result.SuggestedHost == "" {
					b.Fatal("warm cycle returned no host")
				}
			}
			b.StopTimer()
			after, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassHits.WithLabelValues(h.fwk.ProfileName()))
			if err != nil {
				b.Fatal(err)
			}
			hits := after - before
			if hits != float64(b.N) || h.hooks.scoreCalls.Load() != 0 {
				b.Fatalf("warm benchmark fell back: hits=%v/%d, score calls=%d", hits, b.N, h.hooks.scoreCalls.Load())
			}
			b.ReportMetric(hits*100/float64(b.N), "hit-pct")
			b.ReportMetric(float64(h.hooks.filterCalls.Load())/float64(b.N), "filters/op")
		})
	}
}

// BenchmarkEquivalenceFullCycle measures sampling, Filter, PreScore, Score and terminal backfill.
func BenchmarkEquivalenceFullCycle(b *testing.B) {
	b.StopTimer()
	h := newBenchmarkScheduling(b)
	pod := h.pod("benchmark", "benchmark-hash", "1")
	key := equivalenceCacheKey(h.fwk.ProfileName(), "benchmark-hash")
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	evaluated := 0
	for i := 0; i < b.N; i++ {
		h.engine.equivalence.remove(key)
		result, err := h.schedule(b, pod)
		if err != nil {
			b.Fatal(err)
		}
		if result.SuggestedHost == "" {
			b.Fatal("full cycle returned no host")
		}
		evaluated += result.EvaluatedNodes
	}
	b.StopTimer()
	if h.hooks.preScoreCalls.Load() != int32(b.N) || h.hooks.scoreCalls.Load() == 0 {
		b.Fatalf("full benchmark bypassed scoring: prescore=%d, score=%d", h.hooks.preScoreCalls.Load(), h.hooks.scoreCalls.Load())
	}
	b.ReportMetric(float64(evaluated)/float64(b.N), "evaluated-nodes/op")
	b.ReportMetric(float64(h.hooks.scoreCalls.Load())/float64(b.N), "scored-nodes/op")
}

func primeClass(b *testing.B, h *schedulingSuit, key string) {
	b.Helper()
	names := make([]string, benchmarkNodeCount)
	for i := range names {
		names[i] = fmt.Sprintf("node-%04d", i)
	}
	h.engine.equivalence.store(key, quotaNodes(1<<40, names...), h.sched.CurrentCycle(), nil, h.engine.placement)
}

func newBenchmarkScheduling(b *testing.B) *schedulingSuit {
	b.Helper()
	nodes := make([]*corev1.Node, benchmarkNodeCount)
	for i := range nodes {
		nodes[i] = makeNode(fmt.Sprintf("node-%04d", i), "128", "256Gi")
	}
	h := newSchedulingSuit(b, nodes...)
	h.engine.equivalence.ttl = time.Hour
	return h
}

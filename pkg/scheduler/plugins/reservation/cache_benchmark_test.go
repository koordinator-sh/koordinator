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

package reservation

import (
	"fmt"
	"testing"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	corev1 "k8s.io/api/core/v1"
)

// BenchmarkPreAllocatablePodCache benchmarks the preAllocatablePodCache btree operations
// with multi-node scenarios to simulate real cluster environments.
func BenchmarkPreAllocatablePodCache(b *testing.B) {
	// Scale configurations: nodes x podsPerNode
	scales := []struct {
		nodes       int
		podsPerNode int
	}{
		{10, 100},   // 1000 total pods
		{100, 100},  // 10000 total pods
		{1000, 100}, // 100000 total pods
	}

	// Helper: create pods for multiple nodes
	// Returns pods[nodeIndex][podIndex]
	createPods := func(numNodes, podsPerNode int) [][]*corev1.Pod {
		pods := make([][]*corev1.Pod, numNodes)
		for n := 0; n < numNodes; n++ {
			nodeName := fmt.Sprintf("node-%d", n)
			pods[n] = make([]*corev1.Pod, podsPerNode)
			for i := 0; i < podsPerNode; i++ {
				pods[n][i] = createTestPreAllocatablePod(
					fmt.Sprintf("pod-%d-%d", n, i),
					nodeName,
					"1", "1Gi",
					fmt.Sprintf("%d", i%1000), // varying priorities
				)
			}
		}
		return pods
	}

	// Helper: add all pods to cache
	addAllPods := func(cache *reservationCache, pods [][]*corev1.Pod) {
		for _, nodePods := range pods {
			for _, pod := range nodePods {
				cache.addPreAllocatableCandidateOnNode(pod)
			}
		}
	}

	// Benchmark: Add operation
	b.Run("Add", func(b *testing.B) {
		for _, s := range scales {
			b.Run(fmt.Sprintf("nodes_%d_podsPerNode_%d", s.nodes, s.podsPerNode), func(b *testing.B) {
				pods := createPods(s.nodes, s.podsPerNode)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					cache := newReservationCache(nil)
					addAllPods(cache, pods)
				}
			})
		}
	})

	// Benchmark: Delete operation
	b.Run("Delete", func(b *testing.B) {
		for _, s := range scales {
			b.Run(fmt.Sprintf("nodes_%d_podsPerNode_%d", s.nodes, s.podsPerNode), func(b *testing.B) {
				pods := createPods(s.nodes, s.podsPerNode)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					cache := newReservationCache(nil)
					addAllPods(cache, pods)
					b.StartTimer()

					for n, nodePods := range pods {
						nodeName := fmt.Sprintf("node-%d", n)
						for _, pod := range nodePods {
							cache.deletePreAllocatableCandidateOnNode(nodeName, pod.UID)
						}
					}
				}
			})
		}
	})

	// Benchmark: UpdatePriority operation
	b.Run("UpdatePriority", func(b *testing.B) {
		for _, s := range scales {
			b.Run(fmt.Sprintf("nodes_%d_podsPerNode_%d", s.nodes, s.podsPerNode), func(b *testing.B) {
				pods := createPods(s.nodes, s.podsPerNode)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					cache := newReservationCache(nil)
					addAllPods(cache, pods)
					b.StartTimer()

					for _, nodePods := range pods {
						for j, pod := range nodePods {
							pod.Annotations[apiext.AnnotationPodPreAllocatablePriority] = fmt.Sprintf("%d", (j+500)%1000)
							cache.updatePreAllocatableCandidatePriority(pod)
						}
					}
				}
			})
		}
	})

	// Benchmark: GetAll operation
	b.Run("GetAll", func(b *testing.B) {
		for _, s := range scales {
			b.Run(fmt.Sprintf("nodes_%d_podsPerNode_%d", s.nodes, s.podsPerNode), func(b *testing.B) {
				pods := createPods(s.nodes, s.podsPerNode)
				cache := newReservationCache(nil)
				addAllPods(cache, pods)

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = cache.getAllPreAllocatableCandidates()
				}
			})
		}
	})
}

// reservationsByPrefixRatio_default mimics a realistic node-reservation distribution
// where only a small fraction of reservations carry an indexed prefix label.
// This is the regime where the index is expected to deliver the biggest win.
const reservationsByPrefixRatioDefault = 0.2

// buildBenchCache loads `total` reservations into a cache whose index is
// configured according to `indexEnabled`. Reservations are spread across
// `nodes` distinct nodes round-robin. Approximately `prefixRatio` of them
// carry a label whose key starts with `idxPrefixQuota`; the rest carry only
// generic labels that are never matched by the index.
func buildBenchCache(b *testing.B, total, nodes int, indexEnabled bool, prefixRatio float64) *reservationCache {
	b.Helper()
	c := newReservationCache(nil)
	if indexEnabled {
		c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
			Enabled:     true,
			KeyPrefixes: []string{idxPrefixQuota, idxPrefixTenant},
		})
	}
	limit := int(float64(total) * prefixRatio)
	for i := 0; i < total; i++ {
		labels := map[string]string{
			"app": "bench-" + itoa(i%37),
		}
		if i < limit {
			// indexed: a unique key under the indexed prefix.
			tag := itoa(i)
			labels[idxPrefixQuota+tag] = tag
		}
		r := newIndexTestReservation("uid-"+itoa(i), "r-"+itoa(i), "node-"+itoa(i%nodes), labels)
		c.updateReservation(r)
	}
	return c
}

// benchSizes covers small / medium / large clusters with a realistic 1% of
// reservations carrying the indexed prefix.
var benchSizes = []struct {
	name  string
	total int
	nodes int
}{
	{"100R_100N", 100, 100},
	{"1000R_1000N", 1000, 1000},
	{"10000R_10000N", 10000, 10000},
}

// BenchmarkReservationSelectorBaseline measures the legacy hot path: the
// transformer simply asks the cache for ListAllNodes(true). This is what a
// pod with a reservationSelector pays today when the index is not enabled.
func BenchmarkReservationSelectorBaseline(b *testing.B) {
	for _, sz := range benchSizes {
		sz := sz
		b.Run(sz.name, func(b *testing.B) {
			c := buildBenchCache(b, sz.total, sz.nodes, false, reservationsByPrefixRatioDefault)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = c.ListAllNodes(true)
			}
		})
	}
}

// BenchmarkReservationSelectorIndexHit measures the optimized hot path when
// the pod's selector contains a key that matches a configured prefix. The
// candidate set is whatever portion of reservations carry the indexed prefix
// (≈ reservationsByPrefixRatioDefault of total).
func BenchmarkReservationSelectorIndexHit(b *testing.B) {
	for _, sz := range benchSizes {
		sz := sz
		b.Run(sz.name, func(b *testing.B) {
			c := buildBenchCache(b, sz.total, sz.nodes, true, reservationsByPrefixRatioDefault)
			selector := map[string]string{idxPrefixQuota + "any": "v"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nodes, hit := c.FilterByReservationSelector(selector)
				if !hit {
					nodes = c.ListAllNodes(true)
				}
				_ = nodes
			}
		})
	}
}

// BenchmarkReservationSelectorIndexHitHighRatio measures the regime where the
// indexed prefix selects a large fraction (20%) of reservations -- close to
// the upper end of the production 1/10 ~ 1/2 range. After Tuning A (node-level
// index, no UID->node post-lookup) this should remain at parity with or beat
// Baseline.
func BenchmarkReservationSelectorIndexHitHighRatio(b *testing.B) {
	sz := benchSizes[len(benchSizes)-1]
	c := buildBenchCache(b, sz.total, sz.nodes, true, 0.20)
	selector := map[string]string{idxPrefixQuota + "any": "v"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodes, hit := c.FilterByReservationSelector(selector)
		if !hit {
			nodes = c.ListAllNodes(true)
		}
		_ = nodes
	}
}

// BenchmarkReservationSelectorIndexHitHalfRatio covers the 50% upper bound of
// the production hit-rate range (R ≈ N/2). This is the worst-case payload size
// the index has to materialize.
func BenchmarkReservationSelectorIndexHitHalfRatio(b *testing.B) {
	sz := benchSizes[len(benchSizes)-1]
	c := buildBenchCache(b, sz.total, sz.nodes, true, 0.50)
	selector := map[string]string{idxPrefixQuota + "any": "v"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodes, hit := c.FilterByReservationSelector(selector)
		if !hit {
			nodes = c.ListAllNodes(true)
		}
		_ = nodes
	}
}

// BenchmarkReservationSelectorIndexMiss measures the cost of an enabled index
// when no selector key matches any configured prefix. FilterByReservationSelector
// returns (nil, false) immediately and the caller falls back to ListAllNodes,
// matching the legacy behavior. This bench must be ≈ Baseline + a tiny constant
// overhead (the prefix scan).
func BenchmarkReservationSelectorIndexMiss(b *testing.B) {
	for _, sz := range benchSizes {
		sz := sz
		b.Run(sz.name, func(b *testing.B) {
			c := buildBenchCache(b, sz.total, sz.nodes, true, reservationsByPrefixRatioDefault)
			selector := map[string]string{"unrelated/label": "v"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nodes, hit := c.FilterByReservationSelector(selector)
				if !hit {
					nodes = c.ListAllNodes(true)
				}
				_ = nodes
			}
		})
	}
}

// BenchmarkReservationSelectorIndexAddRemove measures the cost of maintaining
// the index on hot write paths (reservation add/update/delete) when the index
// is ENABLED. It serves as a smoke benchmark to ensure the per-reservation
// index overhead is bounded.
func BenchmarkReservationSelectorIndexAddRemove(b *testing.B) {
	c := newReservationCache(nil)
	c.setReservationSelectorIndexConfig(&config.ReservationSelectorIndexArgs{
		Enabled:     true,
		KeyPrefixes: []string{idxPrefixQuota, idxPrefixTenant},
	})
	r := newIndexTestReservation("uid-bench", "r-bench", "node-bench", map[string]string{
		idxPrefixQuota + "tag": "tag",
		idxPrefixTenant + "t":  "t",
		"app":                  "bench",
		"koordinator.sh/qos":   "LS",
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.updateReservation(r)
		c.DeleteReservation(r)
	}
}

// BenchmarkReservationSelectorIndexAddRemoveDisabled mirrors the above but
// with the index DISABLED. It quantifies the residual cost the write path
// still pays for index-maintenance guards when the feature is turned off.
// The delta vs BenchmarkReservationSelectorIndexAddRemove must stay tiny
// (a couple of branch checks at most) -- otherwise the disabled path is
// regressing for clusters that have not opted in.
func BenchmarkReservationSelectorIndexAddRemoveDisabled(b *testing.B) {
	c := newReservationCache(nil)
	r := newIndexTestReservation("uid-bench", "r-bench", "node-bench", map[string]string{
		idxPrefixQuota + "tag": "tag",
		idxPrefixTenant + "t":  "t",
		"app":                  "bench",
		"koordinator.sh/qos":   "LS",
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.updateReservation(r)
		c.DeleteReservation(r)
	}
}

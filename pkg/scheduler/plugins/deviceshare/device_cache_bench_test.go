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

package deviceshare

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// BenchmarkNodeDeviceCache_AssumeForget measures the per-call cost of the assume/forget
// contract in isolation (AssumePod snapshots what Reserve wrote; ForgetPod clears the marker).
func BenchmarkNodeDeviceCache_AssumeForget(b *testing.B) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.AssumePod(pod, "node-1")
		_ = cache.ForgetPod(pod)
	}
}

// BenchmarkNodeDeviceCache_ConcurrentReserveReconcile measures throughput of the new
// assume/reconcile hot path under concurrency: each op runs Reserve's cache write + AssumePod,
// then the informer OnPodAdd (reconcile) and OnPodDelete for a distinct pod, spread across
// several nodes so per-node locks are contended. Correctness under this same concurrency is
// covered by the -race stress tests; this benchmark tracks the performance dimension.
func BenchmarkNodeDeviceCache_ConcurrentReserveReconcile(b *testing.B) {
	cache := newNodeDeviceCache(nil)
	const nodes = 8
	for i := 0; i < nodes; i++ {
		cache.getNodeDevice(fmt.Sprintf("node-%d", i), true)
	}
	var ctr int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := atomic.AddInt64(&ctr, 1)
			node := fmt.Sprintf("node-%d", id%nodes)
			pod := assumeTestPod(fmt.Sprintf("uid-%d", id), node)
			alloc := gpuAllocations(int32(id%8), 100)
			reserveInto(cache, node, pod, alloc)
			_ = cache.AssumePod(pod, node)
			event := eventPodFrom(pod, node, alloc)
			cache.OnPodAdd(event)
			cache.OnPodDelete(event)
		}
	})
}

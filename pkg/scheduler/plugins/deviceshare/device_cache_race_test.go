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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Test_nodeDeviceCache_ConcurrentReserveAndEvents stresses the assume/forget/reconcile paths
// under concurrency to prove the lock model (top-level nodeDeviceCache.lock for the node map
// and assumed set, per-node nodeDevice.lock for allocations) is free of data races and
// deadlocks. Run with -race. Each iteration races Reserve's AssumePod against an informer
// OnPodAdd for the same pod, plus an Unreserve (ForgetPod) or a delete-before-bind OnPodDelete.
func Test_nodeDeviceCache_ConcurrentReserveAndEvents(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	const nodes = 4
	const iterations = 300

	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		node := fmt.Sprintf("node-%d", i%nodes)
		pod := assumeTestPod(fmt.Sprintf("uid-%d", i), node)
		alloc := gpuAllocations(int32(i%8), 100)

		// Reserve writes the allocation to the per-node cache before AssumePod snapshots it.
		reserveInto(cache, node, pod, alloc)

		wg.Add(3)
		go func() { defer wg.Done(); _ = cache.AssumePod(pod, node) }()
		go func() { defer wg.Done(); cache.OnPodAdd(eventPodFrom(pod, node, alloc)) }()
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				_ = cache.ForgetPod(pod)
			} else {
				cache.OnPodDelete(eventPodFrom(pod, node, alloc))
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent reserve/event operations deadlocked")
	}
}

// Test_nodeDeviceCache_ConcurrentReserveOnPodAdd_NoDoubleCount asserts the key correctness
// invariant: a Reserve racing the informer OnPodAdd for the same pod counts the allocation
// exactly once — never double (reconcile is net-zero when the marker is set; the allocateSet
// double-add guard prevents it otherwise) and never zero.
func Test_nodeDeviceCache_ConcurrentReserveOnPodAdd_NoDoubleCount(t *testing.T) {
	for i := 0; i < 200; i++ {
		cache := newNodeDeviceCache(nil)
		pod := assumeTestPod(fmt.Sprintf("uid-%d", i), "node-1")
		alloc := gpuAllocations(0, 100)
		reserveInto(cache, "node-1", pod, alloc)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = cache.AssumePod(pod, "node-1") }()
		go func() { defer wg.Done(); cache.OnPodAdd(eventPodFrom(pod, "node-1", alloc)) }()
		wg.Wait()

		nd := cache.getNodeDevice("node-1", false)
		assert.ElementsMatch(t, []int{0}, gpuMinors(nd, pod.Namespace, pod.Name),
			"allocation must be counted exactly once regardless of interleaving")
	}
}

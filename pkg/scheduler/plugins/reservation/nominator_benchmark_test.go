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
	"sync"
	"testing"

	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// rlockNominator replicates the pre-fix read path that always takes the global RWMutex, kept as a
// baseline so a regression reintroducing the per-node lock stays visible in the benchmark.
type rlockNominator struct {
	lock                sync.RWMutex
	nominatedReservePod map[string][]*framework.PodInfo
}

func (nm *rlockNominator) NominatedReservePodForNode(nodeName string) []*framework.PodInfo {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	reservePods := make([]*framework.PodInfo, len(nm.nominatedReservePod[nodeName]))
	for i := 0; i < len(reservePods); i++ {
		reservePods[i] = nm.nominatedReservePod[nodeName][i].DeepCopy()
	}
	return reservePods
}

// BenchmarkNominatedReservePodForNode_Empty measures the per-node Reservation BeforeFilter hot path
// on an empty nominator (a cluster with no reservation), which the framework still runs once per
// node in parallel. AtomicGuard is the production lock-free short-circuit; RLockNoGuard is the
// pre-fix baseline whose Parallel run exposes the RWMutex contention the guard removes.
func BenchmarkNominatedReservePodForNode_Empty(b *testing.B) {
	const numNodes = 10000
	nodeNames := make([]string, numNodes)
	for i := range nodeNames {
		nodeNames[i] = fmt.Sprintf("node-%d", i)
	}

	bench := func(b *testing.B, fn func(nodeName string) []*framework.PodInfo) {
		b.Run("Serial", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = fn(nodeNames[i%numNodes])
			}
		})
		b.Run("Parallel", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					_ = fn(nodeNames[i%numNodes])
					i++
				}
			})
		})
	}

	b.Run("AtomicGuard", func(b *testing.B) {
		nm := newNominator(nil, nil)
		bench(b, nm.NominatedReservePodForNode)
	})
	b.Run("RLockNoGuard", func(b *testing.B) {
		nm := &rlockNominator{nominatedReservePod: map[string][]*framework.PodInfo{}}
		bench(b, nm.NominatedReservePodForNode)
	})
}

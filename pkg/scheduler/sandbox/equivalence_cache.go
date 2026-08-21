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

package sandbox

import (
	"sync"
	"time"
)

// defaultEquivalenceClassTTL is a backstop lifetime for a cached scheduling decision. The
// primary invalidation dimensions are per-node quota exhaustion and the consumption drift
// threshold below; the TTL only exists so that a stale entry cannot outlive a scheduling burst.
const defaultEquivalenceClassTTL = 5 * time.Second

// defaultDriftFactor bounds how many pods of the class may be placed from one cached decision
// before the score ordering is recomputed: consumed >= driftFactor*len(nodes) drops the entry.
// Every consumption is one Assume against the frozen resource view, so this directly bounds the
// view drift rather than wall-clock time.
const defaultDriftFactor = 2

// equivalenceClassNode is one feasible node of the class together with the remaining number of
// class pods it can still hold. The quota is computed at backfill time as
// min over resource dimensions of (allocatable - requested) / podRequest, where requested
// aggregates every pod already on the node (running and assumed alike, via UpdateSnapshot).
// Class pods are template-identical, so one division per dimension replaces a per-pod fit
// computation; pods of other classes or schedulers landing afterwards are not visible to this
// accounting, which is what the drift threshold and node-event flush bound.
type equivalenceClassNode struct {
	name  string
	quota int64
}

// equivalenceClassEntry holds the score-ordered feasible node list computed by one full
// scheduling cycle of the class. Pods consume it round-robin: the cursor wraps around instead of
// exhausting the list, because nodes stay valid for many class pods (multi-pod-per-node). An
// entry is dropped when every node's quota is spent, when the drift threshold is reached, on TTL
// expiry, or on any node inventory change (flush).
type equivalenceClassEntry struct {
	key       string
	nodes     []equivalenceClassNode
	cursor    int
	consumed  int
	createdAt time.Time
	lastCycle int64
}

type equivalenceCacheMissReason string

const (
	equivalenceCacheMissEmpty          equivalenceCacheMissReason = "empty"
	equivalenceCacheMissUnknownClass   equivalenceCacheMissReason = "unknown_class"
	equivalenceCacheMissExpired        equivalenceCacheMissReason = "expired"
	equivalenceCacheMissNonConsecutive equivalenceCacheMissReason = "non_consecutive"
	equivalenceCacheMissDrift          equivalenceCacheMissReason = "drift"
	equivalenceCacheMissQuotaExhausted equivalenceCacheMissReason = "quota_exhausted"
	equivalenceCacheMissFilterRejected equivalenceCacheMissReason = "filter_rejected"
	equivalenceCacheMissFilterError    equivalenceCacheMissReason = "filter_error"
	equivalenceCacheMissSnapshotError  equivalenceCacheMissReason = "snapshot_error"
	equivalenceCacheMissPreFilter      equivalenceCacheMissReason = "prefilter_failed"
)

func (r equivalenceCacheMissReason) String() string {
	return string(r)
}

// equivalenceClassCache keeps one active scheduling decision. Reuse is limited to consecutive
// scheduling cycles of the same sandbox template hash, mirroring the bounded state used by
// Kubernetes opportunistic batching. A different hash replaces the active decision instead of
// accumulating another node list.
type equivalenceClassCache struct {
	mu    sync.Mutex
	entry *equivalenceClassEntry
	ttl   time.Duration
	now   func() time.Time
}

func newEquivalenceClassCache(ttl time.Duration) *equivalenceClassCache {
	return &equivalenceClassCache{
		ttl: ttl,
		now: time.Now,
	}
}

// store backfills the class with a score-ordered feasible node list carrying per-node quotas.
// An empty list is ignored: a class with no feasible node must fall back to the full path on
// every pod instead of poisoning the cache.
func (c *equivalenceClassCache) store(key string, nodes []equivalenceClassNode, cycle int64) {
	if key == "" || len(nodes) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	nodes = append([]equivalenceClassNode(nil), nodes...)
	c.entry = &equivalenceClassEntry{
		key:       key,
		nodes:     nodes,
		createdAt: c.now(),
		lastCycle: cycle,
	}
}

// recordConsumption accounts one pod placed on the given node by the full path, so the pod that
// paid for the backfill is not double-booked against the frozen view either.
func (c *equivalenceClassCache) recordConsumption(key, node string, cycle int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entry
	if entry == nil || entry.key != key || entry.lastCycle != cycle {
		return
	}
	for i := range entry.nodes {
		if entry.nodes[i].name == node {
			entry.nodes[i].quota--
			entry.consumed++
			return
		}
	}
}

// next returns the next candidate node of the class, decrementing its quota and advancing the
// cursor round-robin. The second return value is false when the class is unknown, expired,
// non-consecutive, drifted beyond the recomputation threshold, or fully out of quota. The third
// return value identifies the miss reason. On a miss, the active entry is dropped and the caller
// falls back to the full path, which backfills a fresh entry.
func (c *equivalenceClassCache) next(key string, cycle int64) (string, bool, equivalenceCacheMissReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entry
	if entry == nil {
		return "", false, equivalenceCacheMissEmpty
	}
	// Multiple candidates may be checked in one scheduling cycle when an earlier cached node no
	// longer passes Filter. Otherwise, only the immediately following cycle may reuse the state.
	if entry.key != key {
		c.entry = nil
		return "", false, equivalenceCacheMissUnknownClass
	}
	if cycle != entry.lastCycle && cycle != entry.lastCycle+1 {
		c.entry = nil
		return "", false, equivalenceCacheMissNonConsecutive
	}
	if c.now().Sub(entry.createdAt) > c.ttl {
		c.entry = nil
		return "", false, equivalenceCacheMissExpired
	}
	if entry.consumed >= defaultDriftFactor*len(entry.nodes) {
		c.entry = nil
		return "", false, equivalenceCacheMissDrift
	}
	entry.lastCycle = cycle
	for i := 0; i < len(entry.nodes); i++ {
		idx := (entry.cursor + i) % len(entry.nodes)
		if entry.nodes[idx].quota <= 0 {
			continue
		}
		entry.nodes[idx].quota--
		entry.consumed++
		entry.cursor = (idx + 1) % len(entry.nodes)
		return entry.nodes[idx].name, true, ""
	}
	c.entry = nil
	return "", false, equivalenceCacheMissQuotaExhausted
}

// flush drops the active class. It is called on node add/update/delete events: any change of the
// node inventory may invalidate the cached decision, and rebuilding it is one full scheduling
// cycle away.
func (c *equivalenceClassCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entry = nil
}

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
	"container/list"
	"sync"
	"time"
)

// defaultEquivalenceClassTTL is a backstop lifetime for a cached scheduling decision. The
// primary invalidation dimensions are per-node quota exhaustion and the consumption drift
// threshold below; the TTL only exists so that a stale entry cannot outlive a scheduling burst.
const defaultEquivalenceClassTTL = 5 * time.Second

// defaultEquivalenceClassCacheSize is the maximum number of sandbox template hashes retained
// by the equivalence cache when no explicit scheduler flag is provided.
const defaultEquivalenceClassCacheSize = 16

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
	key        string
	nodes      []equivalenceClassNode
	cursor     int
	consumed   int
	createdAt  time.Time
	lastCycle  int64
	lruElement *list.Element
}

type equivalenceCacheMissReason string

const (
	equivalenceCacheMissEmpty          equivalenceCacheMissReason = "empty"
	equivalenceCacheMissUnknownClass   equivalenceCacheMissReason = "unknown_class"
	equivalenceCacheMissExpired        equivalenceCacheMissReason = "expired"
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

// equivalenceClassCache keeps a bounded set of scheduling decisions keyed by sandbox template
// hash. Entries are reused independently, so interleaved hashes do not invalidate one another.
// The LRU bound limits memory while retaining the most recently used equivalence classes.
type equivalenceClassCache struct {
	mu       sync.Mutex
	entries  map[string]*equivalenceClassEntry
	lru      *list.List
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

func newEquivalenceClassCache(ttl time.Duration, capacity int) *equivalenceClassCache {
	if capacity <= 0 {
		capacity = defaultEquivalenceClassCacheSize
	}
	return &equivalenceClassCache{
		entries:  make(map[string]*equivalenceClassEntry, capacity),
		lru:      list.New(),
		capacity: capacity,
		ttl:      ttl,
		now:      time.Now,
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

	c.removeExpiredLocked(c.now())
	if oldEntry := c.entries[key]; oldEntry != nil {
		c.removeLocked(oldEntry)
	}

	nodes = append([]equivalenceClassNode(nil), nodes...)
	entry := &equivalenceClassEntry{
		key:       key,
		nodes:     nodes,
		createdAt: c.now(),
		lastCycle: cycle,
	}
	entry.lruElement = c.lru.PushFront(entry)
	c.entries[key] = entry
	for len(c.entries) > c.capacity {
		c.removeLocked(c.lru.Back().Value.(*equivalenceClassEntry))
	}
}

// recordConsumption accounts one pod placed on the given node by the full path, so the pod that
// paid for the backfill is not double-booked against the frozen view either.
func (c *equivalenceClassCache) recordConsumption(key, node string, cycle int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil || entry.key != key || entry.lastCycle != cycle {
		return
	}
	c.lru.MoveToFront(entry.lruElement)
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
// drifted beyond the recomputation threshold, or fully out of quota. The third return value
// identifies the miss reason. On a miss, the affected entry is dropped and the caller falls back
// to the full path, which backfills a fresh entry.
func (c *equivalenceClassCache) next(key string, cycle int64) (string, bool, equivalenceCacheMissReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		if len(c.entries) == 0 {
			return "", false, equivalenceCacheMissEmpty
		}
		return "", false, equivalenceCacheMissUnknownClass
	}
	if c.now().Sub(entry.createdAt) > c.ttl {
		c.removeLocked(entry)
		return "", false, equivalenceCacheMissExpired
	}
	if entry.consumed >= defaultDriftFactor*len(entry.nodes) {
		c.removeLocked(entry)
		return "", false, equivalenceCacheMissDrift
	}
	c.lru.MoveToFront(entry.lruElement)
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
	c.removeLocked(entry)
	return "", false, equivalenceCacheMissQuotaExhausted
}

// flush drops all cached classes. It is called on node add/update/delete events: any change of the
// node inventory may invalidate cached decisions, and rebuilding them is one full scheduling cycle
// away.
func (c *equivalenceClassCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*equivalenceClassEntry, c.capacity)
	c.lru.Init()
}

func (c *equivalenceClassCache) removeExpiredLocked(now time.Time) {
	for element := c.lru.Back(); element != nil; {
		previous := element.Prev()
		entry := element.Value.(*equivalenceClassEntry)
		if now.Sub(entry.createdAt) > c.ttl {
			c.removeLocked(entry)
		}
		element = previous
	}
}

func (c *equivalenceClassCache) removeLocked(entry *equivalenceClassEntry) {
	if entry == nil {
		return
	}
	delete(c.entries, entry.key)
	if entry.lruElement != nil {
		c.lru.Remove(entry.lruElement)
		entry.lruElement = nil
	}
}

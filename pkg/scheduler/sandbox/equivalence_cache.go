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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
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
// expiry, or when a node event removes its last cached node.
type equivalenceClassEntry struct {
	key           string
	nodes         []equivalenceClassNode
	resourceNames sets.Set[corev1.ResourceName]
	cursor        int
	consumed      int
	createdAt     time.Time
	cycle         int64
	lruElement    *list.Element
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
	equivalenceCacheMissNodeEvent      equivalenceCacheMissReason = "node_event"
)

func (r equivalenceCacheMissReason) String() string {
	return string(r)
}

// equivalenceClassCache keeps a bounded set of scheduling decisions keyed by a profile-namespaced
// sandbox template hash. Entries are reused independently, so interleaved profiles and hashes do
// not invalidate one another. The LRU bound limits memory while retaining the most recently used
// equivalence classes.
type equivalenceClassCache struct {
	mu                   sync.Mutex
	entries              map[string]*equivalenceClassEntry
	lru                  *list.List
	capacity             int
	ttl                  time.Duration
	now                  func() time.Time
	nodeEventInvalidated map[string]struct{}
}

func newEquivalenceClassCache(ttl time.Duration, capacity int) *equivalenceClassCache {
	if capacity <= 0 {
		capacity = defaultEquivalenceClassCacheSize
	}
	return &equivalenceClassCache{
		entries:              make(map[string]*equivalenceClassEntry, capacity),
		lru:                  list.New(),
		capacity:             capacity,
		ttl:                  ttl,
		now:                  time.Now,
		nodeEventInvalidated: make(map[string]struct{}),
	}
}

// store backfills the class with a score-ordered feasible node list carrying per-node quotas.
// An empty list removes any old entry: without reusable nodes the class must use the full path.
func (c *equivalenceClassCache) store(key string, nodes []equivalenceClassNode, cycle int64, podRequests corev1.ResourceList) {
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeExpiredLocked(c.now())
	delete(c.nodeEventInvalidated, key)
	if oldEntry := c.entries[key]; oldEntry != nil {
		c.removeLocked(oldEntry)
	}
	if len(nodes) == 0 {
		return
	}

	nodes = append([]equivalenceClassNode(nil), nodes...)
	resourceNames := sets.New(corev1.ResourcePods)
	for name, quantity := range podRequests {
		if quantity.Sign() > 0 {
			resourceNames.Insert(name)
		}
	}
	entry := &equivalenceClassEntry{
		key:           key,
		nodes:         nodes,
		resourceNames: resourceNames,
		createdAt:     c.now(),
		cycle:         cycle,
	}
	entry.lruElement = c.lru.PushFront(entry)
	c.entries[key] = entry
	koordmetrics.RecordSandboxEquivalenceClassCacheEntries(1)
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
	if entry == nil || entry.key != key || entry.cycle != cycle {
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
		if _, ok := c.nodeEventInvalidated[key]; ok {
			delete(c.nodeEventInvalidated, key)
			return "", false, equivalenceCacheMissNodeEvent
		}
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
	entry.cycle = cycle
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

// flush drops all cached classes. It is used for invalidations that cannot be scoped to one node.
func (c *equivalenceClassCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entryCount := len(c.entries)
	c.nodeEventInvalidated = make(map[string]struct{})
	c.entries = make(map[string]*equivalenceClassEntry, c.capacity)
	c.lru.Init()
	koordmetrics.RecordSandboxEquivalenceClassCacheEntries(-entryCount)
}

// flushNodeEvent drops all cached classes and remembers the affected keys so the next lookup can
// attribute the miss to the node event instead of reporting an indistinguishable empty cache.
func (c *equivalenceClassCache) flushNodeEvent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entryCount := len(c.entries)
	for key := range c.entries {
		c.markNodeEventInvalidatedLocked(key)
	}
	c.entries = make(map[string]*equivalenceClassEntry, c.capacity)
	c.lru.Init()
	koordmetrics.RecordSandboxEquivalenceClassCacheEntries(-entryCount)
}

// removeNode removes a node from classes whose quotas depend on changed resources, or all classes when
// changedResources is nil. An entry whose last node was removed reports a node-event miss.
func (c *equivalenceClassCache) removeNode(nodeName string, changedResources sets.Set[corev1.ResourceName]) {
	if nodeName == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	resources := changedResources.UnsortedList()
	for element := c.lru.Back(); element != nil; {
		entry := element.Value.(*equivalenceClassEntry)
		element = element.Prev()
		if changedResources != nil && !entry.resourceNames.HasAny(resources...) {
			continue
		}
		for i := range entry.nodes {
			if entry.nodes[i].name != nodeName {
				continue
			}
			entry.nodes = append(entry.nodes[:i], entry.nodes[i+1:]...)
			if entry.cursor > i {
				entry.cursor--
			}
			if len(entry.nodes) == 0 {
				c.markNodeEventInvalidatedLocked(entry.key)
				c.removeLocked(entry)
			} else if entry.cursor >= len(entry.nodes) {
				entry.cursor = 0
			}
			break
		}
	}
}

func (c *equivalenceClassCache) markNodeEventInvalidatedLocked(key string) {
	if _, exists := c.nodeEventInvalidated[key]; exists {
		return
	}
	for len(c.nodeEventInvalidated) >= c.capacity {
		for staleKey := range c.nodeEventInvalidated {
			delete(c.nodeEventInvalidated, staleKey)
			break
		}
	}
	c.nodeEventInvalidated[key] = struct{}{}
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
	if current, ok := c.entries[entry.key]; ok && current == entry {
		delete(c.entries, entry.key)
		koordmetrics.RecordSandboxEquivalenceClassCacheEntries(-1)
	}
	if entry.lruElement != nil {
		c.lru.Remove(entry.lruElement)
		entry.lruElement = nil
	}
}

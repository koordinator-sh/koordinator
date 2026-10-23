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
	"container/list"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// defaultEquivalenceClassTTL is a backstop lifetime for a cached scheduling decision. The
// primary invalidation dimensions are per-node quota exhaustion and the consumption drift
// threshold below; the TTL only exists so that a stale entry cannot outlive a scheduling burst.
const DefaultEquivalenceClassTTL = 5 * time.Second

// DefaultEquivalenceClassCacheSize is the maximum number of equivalence classes retained by the
// cache when no explicit size is provided.
const DefaultEquivalenceClassCacheSize = 16

// DefaultDriftFactor bounds how many pods of the class may be placed from one cached decision
// before the score ordering is recomputed: consumed >= driftFactor*len(nodes) drops the entry.
// Every consumption is one Assume against the frozen resource view, so this directly bounds the
// view drift rather than wall-clock time.
const DefaultDriftFactor = 2

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
// scheduling cycle of the class. The placement policy selects from this order relative to last,
// the node the previous class pod was placed on. An entry is dropped when every node's quota
// is spent, when the drift threshold is reached, on TTL expiry, or when a node event removes its
// last cached node.
type equivalenceClassEntry struct {
	key           string
	nodes         []equivalenceClassNode
	resourceNames sets.Set[corev1.ResourceName]
	placement     PlacementPolicy
	// last is the index of the node the previous class pod was placed on, or -1 before any.
	last       int
	consumed   int
	createdAt  time.Time
	cycle      int64
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
	equivalenceCacheMissExtenderError  equivalenceCacheMissReason = "extender_error"
	equivalenceCacheMissSnapshotError  equivalenceCacheMissReason = "snapshot_error"
	equivalenceCacheMissPreFilter      equivalenceCacheMissReason = "prefilter_failed"
	equivalenceCacheMissNominated      equivalenceCacheMissReason = "nominated_node"
	equivalenceCacheMissPluginVeto     equivalenceCacheMissReason = "plugin_not_reusable"
	// equivalenceCacheMissNotBackfilled counts full cycles whose feasible nodes all had no quota.
	equivalenceCacheMissNotBackfilled    equivalenceCacheMissReason = "not_backfilled"
	equivalenceCacheMissEligibilityError equivalenceCacheMissReason = "eligibility_error"
	// equivalenceCacheMissSnapshotUnsupported counts pods handed to the upstream path because the
	// profile's snapshot lister cannot be refreshed by the equivalence path.
	equivalenceCacheMissSnapshotUnsupported equivalenceCacheMissReason = "snapshot_unsupported"
)

func (r equivalenceCacheMissReason) String() string {
	return string(r)
}

// equivalenceClassCache keeps a bounded set of scheduling decisions keyed by a profile-namespaced
// equivalence class key. Entries are reused independently, so interleaved profiles and keys do
// not invalidate one another. The LRU bound limits memory while retaining the most recently used
// equivalence classes.
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
		klog.Warningf("capacity must be > 0 for equivalenceClassCache, use default capacity: %d", DefaultEquivalenceClassCacheSize)
		capacity = DefaultEquivalenceClassCacheSize
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
// An empty list removes any old entry: without reusable nodes the class must use the full path.
func (c *equivalenceClassCache) store(key string, nodes []equivalenceClassNode, cycle int64, podRequests corev1.ResourceList, placement PlacementPolicy) {
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeExpiredLocked(c.now())
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
		placement:     placement,
		last:          -1,
		createdAt:     c.now(),
		cycle:         cycle,
	}
	entry.lruElement = c.lru.PushFront(entry)
	c.entries[key] = entry
	koordmetrics.RecordEquivalenceClassCacheEntries(1)
	for len(c.entries) > c.capacity {
		c.removeLocked(c.lru.Back().Value.(*equivalenceClassEntry))
	}
}

// consume accounts one class pod placed on the given node by either path, so later pods are not
// double-booked against the frozen view, and makes the node the next pod's placement anchor.
//
// Consumption is one-way: a subsequent Reserve/Permit/Bind failure that forgets the pod from the
// scheduler cache does not reclaim the slot here. Under-counting a class's remaining capacity can
// only force a conservative fallback to the full path, never an overcommit, so the drift threshold
// and TTL are the intended correction rather than per-failure quota bookkeeping.
func (c *equivalenceClassCache) consume(key, node string, cycle int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil || entry.cycle != cycle {
		return
	}
	c.lru.MoveToFront(entry.lruElement)
	for i := range entry.nodes {
		if entry.nodes[i].name == node {
			entry.nodes[i].quota--
			entry.consumed++
			entry.last = i
			return
		}
	}
}

// rejectNode discards a candidate that failed a pod's Filter or extender check by zeroing its
// quota. The failure shows that the quota computed at backfill time overestimates the node, so
// later pods of the class skip it instead of paying for the same check again. Zeroing is
// intentionally permanent for this entry: a rejection that proves transient (e.g. nominated-pod
// interference) costs one candidate until the next backfill, never an overcommit. The fast-path
// loop does not rely on it to terminate: it stops once the pod has tried every candidate.
func (c *equivalenceClassCache) rejectNode(key, node string, cycle int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil || entry.cycle != cycle {
		return
	}
	for i := range entry.nodes {
		if entry.nodes[i].name == node {
			entry.nodes[i].quota = 0
			return
		}
	}
}

// candidate returns one cached node with quota that is not in exclude. Spread walks the cached
// order after last, wrapping around to last as a fallback; pack tries last before its successors.
// A fresh entry starts at the head with either policy.
//
// It returns no node and a miss reason when the class is unknown, expired, drifted beyond the
// recomputation threshold, or out of quota; the entry is dropped in all of these cases. When
// only excluded nodes have quota left, it reports quota_exhausted but keeps the entry for other
// pods of the class. The caller reports the chosen node to consume.
func (c *equivalenceClassCache) candidate(key string, cycle int64, exclude sets.Set[string]) (string, equivalenceCacheMissReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		if len(c.entries) == 0 {
			return "", equivalenceCacheMissEmpty
		}
		return "", equivalenceCacheMissUnknownClass
	}
	if c.now().Sub(entry.createdAt) > c.ttl {
		c.removeLocked(entry)
		return "", equivalenceCacheMissExpired
	}
	if entry.consumed >= DefaultDriftFactor*len(entry.nodes) {
		c.removeLocked(entry)
		return "", equivalenceCacheMissDrift
	}
	c.lru.MoveToFront(entry.lruElement)
	entry.cycle = cycle

	usable := func(i int) bool {
		return entry.nodes[i].quota > 0 && !exclude.Has(entry.nodes[i].name)
	}
	if entry.placement == PlacementPack && entry.last >= 0 && usable(entry.last) {
		return entry.nodes[entry.last].name, ""
	}
	for i := 1; i <= len(entry.nodes); i++ {
		idx := (entry.last + i) % len(entry.nodes)
		if usable(idx) {
			return entry.nodes[idx].name, ""
		}
	}
	for i := range entry.nodes {
		if entry.nodes[i].quota > 0 {
			return "", equivalenceCacheMissQuotaExhausted
		}
	}
	c.removeLocked(entry)
	return "", equivalenceCacheMissQuotaExhausted
}

// len returns the number of cached entries. It is safe for concurrent use.
func (c *equivalenceClassCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// remove drops the cached decision of one class and reports whether one was cached. Invalidations
// specific to one pod or class use it so that unrelated classes keep their decisions.
func (c *equivalenceClassCache) remove(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return false
	}
	c.removeLocked(entry)
	return true
}

// flushNodeEvent drops every cached class.
func (c *equivalenceClassCache) flushNodeEvent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entryCount := len(c.entries)
	c.entries = make(map[string]*equivalenceClassEntry, c.capacity)
	c.lru.Init()
	koordmetrics.RecordEquivalenceClassCacheEntries(-entryCount)
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
			// Keep last on the same node, or on its predecessor when the node itself is removed,
			// so the next pod continues the walk where it stopped.
			if entry.last >= i {
				entry.last--
			}
			if len(entry.nodes) == 0 {
				c.removeLocked(entry)
			}
			break
		}
	}
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
		koordmetrics.RecordEquivalenceClassCacheEntries(-1)
	}
	if entry.lruElement != nil {
		c.lru.Remove(entry.lruElement)
		entry.lruElement = nil
	}
}

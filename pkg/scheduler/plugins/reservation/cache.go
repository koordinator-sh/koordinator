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
	"strconv"
	"strings"
	"sync"

	"github.com/google/btree"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulinglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// preAllocatablePodItem implements btree.Item for storing pods with priorities
type preAllocatablePodItem struct {
	pod      *corev1.Pod
	priority int64
}

// Less implements btree.Item interface
// Returns true if this item should be ordered before the other item
func (p *preAllocatablePodItem) Less(than btree.Item) bool {
	other := than.(*preAllocatablePodItem)
	// Higher priority comes first (descending order)
	if p.priority != other.priority {
		return p.priority > other.priority
	}
	// If priorities are equal, order by UID for stability
	return p.pod.UID < other.pod.UID
}

// preAllocatablePodCache manages sorted pre-allocatable pods for a node using btree
type preAllocatablePodCache struct {
	tree  *btree.BTree                         // Sorted storage by priority
	index map[types.UID]*preAllocatablePodItem // UID -> item for fast lookup
}

// newPreAllocatablePodCache creates a new cache instance
func newPreAllocatablePodCache() *preAllocatablePodCache {
	return &preAllocatablePodCache{
		tree:  btree.New(32),
		index: make(map[types.UID]*preAllocatablePodItem),
	}
}

type reservationCache struct {
	reservationLister  schedulinglister.ReservationLister
	lock               sync.RWMutex
	reservationInfos   map[types.UID]*frameworkext.ReservationInfo
	reservationsOnNode map[string]map[types.UID]struct{} // all reservations on node
	matchableOnNode    map[string]map[types.UID]struct{} // look up available reservations on node
	allocatedOnNode    map[string]map[types.UID]struct{} // look up allocated available reservations on node
	// preAllocatablePodsOnNode caches sorted pre-allocatable candidate pods per node
	// Uses btree for automatic ordering by priority
	preAllocatablePodsOnNode map[string]*preAllocatablePodCache
	// preAllocatableLabelKey is the resolved label key for identifying pre-allocatable pods.
	preAllocatableLabelKey string
	// preAllocatablePriorityAnnotationKey is the resolved annotation key for pod priority.
	preAllocatablePriorityAnnotationKey string

	// reservationSelector white-list inverted index.
	//
	// Design principle: ROI-based initial filter, NOT precise pre-selection.
	// The index returns the candidate node set from the FIRST matching bucket
	// (in priority order); downstream full selector matching in
	// checkReservationMatchedOrIgnored guarantees correctness.
	//
	// Priority order (read side):
	//   1. KeyPrefixes (config order) — first prefix hit wins
	//   2. Keys (config order) — first exact-key hit wins
	// No AND-join across buckets; no dedup; no smallest-bucket selection.
	//
	// Indexing scope: only matchable reservations (IsMatchable() == true)
	// are indexed. Non-matchable reservations (Failed/Succeeded/expired)
	// are excluded to keep the candidate set tight. State transitions
	// (matchable ↔ non-matchable) are handled by the existing
	// removeFromIndex + addToIndex pattern in updateReservation.
	//
	// Concurrency: indexEnabled / indexedPrefixes / indexedKeys are
	// immutable after initialization so they can be read lock-free.
	// nodesByPrefix, nodesByExactKV and indexEntryByUID are guarded by
	// `lock`.
	//
	// Bucket types:
	//  - nodesByPrefix[p][node] → set[uid]: keyed by label-key prefix
	//  - nodesByExactKV[k][v][node] → set[uid]: keyed by (key, value) tuple
	//
	// We keep the per-(bucket, node) UID set instead of a refcount:
	// reservation count per node is small, and an explicit set guarantees
	// no over/under-decrement under bug or replay.
	indexEnabled    bool
	indexedPrefixes []string
	indexedKeys     sets.Set[string]                                     // O(1) lookup for matchedKey
	indexedKeyList  []string                                             // config order for priority-based lookup
	nodesByPrefix   map[string]map[string]sets.Set[types.UID]            // prefix -> node -> set[uid]
	nodesByExactKV  map[string]map[string]map[string]sets.Set[types.UID] // exact key -> value -> node -> set[uid]
	indexEntryByUID map[types.UID]*reservationIndexEntry
}

// reservationIndexEntry remembers what a single reservation contributed to the
// inverted index, so we can incrementally undo it on removal/update without
// re-scanning the reservation's labels. prefixes is populated when at least
// one indexed prefix bucket fired; kvs records the exact (key, value) pairs
// indexed for this reservation so we can reverse the (k, v, node) entry on
// removal without re-reading the reservation labels (which may have changed
// concurrently). Either field may be nil if its corresponding white-list did
// not fire.
type reservationIndexEntry struct {
	node     string
	prefixes sets.Set[string]
	kvs      map[string]string
}

func newReservationCache(reservationLister schedulinglister.ReservationLister) *reservationCache {
	cache := &reservationCache{
		reservationLister:        reservationLister,
		reservationInfos:         map[types.UID]*frameworkext.ReservationInfo{},
		reservationsOnNode:       map[string]map[types.UID]struct{}{},
		matchableOnNode:          map[string]map[types.UID]struct{}{},
		allocatedOnNode:          map[string]map[types.UID]struct{}{},
		preAllocatablePodsOnNode: map[string]*preAllocatablePodCache{},
	}
	cache.preAllocatableLabelKey = apiext.LabelPodPreAllocatable
	cache.preAllocatablePriorityAnnotationKey = apiext.AnnotationPodPreAllocatablePriority
	return cache
}

// setPreAllocationConfig updates the label and annotation keys for pre-allocatable pod detection.
// This should be called during plugin initialization if custom keys are needed.
func (cache *reservationCache) setPreAllocationConfig(preAllocationConfig *config.PreAllocationConfig) {
	if preAllocationConfig != nil {
		if preAllocationConfig.ClusterLabelKey != "" {
			cache.preAllocatableLabelKey = preAllocationConfig.ClusterLabelKey
		}
		if preAllocationConfig.ClusterPriorityAnnotationKey != "" {
			cache.preAllocatablePriorityAnnotationKey = preAllocationConfig.ClusterPriorityAnnotationKey
		}
	}
}

// setReservationSelectorIndexConfig configures the reservationSelector white-list
// existence index.
//
// Concurrency contract (IMPORTANT): the fast-path readers of indexEnabled /
// indexedPrefixes are intentionally lock-free (see the struct comment), so
// this method MUST be called only during plugin/cache initialization, before
// any event-handler is registered and before any concurrent reader runs.
// Runtime reconfiguration on a live cache is NOT supported and would race
// with those lock-free readers. To change the index config at runtime, the
// reads of indexEnabled / indexedPrefixes would have to be moved under the
// RWMutex first.
//
// As long as the contract above is honored, it is safe to call this method
// after reservationInfos has been populated: any reservation already known
// to the cache will be back-filled into the freshly built index, so the
// index stays complete regardless of the wiring order between
// setReservationSelectorIndexConfig and the informer initial list. When args
// is nil or Enabled=false the index is disabled and any previous state is
// cleared.
func (cache *reservationCache) setReservationSelectorIndexConfig(args *config.ReservationSelectorIndexArgs) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	// Always clear previous index state first so a re-configuration cannot
	// leave stale entries dangling.
	cache.indexEnabled = false
	cache.indexedPrefixes = nil
	cache.indexedKeys = nil
	cache.indexedKeyList = nil
	cache.nodesByPrefix = nil
	cache.nodesByExactKV = nil
	cache.indexEntryByUID = nil

	if args == nil || !args.Enabled {
		return
	}
	prefixes := make([]string, 0, len(args.KeyPrefixes))
	seenPrefix := sets.New[string]()
	for _, p := range args.KeyPrefixes {
		p = strings.TrimSpace(p)
		if p == "" || seenPrefix.Has(p) {
			continue
		}
		seenPrefix.Insert(p)
		prefixes = append(prefixes, p)
	}
	keys := sets.New[string]()
	var keyList []string
	seenKey := sets.New[string]()
	for _, k := range args.Keys {
		k = strings.TrimSpace(k)
		if k == "" || seenKey.Has(k) {
			continue
		}
		seenKey.Insert(k)
		keys.Insert(k)
		keyList = append(keyList, k)
	}
	if len(prefixes) == 0 && keys.Len() == 0 {
		return
	}
	// Publish prefixes/keys BEFORE flipping the enabled flag, so any reader
	// that observes indexEnabled=true via the lock-free fast path is
	// guaranteed to also see fully initialized white-lists.
	cache.indexedPrefixes = prefixes
	cache.indexedKeys = keys
	cache.indexedKeyList = keyList
	cache.nodesByPrefix = map[string]map[string]sets.Set[types.UID]{}
	cache.nodesByExactKV = map[string]map[string]map[string]sets.Set[types.UID]{}
	cache.indexEntryByUID = map[types.UID]*reservationIndexEntry{}
	cache.indexEnabled = true

	// Backfill: replay every reservation already known to the cache through
	// the index. This makes the index correct regardless of whether setConfig
	// runs before or after the event-handler registration / informer initial
	// list, eliminating a class of "missing reservation in index" bugs.
	for _, rInfo := range cache.reservationInfos {
		cache.addToIndex(rInfo)
	}
}

// matchedPrefix returns the first configured prefix that the given key starts
// with, or "" if none matches. Callers must hold no special lock; the
// configuration is immutable after initialization.
func (cache *reservationCache) matchedPrefix(key string) string {
	if !cache.indexEnabled {
		return ""
	}
	for _, p := range cache.indexedPrefixes {
		if strings.HasPrefix(key, p) {
			return p
		}
	}
	return ""
}

// matchedKey reports whether the given label key is on the exact-key
// white-list. Callers must hold no special lock; the configuration is
// immutable after initialization.
func (cache *reservationCache) matchedKey(key string) bool {
	if !cache.indexEnabled || cache.indexedKeys == nil {
		return false
	}
	return cache.indexedKeys.Has(key)
}

// addToIndex inserts the existence entries of the given reservation into the
// inverted index. Caller MUST hold cache.lock for writing.
//
// Only matchable reservations (IsMatchable() == true) that are bound to a
// node are indexed. Non-matchable reservations are skipped to keep the
// candidate set tight; they will be picked up on a subsequent
// updateReservation when they become matchable. The existing
// removeFromIndex + addToIndex pattern in updateReservation handles the
// matchable ↔ non-matchable state transition automatically.
//
// Each label key is independently checked against the prefix white-list and
// the exact-key white-list; a key matching both contributes to both buckets.
func (cache *reservationCache) addToIndex(rInfo *frameworkext.ReservationInfo) {
	if !cache.indexEnabled || rInfo == nil {
		return
	}
	if !rInfo.IsMatchable() {
		return
	}
	obj := rInfo.GetObject()
	if obj == nil {
		return
	}
	uid := rInfo.UID()
	if uid == "" {
		return
	}
	node := rInfo.GetNodeName()
	if node == "" {
		return
	}
	labels := obj.GetLabels()
	if len(labels) == 0 {
		return
	}
	var trackedPrefixes sets.Set[string]
	var trackedKVs map[string]string
	for k, v := range labels {
		// exact-(key,value) bucket: a single reservation has exactly one value
		// per label key, so we only need to record (k -> v) once.
		if cache.matchedKey(k) {
			if trackedKVs == nil {
				trackedKVs = map[string]string{}
			}
			if _, already := trackedKVs[k]; !already {
				trackedKVs[k] = v
				byValue := cache.nodesByExactKV[k]
				if byValue == nil {
					byValue = map[string]map[string]sets.Set[types.UID]{}
					cache.nodesByExactKV[k] = byValue
				}
				byNode := byValue[v]
				if byNode == nil {
					byNode = map[string]sets.Set[types.UID]{}
					byValue[v] = byNode
				}
				uids := byNode[node]
				if uids == nil {
					uids = sets.New[types.UID]()
					byNode[node] = uids
				}
				uids.Insert(uid)
			}
		}
		// prefix bucket
		p := cache.matchedPrefix(k)
		if p == "" {
			continue
		}
		if trackedPrefixes == nil {
			trackedPrefixes = sets.New[string]()
		}
		if trackedPrefixes.Has(p) {
			continue
		}
		trackedPrefixes.Insert(p)
		byNode := cache.nodesByPrefix[p]
		if byNode == nil {
			byNode = map[string]sets.Set[types.UID]{}
			cache.nodesByPrefix[p] = byNode
		}
		uids := byNode[node]
		if uids == nil {
			uids = sets.New[types.UID]()
			byNode[node] = uids
		}
		uids.Insert(uid)
	}
	if trackedPrefixes != nil || trackedKVs != nil {
		cache.indexEntryByUID[uid] = &reservationIndexEntry{
			node:     node,
			prefixes: trackedPrefixes,
			kvs:      trackedKVs,
		}
	}
}

// removeFromIndex removes a reservation's previously indexed entries from
// both the prefix bucket and the exact-(key,value) bucket. Caller MUST hold
// cache.lock for writing.
func (cache *reservationCache) removeFromIndex(uid types.UID) {
	if !cache.indexEnabled {
		return
	}
	entry, ok := cache.indexEntryByUID[uid]
	if !ok {
		return
	}
	delete(cache.indexEntryByUID, uid)
	for p := range entry.prefixes {
		byNode := cache.nodesByPrefix[p]
		if byNode == nil {
			continue
		}
		uids := byNode[entry.node]
		if uids == nil {
			continue
		}
		uids.Delete(uid)
		if uids.Len() == 0 {
			delete(byNode, entry.node)
			if len(byNode) == 0 {
				delete(cache.nodesByPrefix, p)
			}
		}
	}
	for k, v := range entry.kvs {
		byValue := cache.nodesByExactKV[k]
		if byValue == nil {
			continue
		}
		byNode := byValue[v]
		if byNode == nil {
			continue
		}
		uids := byNode[entry.node]
		if uids == nil {
			continue
		}
		uids.Delete(uid)
		if uids.Len() == 0 {
			delete(byNode, entry.node)
			if len(byNode) == 0 {
				delete(byValue, v)
				if len(byValue) == 0 {
					delete(cache.nodesByExactKV, k)
				}
			}
		}
	}
}

// FilterByReservationSelector returns the candidate node names from the
// first matching index bucket, using a priority-based lookup:
//
//  1. KeyPrefixes (config order): for each configured prefix, scan selector
//     keys; the first selector key that starts with this prefix triggers a
//     nodesByPrefix bucket lookup → return immediately.
//  2. Keys (config order): for each configured exact key, check if the
//     selector contains it; the first hit triggers a nodesByExactKV bucket
//     lookup → return immediately.
//
// No AND-join, no dedup, no smallest-bucket selection. The first hit is the
// highest-ROI initial filter; downstream full selector matching in
// checkReservationMatchedOrIgnored guarantees correctness.
//
// When no configured prefix or key matches any selector key, indexHit=false
// and the caller falls back to ListAllNodes.
//
// The returned matchedKey reports which index key (prefix or exact key) triggered
// the hit, for observability and debugging. It is empty when indexHit=false.
func (cache *reservationCache) FilterByReservationSelector(selector map[string]string) ([]string, string, bool) {
	if !cache.indexEnabled || len(selector) == 0 {
		return nil, "", false
	}

	// Phase 1 — prefix buckets (config order): first prefix hit wins.
	// The prefix/key match check only depends on immutable config and the
	// caller-supplied selector, so it runs lock-free. The lock is acquired
	// only for the bucket read and released explicitly (no defer) so the
	// critical section stays tightly scoped even if future edits insert
	// code between the read and the return.
	for _, p := range cache.indexedPrefixes {
		for k := range selector {
			if strings.HasPrefix(k, p) {
				cache.lock.RLock()
				bn := cache.nodesByPrefix[p]
				out := make([]string, 0, len(bn))
				for n := range bn {
					out = append(out, n)
				}
				cache.lock.RUnlock()
				return out, p, true
			}
		}
	}

	// Phase 2 — exact-key buckets (config order): first key hit wins.
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	for _, k := range cache.indexedKeyList {
		v, ok := selector[k]
		if !ok {
			continue
		}
		bn := cache.nodesByExactKV[k][v]
		out := make([]string, 0, len(bn))
		for n := range bn {
			out = append(out, n)
		}
		return out, k, true
	}

	return nil, "", false
}

// DumpReservationSelectorIndex returns a snapshot of the inverted index for
// debugging/observability. The result is a defensive copy and safe to expose
// over a debug HTTP endpoint.
//
// When detail=false the snapshot only carries aggregated counts (per-prefix
// node count and reservation count), which is bounded by len(prefixes) and
// safe to serve as a default response. When detail=true the per-node UID
// lists are also included; in large clusters this payload can be substantial
// (proportional to the number of indexed reservations) and should be guarded
// behind an explicit query parameter to avoid accidental DoS via the debug
// endpoint.
func (cache *reservationCache) DumpReservationSelectorIndex(detail bool) *ReservationSelectorIndexSnapshot {
	snap := &ReservationSelectorIndexSnapshot{
		Enabled:  cache.indexEnabled,
		Prefixes: append([]string(nil), cache.indexedPrefixes...),
	}
	if len(cache.indexedKeyList) > 0 {
		snap.Keys = append([]string(nil), cache.indexedKeyList...)
	}
	if !cache.indexEnabled {
		return snap
	}
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	snap.IndexedReservations = len(cache.indexEntryByUID)
	snap.ByPrefix = make(map[string]*ReservationSelectorIndexBucketStat, len(cache.nodesByPrefix))
	for p, byNode := range cache.nodesByPrefix {
		snap.ByPrefix[p] = dumpBucketStat(byNode, detail)
	}
	if len(cache.nodesByExactKV) > 0 {
		// ByKey: aggregated view at the indexed-key level, summing all values.
		// Useful for a quick "is this key actually used by any reservation?"
		// check without paying the per-(key,value) tuple cost.
		snap.ByKey = make(map[string]*ReservationSelectorIndexBucketStat, len(cache.nodesByExactKV))
		// ByKeyValue: full (key, value) view, mirroring how the read-path
		// actually buckets reservations. detail=true also exposes per-node
		// UID lists for online troubleshooting.
		snap.ByKeyValue = make(map[string]map[string]*ReservationSelectorIndexBucketStat, len(cache.nodesByExactKV))
		for k, byValue := range cache.nodesByExactKV {
			perValue := make(map[string]*ReservationSelectorIndexBucketStat, len(byValue))
			aggregated := map[string]sets.Set[types.UID]{}
			for v, byNode := range byValue {
				perValue[v] = dumpBucketStat(byNode, detail)
				for n, uids := range byNode {
					agg := aggregated[n]
					if agg == nil {
						agg = sets.New[types.UID]()
						aggregated[n] = agg
					}
					agg.Insert(uids.UnsortedList()...)
				}
			}
			snap.ByKeyValue[k] = perValue
			snap.ByKey[k] = dumpBucketStat(aggregated, detail)
		}
	}
	return snap
}

// dumpBucketStat snapshots a single bucket's per-node UID set into the public
// JSON-friendly shape. detail=false omits the per-node UID lists.
func dumpBucketStat(byNode map[string]sets.Set[types.UID], detail bool) *ReservationSelectorIndexBucketStat {
	ps := &ReservationSelectorIndexBucketStat{
		Nodes:        len(byNode),
		Reservations: 0,
	}
	if detail {
		ps.ByNode = make(map[string][]types.UID, len(byNode))
	}
	for n, uids := range byNode {
		ps.Reservations += uids.Len()
		if detail {
			ps.ByNode[n] = uids.UnsortedList()
		}
	}
	return ps
}

// ReservationSelectorIndexSnapshot is the JSON-friendly debug view of the
// reservationSelector inverted index.
type ReservationSelectorIndexSnapshot struct {
	Enabled             bool                                           `json:"enabled"`
	Prefixes            []string                                       `json:"prefixes"`
	Keys                []string                                       `json:"keys,omitempty"`
	IndexedReservations int                                            `json:"indexedReservations"`
	ByPrefix            map[string]*ReservationSelectorIndexBucketStat `json:"byPrefix,omitempty"`
	// ByKey is the indexed-key aggregated view (across every value seen for
	// that key). Suitable for a high-level "how many reservations are indexed
	// under this key?" query.
	ByKey map[string]*ReservationSelectorIndexBucketStat `json:"byKey,omitempty"`
	// ByKeyValue is the full (key, value) granularity view that mirrors the
	// read-path bucketing. Each top-level entry maps an indexed key to a
	// per-value bucket-stat map.
	ByKeyValue map[string]map[string]*ReservationSelectorIndexBucketStat `json:"byKeyValue,omitempty"`
}

// ReservationSelectorIndexBucketStat is the per-bucket stat in the snapshot.
// A bucket is either a configured prefix (snap.ByPrefix) or a configured
// exact key (snap.ByKey).
type ReservationSelectorIndexBucketStat struct {
	Nodes        int                    `json:"nodes"`
	Reservations int                    `json:"reservations"`
	ByNode       map[string][]types.UID `json:"byNode,omitempty"`
}

// ReservationSelectorIndexPrefixStat is kept as a backwards-compatible alias
// for ReservationSelectorIndexBucketStat so external callers that already
// reference the old name keep compiling. Prefer the new name in new code.
type ReservationSelectorIndexPrefixStat = ReservationSelectorIndexBucketStat

func (cache *reservationCache) updateReservationsOnNode(nodeName string, uid types.UID) {
	if nodeName == "" {
		return
	}

	reservations := cache.reservationsOnNode[nodeName]
	if reservations == nil {
		reservations = map[types.UID]struct{}{}
		cache.reservationsOnNode[nodeName] = reservations
	}
	reservations[uid] = struct{}{}
}

func (cache *reservationCache) deleteReservationOnNode(nodeName string, uid types.UID) {
	if nodeName == "" {
		return
	}
	reservations := cache.reservationsOnNode[nodeName]
	delete(reservations, uid)
	if len(reservations) == 0 {
		delete(cache.reservationsOnNode, nodeName)
	}
}

func (cache *reservationCache) assumeReservation(r *schedulingv1alpha1.Reservation) {
	cache.updateReservation(r)
}

func (cache *reservationCache) forgetReservation(r *schedulingv1alpha1.Reservation) {
	cache.DeleteReservation(r)
}

func (cache *reservationCache) updateReservation(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo == nil {
		rInfo = frameworkext.NewReservationInfo(newR)
		cache.reservationInfos[newR.UID] = rInfo
	} else {
		rInfo.UpdateReservation(newR)
	}
	// refresh the white-list label index. Labels may have changed across updates,
	// so always remove the previous indexing before re-adding. The outer guard
	// keeps the disabled-index path free of two function calls on every reservation
	// event (the function bodies also short-circuit, but inlining is blocked by
	// the map operations inside, so callers pay the call cost otherwise).
	if cache.indexEnabled {
		cache.removeFromIndex(newR.UID)
		cache.addToIndex(rInfo)
	}
	uid := newR.UID
	if nodeName := newR.Status.NodeName; nodeName != "" {
		cache.updateReservationsOnNode(nodeName, uid)
		// refresh matchable and allocated
		if rInfo.IsMatchable() { // matchable
			if cache.matchableOnNode[nodeName] == nil {
				cache.matchableOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.matchableOnNode[nodeName][uid] = struct{}{}
			if rInfo.GetAllocatedPods() > 0 { // allocated
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][uid] = struct{}{}
			} else if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
			}
		} else { // not matchable, neither allocated
			if cache.matchableOnNode[nodeName] != nil {
				delete(cache.matchableOnNode[nodeName], uid)
				if len(cache.matchableOnNode[nodeName]) == 0 {
					delete(cache.matchableOnNode, nodeName)
				}
			}
			if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) updateReservationIfExists(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo == nil {
		return
	}
	rInfo.UpdateReservation(newR)
	// labels may change across updates; refresh the inverted index in place.
	// Guarded so the disabled-index path stays call-free on the hot event path.
	if cache.indexEnabled {
		cache.removeFromIndex(newR.UID)
		cache.addToIndex(rInfo)
	}
	uid := newR.UID
	if nodeName := newR.Status.NodeName; nodeName != "" {
		// refresh matchable and allocated
		if rInfo.IsMatchable() { // matchable
			if cache.matchableOnNode[nodeName] == nil {
				cache.matchableOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.matchableOnNode[nodeName][uid] = struct{}{}
			if rInfo.GetAllocatedPods() > 0 { // allocated
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][uid] = struct{}{}
			} else if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
			}
		} else { // not matchable, neither allocated
			if cache.matchableOnNode[nodeName] != nil {
				delete(cache.matchableOnNode[nodeName], uid)
				if len(cache.matchableOnNode[nodeName]) == 0 {
					delete(cache.matchableOnNode, nodeName)
				}
			}
			if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	uid := r.UID
	rInfo := cache.reservationInfos[uid]
	delete(cache.reservationInfos, uid)
	if cache.indexEnabled {
		cache.removeFromIndex(uid)
	}
	nodeName := r.Status.NodeName
	cache.deleteReservationOnNode(nodeName, uid)
	// refresh matchable and allocated
	if cache.matchableOnNode[nodeName] != nil {
		delete(cache.matchableOnNode[nodeName], uid)
		if len(cache.matchableOnNode[nodeName]) == 0 {
			delete(cache.matchableOnNode, nodeName)
		}
	}
	if cache.allocatedOnNode[nodeName] != nil {
		delete(cache.allocatedOnNode[nodeName], uid)
		if len(cache.allocatedOnNode[nodeName]) == 0 {
			delete(cache.allocatedOnNode, nodeName)
		}
	}
	return rInfo
}

func (cache *reservationCache) updateReservationOperatingPod(newPod *corev1.Pod, currentOwner *corev1.ObjectReference) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newPod.UID]
	if rInfo == nil {
		rInfo = frameworkext.NewReservationInfoFromPod(newPod)
		cache.reservationInfos[newPod.UID] = rInfo
	} else {
		rInfo.UpdatePod(newPod)
	}
	if currentOwner != nil {
		rInfo.AddAssignedPod(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      currentOwner.Name,
				Namespace: currentOwner.Namespace,
				UID:       currentOwner.UID,
			},
		})
	}
	// refresh white-list index for the operating pod's labels.
	// Guarded so the disabled-index path stays call-free on the hot pod-event path.
	if cache.indexEnabled {
		cache.removeFromIndex(newPod.UID)
		cache.addToIndex(rInfo)
	}
	uid := newPod.UID
	if nodeName := newPod.Spec.NodeName; nodeName != "" {
		cache.updateReservationsOnNode(nodeName, uid)
		// refresh matchable and allocated
		if rInfo.IsMatchable() { // matchable
			if cache.matchableOnNode[nodeName] == nil {
				cache.matchableOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.matchableOnNode[nodeName][uid] = struct{}{}
			if rInfo.GetAllocatedPods() > 0 { // allocated
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][uid] = struct{}{}
			} else if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
			}
		} else { // not matchable, neither allocated
			if cache.matchableOnNode[nodeName] != nil {
				delete(cache.matchableOnNode[nodeName], uid)
				if len(cache.matchableOnNode[nodeName]) == 0 {
					delete(cache.matchableOnNode, nodeName)
				}
			}
			if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) deleteReservationOperatingPod(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	uid := pod.UID
	delete(cache.reservationInfos, uid)
	if cache.indexEnabled {
		cache.removeFromIndex(uid)
	}
	nodeName := pod.Spec.NodeName
	cache.deleteReservationOnNode(nodeName, uid)
	// refresh matchable and allocated
	if cache.matchableOnNode[nodeName] != nil {
		delete(cache.matchableOnNode[nodeName], uid)
		if len(cache.matchableOnNode[nodeName]) == 0 {
			delete(cache.matchableOnNode, nodeName)
		}
	}
	if cache.allocatedOnNode[nodeName] != nil {
		delete(cache.allocatedOnNode[nodeName], uid)
		if len(cache.allocatedOnNode[nodeName]) == 0 {
			delete(cache.allocatedOnNode, nodeName)
		}
	}
}

func (cache *reservationCache) assumePod(reservationUID types.UID, pod *corev1.Pod) error {
	return cache.assumePods(reservationUID, []*corev1.Pod{pod})
}

func (cache *reservationCache) assumePods(reservationUID types.UID, pods []*corev1.Pod) error {
	return cache.addPods(reservationUID, pods)
}

func (cache *reservationCache) forgetPods(reservationUID types.UID, pods []*corev1.Pod) {
	cache.deletePods(reservationUID, pods)
}

func (cache *reservationCache) addPod(reservationUID types.UID, pod *corev1.Pod) error {
	return cache.addPods(reservationUID, []*corev1.Pod{pod})
}

func (cache *reservationCache) addPods(reservationUID types.UID, pods []*corev1.Pod) error {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo == nil {
		return fmt.Errorf("cannot find target reservation")
	}
	if rInfo.IsTerminating() {
		return fmt.Errorf("target reservation is terminating")
	}
	for _, pod := range pods {
		rInfo.AddAssignedPod(pod)
	}
	// update allocated cache
	if rInfo.IsMatchable() && rInfo.GetAllocatedPods() > 0 {
		nodeName := rInfo.GetNodeName()
		if nodeName != "" {
			if cache.allocatedOnNode[nodeName] == nil {
				cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.allocatedOnNode[nodeName][reservationUID] = struct{}{}
		}
	}
	return nil
}

func (cache *reservationCache) updatePod(oldReservationUID, newReservationUID types.UID, oldPod, newPod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	oldRInfo := cache.reservationInfos[oldReservationUID]
	if oldRInfo != nil && oldPod != nil {
		oldRInfo.RemoveAssignedPod(oldPod)
		// update allocated cache for old reservation
		if oldRInfo.GetAllocatedPods() == 0 {
			nodeName := oldRInfo.GetNodeName()
			if nodeName != "" && cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], oldReservationUID)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
	newRInfo := cache.reservationInfos[newReservationUID]
	if newRInfo != nil && newPod != nil {
		newRInfo.AddAssignedPod(newPod)
		// update allocated cache for new reservation
		if newRInfo.IsMatchable() && newRInfo.GetAllocatedPods() > 0 {
			nodeName := newRInfo.GetNodeName()
			if nodeName != "" {
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][newReservationUID] = struct{}{}
			}
		}
	}
}

func (cache *reservationCache) deletePod(reservationUID types.UID, pod *corev1.Pod) {
	cache.deletePods(reservationUID, []*corev1.Pod{pod})
}

func (cache *reservationCache) deletePods(reservationUID types.UID, pods []*corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		for _, pod := range pods {
			rInfo.RemoveAssignedPod(pod)
		}
		// update allocated cache
		if rInfo.GetAllocatedPods() == 0 {
			nodeName := rInfo.GetNodeName()
			if nodeName != "" && cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], reservationUID)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) getReservationInfo(name string) *frameworkext.ReservationInfo {
	reservation, err := cache.reservationLister.Get(name)
	if err != nil {
		return nil
	}
	return cache.getReservationInfoByUID(reservation.UID)
}

func (cache *reservationCache) getReservationInfoByUID(uid types.UID) *frameworkext.ReservationInfo {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rInfo := cache.reservationInfos[uid]
	if rInfo != nil {
		return rInfo.Clone()
	}
	return nil
}

func (cache *reservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	var target *frameworkext.ReservationInfo
	// TODO: fast lookup pods assigned to reservations
	cache.ForEachMatchableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *fwktype.Status) {
		if _, ok := rInfo.AssignedPods[pod.UID]; ok {
			target = rInfo
			return false, nil
		}
		return true, nil
	})
	return target
}

func (cache *reservationCache) ListAllNodes(matchable bool) []string {
	// list a subset of nodes which has any available reservations.
	// If matchable = false, we suppose the caller wants only the available reservations with allocated pods.
	// If matchable = true, where the caller can match any available reservations, we return nodes having available reservations.
	// To efficiently implement this, we may need to maintain two maps: one for allocated reservations and one for available reservations.
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	if len(cache.matchableOnNode) == 0 {
		return nil
	}
	if matchable {
		nodes := make([]string, 0, len(cache.matchableOnNode))
		for k := range cache.matchableOnNode {
			nodes = append(nodes, k)
		}
		return nodes
	}
	nodes := make([]string, 0, len(cache.allocatedOnNode))
	for k := range cache.allocatedOnNode {
		nodes = append(nodes, k)
	}
	return nodes
}

func (cache *reservationCache) ForEachMatchableReservationOnNode(nodeName string, fn func(rInfo *frameworkext.ReservationInfo) (bool, *fwktype.Status)) *fwktype.Status {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rOnNode := cache.matchableOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	for uid := range rOnNode {
		rInfo := cache.reservationInfos[uid]
		beContinue, status := fn(rInfo)
		if !status.IsSuccess() {
			return status
		}
		if !beContinue {
			return nil
		}
	}
	return nil
}

func (cache *reservationCache) ListAvailableReservationInfosOnNode(nodeName string, listAll bool) []*frameworkext.ReservationInfo {
	var result []*frameworkext.ReservationInfo
	if !listAll {
		cache.ForEachMatchableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *fwktype.Status) {
			result = append(result, rInfo.Clone())
			return true, nil
		})
		return result
	}

	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rOnNode := cache.reservationsOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	for uid := range rOnNode {
		rInfo := cache.reservationInfos[uid]
		if rInfo != nil {
			result = append(result, rInfo.Clone())
		}
	}
	return result
}

// getAllPreAllocatableCandidates retrieves all cached pre-allocatable candidates for all nodes
// Returns a map of nodeName -> sorted list of pods (by priority descending)
func (cache *reservationCache) getAllPreAllocatableCandidates() map[string][]*corev1.Pod {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	result := make(map[string][]*corev1.Pod, len(cache.preAllocatablePodsOnNode))
	for nodeName, podCache := range cache.preAllocatablePodsOnNode {
		if podCache == nil || podCache.tree.Len() == 0 {
			continue
		}
		// Collect pods in sorted order for this node
		pods := make([]*corev1.Pod, 0, podCache.tree.Len())
		podCache.tree.Ascend(func(item btree.Item) bool {
			pods = append(pods, item.(*preAllocatablePodItem).pod)
			return true
		})
		result[nodeName] = pods
	}
	return result
}

// deletePreAllocatableCandidateOnNode removes a specific pod from the cached candidates for a node
func (cache *reservationCache) deletePreAllocatableCandidateOnNode(nodeName string, podUID types.UID) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if nodeName == "" {
		return
	}
	podCache := cache.preAllocatablePodsOnNode[nodeName]
	if podCache == nil {
		return
	}
	// Find and delete the item
	if item, exists := podCache.index[podUID]; exists {
		podCache.tree.Delete(item)
		delete(podCache.index, podUID)
	}
	// Clean up empty cache
	if podCache.tree.Len() == 0 {
		delete(cache.preAllocatablePodsOnNode, nodeName)
	}
}

// addPreAllocatableCandidateOnNode adds a pod to the cached pre-allocatable candidates for a node
func (cache *reservationCache) addPreAllocatableCandidateOnNode(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if pod == nil || pod.Spec.NodeName == "" {
		return
	}
	nodeName := pod.Spec.NodeName
	// Get or create cache for this node
	podCache := cache.preAllocatablePodsOnNode[nodeName]
	if podCache == nil {
		podCache = newPreAllocatablePodCache()
		cache.preAllocatablePodsOnNode[nodeName] = podCache
	}
	// Add or update the pod
	priority := cache.getPreAllocatablePriorityFromPod(pod)
	item := &preAllocatablePodItem{
		pod:      pod,
		priority: priority,
	}
	podCache.tree.ReplaceOrInsert(item)
	podCache.index[pod.UID] = item
}

// updatePreAllocatableCandidatePriority updates the priority of a pod in the cache
func (cache *reservationCache) updatePreAllocatableCandidatePriority(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if pod == nil || pod.Spec.NodeName == "" {
		return
	}
	nodeName := pod.Spec.NodeName
	podCache := cache.preAllocatablePodsOnNode[nodeName]
	if podCache == nil {
		return
	}
	// Delete old item
	if oldItem, exists := podCache.index[pod.UID]; exists {
		podCache.tree.Delete(oldItem)
	}
	// Insert new item with updated priority
	priority := cache.getPreAllocatablePriorityFromPod(pod)
	newItem := &preAllocatablePodItem{
		pod:      pod,
		priority: priority,
	}
	podCache.tree.ReplaceOrInsert(newItem)
	podCache.index[pod.UID] = newItem
}

// getPreAllocatablePriorityFromPod retrieves the pre-allocatable priority from pod annotation
func (cache *reservationCache) getPreAllocatablePriorityFromPod(pod *corev1.Pod) int64 {
	if pod == nil || pod.Annotations == nil {
		return 0
	}
	priorityStr, ok := pod.Annotations[cache.preAllocatablePriorityAnnotationKey]
	if !ok {
		return 0
	}
	priority, err := strconv.ParseInt(priorityStr, 10, 64)
	if err != nil {
		return 0
	}
	return priority
}

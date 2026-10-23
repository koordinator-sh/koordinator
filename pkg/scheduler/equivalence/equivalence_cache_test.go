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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/metrics/testutil"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// flush drops every cached class and pending miss reason. Tests use it to reset the cache and the
// shared entry gauge between cases.
func (c *equivalenceClassCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entryCount := len(c.entries)
	c.entries = make(map[string]*equivalenceClassEntry, c.capacity)
	c.lru.Init()
	koordmetrics.RecordEquivalenceClassCacheEntries(-entryCount)
}

// next takes and consumes the class's next candidate using spread placement.
func (c *equivalenceClassCache) next(key string, cycle int64) (string, bool, equivalenceCacheMissReason) {
	node, reason := c.candidate(key, cycle, nil)
	if node == "" {
		return "", false, reason
	}
	c.consume(key, node, cycle)
	return node, true, ""
}

func quotaNodes(quota int64, names ...string) []equivalenceClassNode {
	nodes := make([]equivalenceClassNode, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, equivalenceClassNode{name: name, quota: quota})
	}
	return nodes
}

func TestEquivalenceClassCacheResourceNames(t *testing.T) {
	c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
	t.Cleanup(c.flush)
	pod := makeQuotaPod("0", "0")
	pod.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
	}
	pod.Spec.InitContainers = []corev1.Container{{
		Name: "init",
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			apiext.MidCPU:                   resource.MustParse("100"),
			apiext.BatchCPU:                 resource.MustParse("0"),
			corev1.ResourceEphemeralStorage: resource.MustParse("1Gi"),
		}},
	}}
	pod.Spec.Overhead = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")}
	requests := podRequestsForQuota(pod)
	c.store("class", quotaNodes(10, "node-a"), 1, requests, PlacementSpread)
	assert.Equal(t, sets.New(corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourceEphemeralStorage,
		corev1.ResourcePods, apiext.MidCPU), c.entries["class"].resourceNames)

	requests[apiext.BatchCPU] = resource.MustParse("100")
	assert.False(t, c.entries["class"].resourceNames.Has(apiext.BatchCPU))
	c.store("class", quotaNodes(10, "node-a"), 2, corev1.ResourceList{apiext.BatchCPU: resource.MustParse("100")}, PlacementSpread)
	assert.Equal(t, sets.New(corev1.ResourcePods, apiext.BatchCPU), c.entries["class"].resourceNames)
}

func TestEquivalenceClassCache(t *testing.T) {
	newTestCache := func() (*equivalenceClassCache, *time.Time) {
		now := time.Now()
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		c.now = func() time.Time { return now }
		return c, &now
	}

	t.Run("miss on unknown class", func(t *testing.T) {
		c, _ := newTestCache()
		_, ok, _ := c.next("unknown", 1)
		assert.False(t, ok)
	})

	t.Run("drains by quota, skipping exhausted nodes", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", []equivalenceClassNode{{name: "node-a", quota: 1}, {name: "node-b", quota: 2}}, 1, nil, PlacementSpread)
		for i, want := range []string{"node-a", "node-b", "node-b"} {
			node, ok, _ := c.next("h1", int64(i+2))
			assert.True(t, ok)
			assert.Equal(t, want, node)
		}
		_, ok, _ := c.next("h1", 5)
		assert.False(t, ok, "fully quota-exhausted class should miss")
	})

	t.Run("cursor wraps around round-robin", func(t *testing.T) {
		c, _ := newTestCache()
		// quota 2 each: two full rounds before hitting the drift threshold (2*2=4).
		c.store("h1", quotaNodes(2, "node-a", "node-b"), 1, nil, PlacementSpread)
		for i, want := range []string{"node-a", "node-b", "node-a", "node-b"} {
			node, ok, _ := c.next("h1", int64(i+2))
			assert.True(t, ok)
			assert.Equal(t, want, node)
		}
	})

	t.Run("drift threshold drops the entry", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(100, "node-a", "node-b"), 1, nil, PlacementSpread)
		for i := 0; i < 4; i++ { // consumed reaches 2*len(nodes)=4
			_, ok, _ := c.next("h1", int64(i+2))
			assert.True(t, ok)
		}
		_, ok, _ := c.next("h1", 6)
		assert.False(t, ok, "entry should be dropped once consumption drift hits the threshold")
	})

	t.Run("consume accounts the full-path pod", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", []equivalenceClassNode{{name: "node-a", quota: 2}, {name: "node-b", quota: 1}}, 1, nil, PlacementSpread)
		c.consume("h1", "node-a", 1)
		// The walk continues after the node the full-path pod took; node-a has 1 quota left.
		node, ok, _ := c.next("h1", 2)
		assert.True(t, ok)
		assert.Equal(t, "node-b", node)
		node, ok, _ = c.next("h1", 3)
		assert.True(t, ok)
		assert.Equal(t, "node-a", node)
		_, ok, _ = c.next("h1", 4)
		assert.False(t, ok, "both nodes spent (one via the full path), class should miss")
	})

	t.Run("consume of a replaced entry is ignored", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		c.consume("h1", "node-a", 2)
		assert.Equal(t, int64(1), c.entries["h1"].nodes[0].quota)
		assert.Zero(t, c.entries["h1"].consumed)
	})

	t.Run("expired entry misses and is dropped", func(t *testing.T) {
		c, now := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		*now = now.Add(2 * time.Second)
		_, ok, _ := c.next("h1", 2)
		assert.False(t, ok)
		// Rewind: the entry must have been deleted, not resurrected.
		*now = now.Add(-2 * time.Second)
		_, ok, _ = c.next("h1", 2)
		assert.False(t, ok)
	})

	t.Run("retains multiple classes", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		c.store("h2", quotaNodes(1, "node-b"), 2, nil, PlacementSpread)
		assert.Len(t, c.entries, 2)

		node, ok, _ := c.next("h1", 3)
		assert.True(t, ok)
		assert.Equal(t, "node-a", node)
		node, ok, _ = c.next("h2", 3)
		assert.True(t, ok)
		assert.Equal(t, "node-b", node)
	})

	t.Run("interleaved classes do not invalidate one another", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(2, "node-a", "node-b"), 1, nil, PlacementSpread)
		c.store("h2", quotaNodes(2, "node-c", "node-d"), 2, nil, PlacementSpread)

		node, ok, _ := c.next("h1", 10)
		assert.True(t, ok)
		assert.Equal(t, "node-a", node)
		node, ok, _ = c.next("h2", 11)
		assert.True(t, ok)
		assert.Equal(t, "node-c", node)
		node, ok, _ = c.next("h1", 12)
		assert.True(t, ok)
		assert.Equal(t, "node-b", node)
		node, ok, _ = c.next("h2", 13)
		assert.True(t, ok)
		assert.Equal(t, "node-d", node)
	})

	t.Run("custom capacity evicts least recently used class", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, 2)
		c.now = func() time.Time { return time.Now() }
		c.store("h1", quotaNodes(10, "node-a"), 1, nil, PlacementSpread)
		c.store("h2", quotaNodes(10, "node-b"), 2, nil, PlacementSpread)
		c.store("h3", quotaNodes(10, "node-c"), 3, nil, PlacementSpread)

		assert.Len(t, c.entries, 2)
		assert.NotContains(t, c.entries, "h1")
		assert.Contains(t, c.entries, "h2")
		assert.Contains(t, c.entries, "h3")

		_, ok, _ := c.next("h2", 4)
		assert.True(t, ok, "accessing h2 should refresh its LRU position")
		c.store("h4", quotaNodes(10, "node-d"), 5, nil, PlacementSpread)

		assert.Len(t, c.entries, 2)
		assert.Contains(t, c.entries, "h2")
		assert.Contains(t, c.entries, "h4")
		assert.NotContains(t, c.entries, "h3")
	})

	t.Run("default capacity bounds high cardinality", func(t *testing.T) {
		c, _ := newTestCache()
		nodes := quotaNodes(1, "node-a")
		for i := 0; i < 150000; i++ {
			c.store(strconv.Itoa(i), nodes, int64(i), nil, PlacementSpread)
		}
		assert.Len(t, c.entries, DefaultEquivalenceClassCacheSize)
		assert.Contains(t, c.entries, "149999")
		assert.NotContains(t, c.entries, "0")
	})

	t.Run("non-consecutive cycle still hits the matching class", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(2, "node-a"), 10, nil, PlacementSpread)
		_, ok, _ := c.next("h1", 12)
		assert.True(t, ok)
		_, ok, _ = c.next("h1", 14)
		assert.True(t, ok)
	})

	t.Run("unknown class does not evict cached classes", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 10, nil, PlacementSpread)
		c.store("h2", quotaNodes(1, "node-b"), 11, nil, PlacementSpread)
		_, ok, reason := c.next("h3", 12)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissUnknownClass, reason)
		assert.Len(t, c.entries, 2)
		assert.Contains(t, c.entries, "h1")
		assert.Contains(t, c.entries, "h2")
	})

	t.Run("multiple candidates may be consumed in one cycle", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a", "node-b"), 10, nil, PlacementSpread)
		node, ok, _ := c.next("h1", 11)
		assert.True(t, ok)
		assert.Equal(t, "node-a", node)
		node, ok, _ = c.next("h1", 11)
		assert.True(t, ok)
		assert.Equal(t, "node-b", node)
	})

	t.Run("remove drops only the given class", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		c.store("h2", quotaNodes(1, "node-b"), 2, nil, PlacementSpread)
		assert.True(t, c.remove("h1"))
		assert.False(t, c.remove("h1"), "nothing left to remove")

		_, ok, reason := c.next("h1", 3)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissUnknownClass, reason)
		node, ok, _ := c.next("h2", 3)
		assert.True(t, ok)
		assert.Equal(t, "node-b", node)
	})

	t.Run("remove of an uncached class is a no-op", func(t *testing.T) {
		c, _ := newTestCache()
		assert.False(t, c.remove("h1"))
		assert.Zero(t, c.len())
	})

	t.Run("remove node keeps other candidates reusable", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a", "node-b"), 1, nil, PlacementSpread)
		c.removeNode("node-a", nil)

		node, ok, reason := c.next("h1", 2)
		assert.True(t, ok)
		assert.Empty(t, reason)
		assert.Equal(t, "node-b", node)
	})

	t.Run("remove last node drops the class", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		c.removeNode("node-a", nil)

		_, ok, reason := c.next("h1", 2)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
		assert.Empty(t, c.entries)
	})

	t.Run("node event flush drops every class", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		c.store("h2", quotaNodes(1, "node-b"), 2, nil, PlacementSpread)
		c.flushNodeEvent()

		assert.Zero(t, c.len())
		for _, key := range []string{"h1", "h2"} {
			_, ok, reason := c.next(key, 3)
			assert.False(t, ok, key)
			assert.Equal(t, equivalenceCacheMissEmpty, reason, key)
		}
	})

	t.Run("rejected candidates do not count toward placement drift", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(100, "rejected", "accepted"), 1, nil, PlacementSpread)
		node, _ := c.candidate("h1", 2, nil)
		require.Equal(t, "rejected", node)
		c.rejectNode("h1", "rejected", 2)
		assert.Zero(t, c.entries["h1"].consumed)
		var ok bool
		for i := 0; i < DefaultDriftFactor*2; i++ {
			node, ok, _ = c.next("h1", int64(i+3))
			require.True(t, ok)
			assert.Equal(t, "accepted", node)
		}
		_, ok, reason := c.next("h1", 10)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissDrift, reason)
	})

	t.Run("empty backfill removes the old class", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(10, "node"), 1, nil, PlacementSpread)
		c.store("h1", nil, 2, nil, PlacementSpread)
		assert.Empty(t, c.entries)
		assert.Zero(t, c.lru.Len())
	})

	t.Run("node invalidation between lookup and rejection leaves other nodes intact", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(10, "node-a", "node-b"), 1, nil, PlacementSpread)
		node, _ := c.candidate("h1", 2, nil)
		require.Equal(t, "node-a", node)
		c.removeNode("node-a", nil)
		c.rejectNode("h1", "node-a", 2)
		assert.Zero(t, c.entries["h1"].consumed)
		assert.Equal(t, quotaNodes(10, "node-b"), c.entries["h1"].nodes)
	})

	t.Run("empty key and empty backfill do not create entries", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		c.store("h1", nil, 1, nil, PlacementSpread)
		_, ok, _ := c.next("", 2)
		assert.False(t, ok)
		_, ok, _ = c.next("h1", 2)
		assert.False(t, ok)
	})
}

func TestEquivalenceClassCacheCandidates(t *testing.T) {
	newEntry := func(policy PlacementPolicy) *equivalenceClassCache {
		c := newEquivalenceClassCache(time.Minute, DefaultEquivalenceClassCacheSize)
		t.Cleanup(c.flush)
		c.store("h1", quotaNodes(3, "a", "b", "c", "d", "e"), 1, nil, policy)
		return c
	}

	for _, policy := range []PlacementPolicy{PlacementSpread, PlacementPack} {
		t.Run(string(policy), func(t *testing.T) {
			t.Run("a fresh entry starts at the head without consuming", func(t *testing.T) {
				c := newEntry(policy)
				node, reason := c.candidate("h1", 2, nil)
				assert.Empty(t, reason)
				assert.Equal(t, "a", node)
				assert.Equal(t, quotaNodes(3, "a", "b", "c", "d", "e"), c.entries["h1"].nodes)
				assert.Zero(t, c.entries["h1"].consumed)
				assert.Equal(t, -1, c.entries["h1"].last)
			})

			t.Run("placement order", func(t *testing.T) {
				c := newEntry(policy)
				want := []string{"a", "b", "c", "d", "e", "a"}
				if policy == PlacementPack {
					want = []string{"a", "a", "a", "b", "b", "b"}
				}
				for i, name := range want {
					cycle := int64(i + 2)
					node, reason := c.candidate("h1", cycle, nil)
					require.Empty(t, reason)
					assert.Equal(t, name, node)
					c.consume("h1", node, cycle)
				}
			})

			t.Run("spent and excluded successors fall back to anchor", func(t *testing.T) {
				c := newEntry(policy)
				c.consume("h1", "c", 1)
				for _, node := range []string{"a", "b", "d"} {
					c.rejectNode("h1", node, 1)
				}
				node, reason := c.candidate("h1", 2, sets.New("e"))
				assert.Empty(t, reason)
				assert.Equal(t, "c", node)
			})

			t.Run("excluded anchor takes the next usable successor", func(t *testing.T) {
				c := newEntry(policy)
				c.consume("h1", "c", 1)
				c.rejectNode("h1", "d", 1)
				node, reason := c.candidate("h1", 2, sets.New("c", "e"))
				assert.Empty(t, reason)
				assert.Equal(t, "a", node)
			})

			t.Run("only excluded nodes left keeps the entry", func(t *testing.T) {
				c := newEntry(policy)
				c.rejectNode("h1", "a", 1)
				node, reason := c.candidate("h1", 2, sets.New("b", "c", "d", "e"))
				assert.Empty(t, node)
				assert.Equal(t, equivalenceCacheMissQuotaExhausted, reason)
				assert.Contains(t, c.entries, "h1", "the remaining quota still serves other pods of the class")
			})

			t.Run("no quota left drops the entry", func(t *testing.T) {
				c := newEntry(policy)
				for _, node := range []string{"a", "b", "c", "d", "e"} {
					c.rejectNode("h1", node, 1)
				}
				node, reason := c.candidate("h1", 2, nil)
				assert.Empty(t, node)
				assert.Equal(t, equivalenceCacheMissQuotaExhausted, reason)
				assert.NotContains(t, c.entries, "h1")
			})

			t.Run("removing nodes keeps the position", func(t *testing.T) {
				c := newEntry(policy)
				c.consume("h1", "c", 1)
				c.removeNode("b", nil)
				node, _ := c.candidate("h1", 2, nil)
				want := "d"
				if policy == PlacementPack {
					want = "c"
				}
				assert.Equal(t, want, node, "removing an earlier node keeps the anchor")
				c.removeNode("e", nil)
				node, _ = c.candidate("h1", 3, nil)
				assert.Equal(t, want, node, "removing a later node keeps the position")
				c.removeNode("c", nil)
				assert.Equal(t, 0, c.entries["h1"].last, "removing the anchor moves last to its predecessor")
				node, _ = c.candidate("h1", 4, nil)
				want = "d"
				if policy == PlacementPack {
					want = "a"
				}
				assert.Equal(t, want, node, "placement continues relative to the predecessor")
				c.removeNode("a", nil)
				node, _ = c.candidate("h1", 5, nil)
				assert.Equal(t, "d", node, "removing the head restarts from the new head")
			})
		})
	}
}

func TestEquivalenceClassCachePackInvalidation(t *testing.T) {
	for _, reason := range []equivalenceCacheMissReason{equivalenceCacheMissDrift, equivalenceCacheMissExpired} {
		t.Run(string(reason), func(t *testing.T) {
			c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
			t.Cleanup(c.flush)
			now := time.Now()
			c.now = func() time.Time { return now }
			c.store("h1", quotaNodes(100, "a", "b"), 1, nil, PlacementPack)
			for i := 0; i < DefaultDriftFactor*2; i++ {
				node, miss := c.candidate("h1", 2, nil)
				require.Empty(t, miss)
				require.Equal(t, "a", node)
				c.consume("h1", node, 2)
				if reason == equivalenceCacheMissExpired {
					now = now.Add(2 * time.Second)
					break
				}
			}
			assert.Positive(t, c.entries["h1"].nodes[0].quota, "the anchor still has capacity")
			node, miss := c.candidate("h1", 3, nil)
			assert.Empty(t, node)
			assert.Equal(t, reason, miss)
			assert.NotContains(t, c.entries, "h1")
		})
	}
}

func TestEquivalenceClassCacheConcurrentAccess(t *testing.T) {
	c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
	const workers = 8
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				cycle := int64(worker*iterations + i)
				switch i % 4 {
				case 0:
					c.store("hash", []equivalenceClassNode{{name: "node-a", quota: iterations}}, cycle, nil, PlacementSpread)
				case 1:
					c.candidate("hash", cycle, sets.New("node-b"))
				case 2:
					c.consume("hash", "node-a", cycle)
				default:
					c.remove("hash")
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestEquivalenceClassCacheWorkingSetBoundary(t *testing.T) {
	for _, classes := range []int{15, 16, 17} {
		t.Run(strconv.Itoa(classes), func(t *testing.T) {
			c := newEquivalenceClassCache(time.Second, 16)
			now := time.Now()
			c.now = func() time.Time { return now }
			for i := 0; i < classes; i++ {
				c.store(strconv.Itoa(i), quotaNodes(100, "node"), 1, nil, PlacementSpread)
			}
			hits := 0
			// A cyclic working set isolates LRU eviction from TTL, drift and binding latency.
			for i := 0; i < classes; i++ {
				key := strconv.Itoa(i)
				if _, ok, _ := c.next(key, 2); ok {
					hits++
				} else {
					c.store(key, quotaNodes(100, "node"), 2, nil, PlacementSpread)
				}
			}
			want := classes
			if classes > c.capacity {
				want = 0
			}
			assert.Equal(t, want, hits)
		})
	}
}

func TestEquivalenceClassCacheEntryGauge(t *testing.T) {
	koordmetrics.Register()
	before, err := testutil.GetGaugeMetricValue(koordmetrics.EquivalenceClassCacheEntries)
	require.NoError(t, err)

	c := newEquivalenceClassCache(time.Second, 2)
	c.store("h1", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
	afterStore, err := testutil.GetGaugeMetricValue(koordmetrics.EquivalenceClassCacheEntries)
	require.NoError(t, err)
	assert.Equal(t, before+1, afterStore)

	c.store("h2", quotaNodes(1, "node-b"), 2, nil, PlacementSpread)
	afterSecondStore, err := testutil.GetGaugeMetricValue(koordmetrics.EquivalenceClassCacheEntries)
	require.NoError(t, err)
	assert.Equal(t, before+2, afterSecondStore)

	c.next("h1", 3)
	c.next("h2", 3)
	c.next("h1", 4)
	c.next("h2", 4)
	afterExhaustion, err := testutil.GetGaugeMetricValue(koordmetrics.EquivalenceClassCacheEntries)
	require.NoError(t, err)
	assert.Equal(t, before, afterExhaustion)

	c.store("h3", quotaNodes(1, "node-c"), 4, nil, PlacementSpread)
	c.flushNodeEvent()
	afterFlush, err := testutil.GetGaugeMetricValue(koordmetrics.EquivalenceClassCacheEntries)
	require.NoError(t, err)
	assert.Equal(t, before, afterFlush)
}

func TestEquivalenceClassCacheMissReasons(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		_, ok, reason := c.next("hash", 1)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})

	t.Run("unknown class", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		c.store("hash-a", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		_, ok, reason := c.next("hash-b", 2)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissUnknownClass, reason)
	})

	t.Run("expired", func(t *testing.T) {
		now := time.Now()
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		c.now = func() time.Time { return now }
		c.store("hash", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		now = now.Add(2 * time.Second)
		_, ok, reason := c.next("hash", 2)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissExpired, reason)
	})

	t.Run("drift", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		c.store("hash", quotaNodes(100, "node-a", "node-b"), 1, nil, PlacementSpread)
		for i := 0; i < 4; i++ {
			_, ok, _ := c.next("hash", int64(i+2))
			assert.True(t, ok)
		}
		_, ok, reason := c.next("hash", 6)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissDrift, reason)
	})

	t.Run("quota exhausted", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		c.store("hash", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		_, ok, _ := c.next("hash", 2)
		assert.True(t, ok)
		_, ok, reason := c.next("hash", 3)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissQuotaExhausted, reason)
	})

	t.Run("empty store drops the class", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		c.store("hash", quotaNodes(1, "node-a"), 1, nil, PlacementSpread)
		c.store("hash", nil, 2, nil, PlacementSpread)
		_, ok, reason := c.next("hash", 3)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
		assert.Zero(t, c.len())
	})

	t.Run("empty store does not block a later backfill", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, DefaultEquivalenceClassCacheSize)
		c.store("hash", nil, 1, nil, PlacementSpread)
		c.store("hash", quotaNodes(1, "node-a"), 2, nil, PlacementSpread)
		node, ok, reason := c.next("hash", 3)
		assert.True(t, ok)
		assert.Empty(t, reason)
		assert.Equal(t, "node-a", node)
	})
}

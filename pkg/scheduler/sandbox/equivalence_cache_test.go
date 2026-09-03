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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func quotaNodes(quota int64, names ...string) []equivalenceClassNode {
	nodes := make([]equivalenceClassNode, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, equivalenceClassNode{name: name, quota: quota})
	}
	return nodes
}

func TestEquivalenceClassCache(t *testing.T) {
	newTestCache := func() (*equivalenceClassCache, *time.Time) {
		now := time.Now()
		c := newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
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
		c.store("h1", []equivalenceClassNode{{name: "node-a", quota: 1}, {name: "node-b", quota: 2}}, 1)
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
		c.store("h1", quotaNodes(2, "node-a", "node-b"), 1)
		for i, want := range []string{"node-a", "node-b", "node-a", "node-b"} {
			node, ok, _ := c.next("h1", int64(i+2))
			assert.True(t, ok)
			assert.Equal(t, want, node)
		}
	})

	t.Run("drift threshold drops the entry", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(100, "node-a", "node-b"), 1)
		for i := 0; i < 4; i++ { // consumed reaches 2*len(nodes)=4
			_, ok, _ := c.next("h1", int64(i+2))
			assert.True(t, ok)
		}
		_, ok, _ := c.next("h1", 6)
		assert.False(t, ok, "entry should be dropped once consumption drift hits the threshold")
	})

	t.Run("recordConsumption accounts the full-path pod", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", []equivalenceClassNode{{name: "node-a", quota: 2}, {name: "node-b", quota: 1}}, 1)
		c.recordConsumption("h1", "node-a", 1)
		// node-a has 1 quota left; round-robin cursor starts at index 0.
		node, ok, _ := c.next("h1", 2)
		assert.True(t, ok)
		assert.Equal(t, "node-a", node)
		node, ok, _ = c.next("h1", 3)
		assert.True(t, ok)
		assert.Equal(t, "node-b", node)
		_, ok, _ = c.next("h1", 4)
		assert.False(t, ok, "both nodes spent (one via recordConsumption), class should miss")
	})

	t.Run("expired entry misses and is dropped", func(t *testing.T) {
		c, now := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1)
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
		c.store("h1", quotaNodes(1, "node-a"), 1)
		c.store("h2", quotaNodes(1, "node-b"), 2)
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
		c.store("h1", quotaNodes(2, "node-a", "node-b"), 1)
		c.store("h2", quotaNodes(2, "node-c", "node-d"), 2)

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
		c.store("h1", quotaNodes(10, "node-a"), 1)
		c.store("h2", quotaNodes(10, "node-b"), 2)
		c.store("h3", quotaNodes(10, "node-c"), 3)

		assert.Len(t, c.entries, 2)
		assert.NotContains(t, c.entries, "h1")
		assert.Contains(t, c.entries, "h2")
		assert.Contains(t, c.entries, "h3")

		_, ok, _ := c.next("h2", 4)
		assert.True(t, ok, "accessing h2 should refresh its LRU position")
		c.store("h4", quotaNodes(10, "node-d"), 5)

		assert.Len(t, c.entries, 2)
		assert.Contains(t, c.entries, "h2")
		assert.Contains(t, c.entries, "h4")
		assert.NotContains(t, c.entries, "h3")
	})

	t.Run("default capacity bounds high cardinality", func(t *testing.T) {
		c, _ := newTestCache()
		nodes := quotaNodes(1, "node-a")
		for i := 0; i < 150000; i++ {
			c.store(strconv.Itoa(i), nodes, int64(i))
		}
		assert.Len(t, c.entries, defaultEquivalenceClassCacheSize)
		assert.Contains(t, c.entries, "149999")
		assert.NotContains(t, c.entries, "0")
	})

	t.Run("non-consecutive cycle still hits the matching class", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(2, "node-a"), 10)
		_, ok, _ := c.next("h1", 12)
		assert.True(t, ok)
		_, ok, _ = c.next("h1", 14)
		assert.True(t, ok)
	})

	t.Run("unknown class does not evict cached classes", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 10)
		c.store("h2", quotaNodes(1, "node-b"), 11)
		_, ok, reason := c.next("h3", 12)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissUnknownClass, reason)
		assert.Len(t, c.entries, 2)
		assert.Contains(t, c.entries, "h1")
		assert.Contains(t, c.entries, "h2")
	})

	t.Run("multiple candidates may be consumed in one cycle", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a", "node-b"), 10)
		node, ok, _ := c.next("h1", 11)
		assert.True(t, ok)
		assert.Equal(t, "node-a", node)
		node, ok, _ = c.next("h1", 11)
		assert.True(t, ok)
		assert.Equal(t, "node-b", node)
	})

	t.Run("flush drops the active class", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("h1", quotaNodes(1, "node-a"), 1)
		c.store("h2", quotaNodes(1, "node-b"), 2)
		c.flush()
		_, ok, _ := c.next("h1", 2)
		assert.False(t, ok)
		_, ok, _ = c.next("h2", 2)
		assert.False(t, ok)
		assert.Empty(t, c.entries)
	})

	t.Run("store ignores empty key and empty list", func(t *testing.T) {
		c, _ := newTestCache()
		c.store("", quotaNodes(1, "node-a"), 1)
		c.store("h1", nil, 1)
		_, ok, _ := c.next("", 2)
		assert.False(t, ok)
		_, ok, _ = c.next("h1", 2)
		assert.False(t, ok)
	})
}

func TestEquivalenceClassCacheConcurrentAccess(t *testing.T) {
	c := newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
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
					c.store("hash", []equivalenceClassNode{{name: "node-a", quota: iterations}}, cycle)
				case 1:
					c.next("hash", cycle)
				case 2:
					c.recordConsumption("hash", "node-a", cycle)
				default:
					c.flush()
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestEquivalenceClassCacheMissReasons(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
		_, ok, reason := c.next("hash", 1)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissEmpty, reason)
	})

	t.Run("unknown class", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
		c.store("hash-a", quotaNodes(1, "node-a"), 1)
		_, ok, reason := c.next("hash-b", 2)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissUnknownClass, reason)
	})

	t.Run("expired", func(t *testing.T) {
		now := time.Now()
		c := newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
		c.now = func() time.Time { return now }
		c.store("hash", quotaNodes(1, "node-a"), 1)
		now = now.Add(2 * time.Second)
		_, ok, reason := c.next("hash", 2)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissExpired, reason)
	})

	t.Run("drift", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
		c.store("hash", quotaNodes(100, "node-a", "node-b"), 1)
		for i := 0; i < 4; i++ {
			_, ok, _ := c.next("hash", int64(i+2))
			assert.True(t, ok)
		}
		_, ok, reason := c.next("hash", 6)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissDrift, reason)
	})

	t.Run("quota exhausted", func(t *testing.T) {
		c := newEquivalenceClassCache(time.Second, defaultEquivalenceClassCacheSize)
		c.store("hash", quotaNodes(1, "node-a"), 1)
		_, ok, _ := c.next("hash", 2)
		assert.True(t, ok)
		_, ok, reason := c.next("hash", 3)
		assert.False(t, ok)
		assert.Equal(t, equivalenceCacheMissQuotaExhausted, reason)
	})
}

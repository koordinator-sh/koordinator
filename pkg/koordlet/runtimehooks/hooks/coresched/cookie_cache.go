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

package coresched

import (
	"sort"
	"sync"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// CookieCacheEntry is an entry which stores the cookie ID and its belonging PIDs.
type CookieCacheEntry struct {
	rwMutex  sync.RWMutex
	cookieID uint64
	pidCache *PIDCache
}

func newCookieCacheEntry(cookieID uint64, pids ...uint32) *CookieCacheEntry {
	m := &PIDCache{}
	m.AddAny(pids...)
	return &CookieCacheEntry{
		cookieID: cookieID,
		pidCache: m,
	}
}

func (c *CookieCacheEntry) DeepCopy() *CookieCacheEntry {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	copiedM := c.pidCache.DeepCopy()
	return &CookieCacheEntry{
		cookieID: c.cookieID,
		pidCache: copiedM,
	}
}

func (c *CookieCacheEntry) GetCookieID() uint64 {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	return c.cookieID
}

func (c *CookieCacheEntry) SetCookieID(cookieID uint64) {
	c.rwMutex.Lock()
	defer c.rwMutex.Unlock()
	c.cookieID = cookieID
}

func (c *CookieCacheEntry) IsEntryInvalid() bool {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	return c.cookieID <= sysutil.DefaultCoreSchedCookieID || c.pidCache.Len() <= 0
}

func (c *CookieCacheEntry) HasPID(pid uint32) bool {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	return c.pidCache.Has(pid)
}

func (c *CookieCacheEntry) ContainsPIDs(pids ...uint32) []uint32 {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	var notFoundPIDs []uint32
	for _, pid := range pids {
		if !c.pidCache.Has(pid) {
			notFoundPIDs = append(notFoundPIDs, pid)
		}
	}
	return notFoundPIDs
}

// GetAllPIDs gets all PIDs sorted in ascending order.
func (c *CookieCacheEntry) GetAllPIDs() []uint32 {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	return c.pidCache.GetAllSorted()
}

func (c *CookieCacheEntry) AddPIDs(pids ...uint32) {
	if len(pids) <= 0 {
		return
	}
	c.rwMutex.Lock()
	defer c.rwMutex.Unlock()
	c.pidCache.AddAny(pids...)
}

func (c *CookieCacheEntry) DeletePIDs(pids ...uint32) {
	if len(pids) <= 0 {
		return
	}
	c.rwMutex.Lock()
	defer c.rwMutex.Unlock()
	c.pidCache.DeleteAny(pids...)
}

type PIDCache map[uint32]struct{}

func NewPIDCache(pids ...uint32) *PIDCache {
	p := &PIDCache{}
	p.AddAny(pids...)
	return p
}

func (p PIDCache) DeepCopy() *PIDCache {
	copiedM := map[uint32]struct{}{}
	for pid := range p {
		copiedM[pid] = struct{}{}
	}
	return (*PIDCache)(&copiedM)
}

func (p PIDCache) Len() int {
	return len(p)
}

func (p PIDCache) Has(pid uint32) bool {
	_, ok := p[pid]
	return ok
}

func (p PIDCache) GetAllSorted() []uint32 {
	if len(p) <= 0 {
		return nil
	}
	pids := make([]uint32, len(p))
	i := 0
	for pid := range p {
		pids[i] = pid
		i++
	}
	sort.Slice(pids, func(i, j int) bool {
		return pids[i] < pids[j]
	})
	return pids
}

func (p PIDCache) AddAny(pids ...uint32) {
	for _, pid := range pids {
		p[pid] = struct{}{}
	}
}

func (p PIDCache) DeleteAny(pids ...uint32) {
	for _, pid := range pids {
		delete(p, pid)
	}
}

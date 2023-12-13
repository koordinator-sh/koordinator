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

	"golang.org/x/exp/constraints"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// CookieCacheEntry is an entry which stores the cookie ID and its belonging PIDs.
type CookieCacheEntry struct {
	rwMutex  sync.RWMutex
	CookieID uint64
	pidCache *OrderedMap[uint32]
}

func newCookieCacheEntry(cookieID uint64, pids ...uint32) *CookieCacheEntry {
	return &CookieCacheEntry{
		CookieID: cookieID,
		pidCache: newUint32OrderedMap(pids...),
	}
}

func (c *CookieCacheEntry) DeepCopy() *CookieCacheEntry {
	return &CookieCacheEntry{
		CookieID: c.CookieID,
		pidCache: c.pidCache.DeepCopy(),
	}
}

func (c *CookieCacheEntry) GetCookieID() uint64 {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	return c.CookieID
}

func (c *CookieCacheEntry) SetCookieID(cookieID uint64) {
	c.rwMutex.Lock()
	defer c.rwMutex.Unlock()
	c.CookieID = cookieID
}

func (c *CookieCacheEntry) IsEntryInvalid() bool {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	return c.CookieID <= sysutil.DefaultCoreSchedCookieID || c.pidCache.Len() <= 0
}

func (c *CookieCacheEntry) HasPID(pid uint32) bool {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	_, ok := c.pidCache.Get(pid)
	return ok
}

func (c *CookieCacheEntry) ContainsPIDs(pids ...uint32) []uint32 {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	var notFoundpids []uint32
	for _, pid := range pids {
		_, ok := c.pidCache.Get(pid)
		if !ok {
			notFoundpids = append(notFoundpids, pid)
		}
	}
	return notFoundpids
}

func (c *CookieCacheEntry) GetAllPIDs() []uint32 {
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	return c.pidCache.GetAll()
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

type IndexedValue[T constraints.Ordered] struct {
	Value T
	Index int
}

const MaxSimpleRecreateSize = 4

// OrderedMap is an ordered map with type T.
type OrderedMap[T constraints.Ordered] struct {
	Set  map[T]*IndexedValue[T]
	List []T // ascending order
}

func newUint32OrderedMap(vs ...uint32) *OrderedMap[uint32] {
	return newOrderedMap[uint32](vs...)
}

func newOrderedMap[T constraints.Ordered](vs ...T) *OrderedMap[T] {
	s := &OrderedMap[T]{
		Set:  map[T]*IndexedValue[T]{},
		List: make([]T, 0),
	}
	if len(vs) > 0 {
		s.AddAny(vs...)
	}
	return s
}

func (o *OrderedMap[T]) DeepCopy() *OrderedMap[T] {
	u := &OrderedMap[T]{
		Set:  map[T]*IndexedValue[T]{},
		List: make([]T, len(o.List)),
	}
	for i := range o.List {
		v := o.List[i]
		u.List[i] = v
		u.Set[v] = &IndexedValue[T]{
			Value: v,
			Index: i,
		}
	}
	return u
}

func (o *OrderedMap[T]) Len() int {
	return len(o.List)
}

func (o *OrderedMap[T]) Get(v T) (T, bool) {
	e, ok := o.Set[v]
	if ok {
		return e.Value, true
	}
	var oo T
	return oo, false
}

func (o *OrderedMap[T]) GetAll() []T {
	if o.Len() <= 0 {
		return nil
	}
	l := make([]T, len(o.List))
	copy(l, o.List)
	return l
}

func (o *OrderedMap[T]) Add(v T) {
	if _, ok := o.Set[v]; ok {
		return
	}

	o.Set[v] = &IndexedValue[T]{
		Value: v,
	}
	n := len(o.List)
	idx := sort.Search(n, func(i int) bool {
		return o.List[i] > v
	})

	if idx >= n {
		o.List = append(o.List, v)
		o.Set[v].Index = n
		return
	}

	var e T
	o.List = append(o.List, e)
	copy(o.List[idx+1:], o.List[idx:n])
	o.List[idx] = v
	o.Set[v].Index = idx
	for i, l := range o.List[idx+1:] {
		o.Set[l].Index = i + idx + 1
	}
}

func (o *OrderedMap[T]) AddAny(vs ...T) {
	if n := len(o.List); n > MaxSimpleRecreateSize && len(vs)*2 <= n {
		for _, v := range vs {
			o.Add(v)
		}
		return
	}

	for _, v := range vs {
		if _, ok := o.Set[v]; ok {
			continue
		}
		o.Set[v] = &IndexedValue[T]{
			Value: v,
		}
		o.List = append(o.List, v)
	}
	sort.Slice(o.List, func(i, j int) bool {
		return o.List[i] < o.List[j]
	})
	for i, v := range o.List {
		o.Set[v].Index = i
	}
}

func (o *OrderedMap[T]) Delete(v T) bool {
	e, ok := o.Set[v]
	if !ok {
		return false
	}
	idx := e.Index
	delete(o.Set, v)
	if idx <= 0 {
		o.List = o.List[idx+1:]
	} else {
		o.List = append(o.List[:idx], o.List[idx+1:]...)
	}
	for i, l := range o.List[idx:] {
		o.Set[l].Index = i + idx
	}
	return true
}

func (o *OrderedMap[T]) DeleteAny(vs ...T) {
	if n := len(o.List); n > MaxSimpleRecreateSize && len(vs)*2 <= n {
		for _, v := range vs {
			o.Delete(v)
		}
		return
	}

	for _, v := range vs {
		if _, ok := o.Set[v]; ok {
			delete(o.Set, v)
		}
	}
	l := make([]T, 0)
	for v := range o.Set {
		l = append(l, v)
	}
	sort.Slice(l, func(i, j int) bool {
		return l[i] < l[j]
	})
	o.List = l
	for i, v := range o.List {
		o.Set[v].Index = i
	}
}

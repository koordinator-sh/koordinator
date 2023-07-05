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

package metriccache

import "sync"

type KVStorage interface {
	Get(key interface{}) (interface{}, bool)
	Set(key, value interface{})
}

type memoryStorage struct {
	value sync.Map
}

func NewMemoryStorage() KVStorage {
	return &memoryStorage{value: sync.Map{}}
}

func (ms *memoryStorage) Get(key interface{}) (interface{}, bool) {
	return ms.value.Load(key)
}

func (ms *memoryStorage) Set(key, value interface{}) {
	ms.value.Store(key, value)
}

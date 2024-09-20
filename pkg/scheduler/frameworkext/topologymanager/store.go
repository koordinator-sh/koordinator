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

package topologymanager

import (
	"sync"

	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	affinityStateKey = "koordinator.sh/topology-affinity-store"
)

type Store struct {
	affinityMap sync.Map
}

func InitStore(cycleState *framework.CycleState) {
	cycleState.Write(affinityStateKey, &Store{})
}

func GetStore(cycleState *framework.CycleState) *Store {
	s, err := cycleState.Read(affinityStateKey)
	if err != nil {
		return &Store{}
	}
	store := s.(*Store)
	return store
}

func (s *Store) Clone() framework.StateData {
	return s
}

func (s *Store) SetAffinity(nodeName string, affinity NUMATopologyHint) {
	s.affinityMap.Store(nodeName, &affinity)
}

func (s *Store) GetAffinity(nodeName string) (NUMATopologyHint, bool) {
	val, ok := s.affinityMap.Load(nodeName)
	if !ok {
		return NUMATopologyHint{}, false
	}
	hint := val.(*NUMATopologyHint)
	return *hint, true
}

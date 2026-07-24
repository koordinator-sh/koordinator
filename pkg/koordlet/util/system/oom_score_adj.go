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

package system

import (
	"fmt"
	"sync"
)

const (
	// VirtualOOMScoreAdjName is the name of a virtual system resource for oom_score_adj.
	VirtualOOMScoreAdjName = "oom_score_adj"
)

var (
	// VirtualOOMScoreAdj represents a virtual system resource for oom_score_adj.
	// It is virtual for denoting the operation on processes' oom_score_adj, and it is not allowed to do
	// any real read or write on the provided filepath.
	VirtualOOMScoreAdj = NewCommonSystemResource("", VirtualOOMScoreAdjName, GetProcRootDir)
)

// OOMScoreAdjInterface defines the operations on per-process /proc/<pid>/oom_score_adj.
type OOMScoreAdjInterface interface {
	// Get reads the current oom_score_adj value for the given PID.
	Get(pid uint32) (int64, error)
	// Set writes the target oom_score_adj value for the given PID.
	// It should validate that the value is within [OOMScoreAdjMin, OOMScoreAdjMax].
	Set(pid uint32, val int64) error
}

// FakeOOMScoreAdj implements OOMScoreAdjInterface for testing.
type FakeOOMScoreAdj struct {
	mu       sync.RWMutex
	PIDToVal map[uint32]int64
	PIDToErr map[uint32]bool
}

// NewFakeOOMScoreAdj creates a FakeOOMScoreAdj for testing.
func NewFakeOOMScoreAdj(pidToVal map[uint32]int64, pidToErr map[uint32]bool) OOMScoreAdjInterface {
	f := &FakeOOMScoreAdj{
		PIDToVal: pidToVal,
		PIDToErr: pidToErr,
	}
	if f.PIDToVal == nil {
		f.PIDToVal = map[uint32]int64{}
	}
	if f.PIDToErr == nil {
		f.PIDToErr = map[uint32]bool{}
	}
	return f
}

func (f *FakeOOMScoreAdj) Get(pid uint32) (int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.PIDToErr[pid]; ok {
		return 0, fmt.Errorf("get oom_score_adj error for pid %d", pid)
	}
	if val, ok := f.PIDToVal[pid]; ok {
		return val, nil
	}
	return 0, nil
}

func (f *FakeOOMScoreAdj) Set(pid uint32, val int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.PIDToErr[pid]; ok {
		return fmt.Errorf("set oom_score_adj error for pid %d", pid)
	}
	if val < OOMScoreAdjMin || val > OOMScoreAdjMax {
		return fmt.Errorf("oom_score_adj value %d out of range [%d, %d] for pid %d", val, OOMScoreAdjMin, OOMScoreAdjMax, pid)
	}
	f.PIDToVal[pid] = val
	return nil
}

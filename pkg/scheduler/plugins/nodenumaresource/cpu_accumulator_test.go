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

package nodenumaresource

import (
	"reflect"
	"testing"

	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
)

func buildCPUTopologyForTest(numSockets, nodesPerSocket, coresPerNode, cpusPerCore int) *CPUTopology {
	topo := &CPUTopology{
		NumSockets: numSockets,
		NumNodes:   nodesPerSocket * numSockets,
		NumCores:   coresPerNode * nodesPerSocket * numSockets,
		NumCPUs:    cpusPerCore * coresPerNode * nodesPerSocket * numSockets,
		CPUDetails: make(map[int]CPUInfo),
	}
	var nodeID, coreID, cpuID int
	for s := 0; s < numSockets; s++ {
		for n := 0; n < nodesPerSocket; n++ {
			for c := 0; c < coresPerNode; c++ {
				for p := 0; p < cpusPerCore; p++ {
					topo.CPUDetails[cpuID] = CPUInfo{
						SocketID: s,
						NodeID:   s<<16 | nodeID,
						CoreID:   coreID,
						CPUID:    cpuID,
					}
					cpuID++
				}
				coreID++
			}
			nodeID++
		}
	}
	return topo
}

func TestTakeFullPCPUs(t *testing.T) {
	tests := []struct {
		name          string
		topology      *CPUTopology
		allocatedCPUs CPUSet
		numCPUsNeeded int
		wantError     bool
		wantResult    CPUSet
	}{
		{
			name:          "allocate on non-NUMA node",
			topology:      buildCPUTopologyForTest(1, 1, 4, 2),
			numCPUsNeeded: 2,
			wantResult:    NewCPUSet(0, 1),
		},
		{
			name:          "with allocated cpus",
			topology:      buildCPUTopologyForTest(1, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 1),
			numCPUsNeeded: 2,
			wantResult:    NewCPUSet(2, 3),
		},
		{
			name:          "allocate whole socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			numCPUsNeeded: 8,
			wantResult:    MustParse("0-7"),
		},
		{
			name:          "allocate across socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			numCPUsNeeded: 12,
			wantResult:    MustParse("0-11"),
		},
		{
			name:          "allocate whole socket with partially-allocated socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 1),
			numCPUsNeeded: 8,
			wantResult:    MustParse("8-15"),
		},
		{
			name:          "allocate in the smallest idle socket",
			topology:      buildCPUTopologyForTest(2, 2, 4, 2),
			allocatedCPUs: MustParse("0-5,16-23"),
			numCPUsNeeded: 6,
			wantResult:    MustParse("24-29"),
		},
		{
			name:          "allocate the most of CPUs on the same socket",
			topology:      buildCPUTopologyForTest(2, 2, 4, 2),
			allocatedCPUs: MustParse("0-5,16-23"),
			numCPUsNeeded: 12,
			wantResult:    MustParse("6-15,24-25"),
		},
		{
			name:          "allocate from first socket",
			topology:      buildCPUTopologyForTest(2, 2, 4, 2),
			allocatedCPUs: MustParse("0-3,8-11"),
			numCPUsNeeded: 4,
			wantResult:    MustParse("4-7"),
		},
		{
			name:          "allocate with less spread cpus",
			topology:      buildCPUTopologyForTest(2, 2, 2, 2),
			allocatedCPUs: NewCPUSet(0, 2, 4, 8, 12),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(10, 11, 14, 15),
		},
		{
			name:          "allocate with the most spread cpus",
			topology:      buildCPUTopologyForTest(2, 2, 2, 2),
			allocatedCPUs: NewCPUSet(0, 2, 4, 8, 10, 12),
			numCPUsNeeded: 6,
			wantResult:    NewCPUSet(5, 6, 7, 13, 14, 15),
		},
		{
			name:          "allocate with the most spread cpus on the smallest idle cpus socket",
			topology:      buildCPUTopologyForTest(2, 2, 2, 2),
			allocatedCPUs: NewCPUSet(0, 2, 4, 8, 9, 10, 12),
			numCPUsNeeded: 6,
			wantResult:    NewCPUSet(6, 7, 11, 13, 14, 15),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			availableCPUs := tt.topology.CPUDetails.CPUs().Difference(tt.allocatedCPUs)
			allocatedCPUsDetails := tt.topology.CPUDetails.KeepOnly(tt.allocatedCPUs)
			result, err := takeCPUs(
				tt.topology, availableCPUs, allocatedCPUsDetails,
				tt.numCPUsNeeded, schedulingconfig.CPUBindPolicyFullPCPUs, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMAMostAllocated)
			if tt.wantError && err == nil {
				t.Fatal("expect error but got nil")
			} else if !tt.wantError && err != nil {
				t.Fatal("expect no error, but got error:", err)
			}
			if !tt.wantResult.Equals(result) {
				t.Fatalf("expect: %s, but got: %s", tt.wantResult.String(), result.String())
			}
		})
	}
}

func TestTakeFullPCPUsWithNUMALeastAllocated(t *testing.T) {
	tests := []struct {
		name          string
		topology      *CPUTopology
		allocatedCPUs CPUSet
		numCPUsNeeded int
		wantError     bool
		wantResult    CPUSet
	}{
		{
			name:          "allocate on non-NUMA node",
			topology:      buildCPUTopologyForTest(1, 1, 4, 2),
			numCPUsNeeded: 2,
			wantResult:    NewCPUSet(0, 1),
		},
		{
			name:          "with allocated cpus",
			topology:      buildCPUTopologyForTest(1, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 1),
			numCPUsNeeded: 2,
			wantResult:    NewCPUSet(2, 3),
		},
		{
			name:          "allocate whole socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			numCPUsNeeded: 8,
			wantResult:    MustParse("0-7"),
		},
		{
			name:          "allocate across socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			numCPUsNeeded: 12,
			wantResult:    MustParse("0-11"),
		},
		{
			name:          "allocate whole socket with partially-allocated socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 1),
			numCPUsNeeded: 8,
			wantResult:    MustParse("8-15"),
		},
		{
			name:          "allocate in the most idle socket",
			topology:      buildCPUTopologyForTest(2, 2, 4, 2),
			allocatedCPUs: MustParse("0-5,16-23"),
			numCPUsNeeded: 6,
			wantResult:    MustParse("8-13"),
		},
		{
			name:          "allocate the most of CPUs on the same socket",
			topology:      buildCPUTopologyForTest(2, 2, 4, 2),
			allocatedCPUs: MustParse("0-5,16-23"),
			numCPUsNeeded: 12,
			wantResult:    MustParse("6-15,24-25"),
		},
		{
			name:          "allocate from second socket",
			topology:      buildCPUTopologyForTest(2, 2, 4, 2),
			allocatedCPUs: MustParse("0-3,8-11"),
			numCPUsNeeded: 4,
			wantResult:    MustParse("16-19"),
		},
		{
			name:          "allocate with less spread cpus",
			topology:      buildCPUTopologyForTest(2, 2, 2, 2),
			allocatedCPUs: NewCPUSet(0, 2, 4, 8, 12),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(10, 11, 14, 15),
		},
		{
			name:          "allocate with the less spread cpus 2",
			topology:      buildCPUTopologyForTest(2, 2, 2, 2),
			allocatedCPUs: NewCPUSet(0, 2, 4, 8, 10, 12),
			numCPUsNeeded: 6,
			wantResult:    NewCPUSet(6, 7, 14, 15, 1, 3),
		},
		{
			name:          "allocate with the most spread cpus on the most idle cpus socket 3",
			topology:      buildCPUTopologyForTest(2, 2, 4, 2),
			allocatedCPUs: NewCPUSet(0, 2, 4, 8, 9, 10, 12),
			numCPUsNeeded: 6,
			wantResult:    NewCPUSet(16, 17, 18, 19, 20, 21),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			availableCPUs := tt.topology.CPUDetails.CPUs().Difference(tt.allocatedCPUs)
			allocatedCPUsDetails := tt.topology.CPUDetails.KeepOnly(tt.allocatedCPUs)
			result, err := takeCPUs(
				tt.topology, availableCPUs, allocatedCPUsDetails,
				tt.numCPUsNeeded, schedulingconfig.CPUBindPolicyFullPCPUs, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMALeastAllocated)
			if tt.wantError && err == nil {
				t.Fatal("expect error but got nil")
			} else if !tt.wantError && err != nil {
				t.Fatal("expect no error, but got error:", err)
			}
			if !tt.wantResult.Equals(result) {
				t.Fatalf("expect: %s, but got: %s", tt.wantResult.String(), result.String())
			}
		})
	}
}

func TestCPUSpreadByPCPUs(t *testing.T) {
	topology := buildCPUTopologyForTest(2, 2, 4, 2)
	acc := newCPUAccumulator(topology, topology.CPUDetails.CPUs(), nil, 8, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMAMostAllocated)
	result := acc.freeCPUs(false)
	result = acc.spreadCPUs(result)
	if !reflect.DeepEqual([]int{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30, 1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23, 25, 27, 29, 31}, result) {
		t.Fatal("unexpect spread result")
	}
}

func TestTakeSpreadByPCPUs(t *testing.T) {
	tests := []struct {
		name          string
		topology      *CPUTopology
		allocatedCPUs CPUSet
		numCPUsNeeded int
		wantError     bool
		wantResult    CPUSet
	}{
		{
			name:          "allocate on non-NUMA node",
			topology:      buildCPUTopologyForTest(1, 1, 4, 2),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(0, 2, 4, 6),
		},
		{
			name:          "allocate satisfied the partially-allocated socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 2),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(1, 3, 4, 6),
		},
		{
			name:          "allocate cpus on full-free socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 1, 2, 3),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(8, 10, 12, 14),
		},
		{
			name:          "allocate most of CPUs in the same socket and overlapped-cores",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 2),
			numCPUsNeeded: 6,
			wantResult:    MustParse("1,3-7"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			availableCPUs := tt.topology.CPUDetails.CPUs().Difference(tt.allocatedCPUs)
			allocatedCPUsDetails := tt.topology.CPUDetails.KeepOnly(tt.allocatedCPUs)
			result, err := takeCPUs(
				tt.topology, availableCPUs, allocatedCPUsDetails,
				tt.numCPUsNeeded, schedulingconfig.CPUBindPolicySpreadByPCPUs, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMAMostAllocated)
			if tt.wantError && err == nil {
				t.Fatal("expect error but got nil")
			} else if !tt.wantError && err != nil {
				t.Fatal("expect no error, but got error:", err)
			}
			if !tt.wantResult.Equals(result) {
				t.Fatalf("expect: %s, but got: %s", tt.wantResult.String(), result.String())
			}
		})
	}
}

func TestCPUSpreadByPCPUsWithNUMALeastAllocated(t *testing.T) {
	topology := buildCPUTopologyForTest(2, 2, 4, 2)
	acc := newCPUAccumulator(topology, topology.CPUDetails.CPUs(), nil, 8, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMALeastAllocated)
	result := acc.freeCPUs(false)
	result = acc.spreadCPUs(result)
	if !reflect.DeepEqual([]int{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30, 1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23, 25, 27, 29, 31}, result) {
		t.Fatal("unexpect spread result")
	}
}

func TestTakeSpreadByPCPUsWithNUMALeastAllocated(t *testing.T) {
	tests := []struct {
		name          string
		topology      *CPUTopology
		allocatedCPUs CPUSet
		numCPUsNeeded int
		wantError     bool
		wantResult    CPUSet
	}{
		{
			name:          "allocate on non-NUMA node",
			topology:      buildCPUTopologyForTest(1, 1, 4, 2),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(0, 2, 4, 6),
		},
		{
			name:          "allocate satisfied the partially-allocated socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 2),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(8, 10, 12, 14),
		},
		{
			name:          "allocate cpus on full-free socket",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 1, 2, 3),
			numCPUsNeeded: 4,
			wantResult:    NewCPUSet(8, 10, 12, 14),
		},
		{
			name:          "allocate most of CPUs in the same socket and overlapped-cores",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedCPUs: NewCPUSet(0, 2),
			numCPUsNeeded: 6,
			wantResult:    MustParse("8,10,12,14,9,11"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			availableCPUs := tt.topology.CPUDetails.CPUs().Difference(tt.allocatedCPUs)
			allocatedCPUsDetails := tt.topology.CPUDetails.KeepOnly(tt.allocatedCPUs)
			result, err := takeCPUs(
				tt.topology, availableCPUs, allocatedCPUsDetails,
				tt.numCPUsNeeded, schedulingconfig.CPUBindPolicySpreadByPCPUs, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMALeastAllocated)
			if tt.wantError && err == nil {
				t.Fatal("expect error but got nil")
			} else if !tt.wantError && err != nil {
				t.Fatal("expect no error, but got error:", err)
			}
			if !tt.wantResult.Equals(result) {
				t.Fatalf("expect: %s, but got: %s", tt.wantResult.String(), result.String())
			}
		})
	}
}

func TestTakeCPUsWithExclusivePolicy(t *testing.T) {
	tests := []struct {
		name                     string
		topology                 *CPUTopology
		allocatedExclusiveCPUs   CPUSet
		allocatedExclusivePolicy schedulingconfig.CPUExclusivePolicy
		bindPolicy               schedulingconfig.CPUBindPolicy
		exclusivePolicy          schedulingconfig.CPUExclusivePolicy
		numCPUsNeeded            int
		wantError                bool
		wantResult               CPUSet
	}{
		{
			name:                   "allocate cpus on full-free socket with PCPULevel",
			topology:               buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedExclusiveCPUs: NewCPUSet(0, 2),
			numCPUsNeeded:          4,
			wantResult:             NewCPUSet(8, 10, 12, 14),
		},
		{
			name:          "allocate overlapped cpus with PCPULevel",
			topology:      buildCPUTopologyForTest(2, 1, 4, 2),
			numCPUsNeeded: 10,
			wantResult:    NewCPUSet(0, 1, 2, 3, 4, 6, 8, 10, 12, 14),
		},
		{
			name:                   "allocate cpus on large-size partially-allocated socket with PCPULevel",
			topology:               buildCPUTopologyForTest(2, 1, 8, 2),
			allocatedExclusiveCPUs: NewCPUSet(0, 2),
			numCPUsNeeded:          4,
			wantResult:             NewCPUSet(4, 6, 8, 10),
		},
		{
			name:                   "allocate cpus with none exclusive policy",
			topology:               buildCPUTopologyForTest(2, 1, 8, 2),
			allocatedExclusiveCPUs: NewCPUSet(0, 2),
			exclusivePolicy:        schedulingconfig.CPUExclusivePolicyNone,
			numCPUsNeeded:          4,
			wantResult:             NewCPUSet(1, 3, 4, 6),
		},
		{
			name:                     "allocate cpus on full-free socket with NUMANodeLevel",
			topology:                 buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedExclusiveCPUs:   NewCPUSet(0, 2),
			allocatedExclusivePolicy: schedulingconfig.CPUExclusivePolicyNUMANodeLevel,
			exclusivePolicy:          schedulingconfig.CPUExclusivePolicyNUMANodeLevel,
			numCPUsNeeded:            4,
			wantResult:               NewCPUSet(8, 10, 12, 14),
		},
		{
			name:                     "allocate cpus on partially-allocated socket without NUMANodeLevel",
			topology:                 buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedExclusiveCPUs:   NewCPUSet(0, 2),
			allocatedExclusivePolicy: schedulingconfig.CPUExclusivePolicyNUMANodeLevel,
			exclusivePolicy:          schedulingconfig.CPUExclusivePolicyNone,
			numCPUsNeeded:            4,
			wantResult:               NewCPUSet(1, 3, 4, 6),
		},
		{
			name:                     "allocate cpus on full-free socket with NUMANodeLevel with PCPUs",
			topology:                 buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedExclusiveCPUs:   NewCPUSet(0, 2),
			allocatedExclusivePolicy: schedulingconfig.CPUExclusivePolicyNUMANodeLevel,
			exclusivePolicy:          schedulingconfig.CPUExclusivePolicyNUMANodeLevel,
			bindPolicy:               schedulingconfig.CPUBindPolicyFullPCPUs,
			numCPUsNeeded:            4,
			wantResult:               NewCPUSet(8, 9, 10, 11),
		},
		{
			name:                     "allocate cpus on partially-allocated socket without NUMANodeLevel with PCPUs",
			topology:                 buildCPUTopologyForTest(2, 1, 4, 2),
			allocatedExclusiveCPUs:   NewCPUSet(0, 2),
			allocatedExclusivePolicy: schedulingconfig.CPUExclusivePolicyNUMANodeLevel,
			exclusivePolicy:          schedulingconfig.CPUExclusivePolicyNone,
			bindPolicy:               schedulingconfig.CPUBindPolicyFullPCPUs,
			numCPUsNeeded:            4,
			wantResult:               NewCPUSet(4, 5, 6, 7),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			availableCPUs := tt.topology.CPUDetails.CPUs().Difference(tt.allocatedExclusiveCPUs)
			allocatedCPUsDetails := tt.topology.CPUDetails.KeepOnly(tt.allocatedExclusiveCPUs)
			for _, cpuID := range tt.allocatedExclusiveCPUs.ToSliceNoSort() {
				cpuInfo := allocatedCPUsDetails[cpuID]
				if tt.allocatedExclusivePolicy != "" {
					cpuInfo.ExclusivePolicy = tt.allocatedExclusivePolicy
				} else {
					cpuInfo.ExclusivePolicy = schedulingconfig.CPUExclusivePolicyPCPULevel
				}
				allocatedCPUsDetails[cpuID] = cpuInfo
			}

			if tt.exclusivePolicy == "" {
				tt.exclusivePolicy = schedulingconfig.CPUExclusivePolicyPCPULevel
			}
			if tt.bindPolicy == "" {
				tt.bindPolicy = schedulingconfig.CPUBindPolicySpreadByPCPUs
			}

			result, err := takeCPUs(
				tt.topology, availableCPUs, allocatedCPUsDetails,
				tt.numCPUsNeeded, tt.bindPolicy, tt.exclusivePolicy, schedulingconfig.NUMAMostAllocated)
			if tt.wantError && err == nil {
				t.Fatal("expect error but got nil")
			} else if !tt.wantError && err != nil {
				t.Fatal("expect no error, but got error:", err)
			}
			if !tt.wantResult.Equals(result) {
				t.Fatalf("expect: %s, but got: %s", tt.wantResult.String(), result.String())
			}
		})
	}
}

func BenchmarkTakeCPUsWithSameCoreFirst(b *testing.B) {
	tests := []struct {
		name          string
		numCPUsNeeded int
	}{
		{
			name:          "2C",
			numCPUsNeeded: 2,
		},
		{
			name:          "4C",
			numCPUsNeeded: 4,
		},
		{
			name:          "8C",
			numCPUsNeeded: 8,
		},
		{
			name:          "12C",
			numCPUsNeeded: 12,
		},
		{
			name:          "16C",
			numCPUsNeeded: 16,
		},
		{
			name:          "24C",
			numCPUsNeeded: 24,
		},
		{
			name:          "32C",
			numCPUsNeeded: 32,
		},
	}

	topology := buildCPUTopologyForTest(2, 1, 16, 2)
	cpus := topology.CPUDetails.CPUs()
	b.ResetTimer()
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := takeCPUs(
					topology, cpus, nil, tt.numCPUsNeeded, schedulingconfig.CPUBindPolicyFullPCPUs, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMAMostAllocated)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTakeCPUsWithSpread(b *testing.B) {
	tests := []struct {
		name          string
		numCPUsNeeded int
		sameCore      bool
	}{
		{
			name:          "2C",
			numCPUsNeeded: 2,
		},
		{
			name:          "4C",
			numCPUsNeeded: 4,
		},
		{
			name:          "8C",
			numCPUsNeeded: 8,
		},
		{
			name:          "12C",
			numCPUsNeeded: 12,
		},
		{
			name:          "16C",
			numCPUsNeeded: 16,
		},
		{
			name:          "24C",
			numCPUsNeeded: 24,
		},
		{
			name:          "32C",
			numCPUsNeeded: 32,
		},
	}

	topology := buildCPUTopologyForTest(2, 1, 16, 2)
	cpus := topology.CPUDetails.CPUs()
	b.ResetTimer()
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := takeCPUs(
					topology, cpus, nil, tt.numCPUsNeeded, schedulingconfig.CPUBindPolicySpreadByPCPUs, schedulingconfig.CPUExclusivePolicyNone, schedulingconfig.NUMAMostAllocated)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

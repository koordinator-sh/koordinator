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
	"github.com/koordinator-sh/koordinator/apis/extension"
)

// CPUTopology contains details of node cpu
type CPUTopology struct {
	NumCPUs    int
	NumCores   int
	NumNodes   int
	NumSockets int
	CPUDetails CPUDetails
}

// IsValid checks if the topology is valid
func (topo *CPUTopology) IsValid() bool {
	return topo.NumSockets != 0 && topo.NumNodes != 0 && topo.NumCores != 0 && topo.NumCPUs != 0
}

// CPUsPerCore returns the number of logical CPUs are associated with each core.
func (topo *CPUTopology) CPUsPerCore() int {
	if topo.NumCores == 0 {
		return 0
	}
	return topo.NumCPUs / topo.NumCores
}

// CPUsPerSocket returns the number of logical CPUs are associated with each socket.
func (topo *CPUTopology) CPUsPerSocket() int {
	if topo.NumSockets == 0 {
		return 0
	}
	return topo.NumCPUs / topo.NumSockets
}

// CPUsPerNode returns the number of logical CPUs are associated with each node.
func (topo *CPUTopology) CPUsPerNode() int {
	if topo.NumNodes == 0 {
		return 0
	}
	return topo.NumCPUs / topo.NumNodes
}

// CPUDetails is a map from logical CPU ID to CPUInfo.
type CPUDetails map[int]CPUInfo

// NewCPUDetails returns CPUDetails instance
func NewCPUDetails() CPUDetails {
	return CPUDetails{}
}

// CPUInfo contains the NUMA, socket, and core IDs associated with a CPU.
type CPUInfo struct {
	CPUID           int
	CoreID          int
	NodeID          int
	SocketID        int
	RefCount        int
	ExclusivePolicy extension.CPUExclusivePolicy
}

// Clone clones the CPUDetails
func (d CPUDetails) Clone() CPUDetails {
	c := make(CPUDetails)
	for k, v := range d {
		c[k] = v
	}
	return c
}

// KeepOnly returns a new CPUDetails object with only the supplied cpus.
func (d CPUDetails) KeepOnly(cpus CPUSet) CPUDetails {
	result := CPUDetails{}
	for cpu, info := range d {
		if cpus.Contains(cpu) {
			result[cpu] = info
		}
	}
	return result
}

// NUMANodes returns the NUMANode IDs associated with the CPUs in this CPUDetails.
func (d CPUDetails) NUMANodes() CPUSet {
	b := NewCPUSetBuilder()
	for _, info := range d {
		b.Add(info.NodeID)
	}
	return b.Result()
}

// NUMANodesInSockets returns the logical NUMANode IDs associated with the given socket IDs in this CPUDetails.
func (d CPUDetails) NUMANodesInSockets(ids ...int) CPUSet {
	b := NewCPUSetBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.SocketID == id {
				b.Add(info.NodeID)
			}
		}
	}
	return b.Result()
}

// Sockets returns the socket IDs associated with the CPUs in this CPUDetails.
func (d CPUDetails) Sockets() CPUSet {
	b := NewCPUSetBuilder()
	for _, info := range d {
		b.Add(info.SocketID)
	}
	return b.Result()
}

// CPUsInSockets returns logical CPU IDs associated with the given socket IDs in this CPUDetails.
func (d CPUDetails) CPUsInSockets(ids ...int) CPUSet {
	b := NewCPUSetBuilder()
	for _, id := range ids {
		for cpu, info := range d {
			if info.SocketID == id {
				b.Add(cpu)
			}
		}
	}
	return b.Result()
}

// SocketsInNUMANodes returns the socket IDs associated with the given NUMANode IDs in this CPUDetails.
func (d CPUDetails) SocketsInNUMANodes(ids ...int) CPUSet {
	b := NewCPUSetBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.NodeID == id {
				b.Add(info.SocketID)
			}
		}
	}
	return b.Result()
}

// Cores returns the core IDs associated with the CPUs in this CPUDetails.
func (d CPUDetails) Cores() CPUSet {
	b := NewCPUSetBuilder()
	for _, info := range d {
		b.Add(info.CoreID)
	}
	return b.Result()
}

// CoresInNUMANodes returns the core IDs associated with the given NUMANode IDs in this CPUDetails.
func (d CPUDetails) CoresInNUMANodes(ids ...int) CPUSet {
	b := NewCPUSetBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.NodeID == id {
				b.Add(info.CoreID)
			}
		}
	}
	return b.Result()
}

// CoresInSockets returns the core IDs associated with the given socket IDs in this CPUDetails.
func (d CPUDetails) CoresInSockets(ids ...int) CPUSet {
	b := NewCPUSetBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.SocketID == id {
				b.Add(info.CoreID)
			}
		}
	}
	return b.Result()
}

// CPUs returns the logical CPU IDs in this CPUDetails.
func (d CPUDetails) CPUs() CPUSet {
	b := NewCPUSetBuilder()
	for cpuID := range d {
		b.Add(cpuID)
	}
	return b.Result()
}

// CPUsInNUMANodes returns the logical CPU IDs associated with the given NUMANode IDs in this CPUDetails.
func (d CPUDetails) CPUsInNUMANodes(ids ...int) CPUSet {
	b := NewCPUSetBuilder()
	for _, id := range ids {
		for cpu, info := range d {
			if info.NodeID == id {
				b.Add(cpu)
			}
		}
	}
	return b.Result()
}

// CPUsInCores returns the logical CPU IDs associated with the given core IDs in this CPUDetails.
func (d CPUDetails) CPUsInCores(ids ...int) CPUSet {
	b := NewCPUSetBuilder()
	for _, id := range ids {
		for cpu, info := range d {
			if info.CoreID == id {
				b.Add(cpu)
			}
		}
	}
	return b.Result()
}

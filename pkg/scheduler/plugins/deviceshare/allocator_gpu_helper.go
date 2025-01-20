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

package deviceshare

import (
	"sort"

	corev1 "k8s.io/api/core/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var (
	GPUPartitionIndexOfNVIDIAHopper = GPUPartitionIndexer{
		1: []*PartitionsOfAllocationScore{
			{
				Partitions: []*apiext.GPUPartition{
					{
						Minors:          []int{0},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{0}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{1},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{1}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{2},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{2}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{3},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{3}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{4},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{4}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{5},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{5}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{6},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{6}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{7},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{7}),
						AllocationScore: 1,
					},
				},
				AllocationScore: 1,
			},
		},
		2: []*PartitionsOfAllocationScore{
			{
				Partitions: []*apiext.GPUPartition{
					{
						Minors:          []int{0, 1},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{0, 1}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{2, 3},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{2, 3}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{4, 5},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{4, 5}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{6, 7},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{6, 7}),
						AllocationScore: 1,
					},
				},
				AllocationScore: 1,
			},
		},
		4: []*PartitionsOfAllocationScore{
			{
				Partitions: []*apiext.GPUPartition{
					{
						Minors:          []int{0, 1, 2, 3},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{0, 1, 2, 3}),
						AllocationScore: 1,
					},
					{
						Minors:          []int{4, 5, 6, 7},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{4, 5, 6, 7}),
						AllocationScore: 1,
					},
				},
				AllocationScore: 1,
			},
		},
		8: []*PartitionsOfAllocationScore{
			{
				Partitions: []*apiext.GPUPartition{
					{
						Minors:          []int{0, 1, 2, 3, 4, 5, 6, 7},
						GPULinkType:     apiext.GPUNVLink,
						MinorsHash:      hashMinors([]int{0, 1, 2, 3, 4, 5, 6, 7}),
						AllocationScore: 1,
					},
				},
				AllocationScore: 1,
			},
		},
	}

	GetDesignatedGPUPartitionIndexer = func(node *corev1.Node) (GPUPartitionIndexer, bool) {
		var partitionIndexer GPUPartitionIndexer
		partitionPolicy := apiext.GetGPUPartitionPolicy(node)
		model := node.Labels[apiext.LabelGPUModel]
		switch model {
		case "H100", "H800", "H20":
			partitionIndexer = GPUPartitionIndexOfNVIDIAHopper
		}
		return partitionIndexer, partitionPolicy == apiext.GPUPartitionPolicyHonor
	}
)

func GetGPUPartitionIndexer(table apiext.GPUPartitionTable) GPUPartitionIndexer {
	if table == nil {
		return nil
	}
	gpuPartitionIndexerHelper := make(map[int]map[int]*PartitionsOfAllocationScore, len(table))
	for numberOfGPUs, partitions := range table {
		for i, partition := range partitions {
			if _, ok := gpuPartitionIndexerHelper[numberOfGPUs]; !ok {
				gpuPartitionIndexerHelper[numberOfGPUs] = make(map[int]*PartitionsOfAllocationScore)
			}
			indexerOfAllocationScore := gpuPartitionIndexerHelper[numberOfGPUs]
			if _, ok := indexerOfAllocationScore[partition.AllocationScore]; !ok {
				indexerOfAllocationScore[partition.AllocationScore] = &PartitionsOfAllocationScore{
					AllocationScore: partition.AllocationScore,
				}
			}
			partitions[i].MinorsHash = hashMinors(partition.Minors)
			partitionsOfAllocationScore := indexerOfAllocationScore[partition.AllocationScore]
			partitionsOfAllocationScore.Partitions = append(partitionsOfAllocationScore.Partitions, &partitions[i])
		}
	}

	gpuPartitionIndexer := make(GPUPartitionIndexer, len(table))
	for numberOfGPUs, indexerOfAllocationScore := range gpuPartitionIndexerHelper {
		if _, ok := gpuPartitionIndexer[numberOfGPUs]; !ok {
			gpuPartitionIndexer[numberOfGPUs] = []*PartitionsOfAllocationScore{}
		}
		for _, partitionsOfAllocationScore := range indexerOfAllocationScore {
			gpuPartitionIndexer[numberOfGPUs] = append(gpuPartitionIndexer[numberOfGPUs], partitionsOfAllocationScore)
		}
		sort.Slice(gpuPartitionIndexer[numberOfGPUs], func(i, j int) bool {
			return gpuPartitionIndexer[numberOfGPUs][i].AllocationScore < gpuPartitionIndexer[numberOfGPUs][j].AllocationScore
		})
	}
	return gpuPartitionIndexer
}

func GetGPUTopologyScope(deviceInfos []*schedulingv1alpha1.DeviceInfo, nodeDeviceResources deviceResources) *GPUTopologyScope {
	if len(deviceInfos) == 0 {
		return nil
	}
	pcieTopologyScopeIndexer := map[int32]map[string]deviceResources{}
	numaTopologyScopeIndexer := map[int32]deviceResources{}
	for _, info := range deviceInfos {
		if info.Topology == nil {
			return nil
		}
		minor := int(*info.Minor)
		if _, ok := numaTopologyScopeIndexer[info.Topology.NodeID]; !ok {
			numaTopologyScopeIndexer[info.Topology.NodeID] = deviceResources{}
		}
		numaTopologyScopeIndexer[info.Topology.NodeID][minor] = nodeDeviceResources[minor]
		if _, ok := pcieTopologyScopeIndexer[info.Topology.NodeID]; !ok {
			pcieTopologyScopeIndexer[info.Topology.NodeID] = map[string]deviceResources{}
		}
		if _, ok := pcieTopologyScopeIndexer[info.Topology.NodeID][info.Topology.PCIEID]; !ok {
			pcieTopologyScopeIndexer[info.Topology.NodeID][info.Topology.PCIEID] = deviceResources{}
		}
		pcieTopologyScopeIndexer[info.Topology.NodeID][info.Topology.PCIEID][minor] = nodeDeviceResources[minor]
	}
	gpuTopologyScope := &GPUTopologyScope{
		scopeName:       apiext.DeviceTopologyScopeNode,
		scopeLevel:      apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopeNode],
		minorsResources: nodeDeviceResources,
		minors:          getMinorsListFromMap(nodeDeviceResources),
		minorsHash:      hashDevices(nodeDeviceResources),
		childScopes:     []*GPUTopologyScope{},
	}
	for numaNodeID, resourcesOfNUMANode := range numaTopologyScopeIndexer {
		numaLevelScope := &GPUTopologyScope{
			scopeName:       apiext.DeviceTopologyScopeNUMANode,
			scopeLevel:      apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopeNUMANode],
			minorsResources: resourcesOfNUMANode,
			minors:          getMinorsListFromMap(resourcesOfNUMANode),
			minorsHash:      hashDevices(resourcesOfNUMANode),
			numaNodeID:      numaNodeID,
		}
		for pcieID, resourcesOfPCIE := range pcieTopologyScopeIndexer[numaNodeID] {
			pcieLevelScope := &GPUTopologyScope{
				scopeName:       apiext.DeviceTopologyScopePCIe,
				scopeLevel:      apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopePCIe],
				minorsResources: resourcesOfPCIE,
				minors:          getMinorsListFromMap(resourcesOfPCIE),
				minorsHash:      hashDevices(resourcesOfPCIE),
				pcieID:          pcieID,
			}
			numaLevelScope.childScopes = append(numaLevelScope.childScopes, pcieLevelScope)
		}
		sort.Slice(numaLevelScope.childScopes, func(i, j int) bool {
			return numaLevelScope.childScopes[i].pcieID < numaLevelScope.childScopes[j].pcieID
		})
		gpuTopologyScope.childScopes = append(gpuTopologyScope.childScopes, numaLevelScope)
	}
	sort.Slice(gpuTopologyScope.childScopes, func(i, j int) bool {
		return gpuTopologyScope.childScopes[i].numaNodeID < gpuTopologyScope.childScopes[j].numaNodeID
	})
	return gpuTopologyScope
}

func getMinorsListFromMap(resources deviceResources) []int {
	var minors []int
	for i := range resources {
		minors = append(minors, i)
	}
	sort.Slice(minors, func(i, j int) bool {
		return minors[i] < minors[j]
	})
	return minors
}

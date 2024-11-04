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

	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	ErrNoGPURequirements                    = "No GPU Requirements"
	ErrInsufficientPartitionedDevice        = "Insufficient Partitioned GPU Devices"
	ErrInsufficientTopologyScopedGPUDevices = "Insufficient Topology Scoped GPU Devices"
	ErrInsufficientGPUDevices               = "Insufficient GPU Devices"
	ErrNodeMissingGPUPartitionTable         = "node(s) missing GPU Partition Table"
	ErrUnsupportedGPURequests               = "node(s) Unsupported number of GPU requests"
	ErrUnsupportedMultiSharedGPU            = "node(s) Unsupported Multi-Shared GPU"
	ErrNodeMissingGPUDeviceTopologyTree     = "node(s) missing GPU Device Topology Tree"
)

func init() {
	deviceAllocators[schedulingv1alpha1.GPU] = &GPUAllocator{}
}

var _ DeviceAllocator = &GPUAllocator{}

type GPUAllocator struct {
}

type AllocateContext struct {
	deviceUsedMinorsHash int
	deviceFree           deviceResources
	deviceTotal          deviceResources
	allocationScorer     *resourceAllocationScorer
}

func getRealUsed(originalUsed, refinedTotal, refinedUsed deviceResources) deviceResources {
	realUsed := deviceResources{}
	for minor := range originalUsed {
		if _, ok := refinedTotal[minor]; !ok {
			realUsed[minor] = nil
		}
	}
	for minor := range refinedUsed {
		realUsed[minor] = nil
	}
	return realUsed
}

func (a *GPUAllocator) Allocate(requestCtx *requestContext, nodeDevice *nodeDevice, desiredCount int, maxDesiredCount int, preferredPCIEs sets.String) ([]*apiext.DeviceAllocation, *framework.Status) {
	gpuRequirements := requestCtx.gpuRequirements
	if gpuRequirements == nil {
		return nil, framework.NewStatus(framework.Unschedulable, ErrNoGPURequirements)
	}
	nodeHonorPartition := nodeDevice.nodeHonorGPUPartition
	gpuPartitionIndexer := nodeDevice.gpuPartitionIndexer
	if gpuPartitionIndexer == nil {
		gpuPartitionIndexer, nodeHonorPartition = GetDesignatedGPUPartitionIndexer(requestCtx.node)
	}
	honorGPUPartition := gpuRequirements.honorGPUPartition || nodeHonorPartition
	realUsed := getRealUsed(requestCtx.nodeDevice.deviceUsed[schedulingv1alpha1.GPU], nodeDevice.deviceTotal[schedulingv1alpha1.GPU], nodeDevice.deviceUsed[schedulingv1alpha1.GPU])
	allocateContext := &AllocateContext{
		deviceUsedMinorsHash: hashDevices(realUsed),
		deviceFree:           nodeDevice.deviceFree[schedulingv1alpha1.GPU],
		deviceTotal:          removeZeroDevice(nodeDevice.deviceTotal[schedulingv1alpha1.GPU]),
		allocationScorer:     requestCtx.allocationScorer,
	}
	allocations, status := allocateByPartition(honorGPUPartition, gpuRequirements, gpuPartitionIndexer, allocateContext)
	if !status.IsSuccess() {
		return nil, status
	}
	// if honorGPUPartition is false, allocateByPartition may return (nil, nil), so we should check if allocations is nil
	if len(allocations) != 0 {
		return allocations, nil
	}
	allocations, status = allocateByDeviceTopology(gpuRequirements, nodeDevice.gpuTopologyScope, allocateContext)
	if !status.IsSuccess() {
		return nil, status
	}
	// if same NUMANode or PCIE are not required, allocateByDeviceTopology may return (nil, nil), so we should check if allocations is nil
	if len(allocations) != 0 {
		return allocations, nil
	}
	return defaultAllocateDevices(nodeDevice, requestCtx, gpuRequirements.requestsPerGPU, desiredCount, maxDesiredCount, schedulingv1alpha1.GPU, nil)
}
func removeZeroDevice(originalResources deviceResources) deviceResources {
	refinedResources := deviceResources{}
	for minor, resource := range originalResources {
		if !quotav1.IsZero(resource) {
			refinedResources[minor] = resource
		}
	}
	return refinedResources
}

type GPUPartitionIndexer map[int][]*PartitionsOfAllocationScore

type PartitionsOfAllocationScore struct {
	Partitions      []*apiext.GPUPartition
	AllocationScore int
}

func allocateByPartition(honorGPUPartition bool, gpuRequirements *GPURequirements, gpuPartitionIndexer GPUPartitionIndexer, allocateContext *AllocateContext) (allocations []*apiext.DeviceAllocation, status *framework.Status) {
	defer func() {
		if !status.IsSuccess() {
			klog.V(5).Infof("gpuRequirements: %+v, gpuPartitionIndexer: %+v, status: %+v", *gpuRequirements, gpuPartitionIndexer, status)
		}
		if !honorGPUPartition {
			// if honorGPUPartition is false, allocateByPartition should return (allocation, nil)
			status = nil
		}
	}()
	if gpuRequirements.gpuShared {
		// TODO when allocate shared gpu, partition binPack logic is equivalent with topology binPack in most machine models. Bus there may be still some unexpected machine model need to be considered
		return nil, nil
	}
	if gpuPartitionIndexer == nil {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrNodeMissingGPUPartitionTable)
	}
	indexerOfAllocationScore, ok := gpuPartitionIndexer[gpuRequirements.numberOfGPUs]
	if !ok || indexerOfAllocationScore == nil {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrUnsupportedGPURequests)
	}

	// we have to calculate this hash during scheduling cycle because reservation restore and preemption may happen
	deviceTotalMinorsHash := hashDevices(allocateContext.deviceTotal)

	var feasiblePartitions []*apiext.GPUPartition
	for _, candidatePartitions := range indexerOfAllocationScore {
		for _, partition := range candidatePartitions.Partitions {
			if partition.MinorsHash&allocateContext.deviceUsedMinorsHash > 0 {
				continue
			}
			if deviceTotalMinorsHash&partition.MinorsHash != partition.MinorsHash {
				continue
			}
			if gpuRequirements.rindBusBandwidth != nil {
				if partition.RingBusBandwidth == nil {
					continue
				}
				if gpuRequirements.rindBusBandwidth.Cmp(*partition.RingBusBandwidth) > 0 {
					continue
				}
			}
			feasiblePartitions = append(feasiblePartitions, partition)
		}
		// we definitely prefer the partition with the higher allocation score
		if len(feasiblePartitions) > 0 || gpuRequirements.restrictedGPUPartition {
			break
		}
	}
	if len(feasiblePartitions) == 0 {
		return nil, framework.NewStatus(framework.Unschedulable, ErrInsufficientPartitionedDevice)
	}
	selectedPartition := selectPartitionByBinPack(allocateContext.deviceUsedMinorsHash, feasiblePartitions, gpuPartitionIndexer, gpuRequirements.numberOfGPUs)
	for _, minor := range selectedPartition.Minors {
		allocations = append(allocations, &apiext.DeviceAllocation{
			Minor:     int32(minor),
			Resources: gpuRequirements.requestsPerGPU,
		})
	}
	return allocations, nil
}

func hashDevices(resources deviceResources) int {
	var minors []int
	for minor := range resources {
		minors = append(minors, minor)
	}
	return hashMinors(minors)
}

func hashMinors(minors []int) int {
	hash := 0
	for _, minor := range minors {
		minorHash := 1 << minor
		hash = hash | minorHash
	}
	return hash
}

type partitionOfBinPackScore struct {
	Partition    *apiext.GPUPartition
	BinPackScore int
}

func selectPartitionByBinPack(deviceUsedMinorsHash int, feasiblePartitions []*apiext.GPUPartition, partitionIndexer GPUPartitionIndexer, desiredNumberOfGPU int) *apiext.GPUPartition {
	if len(feasiblePartitions) == 1 {
		return feasiblePartitions[0]
	}
	scoreOfNumOfGPUs := map[int]int{8: 10000, 4: 100, 2: 1}
	listOfNumberOfGPU := []int{8, 4, 2}
	var partitionsWithBinPackScore []*partitionOfBinPackScore
	for _, feasiblePartition := range feasiblePartitions {
		score := 0
		allocatedMinorsHash := deviceUsedMinorsHash | feasiblePartition.MinorsHash
		for _, numberOfGPUs := range listOfNumberOfGPU {
			if numberOfGPUs < desiredNumberOfGPU {
				continue
			}
			indexerOfGPUNumber, ok := partitionIndexer[numberOfGPUs]
			if !ok || len(indexerOfGPUNumber) == 0 {
				continue
			}
			for _, partition := range indexerOfGPUNumber[0].Partitions {
				if partition.MinorsHash&allocatedMinorsHash > 0 {
					continue
				}
				score += scoreOfNumOfGPUs[numberOfGPUs] * partition.AllocationScore
			}
		}
		partitionsWithBinPackScore = append(partitionsWithBinPackScore, &partitionOfBinPackScore{
			Partition:    feasiblePartition,
			BinPackScore: score,
		})
	}

	sort.Slice(partitionsWithBinPackScore, func(i, j int) bool {
		return partitionsWithBinPackScore[i].BinPackScore > partitionsWithBinPackScore[j].BinPackScore
	})
	return partitionsWithBinPackScore[0].Partition
}

type GPUTopologyScope struct {
	scopeName       apiext.DeviceTopologyScope
	scopeLevel      int
	minorsResources deviceResources
	minors          []int
	minorsHash      int
	childScopes     []*GPUTopologyScope

	// only used for sort the scope
	pcieID     string
	numaNodeID int32
	minor      int32
}

func allocateByDeviceTopology(gpuRequirements *GPURequirements, gpuTopologyScope *GPUTopologyScope, allocateContext *AllocateContext) (allocations []*apiext.DeviceAllocation, status *framework.Status) {
	defer func() {
		// if allocateByDeviceTopology is not required and unsupported in some cases, then we can return nil instead of framework.UnschedulableAndUnresolvable to give the change of success
		if gpuRequirements.requiredTopologyScope == "" && status.Code() == framework.UnschedulableAndUnresolvable {
			status = nil
		}
	}()
	if gpuTopologyScope == nil {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrNodeMissingGPUDeviceTopologyTree)
	}
	if gpuRequirements.gpuShared && gpuRequirements.numberOfGPUs > 1 {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrUnsupportedMultiSharedGPU)
	}
	allocateResultInfo := allocateFromScope(gpuRequirements, gpuTopologyScope, allocateContext, ScopeLevelContext{
		cumulativeNotEmpties: 0,
		depth:                0,
		contextOfDevices:     make(map[int]*DeviceLevelContext, len(gpuTopologyScope.minors)),
	})
	if allocateResultInfo == nil {
		if gpuRequirements.requiredTopologyScope != "" {
			return nil, framework.NewStatus(framework.Unschedulable, ErrInsufficientTopologyScopedGPUDevices)
		}
		return nil, framework.NewStatus(framework.Unschedulable, ErrInsufficientGPUDevices)
	}
	return allocateResultInfo.allocations, nil
}

type ScopeLevelContext struct {
	cumulativeNotEmpties int
	depth                int
	contextOfDevices     map[int]*DeviceLevelContext
}

type DeviceLevelContext struct {
	satisfied bool
	score     int64
}

type ScopeLevelAllocateResult struct {
	allocations          []*apiext.DeviceAllocation
	cumulativeNotEmpties int
	depth                int
	score                int64
}

func allocateFromScope(requirements *GPURequirements, scope *GPUTopologyScope, allocateContext *AllocateContext, scopeLevelContext ScopeLevelContext) *ScopeLevelAllocateResult {
	if len(scope.minors) < requirements.numberOfGPUs {
		return nil
	}
	scopeLevelContext.depth++
	allocatedMinorHashOfScope := scope.minorsHash & allocateContext.deviceUsedMinorsHash
	if allocatedMinorHashOfScope > 0 {
		scopeLevelContext.cumulativeNotEmpties++
	}
	var bestAllocateResult *ScopeLevelAllocateResult
	for _, childScope := range scope.childScopes {
		if len(childScope.minors) < requirements.numberOfGPUs {
			continue
		}
		allocateResultInfo := allocateFromScope(requirements, childScope, allocateContext, scopeLevelContext)
		if allocateResultInfo != nil && len(allocateResultInfo.allocations) > 0 {
			if bestAllocateResult == nil {
				bestAllocateResult = allocateResultInfo
				continue
			}
			if bestAllocateResult.depth < allocateResultInfo.depth ||
				(bestAllocateResult.depth == allocateResultInfo.depth && bestAllocateResult.cumulativeNotEmpties < allocateResultInfo.cumulativeNotEmpties) {
				bestAllocateResult = allocateResultInfo
			}
			if requirements.gpuShared &&
				bestAllocateResult.depth == allocateResultInfo.depth &&
				bestAllocateResult.cumulativeNotEmpties == allocateResultInfo.cumulativeNotEmpties &&
				bestAllocateResult.score < allocateResultInfo.score {
				bestAllocateResult = allocateResultInfo
			}
		}
	}
	if bestAllocateResult != nil && len(bestAllocateResult.allocations) > 0 {
		return bestAllocateResult
	}

	if requirements.requiredTopologyScopeLevel > scope.scopeLevel {
		return nil
	}

	var candidateMinors []int
	bestMinorWhenShared := -1
	bestScoreWhenShared := int64(-1)
	var satisfied bool
	for _, minor := range scope.minors {
		totalResources := scope.minorsResources[minor]
		freeResources := allocateContext.deviceFree[minor]
		contextOfDevice, ok := scopeLevelContext.contextOfDevices[minor]
		if !ok {
			contextOfDevice = &DeviceLevelContext{}
			scopeLevelContext.contextOfDevices[minor] = contextOfDevice
			contextOfDevice.satisfied, _ = quotav1.LessThanOrEqual(requirements.requestsPerGPU, freeResources)
			_, belongToTotal := allocateContext.deviceTotal[minor]
			contextOfDevice.satisfied = contextOfDevice.satisfied && belongToTotal
			if contextOfDevice.satisfied && requirements.gpuShared && allocateContext.allocationScorer != nil {
				contextOfDevice.score = allocateContext.allocationScorer.scoreDevice(requirements.requestsPerGPU, freeResources, totalResources)
			}
		}
		if !contextOfDevice.satisfied {
			continue
		}
		if !requirements.gpuShared {
			candidateMinors = append(candidateMinors, minor)
			if len(candidateMinors) == requirements.numberOfGPUs {
				satisfied = true
				break
			}
			continue
		}
		satisfied = true
		if contextOfDevice.score > bestScoreWhenShared {
			bestMinorWhenShared = minor
		}
	}
	if !satisfied {
		return nil
	}
	allocateResult := &ScopeLevelAllocateResult{
		cumulativeNotEmpties: scopeLevelContext.cumulativeNotEmpties,
		depth:                scopeLevelContext.depth,
		score:                bestScoreWhenShared,
	}
	if requirements.gpuShared {
		candidateMinors = append(candidateMinors, bestMinorWhenShared)
	}
	for _, minor := range candidateMinors {
		allocateResult.allocations = append(allocateResult.allocations, &apiext.DeviceAllocation{
			Minor:     int32(minor),
			Resources: requirements.requestsPerGPU,
		})
	}
	return allocateResult

}

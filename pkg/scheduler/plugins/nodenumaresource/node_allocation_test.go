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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestNodeAllocationAddCPUs(t *testing.T) {
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	for _, v := range cpuTopology.CPUDetails {
		v.CoreID = v.SocketID<<16 | v.CoreID
		cpuTopology.CPUDetails[v.CPUID] = v
	}

	allocationState := NewNodeAllocation("test-node-1")
	assert.NotNil(t, allocationState)
	podUID := uuid.NewUUID()
	allocationState.addCPUs(cpuTopology, podUID, cpuset.MustParse("1-4"), schedulingconfig.CPUExclusivePolicyPCPULevel)

	cpus := cpuset.MustParse("1-4")
	expectAllocatedPods := map[types.UID]PodAllocation{
		podUID: {
			UID:                podUID,
			CPUSet:             cpus,
			CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyPCPULevel,
		},
	}
	expectAllocatedCPUs := CPUDetails{}
	for _, cpuID := range cpus.ToSliceNoSort() {
		cpuInfo := cpuTopology.CPUDetails[cpuID]
		cpuInfo.ExclusivePolicy = schedulingconfig.CPUExclusivePolicyPCPULevel
		cpuInfo.RefCount++
		expectAllocatedCPUs[cpuID] = cpuInfo
	}

	assert.Equal(t, expectAllocatedPods, allocationState.allocatedPods)
	assert.Equal(t, expectAllocatedCPUs, allocationState.allocatedCPUs)

	availableCPUs, _ := allocationState.getAvailableCPUs(cpuTopology, 2, cpuset.NewCPUSet(), cpuset.NewCPUSet())
	expectAvailableCPUs := cpuset.MustParse("0-15")
	assert.Equal(t, expectAvailableCPUs, availableCPUs)

	// test with add already allocated Pod
	allocationState.addCPUs(cpuTopology, podUID, cpuset.MustParse("1-4"), schedulingconfig.CPUExclusivePolicyPCPULevel)
	assert.Equal(t, expectAllocatedPods, allocationState.allocatedPods)
	assert.Equal(t, expectAllocatedCPUs, allocationState.allocatedCPUs)

	availableCPUs, _ = allocationState.getAvailableCPUs(cpuTopology, 2, cpuset.NewCPUSet(), cpuset.NewCPUSet())
	cpuset.MustParse("0-15")
	assert.Equal(t, expectAvailableCPUs, availableCPUs)

	// test with add already allocated cpu(refCount > 1 but less than maxRefCount) and another pod
	anotherPodUID := uuid.NewUUID()
	allocationState.addCPUs(cpuTopology, anotherPodUID, cpuset.MustParse("2-5"), schedulingconfig.CPUExclusivePolicyPCPULevel)
	anotherCPUSet := cpuset.MustParse("2-5")
	expectAllocatedPods[anotherPodUID] = PodAllocation{
		UID:                anotherPodUID,
		CPUSet:             anotherCPUSet,
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyPCPULevel,
	}
	for _, cpuID := range anotherCPUSet.ToSliceNoSort() {
		cpuInfo, ok := expectAllocatedCPUs[cpuID]
		if !ok {
			cpuInfo = cpuTopology.CPUDetails[cpuID]
		}
		cpuInfo.ExclusivePolicy = schedulingconfig.CPUExclusivePolicyPCPULevel
		cpuInfo.RefCount++
		expectAllocatedCPUs[cpuID] = cpuInfo
	}
	assert.Equal(t, expectAllocatedPods, allocationState.allocatedPods)
	assert.Equal(t, expectAllocatedCPUs, allocationState.allocatedCPUs)
}

func TestNodeAllocationStateReleaseCPUs(t *testing.T) {
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	for _, v := range cpuTopology.CPUDetails {
		v.CoreID = v.SocketID<<16 | v.CoreID
		cpuTopology.CPUDetails[v.CPUID] = v
	}

	allocationState := NewNodeAllocation("test-node-1")
	assert.NotNil(t, allocationState)
	podUID := uuid.NewUUID()
	allocationState.addCPUs(cpuTopology, podUID, cpuset.MustParse("1-4"), schedulingconfig.CPUExclusivePolicyPCPULevel)

	allocationState.release(podUID)

	expectAllocatedPods := map[types.UID]PodAllocation{}
	expectAllocatedCPUs := CPUDetails{}
	assert.Equal(t, expectAllocatedPods, allocationState.allocatedPods)
	assert.Equal(t, expectAllocatedCPUs, allocationState.allocatedCPUs)
	for i := 0; i < 16; i++ {
		assert.Equal(t, 0, allocationState.allocatedCPUs[i].RefCount)
	}
}

func Test_cpuAllocation_getAvailableCPUs(t *testing.T) {
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	for _, v := range cpuTopology.CPUDetails {
		v.CoreID = v.SocketID<<16 | v.CoreID
		cpuTopology.CPUDetails[v.CPUID] = v
	}

	allocationState := NewNodeAllocation("test-node-1")
	assert.NotNil(t, allocationState)
	podUID := uuid.NewUUID()
	allocationState.addCPUs(cpuTopology, podUID, cpuset.MustParse("1-4"), schedulingconfig.CPUExclusivePolicyPCPULevel)

	availableCPUs, _ := allocationState.getAvailableCPUs(cpuTopology, 2, cpuset.NewCPUSet(), cpuset.NewCPUSet())
	expectAvailableCPUs := cpuset.MustParse("0-15")
	assert.Equal(t, expectAvailableCPUs, availableCPUs)

	// test with add already allocated cpu(refCount > 1 but less than maxRefCount) and another pod
	anotherPodUID := uuid.NewUUID()
	allocationState.addCPUs(cpuTopology, anotherPodUID, cpuset.MustParse("2-5"), schedulingconfig.CPUExclusivePolicyPCPULevel)
	availableCPUs, _ = allocationState.getAvailableCPUs(cpuTopology, 2, cpuset.NewCPUSet(), cpuset.NewCPUSet())
	expectAvailableCPUs = cpuset.MustParse("0-1,5-15")
	assert.Equal(t, expectAvailableCPUs, availableCPUs)

	allocationState.release(podUID)
	availableCPUs, _ = allocationState.getAvailableCPUs(cpuTopology, 1, cpuset.NewCPUSet(), cpuset.NewCPUSet())
	expectAvailableCPUs = cpuset.MustParse("0-1,6-15")
	assert.Equal(t, expectAvailableCPUs, availableCPUs)
}

func Test_cpuAllocation_getAvailableCPUs_with_preferred_cpus(t *testing.T) {
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	for _, v := range cpuTopology.CPUDetails {
		v.CoreID = v.SocketID<<16 | v.CoreID
		cpuTopology.CPUDetails[v.CPUID] = v
	}

	allocationState := NewNodeAllocation("test-node-1")
	assert.NotNil(t, allocationState)
	podUID := uuid.NewUUID()
	allocationState.addCPUs(cpuTopology, podUID, cpuset.MustParse("0-4"), schedulingconfig.CPUExclusivePolicyPCPULevel)
	availableCPUs, _ := allocationState.getAvailableCPUs(cpuTopology, 1, cpuset.NewCPUSet(), cpuset.NewCPUSet())
	expectAvailableCPUs := cpuset.MustParse("5-15")
	assert.Equal(t, expectAvailableCPUs, availableCPUs)

	availableCPUs, _ = allocationState.getAvailableCPUs(cpuTopology, 1, cpuset.NewCPUSet(), cpuset.NewCPUSet(1, 2))
	expectAvailableCPUs = cpuset.MustParse("1-2,5-15")
	assert.Equal(t, expectAvailableCPUs, availableCPUs)
}

func Test_getAvailableNUMANodeResources(t *testing.T) {
	resources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("16"),
		corev1.ResourceMemory: resource.MustParse("32Gi"),
	}
	amplifiedResources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("24"),
		corev1.ResourceMemory: resource.MustParse("32Gi"),
	}

	tests := []struct {
		name             string
		allocation       *NodeAllocation
		allocatedCPUSets int
		topologyOptions  TopologyOptions
		reusable         map[int]corev1.ResourceList
		wantAvailable    map[int]corev1.ResourceList
		wantAllocated    map[int]corev1.ResourceList
	}{
		{
			name:       "normal node",
			allocation: &NodeAllocation{},
			topologyOptions: TopologyOptions{
				CPUTopology: buildCPUTopologyForTest(2, 1, 8, 2),
				MaxRefCount: 1,
				NUMANodeResources: []NUMANodeResource{
					{Node: 0, Resources: resources.DeepCopy()},
					{Node: 1, Resources: resources.DeepCopy()},
				},
			},
			wantAvailable: map[int]corev1.ResourceList{
				0: resources.DeepCopy(),
				1: resources.DeepCopy(),
			},
			wantAllocated: map[int]corev1.ResourceList{},
		},
		{
			name:       "normal node with amplification ratios",
			allocation: &NodeAllocation{},
			topologyOptions: TopologyOptions{
				CPUTopology: buildCPUTopologyForTest(2, 1, 8, 2),
				MaxRefCount: 1,
				NUMANodeResources: []NUMANodeResource{
					{Node: 0, Resources: amplifiedResources.DeepCopy()},
					{Node: 1, Resources: amplifiedResources.DeepCopy()},
				},
				AmplificationRatios: map[corev1.ResourceName]extension.Ratio{
					corev1.ResourceCPU: 1.5,
				},
			},
			wantAvailable: map[int]corev1.ResourceList{
				0: amplifiedResources.DeepCopy(),
				1: amplifiedResources.DeepCopy(),
			},
			wantAllocated: map[int]corev1.ResourceList{},
		},
		{
			name: "normal node with amplification ratios and allocated CPUSets",
			allocation: &NodeAllocation{
				allocatedResources: map[int]*NUMANodeResource{
					0: {Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}},
				},
			},
			allocatedCPUSets: 4,
			topologyOptions: TopologyOptions{
				CPUTopology: buildCPUTopologyForTest(2, 1, 8, 2),
				MaxRefCount: 1,
				NUMANodeResources: []NUMANodeResource{
					{Node: 0, Resources: amplifiedResources.DeepCopy()},
					{Node: 1, Resources: amplifiedResources.DeepCopy()},
				},
				AmplificationRatios: map[corev1.ResourceName]extension.Ratio{
					corev1.ResourceCPU: 1.5,
				},
			},
			wantAvailable: map[int]corev1.ResourceList{
				0: {
					corev1.ResourceCPU:    *resource.NewMilliQuantity(18000, resource.DecimalSI),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
				1: amplifiedResources.DeepCopy(),
			},
			wantAllocated: map[int]corev1.ResourceList{
				0: {
					corev1.ResourceCPU: *resource.NewMilliQuantity(6000, resource.DecimalSI),
				},
			},
		},
		{
			name: "normal node with amplification ratios and allocated CPUSets and CPU Shares",
			allocation: &NodeAllocation{
				allocatedResources: map[int]*NUMANodeResource{
					0: {Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}},
				},
			},
			allocatedCPUSets: 4,
			topologyOptions: TopologyOptions{
				CPUTopology: buildCPUTopologyForTest(2, 1, 8, 2),
				MaxRefCount: 1,
				NUMANodeResources: []NUMANodeResource{
					{Node: 0, Resources: amplifiedResources.DeepCopy()},
					{Node: 1, Resources: amplifiedResources.DeepCopy()},
				},
				AmplificationRatios: map[corev1.ResourceName]extension.Ratio{
					corev1.ResourceCPU: 1.5,
				},
			},
			wantAvailable: map[int]corev1.ResourceList{
				0: {
					corev1.ResourceCPU:    *resource.NewMilliQuantity(14000, resource.DecimalSI),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
				1: amplifiedResources.DeepCopy(),
			},
			wantAllocated: map[int]corev1.ResourceList{
				0: {
					corev1.ResourceCPU: *resource.NewMilliQuantity(10000, resource.DecimalSI),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.allocation.allocatedCPUs = CPUDetails{}
			for i := 0; i < tt.allocatedCPUSets; i++ {
				tt.allocation.allocatedCPUs[i] = tt.topologyOptions.CPUTopology.CPUDetails[i]
			}
			available, allocated := tt.allocation.getAvailableNUMANodeResources(tt.topologyOptions, tt.reusable)
			assert.Equal(t, tt.wantAvailable, available)
			assert.Equal(t, tt.wantAllocated, allocated)
		})
	}
}

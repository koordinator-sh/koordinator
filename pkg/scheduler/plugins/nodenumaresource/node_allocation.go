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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

type NodeAllocation struct {
	lock               sync.RWMutex
	nodeName           string
	allocatedPods      map[types.UID]PodAllocation
	allocatedCPUs      CPUDetails
	allocatedResources map[int]*NUMANodeResource
	sharedNode         map[int]sets.String
	singleNUMANode     map[int]sets.String
}

type PodAllocation struct {
	UID                types.UID                           `json:"uid,omitempty"`
	Namespace          string                              `json:"namespace,omitempty"`
	Name               string                              `json:"name,omitempty"`
	CPUSet             cpuset.CPUSet                       `json:"cpuset,omitempty"`
	CPUExclusivePolicy schedulingconfig.CPUExclusivePolicy `json:"cpuExclusivePolicy,omitempty"`
	NUMANodeResources  []NUMANodeResource                  `json:"numaNodeResources,omitempty"`
}

func (n *NodeAllocation) GetAllNUMANodeStatus(numaNodes int) []extension.NumaNodeStatus {
	status := make([]extension.NumaNodeStatus, 0, numaNodes)
	for i := 0; i < numaNodes; i++ {
		status = append(status, n.NUMANodeSharedStatus(i))
	}
	return status
}

func (n *NodeAllocation) NUMANodeSharedStatus(nodeid int) extension.NumaNodeStatus {
	if len(n.singleNUMANode[nodeid]) == 0 && len(n.sharedNode[nodeid]) == 0 {
		return extension.NumaNodeStatusIdle
	}
	if len(n.singleNUMANode[nodeid]) > 0 && len(n.sharedNode[nodeid]) == 0 {
		return extension.NumaNodeStatusSingle
	}
	return extension.NumaNodeStatusShared
}

func NewNodeAllocation(nodeName string) *NodeAllocation {
	return &NodeAllocation{
		nodeName:           nodeName,
		allocatedPods:      map[types.UID]PodAllocation{},
		allocatedCPUs:      NewCPUDetails(),
		allocatedResources: map[int]*NUMANodeResource{},
		sharedNode:         map[int]sets.String{},
		singleNUMANode:     map[int]sets.String{},
	}
}

func (n *NodeAllocation) update(allocation *PodAllocation, cpuTopology *CPUTopology) {
	n.release(allocation.UID)
	n.addPodAllocation(allocation, cpuTopology)
}

func (n *NodeAllocation) getCPUs(podUID types.UID) (cpuset.CPUSet, bool) {
	request, ok := n.allocatedPods[podUID]
	return request.CPUSet, ok
}

func (n *NodeAllocation) getNUMAResource(podUID types.UID) (map[int]corev1.ResourceList, bool) {
	request, ok := n.allocatedPods[podUID]
	if !ok {
		return nil, false
	}
	numaResource := map[int]corev1.ResourceList{}
	for _, numaNodeResource := range request.NUMANodeResources {
		numaResource[numaNodeResource.Node] = numaNodeResource.Resources
	}
	return numaResource, ok
}

func (n *NodeAllocation) addCPUs(cpuTopology *CPUTopology, podUID types.UID, cpuset cpuset.CPUSet, exclusivePolicy schedulingconfig.CPUExclusivePolicy) {
	n.addPodAllocation(&PodAllocation{
		UID:                podUID,
		CPUSet:             cpuset,
		CPUExclusivePolicy: exclusivePolicy,
	}, cpuTopology)
}

func (n *NodeAllocation) addPodAllocation(request *PodAllocation, cpuTopology *CPUTopology) {
	if _, ok := n.allocatedPods[request.UID]; ok {
		return
	}
	n.allocatedPods[request.UID] = *request
	usedNUMA := sets.NewInt()

	for _, cpuID := range request.CPUSet.ToSliceNoSort() {
		cpuInfo, ok := n.allocatedCPUs[cpuID]
		if !ok {
			cpuInfo = cpuTopology.CPUDetails[cpuID]
		}
		cpuInfo.ExclusivePolicy = request.CPUExclusivePolicy
		cpuInfo.RefCount++
		n.allocatedCPUs[cpuID] = cpuInfo
		usedNUMA.Insert(cpuInfo.NodeID)
	}
	if len(usedNUMA) > 1 {
		for ni := range usedNUMA {
			if ps := n.sharedNode[ni]; ps == nil {
				n.sharedNode[ni] = sets.NewString(string(request.UID))
			} else {
				n.sharedNode[ni].Insert(string(request.UID))
			}
		}
	} else if len(usedNUMA) == 1 {
		ni := usedNUMA.UnsortedList()[0]
		if ps := n.singleNUMANode[ni]; ps == nil {
			n.singleNUMANode[ni] = sets.NewString(string(request.UID))
		} else {
			n.singleNUMANode[ni].Insert(string(request.UID))
		}
	}

	for nodeID, numaNodeRes := range request.NUMANodeResources {
		res := n.allocatedResources[numaNodeRes.Node]
		if res == nil {
			res = &NUMANodeResource{
				Node:      nodeID,
				Resources: make(corev1.ResourceList),
			}
			n.allocatedResources[numaNodeRes.Node] = res
		}
		res.Resources = quotav1.Add(res.Resources, numaNodeRes.Resources)
	}
}

func (n *NodeAllocation) release(podUID types.UID) {
	request, ok := n.allocatedPods[podUID]
	if !ok {
		return
	}
	delete(n.allocatedPods, podUID)

	usedNUMA := sets.NewInt()
	for _, cpuID := range request.CPUSet.ToSliceNoSort() {
		cpuInfo, ok := n.allocatedCPUs[cpuID]
		if !ok {
			continue
		}
		cpuInfo.RefCount--
		if cpuInfo.RefCount == 0 {
			delete(n.allocatedCPUs, cpuID)
		} else {
			n.allocatedCPUs[cpuID] = cpuInfo
		}
		usedNUMA.Insert(cpuInfo.NodeID)
	}
	for ni := range usedNUMA {
		delete(n.sharedNode[ni], string(podUID))
		delete(n.singleNUMANode[ni], string(podUID))
	}

	for _, numaNodeRes := range request.NUMANodeResources {
		res := n.allocatedResources[numaNodeRes.Node]
		if res != nil {
			res.Resources = quotav1.SubtractWithNonNegativeResult(res.Resources, numaNodeRes.Resources)
		}
	}
}

func (n *NodeAllocation) getAvailableCPUs(cpuTopology *CPUTopology, maxRefCount int, reservedCPUs cpuset.CPUSet, preferredCPUs ...cpuset.CPUSet) (availableCPUs cpuset.CPUSet, allocateInfo CPUDetails) {
	allocateInfo = n.allocatedCPUs.Clone()
	// NOTE: preferredCPUs is a slice since we may restore a cpu multiple times when it is referenced more than once.
	// e.g. For a pod A tries to preempt a pod B in the reservation R, the CPU C is allocated twice by the B and R.
	// So the RefCount of CPU C is 2, and the pod A should allocate it by returning the C twice, where the one is
	// by the reservation restoring and the another is by the preemption restoring.
	for _, preferred := range preferredCPUs {
		if preferred.IsEmpty() {
			continue
		}
		for _, cpuID := range preferred.ToSliceNoSort() {
			cpuInfo, ok := allocateInfo[cpuID]
			if ok {
				cpuInfo.RefCount--
				if cpuInfo.RefCount == 0 {
					delete(allocateInfo, cpuID)
				} else {
					allocateInfo[cpuID] = cpuInfo
				}
			}
		}
	}
	allocated := allocateInfo.CPUs().Filter(func(cpuID int) bool {
		return allocateInfo[cpuID].RefCount >= maxRefCount
	})
	availableCPUs = cpuTopology.CPUDetails.CPUs().Difference(allocated).Difference(reservedCPUs)
	return
}

func (n *NodeAllocation) getAvailableNUMANodeResources(topologyOptions TopologyOptions, reusableResources map[int]corev1.ResourceList) (totalAvailable, totalAllocated map[int]corev1.ResourceList) {
	totalAvailable = make(map[int]corev1.ResourceList)
	totalAllocated = make(map[int]corev1.ResourceList)
	cpuAmplificationRatio := topologyOptions.AmplificationRatios[corev1.ResourceCPU]
	for _, numaNodeRes := range topologyOptions.NUMANodeResources {
		var allocatedRes corev1.ResourceList
		allocated := n.allocatedResources[numaNodeRes.Node]
		if allocated != nil {
			allocatedRes = allocated.Resources
			if cpuAmplificationRatio > 1 {
				allocatedCPUSets := int64(n.allocatedCPUs.CPUsInNUMANodes(numaNodeRes.Node).Size() * 1000)
				amplifiedCPUs := extension.Amplify(allocatedCPUSets, cpuAmplificationRatio)
				quantity := allocatedRes[corev1.ResourceCPU]
				allocatedRes = allocatedRes.DeepCopy()
				allocatedRes[corev1.ResourceCPU] = *resource.NewMilliQuantity(quantity.MilliValue()-allocatedCPUSets+amplifiedCPUs, resource.DecimalSI)
			}
			allocatedRes = quotav1.SubtractWithNonNegativeResult(allocatedRes, reusableResources[numaNodeRes.Node])
			totalAllocated[numaNodeRes.Node] = allocatedRes
		}
		totalAvailable[numaNodeRes.Node] = quotav1.SubtractWithNonNegativeResult(numaNodeRes.Resources, allocatedRes)
	}
	return totalAvailable, totalAllocated
}

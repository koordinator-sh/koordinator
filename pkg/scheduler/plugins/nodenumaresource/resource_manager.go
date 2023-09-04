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
	"errors"
	"fmt"
	"math"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

type ResourceManager interface {
	GetTopologyHints(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) (map[string][]topologymanager.NUMATopologyHint, error)
	Allocate(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) (*PodAllocation, error)
	Score(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) int64

	Update(nodeName string, allocation *PodAllocation)
	Release(nodeName string, podUID types.UID)

	GetNodeAllocation(nodeName string) *NodeAllocation
	GetAllocatedCPUSet(nodeName string, podUID types.UID) (cpuset.CPUSet, bool)
	GetAvailableCPUs(nodeName string, preferredCPUs cpuset.CPUSet) (availableCPUs cpuset.CPUSet, allocated CPUDetails, err error)
}

type ResourceOptions struct {
	numCPUsNeeded      int
	requestCPUBind     bool
	requests           corev1.ResourceList
	cpuBindPolicy      schedulingconfig.CPUBindPolicy
	cpuExclusivePolicy schedulingconfig.CPUExclusivePolicy
	preferredCPUs      cpuset.CPUSet
	hint               topologymanager.NUMATopologyHint
}

type resourceManager struct {
	numaAllocateStrategy schedulingconfig.NUMAAllocateStrategy
	topologyManager      TopologyOptionsManager
	lock                 sync.Mutex
	allocationStates     map[string]*NodeAllocation
}

func NewResourceManager(
	handle framework.Handle,
	defaultNUMAAllocateStrategy schedulingconfig.NUMAAllocateStrategy,
	topologyManager TopologyOptionsManager,
) ResourceManager {
	manager := &resourceManager{
		numaAllocateStrategy: defaultNUMAAllocateStrategy,
		topologyManager:      topologyManager,
		allocationStates:     map[string]*NodeAllocation{},
	}
	handle.SharedInformerFactory().Core().V1().Nodes().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{DeleteFunc: manager.onNodeDelete})
	return manager
}

func (c *resourceManager) onNodeDelete(obj interface{}) {
	var node *corev1.Node
	switch t := obj.(type) {
	case *corev1.Node:
		node = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		node, ok = t.Obj.(*corev1.Node)
		if !ok {
			return
		}
	default:
		break
	}

	if node == nil {
		return
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.allocationStates, node.Name)
}

func (c *resourceManager) getOrCreateNodeAllocation(nodeName string) *NodeAllocation {
	c.lock.Lock()
	defer c.lock.Unlock()
	v := c.allocationStates[nodeName]
	if v == nil {
		v = NewNodeAllocation(nodeName)
		c.allocationStates[nodeName] = v
	}
	return v
}

func (c *resourceManager) GetTopologyHints(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) (map[string][]topologymanager.NUMATopologyHint, error) {
	topologyOptions := c.topologyManager.GetTopologyOptions(node.Name)
	if len(topologyOptions.NUMANodeResources) == 0 {
		return nil, fmt.Errorf("insufficient resources on NUMA Node")
	}

	totalAvailable, _, err := c.getAvailableNUMANodeResources(node.Name)
	if err != nil {
		return nil, err
	}

	hints := make(map[string][]topologymanager.NUMATopologyHint)
	podRequests := options.requests.DeepCopy()
	if options.requestCPUBind {
		if topologyOptions.CPUTopology == nil || !topologyOptions.CPUTopology.IsValid() {
			return nil, errors.New(ErrInvalidCPUTopology)
		}
		availableCPUs, _, err := c.GetAvailableCPUs(node.Name, options.preferredCPUs)
		if err != nil {
			return nil, err
		}
		result := generateCPUSetHints(topologyOptions.CPUTopology, availableCPUs, options.preferredCPUs, options.numCPUsNeeded)
		hints[string(corev1.ResourceCPU)] = result
		delete(podRequests, corev1.ResourceCPU)
	}
	nodes := make([]int, 0, len(topologyOptions.NUMANodeResources))
	for _, v := range topologyOptions.NUMANodeResources {
		nodes = append(nodes, v.Node)
	}
	result := generateResourceHints(nodes, podRequests, totalAvailable)
	for k, v := range result {
		hints[k] = v
	}
	return hints, nil
}

func (c *resourceManager) Allocate(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) (*PodAllocation, error) {
	allocation := &PodAllocation{
		UID:                pod.UID,
		Namespace:          pod.Namespace,
		Name:               pod.Name,
		CPUExclusivePolicy: options.cpuExclusivePolicy,
	}
	if options.hint.NUMANodeAffinity != nil {
		resources, err := c.allocateResourcesByHint(node, pod, options)
		if err != nil {
			return nil, err
		}
		allocation.NUMANodeResources = resources
	}
	if options.requestCPUBind {
		cpus, err := c.allocateCPUSet(node, pod, options)
		if err != nil {
			return nil, err
		}
		allocation.CPUSet = cpus
	}
	return allocation, nil
}

func (c *resourceManager) allocateResourcesByHint(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) ([]NUMANodeResource, error) {
	topologyOptions := c.topologyManager.GetTopologyOptions(node.Name)
	if len(topologyOptions.NUMANodeResources) == 0 {
		return nil, fmt.Errorf("insufficient resources on NUMA Node")
	}

	totalAvailable, _, err := c.getAvailableNUMANodeResources(node.Name)
	if err != nil {
		return nil, err
	}

	requests := options.requests.DeepCopy()
	intersectionResources := sets.NewString()
	var result []NUMANodeResource
	for _, numaNodeID := range options.hint.NUMANodeAffinity.GetBits() {
		allocatable := totalAvailable[numaNodeID]
		r := NUMANodeResource{
			Node:      numaNodeID,
			Resources: corev1.ResourceList{},
		}
		for resourceName, quantity := range requests {
			if allocatableQuantity, ok := allocatable[resourceName]; ok {
				intersectionResources.Insert(string(resourceName))
				var allocated resource.Quantity
				allocatable[resourceName], requests[resourceName], allocated = allocateRes(allocatableQuantity, quantity)
				if !allocated.IsZero() {
					r.Resources[resourceName] = allocated
				}
			}
		}
		if !quotav1.IsZero(r.Resources) {
			result = append(result, r)
		}
		if quotav1.IsZero(requests) {
			break
		}
	}

	var insufficientResources []string
	for resourceName, quantity := range requests {
		if intersectionResources.Has(string(resourceName)) {
			if !quantity.IsZero() {
				insufficientResources = append(insufficientResources, string(resourceName))
			}
		}
	}
	if len(insufficientResources) > 0 {
		return nil, fmt.Errorf("insufficient resources: %v", insufficientResources)
	}
	return result, nil
}

func allocateRes(available, request resource.Quantity) (resource.Quantity, resource.Quantity, resource.Quantity) {
	switch available.Cmp(request) {
	case 1:
		available = available.DeepCopy()
		available.Sub(request)
		allocated := request.DeepCopy()
		request.Sub(request)
		return available, request, allocated
	case -1:
		request = request.DeepCopy()
		request.Sub(available)
		allocated := available.DeepCopy()
		available.Sub(available)
		return available, request, allocated
	default:
		request = request.DeepCopy()
		request.Sub(request)
		return request, request, available.DeepCopy()
	}
}

func (c *resourceManager) allocateCPUSet(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) (cpuset.CPUSet, error) {
	empty := cpuset.CPUSet{}
	// The Pod requires the CPU to be allocated according to CPUBindPolicy,
	// but the current node does not have a NodeResourceTopology or a valid CPUTopology,
	// so this error should be exposed to the user
	topologyOptions := c.topologyManager.GetTopologyOptions(node.Name)
	if topologyOptions.CPUTopology == nil {
		return empty, errors.New(ErrNotFoundCPUTopology)
	}
	if !topologyOptions.CPUTopology.IsValid() {
		return empty, errors.New(ErrInvalidCPUTopology)
	}

	availableCPUs, allocatedCPUs, err := c.GetAvailableCPUs(node.Name, options.preferredCPUs)
	if err != nil {
		return empty, err
	}

	result := cpuset.CPUSet{}
	numaAllocateStrategy := c.getNUMAAllocateStrategy(node)
	numCPUsNeeded := options.numCPUsNeeded
	if options.hint.NUMANodeAffinity != nil {
		alignedCPUs := cpuset.NewCPUSet()
		for _, numaNodeID := range options.hint.NUMANodeAffinity.GetBits() {
			cpusInNUMANode := topologyOptions.CPUTopology.CPUDetails.CPUsInNUMANodes(numaNodeID)
			alignedCPUs = alignedCPUs.Union(availableCPUs.Intersection(cpusInNUMANode))
		}

		numCPUs := alignedCPUs.Size()
		if numCPUsNeeded < numCPUs {
			numCPUs = numCPUsNeeded
		}

		alignedCPUs, err = takePreferredCPUs(
			topologyOptions.CPUTopology,
			topologyOptions.MaxRefCount,
			alignedCPUs,
			options.preferredCPUs,
			allocatedCPUs,
			numCPUs,
			options.cpuBindPolicy,
			options.cpuExclusivePolicy,
			numaAllocateStrategy,
		)
		if err != nil {
			return empty, err
		}

		result = result.Union(alignedCPUs)
		numCPUsNeeded -= result.Size()
		if numCPUsNeeded > 0 {
			return empty, fmt.Errorf("not enough cpus available to satisfy request")
		}
	}

	if numCPUsNeeded > 0 {
		availableCPUs = availableCPUs.Difference(result)
		remainingCPUs, err := takePreferredCPUs(
			topologyOptions.CPUTopology,
			topologyOptions.MaxRefCount,
			availableCPUs,
			options.preferredCPUs,
			allocatedCPUs,
			numCPUsNeeded,
			options.cpuBindPolicy,
			options.cpuExclusivePolicy,
			numaAllocateStrategy,
		)
		if err != nil {
			return empty, err
		}
		result = result.Union(remainingCPUs)
	}
	return result, err
}

func (c *resourceManager) Score(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) int64 {
	topologyOptions := c.topologyManager.GetTopologyOptions(node.Name)
	if topologyOptions.CPUTopology == nil || !topologyOptions.CPUTopology.IsValid() {
		return 0
	}

	numaAllocateStrategy := c.getNUMAAllocateStrategy(node)
	reservedCPUs := topologyOptions.ReservedCPUs

	allocation := c.getOrCreateNodeAllocation(node.Name)
	allocation.lock.RLock()
	defer allocation.lock.RUnlock()

	// TODO(joseph): should support score with preferredCPUs.

	var (
		cpuTopology        = topologyOptions.CPUTopology
		numCPUsNeeded      = options.numCPUsNeeded
		cpuBindPolicy      = options.cpuBindPolicy
		cpuExclusivePolicy = options.cpuExclusivePolicy
		preferredCPUs      = options.preferredCPUs
	)
	availableCPUs, allocated := allocation.getAvailableCPUs(cpuTopology, topologyOptions.MaxRefCount, reservedCPUs, preferredCPUs)
	acc := newCPUAccumulator(
		cpuTopology,
		topologyOptions.MaxRefCount,
		availableCPUs,
		allocated,
		numCPUsNeeded,
		cpuExclusivePolicy,
		numaAllocateStrategy,
	)

	var freeCPUs [][]int
	if cpuBindPolicy == schedulingconfig.CPUBindPolicyFullPCPUs {
		if numCPUsNeeded <= cpuTopology.CPUsPerNode() {
			freeCPUs = acc.freeCoresInNode(true, true)
		} else if numCPUsNeeded <= cpuTopology.CPUsPerSocket() {
			freeCPUs = acc.freeCoresInSocket(true)
		}
	} else {
		if numCPUsNeeded <= cpuTopology.CPUsPerNode() {
			freeCPUs = acc.freeCPUsInNode(true)
		} else if numCPUsNeeded <= cpuTopology.CPUsPerSocket() {
			freeCPUs = acc.freeCPUsInSocket(true)
		}
	}

	scoreFn := mostRequestedScore
	if numaAllocateStrategy == schedulingconfig.NUMALeastAllocated {
		scoreFn = leastRequestedScore
	}

	var maxScore int64
	for _, cpus := range freeCPUs {
		if len(cpus) < numCPUsNeeded {
			continue
		}

		numaScore := scoreFn(int64(numCPUsNeeded), int64(len(cpus)))
		if numaScore > maxScore {
			maxScore = numaScore
		}
	}

	// If the requested CPUs can be aligned according to NUMA Socket, it should be scored,
	// but in order to avoid the situation where the number of CPUs in the NUMA Socket of
	// some special models in the cluster is equal to the number of CPUs in the NUMA Node
	// of other models, it is necessary to reduce the weight of the score of such machines.
	if numCPUsNeeded > cpuTopology.CPUsPerNode() && numCPUsNeeded <= cpuTopology.CPUsPerSocket() {
		maxScore = int64(math.Ceil(math.Log(float64(maxScore)) * socketScoreWeight))
	}

	return maxScore
}

// The used capacity is calculated on a scale of 0-MaxNodeScore (MaxNodeScore is
// constant with value set to 100).
// 0 being the lowest priority and 100 being the highest.
// The more resources are used the higher the score is. This function
// is almost a reversed version of leastRequestedScore.
func mostRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return (requested * framework.MaxNodeScore) / capacity
}

// The unused capacity is calculated on a scale of 0-MaxNodeScore
// 0 being the lowest priority and `MaxNodeScore` being the highest.
// The more unused resources the higher the score is.
func leastRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return ((capacity - requested) * framework.MaxNodeScore) / capacity
}

func (c *resourceManager) Update(nodeName string, allocation *PodAllocation) {
	topologyOptions := c.topologyManager.GetTopologyOptions(nodeName)
	if topologyOptions.CPUTopology == nil || !topologyOptions.CPUTopology.IsValid() {
		return
	}

	nodeAllocation := c.getOrCreateNodeAllocation(nodeName)
	nodeAllocation.lock.Lock()
	defer nodeAllocation.lock.Unlock()

	nodeAllocation.update(allocation, topologyOptions.CPUTopology)
}

func (c *resourceManager) Release(nodeName string, podUID types.UID) {
	nodeAllocation := c.getOrCreateNodeAllocation(nodeName)
	nodeAllocation.lock.Lock()
	defer nodeAllocation.lock.Unlock()
	nodeAllocation.release(podUID)
}

func (c *resourceManager) GetAllocatedCPUSet(nodeName string, podUID types.UID) (cpuset.CPUSet, bool) {
	nodeAllocation := c.getOrCreateNodeAllocation(nodeName)
	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()

	return nodeAllocation.getCPUs(podUID)
}

func (c *resourceManager) getNUMAAllocateStrategy(node *corev1.Node) schedulingconfig.NUMAAllocateStrategy {
	numaAllocateStrategy := c.numaAllocateStrategy
	if val := schedulingconfig.NUMAAllocateStrategy(node.Labels[extension.LabelNodeNUMAAllocateStrategy]); val != "" {
		numaAllocateStrategy = val
	}
	return numaAllocateStrategy
}

func (c *resourceManager) GetAvailableCPUs(nodeName string, preferredCPUs cpuset.CPUSet) (availableCPUs cpuset.CPUSet, allocated CPUDetails, err error) {
	topologyOptions := c.topologyManager.GetTopologyOptions(nodeName)
	if topologyOptions.CPUTopology == nil {
		return cpuset.NewCPUSet(), nil, errors.New(ErrNotFoundCPUTopology)
	}
	if !topologyOptions.CPUTopology.IsValid() {
		return cpuset.NewCPUSet(), nil, fmt.Errorf("cpuTopology is invalid")
	}

	allocation := c.getOrCreateNodeAllocation(nodeName)
	allocation.lock.RLock()
	defer allocation.lock.RUnlock()
	availableCPUs, allocated = allocation.getAvailableCPUs(topologyOptions.CPUTopology, topologyOptions.MaxRefCount, topologyOptions.ReservedCPUs, preferredCPUs)
	return availableCPUs, allocated, nil
}

func (c *resourceManager) GetNodeAllocation(nodeName string) *NodeAllocation {
	return c.getOrCreateNodeAllocation(nodeName)
}

func (c *resourceManager) getAvailableNUMANodeResources(nodeName string) (totalAvailable, totalAllocated map[int]corev1.ResourceList, err error) {
	topologyOptions := c.topologyManager.GetTopologyOptions(nodeName)
	nodeAllocation := c.getOrCreateNodeAllocation(nodeName)
	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()
	totalAvailable, totalAllocated = nodeAllocation.getAvailableNUMANodeResources(topologyOptions)
	return totalAvailable, totalAllocated, nil
}

func generateCPUSetHints(topology *CPUTopology, availableCPUs cpuset.CPUSet, reusableCPUs cpuset.CPUSet, request int) []topologymanager.NUMATopologyHint {
	// Initialize minAffinitySize to include all NUMA Nodes.
	numaNodes := topology.CPUDetails.NUMANodes().ToSlice()
	minAffinitySize := len(numaNodes)

	// Iterate through all combinations of numa nodes bitmask and build hints from them.
	var hints []topologymanager.NUMATopologyHint
	bitmask.IterateBitMasks(numaNodes, func(mask bitmask.BitMask) {
		// First, update minAffinitySize for the current request size.
		cpusInMask := topology.CPUDetails.CPUsInNUMANodes(mask.GetBits()...).Size()
		if cpusInMask >= request && mask.Count() < minAffinitySize {
			minAffinitySize = mask.Count()
		}

		// Then check to see if we have enough CPUs available on the current
		// numa node bitmask to satisfy the CPU request.
		numMatching := 0
		for _, c := range reusableCPUs.ToSlice() {
			// Disregard this mask if its NUMANode isn't part of it.
			if !mask.IsSet(topology.CPUDetails[c].NodeID) {
				return
			}
			numMatching++
		}

		// Finally, check to see if enough available CPUs remain on the current
		// NUMA node combination to satisfy the CPU request.
		for _, c := range availableCPUs.ToSlice() {
			if mask.IsSet(topology.CPUDetails[c].NodeID) {
				numMatching++
			}
		}

		// If they don't, then move onto the next combination.
		if numMatching < request {
			return
		}

		// Otherwise, create a new hint from the numa node bitmask and add it to the
		// list of hints.  We set all hint preferences to 'false' on the first
		// pass through.
		hints = append(hints, topologymanager.NUMATopologyHint{
			NUMANodeAffinity: mask,
			Preferred:        false,
		})
	})

	// Loop back through all hints and update the 'Preferred' field based on
	// counting the number of bits sets in the affinity mask and comparing it
	// to the minAffinitySize. Only those with an equal number of bits set (and
	// with a minimal set of numa nodes) will be considered preferred.
	for i := range hints {
		if hints[i].NUMANodeAffinity.Count() == minAffinitySize {
			hints[i].Preferred = true
		}
	}

	return hints
}

func generateResourceHints(numaNodes []int, podRequests corev1.ResourceList, totalAvailable map[int]corev1.ResourceList) map[string][]topologymanager.NUMATopologyHint {
	// Initialize minAffinitySize to include all NUMA Cells.
	minAffinitySize := len(numaNodes)

	hints := map[string][]topologymanager.NUMATopologyHint{}
	bitmask.IterateBitMasks(numaNodes, func(mask bitmask.BitMask) {
		maskBits := mask.GetBits()

		available := make(corev1.ResourceList)
		for _, nodeID := range maskBits {
			available = quotav1.Add(available, totalAvailable[nodeID])
		}
		if satisfied, _ := quotav1.LessThanOrEqual(podRequests, available); !satisfied {
			return
		}

		// set the minimum amount of NUMA nodes that can satisfy the resources requests
		if mask.Count() < minAffinitySize {
			minAffinitySize = mask.Count()
		}

		for resourceName := range podRequests {
			if _, ok := available[resourceName]; !ok {
				continue
			}
			if _, ok := hints[string(resourceName)]; !ok {
				hints[string(resourceName)] = []topologymanager.NUMATopologyHint{}
			}
			hints[string(resourceName)] = append(hints[string(resourceName)], topologymanager.NUMATopologyHint{
				NUMANodeAffinity: mask,
				Preferred:        false,
			})
		}
	})

	// update hints preferred according to multiNUMAGroups, in case when it wasn't provided, the default
	// behavior to prefer the minimal amount of NUMA nodes will be used
	for resourceName := range podRequests {
		for i, hint := range hints[string(resourceName)] {
			hints[string(resourceName)][i].Preferred = len(hint.NUMANodeAffinity.GetBits()) == minAffinitySize
		}
	}

	return hints
}

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
	"reflect"
	"sort"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

type ResourceManager interface {
	GetTopologyHints(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions, policy apiext.NUMATopologyPolicy, restoreState *nodeReservationRestoreStateData) (map[string][]topologymanager.NUMATopologyHint, error)
	Allocate(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) (*PodAllocation, *framework.Status)

	Update(nodeName string, allocation *PodAllocation)
	Release(nodeName string, podUID types.UID)

	GetNodeAllocation(nodeName string) *NodeAllocation
	GetAllocatedCPUSet(nodeName string, podUID types.UID) (cpuset.CPUSet, bool)
	GetAllocatedNUMAResource(nodeName string, podUID types.UID) (map[int]corev1.ResourceList, bool)
	GetAvailableCPUs(nodeName string, preferredCPUs ...cpuset.CPUSet) (availableCPUs cpuset.CPUSet, allocated CPUDetails, err error)
}

type ResourceOptions struct {
	numCPUsNeeded           int
	requestCPUBind          bool
	requests                corev1.ResourceList
	originalRequests        corev1.ResourceList
	requiredCPUBindPolicy   bool
	cpuBindPolicy           schedulingconfig.CPUBindPolicy
	cpuExclusivePolicy      schedulingconfig.CPUExclusivePolicy
	preferredCPUs           cpuset.CPUSet
	preemptibleCPUs         cpuset.CPUSet // cpus could be allocated by preemption
	reusableResources       map[int]corev1.ResourceList
	requiredResources       map[int]corev1.ResourceList
	requiredFromReservation bool
	hint                    topologymanager.NUMATopologyHint
	topologyOptions         TopologyOptions
	numaScorer              *resourceAllocationScorer
	nodePreemptionState     *preemptibleNodeState
}

type resourceManager struct {
	numaAllocateStrategy   schedulingconfig.NUMAAllocateStrategy
	topologyOptionsManager TopologyOptionsManager
	lock                   sync.Mutex
	nodeAllocations        map[string]*NodeAllocation
}

func NewResourceManager(
	handle framework.Handle,
	defaultNUMAAllocateStrategy schedulingconfig.NUMAAllocateStrategy,
	topologyOptionsManager TopologyOptionsManager,
) ResourceManager {
	manager := &resourceManager{
		numaAllocateStrategy:   defaultNUMAAllocateStrategy,
		topologyOptionsManager: topologyOptionsManager,
		nodeAllocations:        map[string]*NodeAllocation{},
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
	delete(c.nodeAllocations, node.Name)
}

func (c *resourceManager) getOrCreateNodeAllocation(nodeName string) *NodeAllocation {
	c.lock.Lock()
	defer c.lock.Unlock()
	v := c.nodeAllocations[nodeName]
	if v == nil {
		v = NewNodeAllocation(nodeName)
		c.nodeAllocations[nodeName] = v
	}
	return v
}

func (c *resourceManager) GetTopologyHints(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions, policy apiext.NUMATopologyPolicy, restoreStateData *nodeReservationRestoreStateData) (map[string][]topologymanager.NUMATopologyHint, error) {
	topologyOptions := options.topologyOptions
	if len(topologyOptions.NUMANodeResources) == 0 {
		return nil, fmt.Errorf("insufficient resources on NUMA Node")
	}

	// FIXME: restore reserved cpus according to reservations' allocatePolicy
	options.reusableResources = appendAllocated(nil, restoreStateData.mergedUnmatchedUsed, restoreStateData.mergedMatchedAllocatable)
	options.preferredCPUs = restoreStateData.mergedMatchedRemainCPUs
	// update with preemptible resources
	if options.nodePreemptionState != nil && options.nodePreemptionState.nodeAlloc != nil {
		nodePreemptionAlloc := options.nodePreemptionState.nodeAlloc
		options.reusableResources = nodePreemptionAlloc.AppendNUMAResources(options.reusableResources)
		options.preemptibleCPUs = nodePreemptionAlloc.AppendCPUSet(cpuset.NewCPUSet())
	}
	if options.nodePreemptionState != nil && options.nodePreemptionState.reservationsAlloc != nil {
		for _, reservationPreemptionAlloc := range options.nodePreemptionState.reservationsAlloc {
			options.reusableResources = reservationPreemptionAlloc.AppendNUMAResources(options.reusableResources)
			options.preemptibleCPUs = reservationPreemptionAlloc.AppendCPUSet(options.preemptibleCPUs)
		}
	}

	totalAvailable, _, err := c.getAvailableNUMANodeResources(node.Name, topologyOptions, options.reusableResources)
	if err != nil {
		return nil, err
	}
	klog.V(6).InfoS("GetTopologyHints", "pod", klog.KObj(pod), "node", node.Name, "before trimmed", totalAvailable)
	if err := c.trimNUMANodeResources(node.Name, totalAvailable, options); err != nil {
		return nil, err
	}
	klog.V(6).InfoS("GetTopologyHints", "pod", klog.KObj(pod), "node", node.Name, "after trimmed", totalAvailable)

	hints := c.generateResourceHints(node, pod, topologyOptions.NUMANodeResources, options, totalAvailable, policy, restoreStateData)
	return hints, nil
}

func (c *resourceManager) trimNUMANodeResources(nodeName string, totalAvailable map[int]corev1.ResourceList, options *ResourceOptions) error {
	if !options.requiredCPUBindPolicy {
		return nil
	}
	availableCPUs, _, err := c.GetAvailableCPUs(nodeName, options.preferredCPUs, options.preemptibleCPUs)
	if err != nil {
		return err
	}
	cpuDetails := options.topologyOptions.CPUTopology.CPUDetails.KeepOnly(availableCPUs)
	for numaNode, available := range totalAvailable {
		cpuQuantity := available[corev1.ResourceCPU]
		if cpuQuantity.IsZero() {
			continue
		}
		availableCPUs := filterCPUsByRequiredCPUBindPolicy(
			options.cpuBindPolicy,
			cpuDetails.CPUsInNUMANodes(numaNode),
			cpuDetails,
			options.topologyOptions.CPUTopology.CPUsPerCore(),
		)
		if int64(availableCPUs.Size())*1000 < cpuQuantity.MilliValue() {
			cpuQuantity.SetMilli(int64(availableCPUs.Size() * 1000))
			available[corev1.ResourceCPU] = cpuQuantity
		}
	}
	return nil
}

func (c *resourceManager) Allocate(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) (*PodAllocation, *framework.Status) {
	allocation := &PodAllocation{
		UID:                pod.UID,
		Namespace:          pod.Namespace,
		Name:               pod.Name,
		CPUExclusivePolicy: options.cpuExclusivePolicy,
	}
	klog.V(5).Infof("Allocate pod %s/%s on node %s, numaNodeAffinity: %+v, requestCPUBind %v", pod.Namespace, pod.Name, node.Name, options.hint, options.requestCPUBind)
	if options.hint.NUMANodeAffinity != nil {
		resources, err := c.allocateResourcesByHint(node, pod, options)
		if err != nil {
			klog.Errorf("allocateResourcesByHint for pod %s on node %s, failed: %v", util.GetNamespacedName(pod.Namespace, pod.Name), node.Name, err)
			return nil, err
		}
		if len(resources) == 0 {
			klog.Warningf("succeed allocateResourcesByHint but allocatedNUMAResources nil, options: %+v", options)
		}
		allocation.NUMANodeResources = resources

	}
	if options.requestCPUBind {
		cpus, err := c.allocateCPUSet(node, pod, allocation.NUMANodeResources, options)
		if err != nil {
			return nil, framework.NewStatus(framework.Unschedulable, err.Error())
		}
		if cpus.IsEmpty() {
			klog.Warningf("succeed allocateCPUSet but allocatedCPUs empty, options: %+v, allocation.NUMANodeResources: %+v", options, allocation.NUMANodeResources)
		}
		allocation.CPUSet = cpus
	}
	return allocation, nil
}

func (c *resourceManager) allocateResourcesByHint(node *corev1.Node, pod *corev1.Pod, options *ResourceOptions) ([]NUMANodeResource, *framework.Status) {
	if len(options.topologyOptions.NUMANodeResources) == 0 {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, "insufficient resources on NUMA Node")
	}
	if klog.V(6).Enabled() {
		logStruct(reflect.ValueOf(options), fmt.Sprintf("options for pod pod %s/%s on node %s", pod.Namespace, pod.Name, node.Name), 6)
	}
	totalAvailable := options.requiredResources
	if len(totalAvailable) == 0 {
		var err error
		totalAvailable, _, err = c.getAvailableNUMANodeResources(node.Name, options.topologyOptions, options.reusableResources)
		if err != nil {
			return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
		}
	}
	if err := c.trimNUMANodeResources(node.Name, totalAvailable, options); err != nil {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}

	var requests corev1.ResourceList
	if options.requestCPUBind {
		requests = options.originalRequests.DeepCopy()
	} else {
		requests = options.requests.DeepCopy()
	}

	result, reasons := tryBestToDistributeEvenly(requests, totalAvailable, options)
	if len(reasons) > 0 {
		return nil, framework.NewStatus(framework.Unschedulable, reasons...)
	}
	return result, nil
}

func tryBestToDistributeEvenly(requests corev1.ResourceList, totalAvailable map[int]corev1.ResourceList, options *ResourceOptions) ([]NUMANodeResource, []string) {
	resourceNamesByNUMA := sets.NewString()
	for _, available := range totalAvailable {
		for resourceName := range available {
			resourceNamesByNUMA.Insert(string(resourceName))
		}
	}
	sortedNUMANodeByResource := map[corev1.ResourceName][]int{}
	numaNodes := options.hint.NUMANodeAffinity.GetBits()
	for resourceName := range resourceNamesByNUMA {
		sortedNUMANodes := make([]int, len(numaNodes))
		copy(sortedNUMANodes, numaNodes)
		sort.Slice(sortedNUMANodes, func(i, j int) bool {
			iAvailableOfResource := totalAvailable[i][corev1.ResourceName(resourceName)]
			return (&iAvailableOfResource).Cmp(totalAvailable[j][corev1.ResourceName(resourceName)]) < 0
		})
		sortedNUMANodeByResource[corev1.ResourceName(resourceName)] = sortedNUMANodes
	}
	allocatedNUMANodeResources := map[int]*NUMANodeResource{}
	for resourceName, quantity := range requests {
		for i, numaNodeID := range sortedNUMANodeByResource[resourceName] {
			splittedQuantity := splitQuantity(resourceName, quantity, len(numaNodes)-i, options)
			_, _, allocated := allocateRes(totalAvailable[numaNodeID][resourceName], splittedQuantity)
			if !allocated.IsZero() {
				allocatedNUMANodeResource := allocatedNUMANodeResources[numaNodeID]
				if allocatedNUMANodeResource == nil {
					allocatedNUMANodeResource = &NUMANodeResource{
						Node:      numaNodeID,
						Resources: corev1.ResourceList{},
					}
					allocatedNUMANodeResources[numaNodeID] = allocatedNUMANodeResource
				}
				allocatedNUMANodeResource.Resources[resourceName] = allocated
				quantity.Sub(allocated)
			}
		}
		requests[resourceName] = quantity
	}
	var reasons []string
	for resourceName, quantity := range requests {
		if resourceNamesByNUMA.Has(string(resourceName)) {
			if !quantity.IsZero() {
				reasons = append(reasons, fmt.Sprintf("Insufficient NUMA %s", resourceName))
			}
		}
	}
	result := make([]NUMANodeResource, 0, len(allocatedNUMANodeResources))
	for _, numaNodeResource := range allocatedNUMANodeResources {
		result = append(result, *numaNodeResource)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Node < result[j].Node
	})
	return result, reasons
}

func splitQuantity(resourceName corev1.ResourceName, quantity resource.Quantity, numaNodeCount int, options *ResourceOptions) resource.Quantity {
	if resourceName != corev1.ResourceCPU {
		return *resource.NewQuantity(quantity.Value()/int64(numaNodeCount), quantity.Format)
	}
	if !options.requestCPUBind {
		return *resource.NewMilliQuantity(quantity.MilliValue()/int64(numaNodeCount), quantity.Format)
	}
	if options.requiredCPUBindPolicy && options.cpuBindPolicy == schedulingconfig.CPUBindPolicyFullPCPUs {
		cpusPerCore := int64(options.topologyOptions.CPUTopology.CPUsPerCore())
		numOfPCPUs := quantity.Value() / cpusPerCore
		numOfPCPUsPerNUMA := numOfPCPUs / int64(numaNodeCount)
		return *resource.NewQuantity(numOfPCPUsPerNUMA*cpusPerCore, quantity.Format)
	}
	return *resource.NewQuantity(quantity.Value()/int64(numaNodeCount), quantity.Format)
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

func (c *resourceManager) allocateCPUSet(node *corev1.Node, pod *corev1.Pod, allocatedNUMANodes []NUMANodeResource, options *ResourceOptions) (cpuset.CPUSet, error) {
	empty := cpuset.CPUSet{}
	availableCPUs, allocatedCPUs, err := c.GetAvailableCPUs(node.Name, options.preferredCPUs, options.preemptibleCPUs)
	if err != nil {
		klog.V(5).InfoS("failed to allocateCPUSet for pod on node", "pod", klog.KObj(pod), "node", node.Name,
			"preferredCPUs", options.preferredCPUs, "preemptibleCPUs", options.preemptibleCPUs, "err", err)
		return empty, err
	}
	klog.V(6).InfoS("GetAvailableCPUs for pod on node", "pod", klog.KObj(pod), "node", node.Name,
		"numCPUsNeeded", options.numCPUsNeeded, "preferredCPUs", options.preferredCPUs, "preemptibleCPUs", options.preemptibleCPUs,
		"availableCPUs", availableCPUs, "allocatedCPUs", allocatedCPUs, "topologyOptions", options.topologyOptions)

	topologyOptions := &options.topologyOptions
	if options.requiredCPUBindPolicy {
		cpuDetails := topologyOptions.CPUTopology.CPUDetails.KeepOnly(availableCPUs)
		availableCPUs = filterCPUsByRequiredCPUBindPolicy(
			options.cpuBindPolicy,
			availableCPUs,
			cpuDetails,
			topologyOptions.CPUTopology.CPUsPerCore(),
		)
	}

	if availableCPUs.Size() < options.numCPUsNeeded {
		klog.V(5).InfoS("failed to allocateCPUSet for pod on node, availableCPUs not enough",
			"pod", klog.KObj(pod), "node", node.Name, "numCPUsNeeded", options.numCPUsNeeded,
			"preferredCPUs", options.preferredCPUs, "preemptibleCPUs", options.preemptibleCPUs,
			"availableCPUs", availableCPUs, "cpuBindPolicy", options.cpuBindPolicy)
		return empty, fmt.Errorf("not enough cpus available to satisfy request")
	}

	result := cpuset.CPUSet{}
	numaAllocateStrategy := GetNUMAAllocateStrategy(node, c.numaAllocateStrategy)
	numCPUsNeeded := options.numCPUsNeeded
	if len(allocatedNUMANodes) > 0 {
		for _, numaNode := range allocatedNUMANodes {
			cpusInNUMANode := topologyOptions.CPUTopology.CPUDetails.CPUsInNUMANodes(numaNode.Node)
			availableCPUsInNUMANode := availableCPUs.Intersection(cpusInNUMANode)

			numCPUs := availableCPUsInNUMANode.Size()
			quantity := numaNode.Resources[corev1.ResourceCPU]
			nodeNumCPUsNeeded := int(quantity.MilliValue() / 1000)
			if nodeNumCPUsNeeded < numCPUs {
				numCPUs = nodeNumCPUsNeeded
			}

			cpus, err := takePreferredCPUs(
				topologyOptions.CPUTopology,
				topologyOptions.MaxRefCount,
				availableCPUsInNUMANode,
				options.preferredCPUs,
				allocatedCPUs,
				numCPUs,
				options.cpuBindPolicy,
				options.cpuExclusivePolicy,
				numaAllocateStrategy,
			)
			if err != nil {
				klog.V(5).InfoS("failed to allocateCPUSet for pod, takePreferredCPUs on NUMA node error",
					"pod", klog.KObj(pod), "node", node.Name, "NUMA", numaNode.Node, "err", err)
				return empty, err
			}

			result = result.Union(cpus)
		}
		numCPUsNeeded -= result.Size()
		if numCPUsNeeded != 0 {
			klog.V(5).InfoS("failed to allocateCPUSet for pod, CPUs taken not enough on NUMA nodes",
				"pod", klog.KObj(pod), "node", node.Name,
				"result", result.String(), "needed CPUs remain", numCPUsNeeded)
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
			klog.V(5).InfoS("failed to allocateCPUSet for pod on node, takePreferredCPUs error",
				"pod", klog.KObj(pod), "node", node.Name, "err", err)
			return empty, err
		}
		result = result.Union(remainingCPUs)
	}

	if options.requiredCPUBindPolicy {
		err = satisfiedRequiredCPUBindPolicy(options.cpuBindPolicy, result, topologyOptions.CPUTopology)
		if err != nil {
			klog.V(5).InfoS("failed to allocateCPUSet for pod on node, requiredCPUBindPolicy not satisfied",
				"pod", klog.KObj(pod), "node", node.Name,
				"cpuBindPolicy", options.cpuBindPolicy, "result", result.String(), "err", err)
			return empty, err
		}
	}

	return result, err
}

func (c *resourceManager) Update(nodeName string, allocation *PodAllocation) {
	topologyOptions := c.topologyOptionsManager.GetTopologyOptions(nodeName)
	if !topologyOptions.CPUTopology.IsValid() {
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

func (c *resourceManager) GetAllocatedNUMAResource(nodeName string, podUID types.UID) (map[int]corev1.ResourceList, bool) {
	nodeAllocation := c.getOrCreateNodeAllocation(nodeName)
	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()

	return nodeAllocation.getNUMAResource(podUID)
}

func (c *resourceManager) GetAvailableCPUs(nodeName string, preferredCPUs ...cpuset.CPUSet) (availableCPUs cpuset.CPUSet, allocated CPUDetails, err error) {
	topologyOptions := c.topologyOptionsManager.GetTopologyOptions(nodeName)
	if topologyOptions.CPUTopology == nil {
		return cpuset.NewCPUSet(), nil, nil
	}
	if !topologyOptions.CPUTopology.IsValid() {
		return cpuset.NewCPUSet(), nil, errors.New(ErrInvalidCPUTopology)
	}

	allocation := c.getOrCreateNodeAllocation(nodeName)
	allocation.lock.RLock()
	defer allocation.lock.RUnlock()
	availableCPUs, allocated = allocation.getAvailableCPUs(topologyOptions.CPUTopology, topologyOptions.MaxRefCount, topologyOptions.ReservedCPUs, preferredCPUs...)
	return availableCPUs, allocated, nil
}

func (c *resourceManager) GetNodeAllocation(nodeName string) *NodeAllocation {
	return c.getOrCreateNodeAllocation(nodeName)
}

func (c *resourceManager) getAvailableNUMANodeResources(nodeName string, topologyOptions TopologyOptions, reusableResources map[int]corev1.ResourceList) (totalAvailable, totalAllocated map[int]corev1.ResourceList, err error) {
	nodeAllocation := c.getOrCreateNodeAllocation(nodeName)
	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()
	totalAvailable, totalAllocated = nodeAllocation.getAvailableNUMANodeResources(topologyOptions, reusableResources)
	return totalAvailable, totalAllocated, nil
}

func (c *resourceManager) generateResourceHints(node *corev1.Node, pod *corev1.Pod, numaNodeResources []NUMANodeResource, options *ResourceOptions, totalAvailable map[int]corev1.ResourceList, policy apiext.NUMATopologyPolicy, restoreState *nodeReservationRestoreStateData) map[string][]topologymanager.NUMATopologyHint {
	resourceNamesByNUMA := sets.New[corev1.ResourceName]()
	for _, numaNodeResource := range numaNodeResources {
		resourceNamesByNUMA.Insert(quotav1.ResourceNames(numaNodeResource.Resources)...)
	}
	numaNodesLackResource := map[corev1.ResourceName][]int{}
	for resourceName := range resourceNamesByNUMA {
		for nodeID, numaAvailable := range totalAvailable {
			if available, ok := numaAvailable[resourceName]; !ok || available.IsZero() {
				numaNodesLackResource[resourceName] = append(numaNodesLackResource[resourceName], nodeID)
			}
		}
	}

	klog.V(5).InfoS("generate resource hints", "numaNodesLackResource", numaNodesLackResource, "pod", klog.KObj(pod), "node", node.Name)

	generator := hintsGenerator{
		numaNodesLackResource: numaNodesLackResource,
		minAffinitySize:       make(map[corev1.ResourceName]int),
		hints:                 map[string][]topologymanager.NUMATopologyHint{},
	}
	for resourceName := range options.requests {
		generator.minAffinitySize[resourceName] = len(numaNodeResources)
	}

	numaNodes := make([]int, 0, len(numaNodeResources))
	for _, v := range numaNodeResources {
		numaNodes = append(numaNodes, v.Node)
	}

	podRequestResources := framework.NewResource(options.requests)
	totalResourceNames := sets.NewString()
	bitmask.IterateBitMasks(numaNodes, func(mask bitmask.BitMask) {
		maskBits := mask.GetBits()
		available := make(corev1.ResourceList)
		total := make(corev1.ResourceList)
		for _, nodeID := range maskBits {
			util.AddResourceList(available, totalAvailable[nodeID])
			for _, v := range numaNodeResources {
				if v.Node == nodeID {
					util.AddResourceList(total, v.Resources)
					break
				}
			}
		}

		for resourceName := range options.requests {
			if _, ok := total[resourceName]; !ok {
				continue
			}
			totalResourceNames.Insert(string(resourceName))
		}

		var score int64
		if options.numaScorer != nil {
			requested := quotav1.SubtractWithNonNegativeResult(total, available)
			score, _ = options.numaScorer.score(framework.NewResource(requested), framework.NewResource(total), podRequestResources)
		}

		options.hint = topologymanager.NUMATopologyHint{NUMANodeAffinity: mask}
		options.requiredResources = nil
		podAllocation, status := tryAllocateFromReservation(c, restoreState, options, restoreState.matched, pod, node)
		if !status.IsSuccess() {
			return
		}
		if podAllocation == nil {
			_, status = tryAllocateFromNode(c, restoreState, options, pod, node)
			if !status.IsSuccess() {
				return
			}
		}

		for resourceName := range totalResourceNames {
			generator.generateHints(mask, score, corev1.ResourceName(resourceName))
		}
	})

	// update hints preferred according to multiNUMAGroups, in case when it wasn't provided, the default
	// behavior to prefer the minimal amount of NUMA nodes will be used
	for resourceName := range options.requests {
		minAffinitySize := generator.minAffinitySize[resourceName]
		for i, hint := range generator.hints[string(resourceName)] {
			generator.hints[string(resourceName)][i].Preferred = len(hint.NUMANodeAffinity.GetBits()) == minAffinitySize || policy == apiext.NUMATopologyPolicyRestricted
		}
	}

	for resourceName := range options.requests {
		if totalResourceNames.Has(string(resourceName)) {
			hints := generator.hints[string(resourceName)]
			if hints == nil {
				// no possible NUMA affinities for resource
				hints = []topologymanager.NUMATopologyHint{}
				generator.hints[string(resourceName)] = hints
			}
		}
	}
	return generator.hints
}

type hintsGenerator struct {
	numaNodesLackResource map[corev1.ResourceName][]int
	minAffinitySize       map[corev1.ResourceName]int
	hints                 map[string][]topologymanager.NUMATopologyHint
}

func (g *hintsGenerator) generateHints(mask bitmask.BitMask, score int64, resourceNames ...corev1.ResourceName) {
	for _, resourceName := range resourceNames {
		if mask.AnySet(g.numaNodesLackResource[resourceName]) {
			return
		}
	}

	nodeCount := mask.Count()

	for _, resourceName := range resourceNames {
		affinitySize := g.minAffinitySize[resourceName]
		if nodeCount < affinitySize {
			g.minAffinitySize[resourceName] = nodeCount
		}
		if _, ok := g.hints[string(resourceName)]; !ok {
			g.hints[string(resourceName)] = []topologymanager.NUMATopologyHint{}
		}
		g.hints[string(resourceName)] = append(g.hints[string(resourceName)], topologymanager.NUMATopologyHint{
			NUMANodeAffinity: mask,
			Preferred:        false,
			Score:            score,
		})
	}
}

func filterCPUsByRequiredCPUBindPolicy(policy schedulingconfig.CPUBindPolicy, availableCPUs cpuset.CPUSet, cpuDetails CPUDetails, cpusPerCore int) cpuset.CPUSet {
	builder := cpuset.NewCPUSetBuilder()
	cpuDetails = cpuDetails.KeepOnly(availableCPUs)
	switch policy {
	case schedulingconfig.CPUBindPolicyFullPCPUs:
		for _, core := range cpuDetails.Cores().ToSliceNoSort() {
			cpus := cpuDetails.CPUsInCores(core)
			if cpus.Size() == cpusPerCore {
				builder.Add(cpus.ToSliceNoSort()...)
			}
		}
		availableCPUs = builder.Result()
	case schedulingconfig.CPUBindPolicySpreadByPCPUs:
		for _, core := range cpuDetails.Cores().ToSliceNoSort() {
			// TODO(joseph): Maybe we should support required exclusive policy as following
			//	allocated := allocatedCPUs.CPUsInCores(core)
			//	if allocated.Size() > 0 {
			//		cpuInfo := allocatedCPUs[allocated.ToSliceNoSort()[0]]
			//		if cpuInfo.ExclusivePolicy != "" &&
			//			cpuInfo.ExclusivePolicy != schedulingconfig.CPUExclusivePolicyNone &&
			//			cpuInfo.ExclusivePolicy == exclusivePolicy {
			//			continue
			//		}
			//	}

			// Using only one CPU per core ensures correct hints are generated
			cpus := cpuDetails.CPUsInCores(core).ToSlice()
			builder.Add(cpus[0])
		}
		availableCPUs = builder.Result()
	}
	return availableCPUs
}

func satisfiedRequiredCPUBindPolicy(policy schedulingconfig.CPUBindPolicy, cpus cpuset.CPUSet, topology *CPUTopology) error {
	satisfied := true
	if policy == schedulingconfig.CPUBindPolicyFullPCPUs {
		satisfied = determineFullPCPUs(cpus, topology.CPUDetails, topology.CPUsPerCore())
	} else if policy == schedulingconfig.CPUBindPolicySpreadByPCPUs {
		satisfied = determineSpreadByPCPUs(cpus, topology.CPUDetails)
	}
	if !satisfied {
		return fmt.Errorf("insufficient CPUs to satisfy required cpu bind policy %s", policy)
	}
	return nil
}

func determineFullPCPUs(cpus cpuset.CPUSet, details CPUDetails, cpusPerCore int) bool {
	details = details.KeepOnly(cpus)
	return details.Cores().Size()*cpusPerCore == cpus.Size()
}

func determineSpreadByPCPUs(cpus cpuset.CPUSet, details CPUDetails) bool {
	details = details.KeepOnly(cpus)
	return details.Cores().Size() == cpus.Size()
}

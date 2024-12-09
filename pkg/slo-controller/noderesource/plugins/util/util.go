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

package util

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

var (
	updateNRTResourceSet = sets.NewString(string(extension.BatchCPU), string(extension.BatchMemory))
)

const (
	MidCPUThreshold       = "midCPUThreshold"
	MidMemoryThreshold    = "midMemoryThreshold"
	MidUnallocatedPercent = "midUnallocatedPercent"
)

func CalculateBatchResourceByPolicy(strategy *configuration.ColocationStrategy, nodeCapacity, nodeSafetyMargin, nodeReserved,
	systemUsed, podHPReq, podHPUsed, podHPMaxUsedReq corev1.ResourceList) (corev1.ResourceList, string, string) {
	// Node(Batch).Alloc[usage] := Node.Total - Node.SafetyMargin - System.Used - sum(Pod(Prod/Mid).Used)
	// System.Used = max(Node.Used - Pod(All).Used, Node.Anno.Reserved, Node.Kubelet.Reserved)
	systemUsed = quotav1.Max(systemUsed, nodeReserved)
	batchAllocatableByUsage := quotav1.Max(quotav1.Subtract(quotav1.Subtract(quotav1.Subtract(
		nodeCapacity, nodeSafetyMargin), systemUsed), podHPUsed), util.NewZeroResourceList())

	// Node(Batch).Alloc[request] := Node.Total - Node.SafetyMargin - System.Reserved - sum(Pod(Prod/Mid).Request)
	// System.Reserved = max(Node.Anno.Reserved, Node.Kubelet.Reserved)
	batchAllocatableByRequest := quotav1.Max(quotav1.Subtract(quotav1.Subtract(quotav1.Subtract(
		nodeCapacity, nodeSafetyMargin), nodeReserved), podHPReq), util.NewZeroResourceList())

	// Node(Batch).Alloc[maxUsageRequest] := Node.Total - Node.SafetyMargin - System.Used - sum(max(Pod(Prod/Mid).Request, Pod(Prod/Mid).Used))
	batchAllocatableByMaxUsageRequest := quotav1.Max(quotav1.Subtract(quotav1.Subtract(quotav1.Subtract(
		nodeCapacity, nodeSafetyMargin), systemUsed), podHPMaxUsedReq), util.NewZeroResourceList())

	batchAllocatable := batchAllocatableByUsage

	var cpuMsg string
	// batch cpu support policy "usage" and "maxUsageRequest"
	if strategy != nil && strategy.CPUCalculatePolicy != nil && *strategy.CPUCalculatePolicy == configuration.CalculateByPodMaxUsageRequest {
		batchAllocatable[corev1.ResourceCPU] = *batchAllocatableByMaxUsageRequest.Cpu()
		cpuMsg = fmt.Sprintf("batchAllocatable[CPU(Milli-Core)]:%v = nodeCapacity:%v - nodeSafetyMargin:%v - systemUsageOrNodeReserved:%v - podHPMaxUsedRequest:%v",
			batchAllocatable.Cpu().MilliValue(), nodeCapacity.Cpu().MilliValue(), nodeSafetyMargin.Cpu().MilliValue(),
			systemUsed.Cpu().MilliValue(), podHPMaxUsedReq.Cpu().MilliValue())
	} else { // use CalculatePolicy "usage" by default
		cpuMsg = fmt.Sprintf("batchAllocatable[CPU(Milli-Core)]:%v = nodeCapacity:%v - nodeSafetyMargin:%v - systemUsageOrNodeReserved:%v - podHPUsed:%v",
			batchAllocatable.Cpu().MilliValue(), nodeCapacity.Cpu().MilliValue(), nodeSafetyMargin.Cpu().MilliValue(),
			systemUsed.Cpu().MilliValue(), podHPUsed.Cpu().MilliValue())
	}

	var memMsg string
	// batch memory support policy "usage", "request" and "maxUsageRequest"
	if strategy != nil && strategy.MemoryCalculatePolicy != nil && *strategy.MemoryCalculatePolicy == configuration.CalculateByPodRequest {
		batchAllocatable[corev1.ResourceMemory] = *batchAllocatableByRequest.Memory()
		memMsg = fmt.Sprintf("batchAllocatable[Mem(GB)]:%v = nodeCapacity:%v - nodeSafetyMargin:%v - nodeReserved:%v - podHPRequest:%v",
			batchAllocatable.Memory().ScaledValue(resource.Giga), nodeCapacity.Memory().ScaledValue(resource.Giga),
			nodeSafetyMargin.Memory().ScaledValue(resource.Giga), nodeReserved.Memory().ScaledValue(resource.Giga),
			podHPReq.Memory().ScaledValue(resource.Giga))
	} else if strategy != nil && strategy.MemoryCalculatePolicy != nil && *strategy.MemoryCalculatePolicy == configuration.CalculateByPodMaxUsageRequest {
		batchAllocatable[corev1.ResourceMemory] = *batchAllocatableByMaxUsageRequest.Memory()
		memMsg = fmt.Sprintf("batchAllocatable[Mem(GB)]:%v = nodeCapacity:%v - nodeSafetyMargin:%v - systemUsage:%v - podHPMaxUsedRequest:%v",
			batchAllocatable.Memory().ScaledValue(resource.Giga), nodeCapacity.Memory().ScaledValue(resource.Giga),
			nodeSafetyMargin.Memory().ScaledValue(resource.Giga), systemUsed.Memory().ScaledValue(resource.Giga),
			podHPMaxUsedReq.Memory().ScaledValue(resource.Giga))
	} else { // use CalculatePolicy "usage" by default
		memMsg = fmt.Sprintf("batchAllocatable[Mem(GB)]:%v = nodeCapacity:%v - nodeSafetyMargin:%v - systemUsage:%v - podHPUsed:%v",
			batchAllocatable.Memory().ScaledValue(resource.Giga), nodeCapacity.Memory().ScaledValue(resource.Giga),
			nodeSafetyMargin.Memory().ScaledValue(resource.Giga), systemUsed.Memory().ScaledValue(resource.Giga),
			podHPUsed.Memory().ScaledValue(resource.Giga))
	}
	return batchAllocatable, cpuMsg, memMsg
}

func CalculateMidResourceByPolicy(strategy *configuration.ColocationStrategy, nodeCapacity, unallocated, nodeUnused corev1.ResourceList, allocatableMilliCPU, allocatableMemory int64,
	prodReclaimableCPU, prodReclaimableMemory *resource.Quantity, nodeName string) (*resource.Quantity, *resource.Quantity, string, string) {
	defaultStrategy := sloconfig.DefaultColocationStrategy()
	cpuThresholdRatio := getPercentFromStrategy(strategy, &defaultStrategy, MidCPUThreshold)
	if maxMilliCPU := float64(nodeCapacity.Cpu().MilliValue()) * cpuThresholdRatio; allocatableMilliCPU > int64(maxMilliCPU) {
		allocatableMilliCPU = int64(maxMilliCPU)
	}
	if allocatableMilliCPU > nodeUnused.Cpu().MilliValue() {
		allocatableMilliCPU = nodeUnused.Cpu().MilliValue()
	}
	if allocatableMilliCPU < 0 {
		klog.V(5).Infof("mid allocatable milli cpu of node %s is %v less than zero, set to zero",
			nodeName, allocatableMilliCPU)
		allocatableMilliCPU = 0
	}
	cpuInMilliCores := resource.NewQuantity(allocatableMilliCPU, resource.DecimalSI)

	memThresholdRatio := getPercentFromStrategy(strategy, &defaultStrategy, MidMemoryThreshold)
	if maxMemory := float64(nodeCapacity.Memory().Value()) * memThresholdRatio; allocatableMemory > int64(maxMemory) {
		allocatableMemory = int64(maxMemory)
	}
	if allocatableMemory > nodeUnused.Memory().Value() {
		allocatableMemory = nodeUnused.Memory().Value()
	}
	if allocatableMemory < 0 {
		klog.V(5).Infof("mid allocatable memory of node %s is %v less than zero, set to zero",
			nodeName, allocatableMemory)
		allocatableMemory = 0
	}
	memory := resource.NewQuantity(allocatableMemory, resource.BinarySI)

	// CPU need turn into milli value
	unallocatedMilliCPU, unallocatedMemory := resource.NewQuantity(unallocated.Cpu().MilliValue(), resource.DecimalSI), unallocated.Memory()
	midUnallocatedRatio := getPercentFromStrategy(strategy, &defaultStrategy, MidUnallocatedPercent)
	adjustedUnallocatedMilliCPU := resource.NewQuantity(int64(float64(unallocatedMilliCPU.Value())*midUnallocatedRatio), resource.DecimalSI)
	adjustedUnallocatedMemory := resource.NewQuantity(int64(float64(unallocatedMemory.Value())*midUnallocatedRatio), resource.BinarySI)

	cpuInMilliCores.Add(*adjustedUnallocatedMilliCPU)
	memory.Add(*adjustedUnallocatedMemory)

	cpuMsg := fmt.Sprintf("midAllocatable[CPU(milli-core)]:%v = min(nodeCapacity:%v * thresholdRatio:%v, ProdReclaimable:%v, NodeUnused:%v) + Unallocated:%v * midUnallocatedRatio:%v",
		cpuInMilliCores.Value(), nodeCapacity.Cpu().MilliValue(),
		cpuThresholdRatio, prodReclaimableCPU.MilliValue(), nodeUnused.Cpu().MilliValue(),
		unallocatedMilliCPU.Value(), midUnallocatedRatio)

	memMsg := fmt.Sprintf("midAllocatable[Memory(GB)]:%v = min(nodeCapacity:%v * thresholdRatio:%v, ProdReclaimable:%v, NodeUnused:%v) + Unallocated:%v * midUnallocatedRatio:%v",
		memory.ScaledValue(resource.Giga), nodeCapacity.Memory().ScaledValue(resource.Giga),
		memThresholdRatio, prodReclaimableMemory.ScaledValue(resource.Giga), nodeUnused.Memory().ScaledValue(resource.Giga),
		unallocatedMemory.ScaledValue(resource.Giga), midUnallocatedRatio)

	return cpuInMilliCores, memory, cpuMsg, memMsg
}

func PrepareNodeForResource(node *corev1.Node, nr *framework.NodeResource, name corev1.ResourceName) {
	q := nr.Resources[name]
	if q == nil || nr.Resets[name] { // if the specified resource has no quantity
		delete(node.Status.Capacity, name)
		delete(node.Status.Allocatable, name)
		return
	}

	// TODO mv to post-calculate stage for merging multiple calculate results
	// amplify batch cpu according to cpu normalization ratio
	if name == extension.BatchCPU {
		ratio, err := getCPUNormalizationRatio(nr)
		if err != nil {
			klog.V(5).InfoS("failed to get cpu normalization ratio for node extended resources",
				"node", node.Name, "err", err)
		}
		if ratio > 1.0 { // skip for invalid ratio
			newQuantity := util.MultiplyMilliQuant(*q, ratio)
			q = &newQuantity
		}
	}

	// NOTE: extended resource would be validated as an integer, so it should be checked before the update
	if _, ok := q.AsInt64(); !ok {
		klog.V(4).InfoS("node resource's quantity is not int64 and will be rounded",
			"node", node.Name, "resource", name, "original", *q, "rounded", q.Value())
		q.Set(q.Value())
	}
	node.Status.Capacity[name] = *q
	node.Status.Allocatable[name] = *q
}

// GetPodMetricUsage gets pod usage from the PodMetricInfo
func GetPodMetricUsage(info *slov1alpha1.PodMetricInfo) corev1.ResourceList {
	return GetResourceListForCPUAndMemory(info.PodUsage.ResourceList)
}

func GetHostAppHPUsed(resourceMetrics *framework.ResourceMetrics, resPriority extension.PriorityClass) corev1.ResourceList {
	hostAppHPUsed := util.NewZeroResourceList()
	for _, hostAppMetric := range resourceMetrics.NodeMetric.Status.HostApplicationMetric {
		if extension.GetDefaultPriorityByPriorityClass(hostAppMetric.Priority) <= extension.GetDefaultPriorityByPriorityClass(resPriority) {
			// consider higher priority usage for mid or batch allocatable
			// now only support product and batch(hadoop-yarn) priority for host application
			continue
		}
		hostAppHPUsed = quotav1.Add(hostAppHPUsed, GetHostAppMetricUsage(hostAppMetric))
	}
	return hostAppHPUsed
}

// GetHostAppMetricUsage gets host application usage from HostApplicationMetricInfo
func GetHostAppMetricUsage(info *slov1alpha1.HostApplicationMetricInfo) corev1.ResourceList {
	return GetResourceListForCPUAndMemory(info.Usage.ResourceList)
}

// GetPodNUMARequestAndUsage returns the pod request and usage on each NUMA nodes.
// It averages the metrics over all sharepools when the pod does not allocate any sharepool or use all sharepools.
func GetPodNUMARequestAndUsage(pod *corev1.Pod, podRequest, podUsage corev1.ResourceList, numaNum int) ([]corev1.ResourceList, []corev1.ResourceList) {
	// get pod NUMA allocation
	var podAlloc *extension.ResourceStatus
	if pod.Annotations == nil {
		podAlloc = &extension.ResourceStatus{}
	} else if podAllocFromAnnotations, err := extension.GetResourceStatus(pod.Annotations); err != nil {
		podAlloc = &extension.ResourceStatus{}
		klog.V(5).Infof("failed to get NUMA resource status of the pod %s, suppose it is LS, err: %s",
			util.GetPodKey(pod), err)
	} else {
		podAlloc = podAllocFromAnnotations
	}

	// NOTE: For the pod which does not set NUMA-aware allocation policy, it has set particular cpuset cpus but may not
	//       have NUMA allocation information in annotations. In this case, it can be inaccurate to average the
	//       request/usage over all NUMA nodes.
	podNUMARequest := make([]corev1.ResourceList, numaNum)
	podNUMAUsage := make([]corev1.ResourceList, numaNum)

	allocatedNUMAMap := map[int]struct{}{}
	allocatedNUMANum := 0 // the number of allocated NUMA node
	for _, numaResource := range podAlloc.NUMANodeResources {
		allocatedNUMAMap[int(numaResource.Node)] = struct{}{}
		// The invalid allocated NUMA ids will be ignored since it cannot be successfully bind on the node either.
		if int(numaResource.Node) < numaNum && numaResource.Node >= 0 {
			allocatedNUMANum++
		}
	}

	if allocatedNUMANum <= 0 { // share all NUMAs
		for i := 0; i < numaNum; i++ {
			podNUMARequest[i] = DivideResourceList(podRequest, float64(numaNum))
			podNUMAUsage[i] = DivideResourceList(podUsage, float64(numaNum))
		}
	} else {
		for i := 0; i < numaNum; i++ {
			_, ok := allocatedNUMAMap[i]
			if !ok {
				podNUMARequest[i] = util.NewZeroResourceList()
				podNUMAUsage[i] = util.NewZeroResourceList()
				continue
			}

			podNUMARequest[i] = DivideResourceList(podRequest, float64(allocatedNUMANum))
			podNUMAUsage[i] = DivideResourceList(podUsage, float64(allocatedNUMANum))
		}
	}

	return podNUMARequest, podNUMAUsage
}

func GetPodUnknownNUMAUsage(podUsage corev1.ResourceList, numaNum int) []corev1.ResourceList {
	if numaNum <= 0 {
		return nil
	}
	podNUMAUsage := make([]corev1.ResourceList, numaNum)
	for i := 0; i < numaNum; i++ {
		podNUMAUsage[i] = DivideResourceList(podUsage, float64(numaNum))
	}
	return podNUMAUsage
}

// GetNodeCapacity gets node capacity and filters out non-CPU and non-Mem resources
func GetNodeCapacity(node *corev1.Node) corev1.ResourceList {
	return GetResourceListForCPUAndMemory(node.Status.Capacity)
}

// GetNodeSafetyMargin gets node-level safe-guarding reservation with the node's allocatable
func GetNodeSafetyMargin(strategy *configuration.ColocationStrategy, nodeCapacity corev1.ResourceList) corev1.ResourceList {
	cpuReserveQuant := util.MultiplyMilliQuant(nodeCapacity[corev1.ResourceCPU], getReserveRatio(*strategy.CPUReclaimThresholdPercent))
	memReserveQuant := util.MultiplyQuant(nodeCapacity[corev1.ResourceMemory], getReserveRatio(*strategy.MemoryReclaimThresholdPercent))

	return corev1.ResourceList{
		corev1.ResourceCPU:    cpuReserveQuant,
		corev1.ResourceMemory: memReserveQuant,
	}
}

func DivideResourceList(rl corev1.ResourceList, divisor float64) corev1.ResourceList {
	if divisor == 0 {
		return rl
	}
	divided := corev1.ResourceList{}
	for resourceName, q := range rl {
		divided[resourceName] = *resource.NewMilliQuantity(int64(math.Ceil(float64(q.MilliValue())/divisor)), q.Format)
	}
	return divided
}

func zoneResourceListHandler(a, b []corev1.ResourceList, zoneNum int,
	handleFn func(a corev1.ResourceList, b corev1.ResourceList) corev1.ResourceList) []corev1.ResourceList {
	// assert len(a) == len(b) == zoneNum
	result := make([]corev1.ResourceList, zoneNum)
	for i := 0; i < zoneNum; i++ {
		result[i] = handleFn(a[i], b[i])
	}
	return result
}

func AddZoneResourceList(a, b []corev1.ResourceList, zoneNum int) []corev1.ResourceList {
	return zoneResourceListHandler(a, b, zoneNum, quotav1.Add)
}

func MaxZoneResourceList(a, b []corev1.ResourceList, zoneNum int) []corev1.ResourceList {
	return zoneResourceListHandler(a, b, zoneNum, quotav1.Max)
}

func GetResourceListForCPUAndMemory(rl corev1.ResourceList) corev1.ResourceList {
	return quotav1.Mask(rl, []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory})
}

func MixResourceListCPUAndMemory(resourcesForCPU, resourcesForMemory corev1.ResourceList) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resourcesForCPU[corev1.ResourceCPU],
		corev1.ResourceMemory: resourcesForMemory[corev1.ResourceMemory],
	}
}

func MinxZoneResourceListCPUAndMemory(resourcesForCPU, resourcesForMemory []corev1.ResourceList, zoneNum int) []corev1.ResourceList {
	// assert len(a) == len(b) == zoneNum
	result := make([]corev1.ResourceList, zoneNum)
	for i := 0; i < zoneNum; i++ {
		result[i] = MixResourceListCPUAndMemory(resourcesForCPU[i], resourcesForMemory[i])
	}
	return result
}

// getReserveRatio returns resource reserved ratio
func getReserveRatio(reclaimThreshold int64) float64 {
	return float64(100-reclaimThreshold) / 100.0
}

func UpdateNRTZoneListIfNeeded(node *corev1.Node, zoneList topologyv1alpha1.ZoneList, nr *framework.NodeResource, diffThreshold float64) bool {
	ratio, err := getCPUNormalizationRatio(nr)
	if err != nil {
		klog.V(5).InfoS("failed to get cpu normalization ratio for zone resources",
			"node", node.Name, "err", err)
	}

	needUpdate := false
	for i := range zoneList {
		zone := zoneList[i]
		zoneResource, ok := nr.ZoneResources[zone.Name]
		if !ok { // the resources of the zone should be reset
			for _, resourceInfo := range zone.Resources {
				if !updateNRTResourceSet.Has(resourceInfo.Name) {
					continue
				}
				// FIXME: currently we set value to zero instead of deleting resource
				if resourceInfo.Capacity.IsZero() && resourceInfo.Allocatable.IsZero() &&
					resourceInfo.Available.IsZero() { // already reset
					continue
				}
				needUpdate = true
				resourceInfo.Capacity = *resource.NewQuantity(0, resourceInfo.Capacity.Format)
				resourceInfo.Allocatable = *resource.NewQuantity(0, resourceInfo.Allocatable.Format)
				resourceInfo.Available = *resource.NewQuantity(0, resourceInfo.Available.Format)
				klog.V(6).InfoS("reset batch resource for zone", "node", node.Name,
					"zone", zone.Name, "resource", resourceInfo.Name)
			}
		}

		for resourceName := range zoneResource {
			quantity := zoneResource[resourceName]

			// amplify batch cpu according to cpu normalization ratio
			if resourceName == extension.BatchCPU && ratio > 1.0 {
				quantity = util.MultiplyMilliQuant(quantity, ratio)
			}

			oldHasResource := false
			for j, resourceInfo := range zone.Resources {
				if resourceInfo.Name == string(resourceName) { // old has the resource key
					if util.IsQuantityDiff(resourceInfo.Capacity, quantity, diffThreshold) {
						needUpdate = true
						resourceInfo.Capacity = quantity
					}
					if util.IsQuantityDiff(resourceInfo.Allocatable, quantity, diffThreshold) {
						needUpdate = true
						resourceInfo.Allocatable = quantity
					}
					if util.IsQuantityDiff(resourceInfo.Available, quantity, diffThreshold) {
						needUpdate = true
						resourceInfo.Available = quantity
					}
					zone.Resources[j] = resourceInfo
					oldHasResource = true
					klog.V(6).InfoS("update batch resource for zone", "node", node.Name,
						"zone", zone.Name, "resource", resourceName)
					break
				}
			}
			if !oldHasResource { // old has no resource key
				needUpdate = true
				zone.Resources = append(zone.Resources, topologyv1alpha1.ResourceInfo{
					Name:        string(resourceName),
					Capacity:    quantity,
					Allocatable: quantity,
					Available:   quantity,
				})
				sort.Slice(zone.Resources, func(p, q int) bool { // keep the resources order
					return zone.Resources[p].Name < zone.Resources[q].Name
				})
				klog.V(6).InfoS("add batch resource for zone", "node", node.Name,
					"zone", zone.Name, "resource", resourceName)
			}
		}

		zoneList[i] = zone
	}

	return needUpdate
}

func getCPUNormalizationRatio(nr *framework.NodeResource) (float64, error) {
	ratioStr, ok := nr.Annotations[extension.AnnotationCPUNormalizationRatio]
	if !ok {
		return -1, nil
	}
	ratio, err := strconv.ParseFloat(ratioStr, 64)
	if err != nil {
		return -1, fmt.Errorf("failed to parse ratio in NodeResource, err: %w", err)
	}
	return ratio, nil
}

func getPercentFromStrategy(strategy, defaultStrategy *configuration.ColocationStrategy, strategyType string) float64 {
	switch strategyType {
	case MidCPUThreshold:
		if strategy == nil || strategy.MidCPUThresholdPercent == nil {
			return float64(*defaultStrategy.MidCPUThresholdPercent) / 100
		}
		return float64(*strategy.MidCPUThresholdPercent) / 100
	case MidMemoryThreshold:
		if strategy == nil || strategy.MidMemoryThresholdPercent == nil {
			return float64(*defaultStrategy.MidMemoryThresholdPercent) / 100
		}
		return float64(*strategy.MidMemoryThresholdPercent) / 100
	case MidUnallocatedPercent:
		if strategy == nil || strategy.MidUnallocatedPercent == nil {
			return float64(*defaultStrategy.MidUnallocatedPercent) / 100
		}
		return float64(*strategy.MidUnallocatedPercent) / 100
	default:
		return 0
	}
}

func IsValidNodeUsage(nodeMetric *slov1alpha1.NodeMetric) (bool, string) {
	if nodeMetric == nil || nodeMetric.Status.NodeMetric == nil || nodeMetric.Status.NodeMetric.NodeUsage.ResourceList == nil {
		return false, "node metric is incomplete"
	}
	_, ok := nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[corev1.ResourceCPU]
	if !ok {
		return false, "cpu usage is missing"
	}
	_, ok = nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[corev1.ResourceMemory]
	if !ok {
		return false, "memory usage is missing"
	}
	return true, ""
}

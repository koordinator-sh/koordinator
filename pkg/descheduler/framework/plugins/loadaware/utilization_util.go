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

package loadaware

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	slolisters "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	nodeutil "github.com/koordinator-sh/koordinator/pkg/descheduler/node"
	podutil "github.com/koordinator-sh/koordinator/pkg/descheduler/pod"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils/sorter"
)

type Percentage = deschedulerconfig.Percentage
type ResourceThresholds = deschedulerconfig.ResourceThresholds

type NodeUsage struct {
	node       *corev1.Node
	allPods    []*corev1.Pod
	prodPods   []*corev1.Pod
	usage      map[corev1.ResourceName]*resource.Quantity
	prodUsage  map[corev1.ResourceName]*resource.Quantity
	podMetrics map[types.NamespacedName]*slov1alpha1.ResourceMap
}

type NodeThresholds struct {
	lowResourceThreshold      map[corev1.ResourceName]*resource.Quantity
	highResourceThreshold     map[corev1.ResourceName]*resource.Quantity
	prodLowResourceThreshold  map[corev1.ResourceName]*resource.Quantity
	prodHighResourceThreshold map[corev1.ResourceName]*resource.Quantity
}

type NodeInfo struct {
	*NodeUsage
	thresholds NodeThresholds
}

type continueEvictionCond func(nodeInfo NodeInfo, totalAvailableUsages map[corev1.ResourceName]*resource.Quantity, prod bool) bool

type evictionReasonGeneratorFn func(nodeInfo NodeInfo, prod bool) string

const (
	MinResourcePercentage = 0
	MaxResourcePercentage = 100
)

func normalizePercentage(percent Percentage) Percentage {
	if percent > MaxResourcePercentage {
		return MaxResourcePercentage
	}
	if percent < MinResourcePercentage {
		return MinResourcePercentage
	}
	return percent
}

func getNodeThresholds(
	nodeUsages map[string]*NodeUsage,
	lowThreshold, highThreshold, prodLowThreshold, prodHighThreshold ResourceThresholds,
	resourceNames []corev1.ResourceName,
	useDeviationThresholds bool,
) map[string]NodeThresholds {
	var averageResourceUsagePercent, prodAverageResourceUsagePercent ResourceThresholds
	if useDeviationThresholds {
		averageResourceUsagePercent, prodAverageResourceUsagePercent = calcAverageResourceUsagePercent(nodeUsages)
		klog.V(4).InfoS("useDeviationThresholds", "node", averageResourceUsagePercent, "prod", prodAverageResourceUsagePercent)
	}

	nodeThresholdsMap := map[string]NodeThresholds{}
	for _, nodeUsage := range nodeUsages {
		thresholds := NodeThresholds{
			lowResourceThreshold:      map[corev1.ResourceName]*resource.Quantity{},
			highResourceThreshold:     map[corev1.ResourceName]*resource.Quantity{},
			prodLowResourceThreshold:  map[corev1.ResourceName]*resource.Quantity{},
			prodHighResourceThreshold: map[corev1.ResourceName]*resource.Quantity{},
		}
		allocatable := nodeUsage.node.Status.Allocatable
		for _, resourceName := range resourceNames {
			if useDeviationThresholds {
				resourceCapacity := allocatable[resourceName]
				if lowThreshold[resourceName] == MinResourcePercentage {
					thresholds.lowResourceThreshold[resourceName] = &resourceCapacity
					thresholds.highResourceThreshold[resourceName] = &resourceCapacity
				} else {
					thresholds.lowResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, normalizePercentage(averageResourceUsagePercent[resourceName]-lowThreshold[resourceName]))
					thresholds.highResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, normalizePercentage(averageResourceUsagePercent[resourceName]+highThreshold[resourceName]))
				}
				if prodLowThreshold[resourceName] == MinResourcePercentage {
					thresholds.prodLowResourceThreshold[resourceName] = &resourceCapacity
					thresholds.prodHighResourceThreshold[resourceName] = &resourceCapacity
				} else {
					thresholds.prodLowResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, normalizePercentage(prodAverageResourceUsagePercent[resourceName]-prodLowThreshold[resourceName]))
					thresholds.prodHighResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, normalizePercentage(prodAverageResourceUsagePercent[resourceName]+prodHighThreshold[resourceName]))
				}
			} else {
				thresholds.lowResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, lowThreshold[resourceName])
				thresholds.highResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, highThreshold[resourceName])
				thresholds.prodLowResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, prodLowThreshold[resourceName])
				thresholds.prodHighResourceThreshold[resourceName] = resourceThreshold(allocatable, resourceName, prodHighThreshold[resourceName])
			}
		}
		nodeThresholdsMap[nodeUsage.node.Name] = thresholds
	}
	return nodeThresholdsMap
}

func resourceThreshold(nodeCapacity corev1.ResourceList, resourceName corev1.ResourceName, threshold Percentage) *resource.Quantity {
	resourceCapacityFraction := func(resourceNodeCapacity int64) int64 {
		// A threshold is in percentages but in <0;100> interval.
		// Performing `threshold * 0.01` will convert <0;100> interval into <0;1>.
		// Multiplying it with capacity will give fraction of the capacity corresponding to the given resource threshold in Quantity units.
		return int64(float64(threshold) * 0.01 * float64(resourceNodeCapacity))
	}

	resourceCapacityQuantity := nodeCapacity[resourceName]
	if resourceName == corev1.ResourceCPU {
		return resource.NewMilliQuantity(resourceCapacityFraction(resourceCapacityQuantity.MilliValue()), resourceCapacityQuantity.Format)
	}
	return resource.NewQuantity(resourceCapacityFraction(resourceCapacityQuantity.Value()), resourceCapacityQuantity.Format)
}

func getNodeUsage(nodes []*corev1.Node, resourceNames []corev1.ResourceName, nodeMetricLister slolisters.NodeMetricLister, getPodsAssignedToNode podutil.GetPodsAssignedToNodeFunc, nodeMetricExpirationSeconds *int64) map[string]*NodeUsage {
	nodeUsages := map[string]*NodeUsage{}
	for _, v := range nodes {
		pods, err := podutil.ListPodsOnANode(v.Name, getPodsAssignedToNode, nil)
		if err != nil {
			klog.ErrorS(err, "Node will not be processed, error accessing its pods", "node", klog.KObj(v))
			continue
		}
		prodPods := make([]*corev1.Pod, 0)
		prodPodsMap := make(map[string]*corev1.Pod)
		for _, pod := range pods {
			if extension.GetPodPriorityClassWithDefault(pod) == extension.PriorityProd {
				prodPods = append(prodPods, pod)
				podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
				prodPodsMap[podKey] = pod
			}
		}

		nodeMetric, err := nodeMetricLister.Get(v.Name)
		if err != nil {
			klog.ErrorS(err, "Failed to get NodeMetric", "node", klog.KObj(v))
			continue
		}
		// We should check if NodeMetric is expired.
		if nodeMetric.Status.NodeMetric == nil || nodeMetricExpirationSeconds != nil &&
			isNodeMetricExpired(nodeMetric.Status.UpdateTime, *nodeMetricExpirationSeconds) {
			klog.ErrorS(err, "NodeMetric has expired", "node", klog.KObj(v), "effective period", time.Duration(*nodeMetricExpirationSeconds)*time.Second)
			continue
		}

		usage := map[corev1.ResourceName]*resource.Quantity{}
		prodUsage := map[corev1.ResourceName]*resource.Quantity{}
		for _, resourceName := range resourceNames {
			sysUsage := nodeMetric.Status.NodeMetric.SystemUsage.ResourceList[resourceName]
			var podUsage, prodPodUsage resource.Quantity
			for _, podMetricInfo := range nodeMetric.Status.PodsMetric {
				podUsage.Add(podMetricInfo.PodUsage.ResourceList[resourceName])
				podKey := fmt.Sprintf("%s/%s", podMetricInfo.Namespace, podMetricInfo.Name)
				if _, ok := prodPodsMap[podKey]; ok {
					prodPodUsage.Add(podMetricInfo.PodUsage.ResourceList[resourceName])
				}
			}
			var usageQuantity resource.Quantity
			usageQuantity.Add(sysUsage)
			usageQuantity.Add(podUsage)

			usageQuantity = ResetResourceUsageIsZero(resourceName, usageQuantity)
			prodPodUsage = ResetResourceUsageIsZero(resourceName, prodPodUsage)
			usage[resourceName] = &usageQuantity
			prodUsage[resourceName] = &prodPodUsage
		}
		usage[corev1.ResourcePods] = resource.NewQuantity(int64(len(pods)), resource.DecimalSI)
		prodUsage[corev1.ResourcePods] = resource.NewQuantity(int64(len(prodPods)), resource.DecimalSI)

		podMetrics := make(map[types.NamespacedName]*slov1alpha1.ResourceMap)
		for _, podMetric := range nodeMetric.Status.PodsMetric {
			podMetrics[types.NamespacedName{Namespace: podMetric.Namespace, Name: podMetric.Name}] = podMetric.PodUsage.DeepCopy()
		}

		nodeUsages[v.Name] = &NodeUsage{
			node:       v,
			allPods:    pods,
			usage:      usage,
			prodUsage:  prodUsage,
			prodPods:   prodPods,
			podMetrics: podMetrics,
		}
	}

	return nodeUsages
}

func ResetResourceUsageIsZero(resourceName corev1.ResourceName, usageQuantity resource.Quantity) resource.Quantity {
	if usageQuantity.IsZero() {
		switch resourceName {
		case corev1.ResourceCPU:
			usageQuantity = *resource.NewMilliQuantity(0, resource.DecimalSI)
		case corev1.ResourceMemory, corev1.ResourceEphemeralStorage, corev1.ResourceStorage:
			usageQuantity = *resource.NewQuantity(0, resource.BinarySI)
		default:
			usageQuantity = *resource.NewQuantity(0, resource.DecimalSI)
		}
		return usageQuantity
	}
	return usageQuantity
}

// classifyNodes classifies the nodes into low-utilization or high-utilization nodes.
// If a node lies between low and high thresholds, it is simply ignored.
func classifyNodes(
	nodeUsages map[string]*NodeUsage,
	nodeThresholds map[string]NodeThresholds,
	lowThresholdFilter, highThresholdFilter, prodLowThresholdFilter, prodHighThresholdFilter func(usage *NodeUsage, threshold NodeThresholds) bool,
) (lowNodes []NodeInfo, highNodes []NodeInfo, prodLowNodes []NodeInfo, prodHighNodes []NodeInfo, bothLowNodes []NodeInfo) {
	for _, nodeUsage := range nodeUsages {
		nodeInfo := NodeInfo{
			NodeUsage:  nodeUsage,
			thresholds: nodeThresholds[nodeUsage.node.Name],
		}
		nodeUsageExplain := ""
		if lowThresholdFilter(nodeUsage, nodeThresholds[nodeUsage.node.Name]) {
			if prodHighThresholdFilter(nodeUsage, nodeThresholds[nodeUsage.node.Name]) {
				prodHighNodes = append(prodHighNodes, nodeInfo)
				nodeUsageExplain = "lower than node usage but high than prod usage"
			} else if prodLowThresholdFilter(nodeUsage, nodeThresholds[nodeUsage.node.Name]) {
				bothLowNodes = append(bothLowNodes, nodeInfo)
				nodeUsageExplain = "both lower than node && prod usage"
			} else {
				lowNodes = append(lowNodes, nodeInfo)
				nodeUsageExplain = "lower than node usage and it's appropriately for prod usage"
			}
			klog.V(4).InfoS("Node's utilization", "node", klog.KObj(nodeUsage.node), "result information", nodeUsageExplain, "node usage", nodeUsage.usage, "node usagePercentage", resourceUsagePercentages(nodeUsage, false),
				"node high threshold", nodeThresholds[nodeUsage.node.Name].highResourceThreshold, "node low threshold", nodeThresholds[nodeUsage.node.Name].lowResourceThreshold, "prod usage", nodeUsage.prodUsage,
				"prod usagePercentage", resourceUsagePercentages(nodeUsage, true), "prod high threshold", nodeThresholds[nodeUsage.node.Name].prodHighResourceThreshold, "prod low threshold", nodeThresholds[nodeUsage.node.Name].prodLowResourceThreshold)
		} else if highThresholdFilter(nodeUsage, nodeThresholds[nodeUsage.node.Name]) {
			highNodes = append(highNodes, nodeInfo)
			nodeUsageExplain = "higher than node usage"
			klog.V(4).InfoS("Node's utilization", "node", klog.KObj(nodeUsage.node), "result information", nodeUsageExplain, "node usage", nodeUsage.usage, "node usagePercentage", resourceUsagePercentages(nodeUsage, false),
				"node high threshold", nodeThresholds[nodeUsage.node.Name].highResourceThreshold, "node low threshold", nodeThresholds[nodeUsage.node.Name].lowResourceThreshold, "prod usage", nodeUsage.prodUsage,
				"prod usagePercentage", resourceUsagePercentages(nodeUsage, true), "prod high threshold", nodeThresholds[nodeUsage.node.Name].prodHighResourceThreshold, "prod low threshold", nodeThresholds[nodeUsage.node.Name].prodLowResourceThreshold)
		} else {
			if prodHighThresholdFilter(nodeUsage, nodeThresholds[nodeUsage.node.Name]) {
				prodHighNodes = append(prodHighNodes, nodeInfo)
				nodeUsageExplain = "appropriately for node usage but higher than prod usage"
			} else if prodLowThresholdFilter(nodeUsage, nodeThresholds[nodeUsage.node.Name]) {
				prodLowNodes = append(prodLowNodes, nodeInfo)
				nodeUsageExplain = "appropriately for node usage but lower than prod usage"
			} else {
				nodeUsageExplain = "both appropriately for node && prod usage"
			}
			klog.V(4).InfoS("Node's utilization", "node", klog.KObj(nodeUsage.node), "result information", nodeUsageExplain, "node usage", nodeUsage.usage, "node usagePercentage", resourceUsagePercentages(nodeUsage, false),
				"node high threshold", nodeThresholds[nodeUsage.node.Name].highResourceThreshold, "node low threshold", nodeThresholds[nodeUsage.node.Name].lowResourceThreshold, "prod usage", nodeUsage.prodUsage,
				"prod usagePercentage", resourceUsagePercentages(nodeUsage, true), "prod high threshold", nodeThresholds[nodeUsage.node.Name].prodHighResourceThreshold, "prod low threshold", nodeThresholds[nodeUsage.node.Name].prodLowResourceThreshold)
		}
	}

	return lowNodes, highNodes, prodLowNodes, prodHighNodes, bothLowNodes
}

func resourceUsagePercentages(nodeUsage *NodeUsage, prod bool) map[corev1.ResourceName]float64 {
	allocatable := nodeUsage.node.Status.Allocatable
	resourceUsagePercentage := map[corev1.ResourceName]float64{}
	var usage map[corev1.ResourceName]*resource.Quantity
	if prod {
		usage = nodeUsage.prodUsage
	} else {
		usage = nodeUsage.usage
	}
	for resourceName, resourceUsage := range usage {
		resourceCapacity := allocatable[resourceName]
		if !resourceCapacity.IsZero() {
			resourceUsagePercentage[resourceName] = 100 * float64(resourceUsage.MilliValue()) / float64(resourceCapacity.MilliValue())
		}
	}

	return resourceUsagePercentage
}

func evictPodsFromSourceNodes(
	ctx context.Context,
	nodePoolName string,
	sourceNodes, destinationNodes,
	prodSourceNodes, prodDestinationNodes, bothDestinationNodes []NodeInfo,
	nodeUsages map[string]*NodeUsage,
	nodeThresholds map[string]NodeThresholds,
	dryRun bool,
	nodeFit bool,
	resourceWeights map[corev1.ResourceName]int64,
	podEvictor framework.Evictor,
	podFilter framework.FilterFunc,
	nodeIndexer podutil.GetPodsAssignedToNodeFunc,
	resourceNames []corev1.ResourceName,
	continueEviction continueEvictionCond,
	evictionReasonGenerator evictionReasonGeneratorFn,
) {
	totalAvailableUsages, targetNodes := targetAvailableUsage(destinationNodes, resourceNames, false)
	prodAvailableUsages, prodTargetNodes := targetAvailableUsage(prodDestinationNodes, resourceNames, true)
	bothTotalAvailableUsage, bothTotalNodes := targetAvailableUsage(bothDestinationNodes, resourceNames, false)
	prodBothAvailableUsage, prodBothTotalNodes := targetAvailableUsage(bothDestinationNodes, resourceNames, true)
	klog.V(4).InfoS("node pool availableUsage", "onlyNodeTotal", totalAvailableUsages, "onlyProdOnly", prodAvailableUsages,
		"bothLowNodesTotal", bothTotalAvailableUsage, "bothLowProdTotal", prodBothAvailableUsage)

	nodeTotalAvailableUsages := newAvailableUsage(resourceNames)
	for _, resourceName := range resourceNames {
		if quantity, ok := nodeTotalAvailableUsages[resourceName]; ok {
			if _, totalOk := totalAvailableUsages[resourceName]; totalOk {
				quantity.Add(*totalAvailableUsages[resourceName])
			}
			if _, bothOk := bothTotalAvailableUsage[resourceName]; bothOk {
				quantity.Add(*bothTotalAvailableUsage[resourceName])
			}
		}
	}
	nodeKeysAndValues := []interface{}{
		"nodePool", nodePoolName,
	}
	for resourceName, quantity := range nodeTotalAvailableUsages {
		nodeKeysAndValues = append(nodeKeysAndValues, string(resourceName), quantity.String())
	}
	klog.V(4).InfoS("Total node usage capacity to be moved", nodeKeysAndValues...)

	targetNodes = append(targetNodes, bothTotalNodes...)
	balancePods(ctx, nodePoolName, sourceNodes, targetNodes, nodeUsages, nodeThresholds,
		nodeTotalAvailableUsages, dryRun, nodeFit, false, resourceWeights, podEvictor,
		podFilter, nodeIndexer, continueEviction, evictionReasonGenerator)

	// bothLowNode will be used by nodeHigh and prodHigh nodes, needs sub resources used by pods on nodeHigh.
	for _, resourceName := range resourceNames {
		if quantity, ok := nodeTotalAvailableUsages[resourceName]; ok {
			// A part of bothTotalAvailableUsage has been used,
			// then the remaining part of nodeTotalAvailableUsage can be utilized.
			if bothTotalAvailableUsage[resourceName].Cmp(*quantity) > 0 {
				bothTotalAvailableUsage[resourceName] = quantity
			}
		}
	}

	prodTotalAvailableUsages := newAvailableUsage(resourceNames)
	for _, resourceName := range resourceNames {
		if prodTotalQuantity, ok := prodTotalAvailableUsages[resourceName]; ok {
			if _, prodOk := prodAvailableUsages[resourceName]; prodOk {
				prodTotalQuantity.Add(*prodAvailableUsages[resourceName])
			}
			// add min(prodBothAvailableUsage, bothTotalAvailableUsage) to prodTotalAvailableUsages
			if _, prodBothOk := prodBothAvailableUsage[resourceName]; prodBothOk {
				if prodBothAvailableUsage[resourceName].Cmp(*bothTotalAvailableUsage[resourceName]) > 0 {
					prodTotalQuantity.Add(*bothTotalAvailableUsage[resourceName])
				} else {
					prodTotalQuantity.Add(*prodBothAvailableUsage[resourceName])
				}
			}
		}
	}
	prodTargetNodes = append(prodTargetNodes, prodBothTotalNodes...)
	prodKeysAndValues := []interface{}{
		"nodePool", nodePoolName,
	}
	for resourceName, quantity := range prodTotalAvailableUsages {
		prodKeysAndValues = append(prodKeysAndValues, string(resourceName), quantity.String())
	}
	klog.V(4).InfoS("Total prod usage capacity to be moved", prodKeysAndValues...)
	balancePods(ctx, nodePoolName, prodSourceNodes, prodTargetNodes, nodeUsages, nodeThresholds,
		prodTotalAvailableUsages, dryRun, nodeFit, true, resourceWeights, podEvictor,
		podFilter, nodeIndexer, continueEviction, evictionReasonGenerator)
}

func newAvailableUsage(resourceNames []corev1.ResourceName) map[corev1.ResourceName]*resource.Quantity {
	availableUsage := make(map[corev1.ResourceName]*resource.Quantity)
	for _, resourceName := range resourceNames {
		var quantity *resource.Quantity
		switch resourceName {
		case corev1.ResourceCPU:
			quantity = resource.NewMilliQuantity(0, resource.DecimalSI)
		case corev1.ResourceMemory, corev1.ResourceEphemeralStorage, corev1.ResourceStorage:
			quantity = resource.NewQuantity(0, resource.BinarySI)
		default:
			quantity = resource.NewQuantity(0, resource.DecimalSI)
		}
		availableUsage[resourceName] = quantity
	}
	return availableUsage
}

func balancePods(ctx context.Context,
	nodePoolName string,
	sourceNodes []NodeInfo,
	targetNodes []*corev1.Node,
	nodeUsages map[string]*NodeUsage,
	nodeThresholds map[string]NodeThresholds,
	totalAvailableUsages map[corev1.ResourceName]*resource.Quantity,
	dryRun bool,
	nodeFit, prod bool,
	resourceWeights map[corev1.ResourceName]int64,
	podEvictor framework.Evictor,
	podFilter framework.FilterFunc,
	nodeIndexer podutil.GetPodsAssignedToNodeFunc,
	continueEviction continueEvictionCond,
	evictionReasonGenerator evictionReasonGeneratorFn) {
	for _, srcNode := range sourceNodes {
		var allPods []*corev1.Pod
		if prod {
			allPods = srcNode.prodPods
		} else {
			allPods = srcNode.allPods
		}
		nonRemovablePods, removablePods := classifyPods(
			allPods,
			podutil.WrapFilterFuncs(podFilter, func(pod *corev1.Pod) bool {
				if !nodeFit {
					return true
				}
				podNamespacedName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
				podMetric := srcNode.podMetrics[podNamespacedName]
				return podFitsAnyNodeWithThreshold(nodeIndexer, pod, targetNodes, nodeUsages, nodeThresholds, prod, podMetric)
			}),
		)
		klog.V(4).InfoS("Evicting pods from node",
			"nodePool", nodePoolName, "node", klog.KObj(srcNode.node), "prod", prod, "usage", srcNode.usage,
			"allPods", len(allPods), "nonRemovablePods", len(nonRemovablePods), "removablePods", len(removablePods))

		if len(removablePods) == 0 {
			klog.V(4).InfoS("No removable pods on node, try next node", "node", klog.KObj(srcNode.node), "nodePool", nodePoolName)
			continue
		}
		sortPodsOnOneOverloadedNode(srcNode, removablePods, resourceWeights, prod)

		evictPods(ctx, nodePoolName, dryRun, prod, removablePods, srcNode, totalAvailableUsages, podEvictor, podFilter, continueEviction, evictionReasonGenerator)
	}
}

func targetAvailableUsage(destinationNodes []NodeInfo, resourceNames []corev1.ResourceName, prod bool) (map[corev1.ResourceName]*resource.Quantity, []*corev1.Node) {
	var targetNodes []*corev1.Node
	totalAvailableUsages := map[corev1.ResourceName]*resource.Quantity{}
	for _, resourceName := range resourceNames {
		var quantity *resource.Quantity
		switch resourceName {
		case corev1.ResourceCPU:
			quantity = resource.NewMilliQuantity(0, resource.DecimalSI)
		case corev1.ResourceMemory, corev1.ResourceEphemeralStorage, corev1.ResourceStorage:
			quantity = resource.NewQuantity(0, resource.BinarySI)
		default:
			quantity = resource.NewQuantity(0, resource.DecimalSI)
		}
		totalAvailableUsages[resourceName] = quantity
	}

	for _, destinationNode := range destinationNodes {
		targetNodes = append(targetNodes, destinationNode.node)
		for _, resourceName := range resourceNames {
			if prod {
				totalAvailableUsages[resourceName].Add(*destinationNode.thresholds.prodHighResourceThreshold[resourceName])
				totalAvailableUsages[resourceName].Sub(*destinationNode.prodUsage[resourceName])
			} else {
				totalAvailableUsages[resourceName].Add(*destinationNode.thresholds.highResourceThreshold[resourceName])
				totalAvailableUsages[resourceName].Sub(*destinationNode.usage[resourceName])
			}
		}
	}

	return totalAvailableUsages, targetNodes
}

func evictPods(
	ctx context.Context,
	nodePoolName string,
	dryRun bool,
	prod bool,
	inputPods []*corev1.Pod,
	nodeInfo NodeInfo,
	totalAvailableUsages map[corev1.ResourceName]*resource.Quantity,
	podEvictor framework.Evictor,
	podFilter framework.FilterFunc,
	continueEviction continueEvictionCond,
	evictionReasonGenerator evictionReasonGeneratorFn,
) {
	for _, pod := range inputPods {
		if !continueEviction(nodeInfo, totalAvailableUsages, prod) {
			return
		}

		if !podFilter(pod) {
			klog.V(4).InfoS("Pod aborted eviction because it was filtered by filters", "pod", klog.KObj(pod), "node", klog.KObj(nodeInfo.node), "nodePool", nodePoolName)
			continue
		}
		if dryRun {
			klog.InfoS("Evict pod in dry run mode", "pod", klog.KObj(pod), "node", klog.KObj(nodeInfo.node), "nodePool", nodePoolName)
		} else {
			evictionOptions := framework.EvictOptions{
				Reason: evictionReasonGenerator(nodeInfo, prod),
			}
			if !podEvictor.Evict(ctx, pod, evictionOptions) {
				klog.InfoS("Failed to Evict Pod", "pod", klog.KObj(pod), "node", klog.KObj(nodeInfo.node), "nodePool", nodePoolName)
				continue
			}
			klog.InfoS("Evicted Pod", "pod", klog.KObj(pod), "node", klog.KObj(nodeInfo.node), "nodePool", nodePoolName)
		}

		podMetric := nodeInfo.podMetrics[types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}]
		if podMetric == nil {
			klog.V(4).InfoS("Failed to find PodMetric", "pod", klog.KObj(pod), "node", klog.KObj(nodeInfo.node), "nodePool", nodePoolName)
			continue
		}
		for resourceName, availableUsage := range totalAvailableUsages {
			var quantity resource.Quantity
			if resourceName == corev1.ResourcePods {
				quantity = *resource.NewQuantity(1, resource.DecimalSI)
			} else {
				quantity = podMetric.ResourceList[resourceName]
			}
			availableUsage.Sub(quantity)
			if nodeUsage := nodeInfo.usage[resourceName]; nodeUsage != nil {
				nodeUsage.Sub(quantity)
			}
			if prodUsage := nodeInfo.prodUsage[resourceName]; prod && prodUsage != nil {
				prodUsage.Sub(quantity)
			}
		}

		keysAndValues := []interface{}{
			"node", nodeInfo.node.Name,
			"nodePool", nodePoolName,
		}
		usage := nodeInfo.usage
		if prod {
			usage = nodeInfo.prodUsage
		}
		for k, v := range usage {
			keysAndValues = append(keysAndValues, k.String(), v.String())
		}
		for resourceName, quantity := range totalAvailableUsages {
			keysAndValues = append(keysAndValues, fmt.Sprintf("%s/totalAvailable", resourceName), quantity.String())
		}

		if prod {
			klog.V(4).InfoS("Updated node prodUsage", keysAndValues...)
		} else {
			klog.V(4).InfoS("Updated node usage", keysAndValues...)
		}
	}
}

// sortNodesByUsage sorts nodes based on usage.
func sortNodesByUsage(nodes []NodeInfo, resourceToWeightMap map[corev1.ResourceName]int64, ascending, prod bool) {
	scorer := sorter.ResourceUsageScorer(resourceToWeightMap)
	sort.Slice(nodes, func(i, j int) bool {
		var iNodeUsage, jNodeUsage corev1.ResourceList
		if prod {
			iNodeUsage = usageToResourceList(nodes[i].prodUsage)
			jNodeUsage = usageToResourceList(nodes[j].prodUsage)
		} else {
			iNodeUsage = usageToResourceList(nodes[i].usage)
			jNodeUsage = usageToResourceList(nodes[j].usage)
		}

		iScore := scorer(iNodeUsage, nodes[i].node.Status.Allocatable)
		jScore := scorer(jNodeUsage, nodes[j].node.Status.Allocatable)
		if ascending {
			return iScore < jScore
		}
		return iScore > jScore
	})
}

func usageToResourceList(usage map[corev1.ResourceName]*resource.Quantity) corev1.ResourceList {
	m := corev1.ResourceList{}
	for k, v := range usage {
		m[k] = *v
	}
	return m
}

func isNodeOverutilized(usage, thresholds map[corev1.ResourceName]*resource.Quantity) (corev1.ResourceList, bool) {
	// At least one resource has to be above the threshold
	overutilizedResources := corev1.ResourceList{}
	for resourceName, threshold := range thresholds {
		if used := usage[resourceName]; used != nil {
			if used.Cmp(*threshold) > 0 {
				overutilizedResources[resourceName] = *used
			}
		}
	}
	return overutilizedResources, len(overutilizedResources) > 0
}

func isNodeUnderutilized(usage, thresholds map[corev1.ResourceName]*resource.Quantity) bool {
	// All resources have to be below the low threshold
	for resourceName, threshold := range thresholds {
		if used := usage[resourceName]; used != nil {
			if used.Cmp(*threshold) > 0 {
				return false
			}
		}
	}
	return true
}

func isNodeMetricExpired(lastUpdateTime *metav1.Time, nodeMetricExpirationSeconds int64) bool {
	return lastUpdateTime == nil ||
		nodeMetricExpirationSeconds > 0 &&
			time.Since(lastUpdateTime.Time) >= time.Duration(nodeMetricExpirationSeconds)*time.Second
}

func getResourceNames(thresholds ResourceThresholds) []corev1.ResourceName {
	names := make([]corev1.ResourceName, 0, len(thresholds))
	for resourceName := range thresholds {
		names = append(names, resourceName)
	}
	return names
}

func classifyPods(pods []*corev1.Pod, filter func(pod *corev1.Pod) bool) ([]*corev1.Pod, []*corev1.Pod) {
	var nonRemovablePods, removablePods []*corev1.Pod

	for _, pod := range pods {
		if !filter(pod) {
			nonRemovablePods = append(nonRemovablePods, pod)
		} else {
			removablePods = append(removablePods, pod)
		}
	}

	return nonRemovablePods, removablePods
}

func calcAverageResourceUsagePercent(nodeUsages map[string]*NodeUsage) (ResourceThresholds, ResourceThresholds) {
	allUsedPercentages := ResourceThresholds{}
	prodUsedPercentages := ResourceThresholds{}
	for _, nodeUsage := range nodeUsages {
		usage := nodeUsage.usage
		prodUsage := nodeUsage.prodUsage
		allocatable := nodeUsage.node.Status.Allocatable
		for resourceName, used := range usage {
			total := allocatable[resourceName]
			if total.IsZero() {
				continue
			}
			if resourceName == corev1.ResourceCPU {
				allUsedPercentages[resourceName] += Percentage(used.MilliValue()) / Percentage(total.MilliValue()) * 100.0
			} else {
				allUsedPercentages[resourceName] += Percentage(used.Value()) / Percentage(total.Value()) * 100.0
			}
		}
		for resourceName, used := range prodUsage {
			total := allocatable[resourceName]
			if total.IsZero() {
				continue
			}
			if resourceName == corev1.ResourceCPU {
				prodUsedPercentages[resourceName] += Percentage(used.MilliValue()) / Percentage(total.MilliValue()) * 100.0
			} else {
				prodUsedPercentages[resourceName] += Percentage(used.Value()) / Percentage(total.Value()) * 100.0
			}
		}
	}

	average := ResourceThresholds{}
	prodAverage := ResourceThresholds{}
	numberOfNodes := len(nodeUsages)
	for resourceName, totalPercentage := range allUsedPercentages {
		average[resourceName] = totalPercentage / Percentage(numberOfNodes)
	}
	for resourceName, totalPercentage := range prodUsedPercentages {
		prodAverage[resourceName] = totalPercentage / Percentage(numberOfNodes)
	}
	return average, prodAverage
}
func sortPodsOnOneOverloadedNode(srcNode NodeInfo, removablePods []*corev1.Pod, resourceWeights map[corev1.ResourceName]int64, prod bool) {
	weights := make(map[corev1.ResourceName]int64)
	// get the overused resource of this node, and the weights of appropriately using resources will be zero.
	var overusedResources corev1.ResourceList
	if prod {
		overusedResources, _ = isNodeOverutilized(srcNode.prodUsage, srcNode.thresholds.prodHighResourceThreshold)
	} else {
		overusedResources, _ = isNodeOverutilized(srcNode.usage, srcNode.thresholds.highResourceThreshold)
	}
	resourcesThatExceedThresholds := map[corev1.ResourceName]resource.Quantity{}
	for or, used := range overusedResources {
		usedCopy := used.DeepCopy()
		weights[or] = resourceWeights[or]
		if prod {
			usedCopy.Sub(*srcNode.thresholds.prodHighResourceThreshold[or])
		} else {
			usedCopy.Sub(*srcNode.thresholds.highResourceThreshold[or])
		}
		resourcesThatExceedThresholds[or] = usedCopy
	}
	sorter.SortPodsByUsage(
		resourcesThatExceedThresholds,
		removablePods,
		srcNode.podMetrics,
		map[string]corev1.ResourceList{srcNode.node.Name: srcNode.node.Status.Allocatable},
		weights,
	)
}

// podFitsAnyNodeWithThreshold checks if the given pod will fit any of the given nodes. It also checks if the node
// utilization will exceed the threshold after this pod was scheduled on it.
func podFitsAnyNodeWithThreshold(nodeIndexer podutil.GetPodsAssignedToNodeFunc, pod *corev1.Pod, nodes []*corev1.Node,
	nodeUsages map[string]*NodeUsage, nodeThresholds map[string]NodeThresholds, prod bool, podMetric *slov1alpha1.ResourceMap) bool {
	for _, node := range nodes {
		errors := nodeutil.NodeFit(nodeIndexer, pod, node)
		if len(errors) == 0 {
			// check if node utilization exceeds threshold if pod scheduled
			nodeUsage, usageOk := nodeUsages[node.Name]
			nodeThreshold, thresholdOk := nodeThresholds[node.Name]
			if usageOk && thresholdOk {
				var usage, thresholds map[corev1.ResourceName]*resource.Quantity
				if prod {
					usage = nodeUsage.prodUsage
					thresholds = nodeThreshold.prodHighResourceThreshold
				} else {
					usage = nodeUsage.usage
					thresholds = nodeThreshold.highResourceThreshold
				}
				exceeded := false
				for resourceName, threshold := range thresholds {
					if used := usage[resourceName]; used != nil {
						used.Add(podMetric.ResourceList[resourceName])
						if used.Cmp(*threshold) > 0 {
							exceeded = true
							break
						}
					}

				}
				if exceeded {
					klog.V(4).InfoS("Pod may cause node over-utilized", "pod", klog.KObj(pod), "node", klog.KObj(node))
					continue
				}
			}
			klog.V(4).InfoS("Pod fits on node", "pod", klog.KObj(pod), "node", klog.KObj(node))
			return true
		} else {
			klog.V(4).InfoS("Pod does not fit on node", "pod", klog.KObj(pod), "node", klog.KObj(node), "errors", utilerrors.NewAggregate(errors))
		}
	}
	return false
}

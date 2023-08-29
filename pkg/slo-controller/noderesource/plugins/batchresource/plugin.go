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

package batchresource

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/clock"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const PluginName = "BatchResource"

var ResourceNames = []corev1.ResourceName{extension.BatchCPU, extension.BatchMemory}

var Clock clock.Clock = clock.RealClock{} // for testing

type Plugin struct{}

func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) NeedSync(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	// batch resource diff is bigger than ResourceDiffThreshold
	resourcesToDiff := ResourceNames
	for _, resourceName := range resourcesToDiff {
		if util.IsResourceDiff(oldNode.Status.Allocatable, newNode.Status.Allocatable, resourceName,
			*strategy.ResourceDiffThreshold) {
			klog.V(4).Infof("node %v resource %v diff bigger than %v, need sync",
				newNode.Name, resourceName, *strategy.ResourceDiffThreshold)
			return true, "batch resource diff is big than threshold"
		}
	}

	return false, ""
}

func (p *Plugin) Execute(strategy *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	for _, resourceName := range ResourceNames {
		prepareNodeForResource(node, nr, resourceName)
	}
	return nil
}

func (p *Plugin) Reset(node *corev1.Node, message string) []framework.ResourceItem {
	items := make([]framework.ResourceItem, len(ResourceNames))
	for i := range ResourceNames {
		items[i].Name = ResourceNames[i]
		items[i].Message = message
		items[i].Reset = true
	}

	return items
}

// Calculate calculates Batch resources using the formula below:
// Node.Total - Node.Reserved - System.Used - Pod(High-Priority).Used, System.Used = Node.Used - Pod(All).Used.
// As node and podList are the nearly latest state at time T1, the resourceMetrics are the node metric and pod
// metrics collected and snapshot at time T0 (T0 < T1). There can be gaps between the states of T0 and T1.
// We firstly calculate an infimum of the batch allocatable at time T0.
// `BatchAllocatable0 = NodeAllocatable * ratio - SystemUsed0 - Pod(HP and in Pods1).Used0` - Pod(not in Pods1).Used0.
// Then we minus the sum requests of the pods newly scheduled but have not been reported metrics to give a safe result.
func (p *Plugin) Calculate(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	resourceMetrics *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	if strategy == nil || node == nil || podList == nil || resourceMetrics == nil || resourceMetrics.NodeMetric == nil {
		return nil, fmt.Errorf("missing essential arguments")
	}

	// if the node metric is abnormal, do degraded calculation
	if p.isDegradeNeeded(strategy, resourceMetrics.NodeMetric, node) {
		klog.InfoS("node need degradation, reset node resources", "node", node.Name)
		return p.degradeCalculate(node,
			"degrade node resource because of abnormal nodeMetric, reason: degradedByBatchResource"), nil
	}

	// compute the requests and usages according to the pods' priority classes.
	// HP means High-Priority (i.e. not Batch or Free) pods
	podHPRequest := util.NewZeroResourceList()
	podHPUsed := util.NewZeroResourceList()
	// podAllUsed is the sum usage of all pods reported in NodeMetric.
	// podKnownUsed is the sum usage of pods which are both reported in NodeMetric and shown in current pod list.
	podAllUsed := util.NewZeroResourceList()
	podKnownUsed := util.NewZeroResourceList()

	nodeMetric := resourceMetrics.NodeMetric
	podMetricMap := make(map[string]*slov1alpha1.PodMetricInfo)
	for _, podMetric := range nodeMetric.Status.PodsMetric {
		podMetricMap[util.GetPodMetricKey(podMetric)] = podMetric
		podAllUsed = quotav1.Add(podAllUsed, getPodMetricUsage(podMetric))
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}

		// check if the pod has metrics
		podKey := util.GetPodKey(pod)
		podMetric, hasMetric := podMetricMap[podKey]
		if hasMetric {
			podKnownUsed = quotav1.Add(podKnownUsed, getPodMetricUsage(podMetric))
		}

		// count the high-priority usage
		priorityClass := extension.GetPodPriorityClassWithDefault(pod)
		podRequest := util.GetPodRequest(pod, corev1.ResourceCPU, corev1.ResourceMemory)
		isPodHighPriority := priorityClass != extension.PriorityBatch && priorityClass != extension.PriorityFree
		if !isPodHighPriority {
			continue
		}
		podHPRequest = quotav1.Add(podHPRequest, podRequest)
		if hasMetric {
			podHPUsed = quotav1.Add(podHPUsed, getPodMetricUsage(podMetric))
		} else {
			podHPUsed = quotav1.Add(podHPUsed, podRequest)
		}
	}

	// For the pods reported with metrics but not shown in the current list, count them into the HP used.
	podUnknownPriorityUsed := quotav1.Subtract(podAllUsed, podKnownUsed)
	podHPUsed = quotav1.Add(podHPUsed, podUnknownPriorityUsed)
	klog.V(6).InfoS("batch resource got unknown priority pods used", "node", node.Name,
		"cpu", podUnknownPriorityUsed.Cpu().String(), "memory", podUnknownPriorityUsed.Memory().String())

	nodeAllocatable := getNodeAllocatable(node)
	nodeReservation := getNodeReservation(strategy, node)

	// System.Used = max(Node.Used - Pod(All).Used, Node.Anno.Reserved)
	systemUsed := getResourceListForCPUAndMemory(nodeMetric.Status.NodeMetric.SystemUsage.ResourceList)
	nodeAnnoReserved := util.GetNodeReservationFromAnnotation(node.Annotations)
	systemUsed = quotav1.Max(systemUsed, nodeAnnoReserved)

	batchAllocatable, cpuMsg, memMsg := calculateBatchResourceByPolicy(strategy, node, nodeAllocatable,
		nodeReservation, systemUsed, podHPRequest, podHPUsed)

	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.BatchCPU), metrics.UnitInteger, float64(batchAllocatable.Cpu().MilliValue())/1000)
	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.BatchMemory), metrics.UnitByte, float64(batchAllocatable.Memory().Value()))
	klog.V(6).InfoS("calculate batch resource for node", "node", node.Name, "batch resource",
		batchAllocatable, "cpu", cpuMsg, "memory", memMsg)

	return []framework.ResourceItem{
		{
			Name:     extension.BatchCPU,
			Quantity: resource.NewQuantity(batchAllocatable.Cpu().MilliValue(), resource.DecimalSI),
			Message:  cpuMsg,
		},
		{
			Name:     extension.BatchMemory,
			Quantity: batchAllocatable.Memory(),
			Message:  memMsg,
		},
	}, nil
}

func calculateBatchResourceByPolicy(strategy *configuration.ColocationStrategy, node *corev1.Node,
	nodeAllocatable, nodeReserve, systemUsed, podHPReq, podHPUsed corev1.ResourceList) (corev1.ResourceList, string, string) {
	// Node(Batch).Alloc = Node.Total - Node.Reserved - System.Used - Pod(Prod/Mid).Used
	batchAllocatableByUsage := quotav1.Max(quotav1.Subtract(quotav1.Subtract(quotav1.Subtract(
		nodeAllocatable, nodeReserve), systemUsed), podHPUsed), util.NewZeroResourceList())

	// Node(Batch).Alloc = Node.Total - Node.Reserved - Pod(Prod/Mid).Request
	batchAllocatableByRequest := quotav1.Max(quotav1.Subtract(quotav1.Subtract(nodeAllocatable, nodeReserve),
		podHPReq), util.NewZeroResourceList())

	batchAllocatable := batchAllocatableByUsage
	cpuMsg := fmt.Sprintf("batchAllocatable[CPU(Milli-Core)]:%v = nodeAllocatable:%v - nodeReservation:%v - systemUsage:%v - podHPUsed:%v",
		batchAllocatable.Cpu().MilliValue(), nodeAllocatable.Cpu().MilliValue(), nodeReserve.Cpu().MilliValue(),
		systemUsed.Cpu().MilliValue(), podHPUsed.Cpu().MilliValue())

	var memMsg string
	if strategy != nil && strategy.MemoryCalculatePolicy != nil && *strategy.MemoryCalculatePolicy == configuration.CalculateByPodRequest {
		batchAllocatable[corev1.ResourceMemory] = *batchAllocatableByRequest.Memory()
		memMsg = fmt.Sprintf("batchAllocatable[Mem(GB)]:%v = nodeAllocatable:%v - nodeReservation:%v - podHPRequest:%v",
			batchAllocatable.Memory().ScaledValue(resource.Giga), nodeAllocatable.Memory().ScaledValue(resource.Giga),
			nodeReserve.Memory().ScaledValue(resource.Giga), podHPReq.Memory().ScaledValue(resource.Giga))
	} else { // use CalculatePolicy "usage" by default
		memMsg = fmt.Sprintf("batchAllocatable[Mem(GB)]:%v = nodeAllocatable:%v - nodeReservation:%v - systemUsage:%v - podHPUsed:%v",
			batchAllocatable.Memory().ScaledValue(resource.Giga), nodeAllocatable.Memory().ScaledValue(resource.Giga),
			nodeReserve.Memory().ScaledValue(resource.Giga), systemUsed.Memory().ScaledValue(resource.Giga),
			podHPUsed.Memory().ScaledValue(resource.Giga))
	}

	return batchAllocatable, cpuMsg, memMsg
}

func (p *Plugin) isDegradeNeeded(strategy *configuration.ColocationStrategy, nodeMetric *slov1alpha1.NodeMetric, node *corev1.Node) bool {
	if nodeMetric == nil || nodeMetric.Status.UpdateTime == nil {
		klog.V(3).Infof("invalid NodeMetric: %v, need degradation", nodeMetric)
		return true
	}

	now := Clock.Now()
	if now.After(nodeMetric.Status.UpdateTime.Add(time.Duration(*strategy.DegradeTimeMinutes) * time.Minute)) {
		klog.V(3).Infof("timeout NodeMetric: %v, current timestamp: %v, metric last update timestamp: %v",
			nodeMetric.Name, now, nodeMetric.Status.UpdateTime)
		return true
	}

	return false
}

func (p *Plugin) degradeCalculate(node *corev1.Node, message string) []framework.ResourceItem {
	return p.Reset(node, message)
}

func prepareNodeForResource(node *corev1.Node, nr *framework.NodeResource, name corev1.ResourceName) {
	if nr.Resets[name] {
		delete(node.Status.Capacity, name)
		delete(node.Status.Allocatable, name)
	} else if q := nr.Resources[name]; q == nil { // if the specified resource has no quantity
		delete(node.Status.Capacity, name)
		delete(node.Status.Allocatable, name)
	} else {
		// NOTE: extended resource would be validated as an integer, so it should be checked before the update
		if _, ok := q.AsInt64(); !ok {
			klog.V(2).InfoS("node resource's quantity is not int64 and will be rounded",
				"resource", name, "original", *q)
			q.Set(q.Value())
		}
		node.Status.Capacity[name] = *q
		node.Status.Allocatable[name] = *q
	}
}

// getPodMetricUsage gets pod usage from the PodMetricInfo
func getPodMetricUsage(info *slov1alpha1.PodMetricInfo) corev1.ResourceList {
	return getResourceListForCPUAndMemory(info.PodUsage.ResourceList)
}

// getNodeAllocatable gets node allocatable and filters out non-CPU and non-Mem resources
func getNodeAllocatable(node *corev1.Node) corev1.ResourceList {
	return getResourceListForCPUAndMemory(node.Status.Allocatable)
}

// getNodeReservation gets node-level safe-guarding reservation with the node's allocatable
func getNodeReservation(strategy *configuration.ColocationStrategy, node *corev1.Node) corev1.ResourceList {
	nodeAllocatable := getNodeAllocatable(node)
	cpuReserveQuant := util.MultiplyMilliQuant(nodeAllocatable[corev1.ResourceCPU], getReserveRatio(*strategy.CPUReclaimThresholdPercent))
	memReserveQuant := util.MultiplyQuant(nodeAllocatable[corev1.ResourceMemory], getReserveRatio(*strategy.MemoryReclaimThresholdPercent))

	return corev1.ResourceList{
		corev1.ResourceCPU:    cpuReserveQuant,
		corev1.ResourceMemory: memReserveQuant,
	}
}

func getResourceListForCPUAndMemory(rl corev1.ResourceList) corev1.ResourceList {
	return quotav1.Mask(rl, []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory})
}

// getReserveRatio returns resource reserved ratio
func getReserveRatio(reclaimThreshold int64) float64 {
	return float64(100-reclaimThreshold) / 100.0
}

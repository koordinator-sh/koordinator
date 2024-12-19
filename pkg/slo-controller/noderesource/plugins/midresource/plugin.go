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

package midresource

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	resutil "github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/util"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const PluginName = "MidResource"

// ResourceNames defines the Mid-tier extended resource names to update.
var ResourceNames = []corev1.ResourceName{extension.MidCPU, extension.MidMemory}

var clk clock.WithTickerAndDelayedExecution = clock.RealClock{} // for testing

type Plugin struct{}

func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) NeedSync(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	// mid resource diff is bigger than ResourceDiffThreshold
	resourcesToDiff := ResourceNames
	for _, resourceName := range resourcesToDiff {
		if util.IsResourceDiff(oldNode.Status.Allocatable, newNode.Status.Allocatable, resourceName,
			*strategy.ResourceDiffThreshold) {
			klog.V(4).Infof("node %v mid resource %v diff bigger than %v, need sync",
				newNode.Name, resourceName, *strategy.ResourceDiffThreshold)
			return true, "mid resource diff is big than threshold"
		}
	}

	return false, ""
}

func (p *Plugin) Prepare(_ *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	for _, resourceName := range ResourceNames {
		resutil.PrepareNodeForResource(node, nr, resourceName)
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

// Calculate calculates Mid resources using the formula below:
// min(ProdReclaimable, NodeAllocable * MidThresholdRatio).
func (p *Plugin) Calculate(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	metrics *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	if strategy == nil || node == nil || node.Status.Allocatable == nil || podList == nil ||
		metrics == nil || metrics.NodeMetric == nil {
		return nil, fmt.Errorf("missing essential arguments")
	}

	// if the node metric is abnormal, do degraded calculation
	if p.isDegradeNeeded(strategy, metrics.NodeMetric, node) {
		klog.V(5).InfoS("node Mid-tier need degradation, reset node resources", "node", node.Name)
		return p.degradeCalculate(node,
			"degrade node Mid resource because of abnormal nodeMetric, reason: degradedByMidResource"), nil
	}

	return p.calculate(strategy, node, podList, metrics), nil
}

func (p *Plugin) isDegradeNeeded(strategy *configuration.ColocationStrategy, nodeMetric *slov1alpha1.NodeMetric, node *corev1.Node) bool {
	if nodeMetric == nil || nodeMetric.Status.UpdateTime == nil {
		klog.V(4).Infof("need degradation for Mid-tier, err: invalid nodeMetric %v", nodeMetric)
		return true
	}

	now := clk.Now()
	if now.After(nodeMetric.Status.UpdateTime.Add(time.Duration(*strategy.DegradeTimeMinutes) * time.Minute)) {
		klog.V(4).Infof("need degradation for Mid-tier, err: timeout nodeMetric: %v, current timestamp: %v,"+
			" metric last update timestamp: %v", nodeMetric.Name, now, nodeMetric.Status.UpdateTime)
		return true
	}

	if nodeMetric.Status.ProdReclaimableMetric == nil ||
		nodeMetric.Status.ProdReclaimableMetric.Resource.ResourceList == nil {
		klog.V(4).Infof("need degradation for Mid-tier, err: nodeMetric %v has no valid prod reclaimable, set it to zero: %v",
			nodeMetric.Name, nodeMetric.Status.ProdReclaimableMetric)
		return false
	}

	return false
}

func (p *Plugin) degradeCalculate(node *corev1.Node, message string) []framework.ResourceItem {
	return p.Reset(node, message)
}

// Unallocated[Mid] = max(NodeCapacity - NodeReserved - Allocated[Prod], 0)
func (p *Plugin) getUnallocated(nodeName string, podList *corev1.PodList, nodeCapacity, nodeReserved corev1.ResourceList) corev1.ResourceList {
	prodPodAllocated := corev1.ResourceList{}
	for i := range podList.Items {
		pod := &podList.Items[i]
		priorityClass := extension.GetPodPriorityClassWithDefault(pod)
		// If the pod is not marked as low priority, it is considered high priority
		isHighPriority := priorityClass != extension.PriorityMid && priorityClass != extension.PriorityBatch && priorityClass != extension.PriorityFree
		if !isHighPriority {
			continue
		}

		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}
		podRequest := util.GetPodRequest(pod, corev1.ResourceCPU, corev1.ResourceMemory)
		prodPodAllocated = quotav1.Add(prodPodAllocated, podRequest)
	}

	midUnallocated := quotav1.Max(quotav1.Subtract(quotav1.Subtract(nodeCapacity, nodeReserved), prodPodAllocated), util.NewZeroResourceList())
	cpuMsg := fmt.Sprintf("midUnallocatedCPU[core]:%v = max(nodeCapacity:%v - nodeReserved:%v - prodPodAllocated:%v, 0)",
		midUnallocated.Cpu(), nodeCapacity.Cpu(), nodeReserved.Cpu(), prodPodAllocated.Cpu())
	memMsg := fmt.Sprintf("midUnallocatedMem[GB]:%v = max(nodeCapacity:%v - nodeReserved:%v - prodPodAllocated:%v, 0)",
		midUnallocated.Memory().ScaledValue(resource.Giga), nodeCapacity.Memory().ScaledValue(resource.Giga),
		nodeReserved.Memory().ScaledValue(resource.Giga), prodPodAllocated.Memory().ScaledValue(resource.Giga))

	klog.V(6).Infof("calculated mid unallocated for node %s, cpu(core) %v, memory(GB) %v", nodeName, cpuMsg, memMsg)
	return midUnallocated
}

func (p *Plugin) calculate(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	resourceMetrics *framework.ResourceMetrics) []framework.ResourceItem {
	// Allocatable[Mid]' := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio, NodeUnused) + Unallocated[Mid] * midUnallocatedRatio
	// Unallocated[Mid] = max(NodeCapacity - NodeReserved - Allocated[Prod], 0)

	var allocatableMilliCPU, allocatableMemory int64
	prodReclaimableCPU, prodReclaimableMemory := resource.NewQuantity(0, resource.DecimalSI), resource.NewQuantity(0, resource.BinarySI)
	prodReclaimableMetric := resourceMetrics.NodeMetric.Status.ProdReclaimableMetric

	if prodReclaimableMetric == nil || prodReclaimableMetric.Resource.ResourceList == nil {
		klog.V(4).Infof("no valid prod reclaimable, so use default zero value")
		allocatableMilliCPU = 0
		allocatableMemory = 0
	} else {
		prodReclaimable := resourceMetrics.NodeMetric.Status.ProdReclaimableMetric.Resource
		prodReclaimableCPU = prodReclaimable.Cpu()
		prodReclaimableMemory = prodReclaimable.Memory()
		allocatableMilliCPU = prodReclaimableCPU.MilliValue()
		allocatableMemory = prodReclaimableMemory.Value()
	}

	nodeMetric := resourceMetrics.NodeMetric

	hostAppHPUsed := resutil.GetHostAppHPUsed(resourceMetrics, extension.PriorityMid)

	nodeCapacity := resutil.GetNodeCapacity(node)

	systemUsed := resutil.GetResourceListForCPUAndMemory(nodeMetric.Status.NodeMetric.SystemUsage.ResourceList)
	// resource usage of host applications with prod priority will be count as host system usage since they consume the
	// node reserved resource.
	systemUsed = quotav1.Add(systemUsed, hostAppHPUsed)

	// System.Reserved = Node.Anno.Reserved, Node.Kubelet.Reserved)
	nodeAnnoReserved := util.GetNodeReservationFromAnnotation(node.Annotations)
	nodeKubeletReserved := util.GetNodeReservationFromKubelet(node)
	// FIXME: resource reservation taking max is rather confusing.
	nodeReserved := quotav1.Max(nodeKubeletReserved, nodeAnnoReserved)
	nodeReserved = quotav1.Max(systemUsed, nodeReserved)

	unallocated := p.getUnallocated(node.Name, podList, nodeCapacity, nodeReserved)

	nodeUnused, err := getNodeUnused(node, nodeMetric)
	if err != nil {
		// failed to get nodeUsage, so radically belief that there is no resource left
		// to keep mid-resource calculations relatively strict
		nodeUnused = corev1.ResourceList{}
	}
	cpuInMilliCores, memory, cpuMsg, memMsg := resutil.CalculateMidResourceByPolicy(strategy, nodeCapacity,
		unallocated, nodeUnused, allocatableMilliCPU, allocatableMemory, prodReclaimableCPU, prodReclaimableMemory, node.Name)

	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.MidCPU), metrics.UnitInteger, float64(cpuInMilliCores.MilliValue())/1000)
	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.MidMemory), metrics.UnitByte, float64(memory.Value()))
	klog.V(6).Infof("calculated mid allocatable for node %s, cpu(milli-core) %v, memory(byte) %v",
		node.Name, cpuInMilliCores.String(), memory.String())

	return []framework.ResourceItem{
		{
			Name:     extension.MidCPU,
			Quantity: cpuInMilliCores, // in milli-cores
			Message:  cpuMsg,
		},
		{
			Name:     extension.MidMemory,
			Quantity: memory,
			Message:  memMsg,
		},
	}
}

func getNodeUnused(node *corev1.Node, nodeMetrics *slov1alpha1.NodeMetric) (corev1.ResourceList, error) {
	// nodeCapacity - nodeUsed
	nodeCapacity := resutil.GetNodeCapacity(node)
	nodeUsed := nodeMetrics.Status.NodeMetric.NodeUsage.ResourceList
	if isValid, mes := resutil.IsValidNodeUsage(nodeMetrics); isValid {
		return quotav1.Subtract(nodeCapacity, nodeUsed), nil
	} else {
		return nil, fmt.Errorf("invalid node usage: %v", mes)
	}
}

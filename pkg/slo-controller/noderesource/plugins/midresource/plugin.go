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
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const PluginName = "MidResource"

// ResourceNames defines the Mid-tier extended resource names to update.
var ResourceNames = []corev1.ResourceName{extension.MidCPU, extension.MidMemory}

var clk clock.Clock = clock.RealClock{} // for testing

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
		klog.InfoS("node Mid-tier need degradation, reset node resources", "node", node.Name)
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

	if nodeMetric.Status.ProdReclaimableMetric == nil ||
		nodeMetric.Status.ProdReclaimableMetric.Resource.ResourceList == nil {
		klog.V(4).Infof("need degradation for Mid-tier, err: nodeMetric %v has no valid prod reclaimable: %v",
			nodeMetric.Name, nodeMetric.Status.ProdReclaimableMetric)
		return true
	}

	now := clk.Now()
	if now.After(nodeMetric.Status.UpdateTime.Add(time.Duration(*strategy.DegradeTimeMinutes) * time.Minute)) {
		klog.V(4).Infof("need degradation for Mid-tier, err: timeout nodeMetric: %v, current timestamp: %v,"+
			" metric last update timestamp: %v", nodeMetric.Name, now, nodeMetric.Status.UpdateTime)
		return true
	}

	return false
}

func (p *Plugin) degradeCalculate(node *corev1.Node, message string) []framework.ResourceItem {
	return p.Reset(node, message)
}

func (p *Plugin) calculate(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	resourceMetrics *framework.ResourceMetrics) []framework.ResourceItem {
	// MidAllocatable := min(NodeAllocatable * thresholdRatio, ProdReclaimable)
	prodReclaimable := resourceMetrics.NodeMetric.Status.ProdReclaimableMetric.Resource
	allocatableMilliCPU := prodReclaimable.Cpu().MilliValue()
	allocatableMemory := prodReclaimable.Memory().Value()

	nodeAllocatable := node.Status.Allocatable
	cpuThresholdRatio := 1.0
	if strategy != nil && strategy.MidCPUThresholdPercent != nil {
		cpuThresholdRatio = float64(*strategy.MidCPUThresholdPercent) / 100
	}
	if maxMilliCPU := float64(nodeAllocatable.Cpu().MilliValue()) * cpuThresholdRatio; allocatableMilliCPU > int64(maxMilliCPU) {
		allocatableMilliCPU = int64(maxMilliCPU)
	}
	if allocatableMilliCPU < 0 {
		klog.V(5).Infof("mid allocatable cpu of node %s is %v less than zero, set to zero",
			node.Name, allocatableMilliCPU)
		allocatableMilliCPU = 0
	}
	cpuInMilliCores := resource.NewQuantity(allocatableMilliCPU, resource.DecimalSI)

	memThresholdRatio := 1.0
	if strategy != nil && strategy.MidMemoryThresholdPercent != nil {
		memThresholdRatio = float64(*strategy.MidMemoryThresholdPercent) / 100
	}
	if maxMemory := float64(nodeAllocatable.Memory().Value()) * memThresholdRatio; allocatableMemory > int64(maxMemory) {
		allocatableMemory = int64(maxMemory)
	}
	if allocatableMemory < 0 {
		klog.V(5).Infof("mid allocatable memory of node %s is %v less than zero, set to zero",
			node.Name, allocatableMemory)
		allocatableMemory = 0
	}
	memory := resource.NewQuantity(allocatableMemory, resource.BinarySI)

	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.MidCPU), metrics.UnitInteger, float64(cpuInMilliCores.MilliValue())/1000)
	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.MidMemory), metrics.UnitByte, float64(memory.Value()))
	klog.V(6).Infof("calculated mid allocatable for node %s, cpu(milli-core) %v, memory(byte) %v",
		node.Name, cpuInMilliCores.String(), memory.String())

	return []framework.ResourceItem{
		{
			Name:     extension.MidCPU,
			Quantity: cpuInMilliCores, // in milli-cores
			Message: fmt.Sprintf("midAllocatable[CPU(milli-core)]:%v = min(nodeAllocatable:%v * thresholdRatio:%v, ProdReclaimable:%v)",
				cpuInMilliCores.Value(), nodeAllocatable.Cpu().MilliValue(), cpuThresholdRatio, prodReclaimable.Cpu().MilliValue()),
		},
		{
			Name:     extension.MidMemory,
			Quantity: memory,
			Message: fmt.Sprintf("midAllocatable[Memory(byte)]:%s = min(nodeAllocatable:%s * thresholdRatio:%v, ProdReclaimable:%s)",
				memory.String(), nodeAllocatable.Memory().String(), memThresholdRatio, prodReclaimable.Memory().String()),
		},
	}
}

func prepareNodeForResource(node *corev1.Node, nr *framework.NodeResource, name corev1.ResourceName) {
	if q := nr.Resources[name]; nr.Resets[name] || q == nil {
		delete(node.Status.Capacity, name)
		delete(node.Status.Allocatable, name)
	} else {
		if _, ok := q.AsInt64(); !ok {
			klog.V(2).InfoS("node mid resource's quantity is not int64 and will be rounded",
				"resource", name, "original", *q)
			q.Set(q.Value())
		}
		node.Status.Capacity[name] = *q
		node.Status.Allocatable[name] = *q
	}
}

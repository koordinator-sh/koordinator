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
	"math"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	slolisters "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	Name                          = "LoadAwareScheduling"
	ErrReasonNodeMetricExpired    = "node(s) nodeMetric expired"
	ErrReasonUsageExceedThreshold = "node(s) %s usage exceed threshold"
)

const (
	// DefaultMilliCPURequest defines default milli cpu request number.
	DefaultMilliCPURequest int64 = 250 // 0.25 core
	// DefaultMemoryRequest defines default memory request size.
	DefaultMemoryRequest int64 = 200 * 1024 * 1024 // 200 MB
	// DefaultNodeMetricReportInterval defines the default koodlet report NodeMetric interval.
	DefaultNodeMetricReportInterval = 60 * time.Second
)

var (
	_ framework.FilterPlugin  = &Plugin{}
	_ framework.ScorePlugin   = &Plugin{}
	_ framework.ReservePlugin = &Plugin{}
)

type Plugin struct {
	handle           framework.Handle
	args             *config.LoadAwareSchedulingArgs
	nodeMetricLister slolisters.NodeMetricLister
	podAssignCache   *podAssignCache
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.LoadAwareSchedulingArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type LoadAwareSchedulingArgs, got %T", args)
	}

	if err := validation.ValidateLoadAwareSchedulingArgs(pluginArgs); err != nil {
		return nil, err
	}

	frameworkExtender, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("want handle to be of type frameworkext.ExtendedHandle, got %T", handle)
	}

	assignCache := newPodAssignCache()
	frameworkExtender.SharedInformerFactory().Core().V1().Pods().Informer().AddEventHandler(assignCache)
	nodeMetricLister := frameworkExtender.KoordinatorSharedInformerFactory().Slo().V1alpha1().NodeMetrics().Lister()

	return &Plugin{
		handle:           handle,
		args:             pluginArgs,
		nodeMetricLister: nodeMetricLister,
		podAssignCache:   assignCache,
	}, nil
}

func (p *Plugin) Name() string { return Name }

func (p *Plugin) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	nodeMetric, err := p.nodeMetricLister.Get(node.Name)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	if p.args.FilterExpiredNodeMetrics != nil && *p.args.FilterExpiredNodeMetrics && p.args.NodeMetricExpirationSeconds != nil {
		if isNodeMetricExpired(nodeMetric, *p.args.NodeMetricExpirationSeconds) {
			return framework.NewStatus(framework.Unschedulable, ErrReasonNodeMetricExpired)
		}
	}

	usageThresholds := p.args.UsageThresholds
	customUsageThresholds, err := extension.GetCustomUsageThresholds(node)
	if err != nil {
		klog.V(5).ErrorS(err, "failed to GetCustomUsageThresholds from", "node", node.Name)
	} else {
		if len(customUsageThresholds.UsageThresholds) > 0 {
			usageThresholds = customUsageThresholds.UsageThresholds
		}
	}

	if len(usageThresholds) > 0 {
		if nodeMetric.Status.NodeMetric == nil {
			return nil
		}
		for resourceName, threshold := range usageThresholds {
			if threshold == 0 {
				continue
			}
			total := node.Status.Allocatable[resourceName]
			if total.IsZero() {
				continue
			}
			used := nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[resourceName]
			usage := int64(math.Round(float64(used.MilliValue()) / float64(total.MilliValue()) * 100))
			if usage >= threshold {
				return framework.NewStatus(framework.Unschedulable, fmt.Sprintf(ErrReasonUsageExceedThreshold, resourceName))
			}
		}
	}

	return nil
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (p *Plugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	p.podAssignCache.assign(nodeName, pod)
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	p.podAssignCache.unAssign(nodeName, pod)
}

func (p *Plugin) Score(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}
	node := nodeInfo.Node()
	if node == nil {
		return 0, framework.NewStatus(framework.Error, "node not found")
	}
	nodeMetric, err := p.nodeMetricLister.Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, "nodeMetric not found")
	}
	if p.args.NodeMetricExpirationSeconds != nil && isNodeMetricExpired(nodeMetric, *p.args.NodeMetricExpirationSeconds) {
		return 0, nil
	}

	estimatedUsed := estimatedPodUsed(pod, p.args.ResourceWeights, p.args.EstimatedScalingFactors)
	estimatedAssignedPodUsage := p.estimatedAssignedPodUsage(nodeName, nodeMetric)
	for resourceName, value := range estimatedAssignedPodUsage {
		estimatedUsed[resourceName] += value
	}

	allocatable := make(map[corev1.ResourceName]int64)
	for resourceName := range p.args.ResourceWeights {
		quantity := node.Status.Allocatable[resourceName]
		if resourceName == corev1.ResourceCPU {
			allocatable[resourceName] = quantity.MilliValue()
		} else {
			allocatable[resourceName] = quantity.Value()
		}
		if nodeMetric.Status.NodeMetric != nil {
			quantity = nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[resourceName]
			if resourceName == corev1.ResourceCPU {
				estimatedUsed[resourceName] += quantity.MilliValue()
			} else {
				estimatedUsed[resourceName] += quantity.Value()
			}
		}
	}

	score := loadAwareSchedulingScorer(p.args.ResourceWeights, estimatedUsed, allocatable)
	return score, nil
}

func isNodeMetricExpired(nodeMetric *slov1alpha1.NodeMetric, nodeMetricExpirationSeconds int64) bool {
	return nodeMetric == nil ||
		nodeMetric.Status.UpdateTime == nil ||
		nodeMetricExpirationSeconds > 0 &&
			time.Since(nodeMetric.Status.UpdateTime.Time) >= time.Duration(nodeMetricExpirationSeconds)*time.Second
}

func (p *Plugin) estimatedAssignedPodUsage(nodeName string, nodeMetric *slov1alpha1.NodeMetric) map[corev1.ResourceName]int64 {
	estimatedUsed := make(map[corev1.ResourceName]int64)
	nodeMetricReportInterval := getNodeMetricReportInterval(nodeMetric)
	p.podAssignCache.lock.RLock()
	defer p.podAssignCache.lock.RUnlock()
	for _, assignInfo := range p.podAssignCache.podInfoItems[nodeName] {
		if assignInfo.timestamp.After(nodeMetric.Status.UpdateTime.Time) ||
			assignInfo.timestamp.Before(nodeMetric.Status.UpdateTime.Time) &&
				nodeMetric.Status.UpdateTime.Sub(assignInfo.timestamp) < nodeMetricReportInterval {
			estimated := estimatedPodUsed(assignInfo.pod, p.args.ResourceWeights, p.args.EstimatedScalingFactors)
			for resourceName, value := range estimated {
				estimatedUsed[resourceName] += value
			}
		}
	}
	return estimatedUsed
}

func getNodeMetricReportInterval(nodeMetric *slov1alpha1.NodeMetric) time.Duration {
	if nodeMetric.Spec.CollectPolicy == nil || nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds == nil {
		return DefaultNodeMetricReportInterval
	}
	return time.Duration(*nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds) * time.Second
}

func estimatedPodUsed(pod *corev1.Pod, resourceWeights map[corev1.ResourceName]int64, scalingFactors map[corev1.ResourceName]int64) map[corev1.ResourceName]int64 {
	requests, limits := resourceapi.PodRequestsAndLimits(pod)
	estimatedUsed := make(map[corev1.ResourceName]int64)
	priorityClass := extension.GetPriorityClass(pod)
	for resourceName := range resourceWeights {
		realResourceName := extension.TranslateResourceNameByPriorityClass(priorityClass, resourceName)
		estimatedUsed[resourceName] = estimatedUsedByResource(requests, limits, realResourceName, scalingFactors[resourceName])
	}
	return estimatedUsed
}

func estimatedUsedByResource(requests, limits corev1.ResourceList, resourceName corev1.ResourceName, scalingFactor int64) int64 {
	limitQuantity := limits[resourceName]
	requestQuantity := requests[resourceName]
	var quantity resource.Quantity
	if limitQuantity.Cmp(requestQuantity) > 0 {
		scalingFactor = 100
		quantity = limitQuantity
	} else {
		quantity = requestQuantity
	}

	if quantity.IsZero() {
		switch resourceName {
		case corev1.ResourceCPU, extension.BatchCPU:
			return DefaultMilliCPURequest
		case corev1.ResourceMemory, extension.BatchMemory:
			return DefaultMemoryRequest
		}
		return 0
	}

	var estimatedUsed int64
	switch resourceName {
	case corev1.ResourceCPU:
		estimatedUsed = int64(math.Round(float64(quantity.MilliValue()) * float64(scalingFactor) / 100))
		if estimatedUsed > limitQuantity.MilliValue() {
			estimatedUsed = limitQuantity.MilliValue()
		}
	default:
		estimatedUsed = int64(math.Round(float64(quantity.Value()) * float64(scalingFactor) / 100))
		if estimatedUsed > limitQuantity.Value() {
			estimatedUsed = limitQuantity.Value()
		}
	}
	return estimatedUsed
}

func loadAwareSchedulingScorer(resToWeightMap map[corev1.ResourceName]int64, used, allocatable map[corev1.ResourceName]int64) int64 {
	var nodeScore, weightSum int64
	for resourceName, weight := range resToWeightMap {
		resourceScore := leastRequestedScore(used[resourceName], allocatable[resourceName])
		nodeScore += resourceScore * weight
		weightSum += weight
	}
	return nodeScore / weightSum
}

func leastRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return ((capacity - requested) * framework.MaxNodeScore) / capacity
}

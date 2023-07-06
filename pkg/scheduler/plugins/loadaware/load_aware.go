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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	slolisters "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
)

const (
	Name                                    = "LoadAwareScheduling"
	ErrReasonNodeMetricExpired              = "node(s) nodeMetric expired"
	ErrReasonUsageExceedThreshold           = "node(s) %s usage exceed threshold"
	ErrReasonAggregatedUsageExceedThreshold = "node(s) %s aggregated usage exceed threshold"
	ErrReasonFailedEstimatePod
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
	_ framework.EnqueueExtensions = &Plugin{}

	_ framework.FilterPlugin  = &Plugin{}
	_ framework.ScorePlugin   = &Plugin{}
	_ framework.ReservePlugin = &Plugin{}
)

type Plugin struct {
	handle           framework.Handle
	args             *config.LoadAwareSchedulingArgs
	podLister        corev1listers.PodLister
	nodeMetricLister slolisters.NodeMetricLister
	estimator        estimator.Estimator
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
	podInformer := frameworkExtender.SharedInformerFactory().Core().V1().Pods()
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), frameworkExtender.SharedInformerFactory(), podInformer.Informer(), assignCache)
	podLister := podInformer.Lister()
	nodeMetricLister := frameworkExtender.KoordinatorSharedInformerFactory().Slo().V1alpha1().NodeMetrics().Lister()

	estimator, err := estimator.NewEstimator(pluginArgs, handle)
	if err != nil {
		return nil, err
	}

	return &Plugin{
		handle:           handle,
		args:             pluginArgs,
		podLister:        podLister,
		nodeMetricLister: nodeMetricLister,
		estimator:        estimator,
		podAssignCache:   assignCache,
	}, nil
}

func (p *Plugin) Name() string { return Name }

func (p *Plugin) EventsToRegister() []framework.ClusterEvent {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("nodemetrics.%v.%v", slov1alpha1.GroupVersion.Version, slov1alpha1.GroupVersion.Group)
	return []framework.ClusterEvent{
		{Resource: framework.GVK(gvk), ActionType: framework.Add | framework.Update | framework.Delete},
	}
}

func (p *Plugin) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	if isDaemonSetPod(pod.OwnerReferences) {
		return nil
	}

	nodeMetric, err := p.nodeMetricLister.Get(node.Name)
	if err != nil {
		// For nodes that lack load information, fall back to the situation where there is no load-aware scheduling.
		// Some nodes in the cluster do not install the koordlet, but users newly created Pod use koord-scheduler to schedule,
		// and the load-aware scheduling itself is an optimization, so we should skip these nodes.
		if errors.IsNotFound(err) {
			return nil
		}
		return framework.NewStatus(framework.Error, err.Error())
	}

	if p.args.FilterExpiredNodeMetrics != nil && *p.args.FilterExpiredNodeMetrics && p.args.NodeMetricExpirationSeconds != nil {
		if isNodeMetricExpired(nodeMetric, *p.args.NodeMetricExpirationSeconds) {
			return framework.NewStatus(framework.Unschedulable, ErrReasonNodeMetricExpired)
		}
	}

	filterProfile := generateUsageThresholdsFilterProfile(node, p.args)
	if len(filterProfile.ProdUsageThresholds) > 0 && extension.GetPriorityClass(pod) == extension.PriorityProd {
		status := p.filterProdUsage(node, nodeMetric, filterProfile.ProdUsageThresholds)
		if !status.IsSuccess() {
			return status
		}
	} else {
		var usageThresholds map[corev1.ResourceName]int64
		if filterProfile.AggregatedUsage != nil {
			usageThresholds = filterProfile.AggregatedUsage.UsageThresholds
		} else {
			usageThresholds = filterProfile.UsageThresholds
		}
		if len(usageThresholds) > 0 {
			status := p.filterNodeUsage(node, nodeMetric, filterProfile)
			if !status.IsSuccess() {
				return status
			}
		}
	}

	return nil
}

func (p *Plugin) filterNodeUsage(node *corev1.Node, nodeMetric *slov1alpha1.NodeMetric, filterProfile *usageThresholdsFilterProfile) *framework.Status {
	if nodeMetric.Status.NodeMetric == nil {
		return nil
	}

	var usageThresholds map[corev1.ResourceName]int64
	if filterProfile.AggregatedUsage != nil {
		usageThresholds = filterProfile.AggregatedUsage.UsageThresholds
	} else {
		usageThresholds = filterProfile.UsageThresholds
	}

	for resourceName, threshold := range usageThresholds {
		if threshold == 0 {
			continue
		}
		allocatable, err := p.estimator.EstimateNode(node)
		if err != nil {
			klog.ErrorS(err, "Failed to EstimateNode", "node", node.Name)
			return nil
		}
		total := allocatable[resourceName]
		if total.IsZero() {
			continue
		}
		// TODO(joseph): maybe we should estimate the Pod that just be scheduled that have not reported
		var nodeUsage *slov1alpha1.ResourceMap
		if filterProfile.AggregatedUsage != nil {
			nodeUsage = getTargetAggregatedUsage(
				nodeMetric,
				filterProfile.AggregatedUsage.UsageAggregatedDuration,
				filterProfile.AggregatedUsage.UsageAggregationType,
			)
		} else {
			nodeUsage = &nodeMetric.Status.NodeMetric.NodeUsage
		}
		if nodeUsage == nil {
			continue
		}

		used := nodeUsage.ResourceList[resourceName]
		usage := int64(math.Round(float64(used.MilliValue()) / float64(total.MilliValue()) * 100))
		if usage >= threshold {
			reason := ErrReasonUsageExceedThreshold
			if filterProfile.AggregatedUsage != nil {
				reason = ErrReasonAggregatedUsageExceedThreshold
			}
			return framework.NewStatus(framework.Unschedulable, fmt.Sprintf(reason, resourceName))
		}
	}
	return nil
}

func (p *Plugin) filterProdUsage(node *corev1.Node, nodeMetric *slov1alpha1.NodeMetric, prodUsageThresholds map[corev1.ResourceName]int64) *framework.Status {
	if len(nodeMetric.Status.PodsMetric) == 0 {
		return nil
	}

	// TODO(joseph): maybe we should estimate the Pod that just be scheduled that have not reported
	podMetrics := buildPodMetricMap(p.podLister, nodeMetric, true)
	prodPodUsages, _ := sumPodUsages(podMetrics, nil)
	for resourceName, threshold := range prodUsageThresholds {
		if threshold == 0 {
			continue
		}
		allocatable, err := p.estimator.EstimateNode(node)
		if err != nil {
			klog.ErrorS(err, "Failed to EstimateNode", "node", node.Name)
			return nil
		}
		total := allocatable[resourceName]
		if total.IsZero() {
			continue
		}
		used := prodPodUsages[resourceName]
		usage := int64(math.Round(float64(used.MilliValue()) / float64(total.MilliValue()) * 100))
		if usage >= threshold {
			return framework.NewStatus(framework.Unschedulable, fmt.Sprintf(ErrReasonUsageExceedThreshold, resourceName))
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
		// caused by load-aware scheduling itself is an optimization,
		// so we should skip the node and score the node 0
		if errors.IsNotFound(err) {
			return 0, nil
		}
		return 0, framework.NewStatus(framework.Error, err.Error())
	}
	if p.args.NodeMetricExpirationSeconds != nil && isNodeMetricExpired(nodeMetric, *p.args.NodeMetricExpirationSeconds) {
		return 0, nil
	}

	prodPod := extension.GetPriorityClass(pod) == extension.PriorityProd && p.args.ScoreAccordingProdUsage
	podMetrics := buildPodMetricMap(p.podLister, nodeMetric, prodPod)

	estimatedUsed, err := p.estimator.EstimatePod(pod)
	if err != nil {
		return 0, nil
	}
	assignedPodEstimatedUsed, estimatedPods := p.estimatedAssignedPodUsed(nodeName, nodeMetric, podMetrics, prodPod)
	for resourceName, value := range assignedPodEstimatedUsed {
		estimatedUsed[resourceName] += value
	}
	podActualUsages, estimatedPodActualUsages := sumPodUsages(podMetrics, estimatedPods)
	if prodPod {
		for resourceName, quantity := range podActualUsages {
			estimatedUsed[resourceName] += getResourceValue(resourceName, quantity)
		}
	} else {
		if nodeMetric.Status.NodeMetric != nil {
			var nodeUsage *slov1alpha1.ResourceMap
			if scoreWithAggregation(p.args.Aggregated) {
				nodeUsage = getTargetAggregatedUsage(nodeMetric, &p.args.Aggregated.ScoreAggregatedDuration, p.args.Aggregated.ScoreAggregationType)
			} else {
				nodeUsage = &nodeMetric.Status.NodeMetric.NodeUsage
			}
			if nodeUsage != nil {
				for resourceName, quantity := range nodeUsage.ResourceList {
					if q := estimatedPodActualUsages[resourceName]; !q.IsZero() {
						quantity = quantity.DeepCopy()
						if quantity.Cmp(q) >= 0 {
							quantity.Sub(q)
						}
					}
					estimatedUsed[resourceName] += getResourceValue(resourceName, quantity)
				}
			}
		}
	}

	allocatable, err := p.estimator.EstimateNode(node)
	if err != nil {
		return 0, nil
	}
	score := loadAwareSchedulingScorer(p.args.ResourceWeights, estimatedUsed, allocatable)
	return score, nil
}

func (p *Plugin) estimatedAssignedPodUsed(nodeName string, nodeMetric *slov1alpha1.NodeMetric, podMetrics map[string]corev1.ResourceList, filterProdPod bool) (map[corev1.ResourceName]int64, sets.String) {
	estimatedUsed := make(map[corev1.ResourceName]int64)
	estimatedPods := sets.NewString()
	var nodeMetricUpdateTime time.Time
	if nodeMetric.Status.UpdateTime != nil {
		nodeMetricUpdateTime = nodeMetric.Status.UpdateTime.Time
	}
	nodeMetricReportInterval := getNodeMetricReportInterval(nodeMetric)

	p.podAssignCache.lock.RLock()
	defer p.podAssignCache.lock.RUnlock()
	for _, assignInfo := range p.podAssignCache.podInfoItems[nodeName] {
		if filterProdPod && extension.GetPriorityClass(assignInfo.pod) != extension.PriorityProd {
			continue
		}
		podName := getPodNamespacedName(assignInfo.pod.Namespace, assignInfo.pod.Name)
		podUsage := podMetrics[podName]
		if len(podUsage) == 0 ||
			missedLatestUpdateTime(assignInfo.timestamp, nodeMetricUpdateTime) ||
			stillInTheReportInterval(assignInfo.timestamp, nodeMetricUpdateTime, nodeMetricReportInterval) ||
			(scoreWithAggregation(p.args.Aggregated) &&
				getTargetAggregatedUsage(nodeMetric, &p.args.Aggregated.ScoreAggregatedDuration, p.args.Aggregated.ScoreAggregationType) == nil) {
			estimated, err := p.estimator.EstimatePod(assignInfo.pod)
			if err != nil {
				continue
			}
			for resourceName, value := range estimated {
				if quantity, ok := podUsage[resourceName]; ok {
					usage := getResourceValue(resourceName, quantity)
					if usage > value {
						value = usage
					}
				}
				estimatedUsed[resourceName] += value
			}
			estimatedPods.Insert(podName)
		}
	}
	return estimatedUsed, estimatedPods
}

func loadAwareSchedulingScorer(resToWeightMap, used map[corev1.ResourceName]int64, allocatable corev1.ResourceList) int64 {
	var nodeScore, weightSum int64
	for resourceName, weight := range resToWeightMap {
		resourceScore := leastRequestedScore(used[resourceName], getResourceValue(resourceName, allocatable[resourceName]))
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

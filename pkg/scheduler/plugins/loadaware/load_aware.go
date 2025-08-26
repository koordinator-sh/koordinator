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
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
)

const (
	Name = "LoadAwareScheduling"

	// podEstimatedStateKey is the key in CycleState to LoadAwareScheduling Estimated resources for incoming pod.
	// Using the name of the plugin will likely help us avoid collisions with other plugins.
	incomingPodEstimatedStateKey = "IncomingPodEstimated" + Name

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
	handle         framework.Handle
	args           *config.LoadAwareSchedulingArgs
	vectorizer     ResourceVectorizer
	filterProfile  *usageThresholdsFilterProfile
	scoreWeights   ResourceVector
	estimator      estimator.Estimator
	podAssignCache *podAssignCache
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

	estimator, err := estimator.NewEstimator(pluginArgs, handle)
	if err != nil {
		return nil, err
	}

	vectorizer := NewResourceVectorizerFromArgs(pluginArgs)
	assignCache := newPodAssignCache(estimator, vectorizer, pluginArgs)
	podInformer := frameworkExtender.SharedInformerFactory().Core().V1().Pods()
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), frameworkExtender.SharedInformerFactory(), podInformer.Informer(), assignCache)
	frameworkExtender.RegisterForgetPodHandler(func(pod *corev1.Pod) {
		assignCache.unAssign(pod.Spec.NodeName, pod)
	})
	koordInformers := frameworkExtender.KoordinatorSharedInformerFactory()
	nodeMetricInformer := koordInformers.Slo().V1alpha1().NodeMetrics()
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), koordInformers, nodeMetricInformer.Informer(), assignCache.NodeMetricHandler())
	scoreWeights := vectorizer.ToFactorVec(pluginArgs.ResourceWeights)
	if scoreWeights.Empty() {
		scoreWeights = nil
	}
	return &Plugin{
		handle:         handle,
		args:           pluginArgs,
		vectorizer:     vectorizer,
		filterProfile:  NewUsageThresholdsFilterProfile(pluginArgs, vectorizer),
		scoreWeights:   scoreWeights,
		estimator:      estimator,
		podAssignCache: assignCache,
	}, nil
}

func (p *Plugin) Name() string { return Name }

func (p *Plugin) EventsToRegister() []framework.ClusterEventWithHint {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("nodemetrics.%v.%v", slov1alpha1.GroupVersion.Version, slov1alpha1.GroupVersion.Group)
	return []framework.ClusterEventWithHint{
		{Event: framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Delete}},
		{Event: framework.ClusterEvent{Resource: framework.GVK(gvk), ActionType: framework.Add | framework.Update | framework.Delete}},
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

	filterProfile := p.filterProfile.generateUsageThresholdsFilterProfile(node, p.vectorizer)
	prodPod := !filterProfile.ProdUsageThresholds.Empty() && extension.GetPodPriorityClassWithDefault(pod) == extension.PriorityProd
	var usageThresholds ResourceVector
	isAgg := false
	if prodPod {
		usageThresholds = filterProfile.ProdUsageThresholds
	} else if agg := filterProfile.AggregatedUsage; agg != nil {
		usageThresholds = agg.UsageThresholds
		isAgg = true
	} else {
		usageThresholds = filterProfile.UsageThresholds
	}
	if usageThresholds.Empty() {
		return nil // skip if filter thresholds are disabled
	}

	cached, err := p.podAssignCache.GetNodeMetric(node.Name)
	if err != nil {
		// For nodes that lack load information, fall back to the situation where there is no load-aware scheduling.
		// Some nodes in the cluster do not install the koordlet, but users newly created Pod use koord-scheduler to schedule,
		// and the load-aware scheduling itself is an optimization, so we should skip these nodes.
		if errors.IsNotFound(err) {
			return nil
		}
		return framework.NewStatus(framework.Error, err.Error())
	}
	nodeMetric := cached.NodeMetric
	if p.args.FilterExpiredNodeMetrics != nil && *p.args.FilterExpiredNodeMetrics &&
		p.args.NodeMetricExpirationSeconds != nil && isNodeMetricExpired(nodeMetric, *p.args.NodeMetricExpirationSeconds) {
		if p.args.EnableScheduleWhenNodeMetricsExpired != nil && !*p.args.EnableScheduleWhenNodeMetricsExpired {
			return framework.NewStatus(framework.Unschedulable, ErrReasonNodeMetricExpired)
		}
		return nil
	}
	if nodeMetric.Status.NodeMetric == nil {
		klog.Warningf("nodeMetrics(%s) should not be nil.", node.Name)
		return nil
	}

	allocatableList, err := p.estimator.EstimateNode(node)
	if err != nil {
		klog.ErrorS(err, "Estimated node allocatable failed!", "node", node.Name)
		return nil
	}
	allocatable := p.vectorizer.ToVec(allocatableList)

	var nodeUsage ResourceVector
	if !prodPod {
		if isAgg {
			agg := filterProfile.AggregatedUsage
			nodeUsage = cached.getTargetAggregatedUsage(agg.UsageAggregatedDuration, agg.UsageAggregationType)
		} else {
			nodeUsage = cached.nodeUsage
		}
	}

	estimated, estimatedPods := p.getEstimatedOfExisting(cached, nodeUsage, prodPod)
	if ret := p.filterNodeUsage(node.Name, pod, usageThresholds, estimated, allocatable, isAgg); ret != nil {
		return ret
	}
	if err = p.addEstimatedOfIncoming(estimated, state, pod); err != nil {
		klog.ErrorS(err, "Failed to estimate incoming pod usage", "pod", klog.KObj(pod))
		return nil
	}
	if klog.V(6).Enabled() {
		klog.InfoS("Estimate node usage for filtering", "pod", klog.KObj(pod), "node", nodeMetric.Name,
			"estimated", klog.Format(p.vectorizer.ToList(estimated)),
			"estimatedExistingPods", klog.KObjSlice(estimatedPods.UnsortedList()))
	}
	return p.filterNodeUsage(node.Name, pod, usageThresholds, estimated, allocatable, isAgg)
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
	if p.scoreWeights == nil {
		return 0, nil // skip if score weights are disabled
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}
	node := nodeInfo.Node()
	if node == nil {
		return 0, framework.NewStatus(framework.Error, "node not found")
	}

	cached, err := p.podAssignCache.GetNodeMetric(nodeName)
	if err != nil {
		// caused by load-aware scheduling itself is an optimization,
		// so we should skip the node and score the node 0
		if errors.IsNotFound(err) {
			return 0, nil
		}
		return 0, framework.NewStatus(framework.Error, err.Error())
	}
	nodeMetric := cached.NodeMetric
	if p.args.NodeMetricExpirationSeconds != nil && isNodeMetricExpired(nodeMetric, *p.args.NodeMetricExpirationSeconds) {
		return 0, nil
	}
	if nodeMetric.Status.NodeMetric == nil {
		klog.Warningf("nodeMetrics(%s) should not be nil.", node.Name)
		return 0, nil
	}

	allocatableList, err := p.estimator.EstimateNode(node)
	if err != nil {
		klog.ErrorS(err, "Estimated node allocatable failed!", "node", node.Name)
		return 0, nil
	}
	allocatable := p.vectorizer.ToVec(allocatableList)

	prodPod := p.args.ScoreAccordingProdUsage && extension.GetPodPriorityClassWithDefault(pod) == extension.PriorityProd
	var nodeUsage ResourceVector
	if !prodPod {
		if agg := p.args.Aggregated; agg != nil && agg.ScoreAggregationType != "" {
			nodeUsage = cached.getTargetAggregatedUsage(agg.ScoreAggregatedDuration, agg.ScoreAggregationType)
		} else {
			nodeUsage = cached.nodeUsage
		}
	}

	estimated, estimatedPods := p.getEstimatedOfExisting(cached, nodeUsage, prodPod)
	if err = p.addEstimatedOfIncoming(estimated, state, pod); err != nil {
		klog.ErrorS(err, "Failed to estimate incoming pod usage", "pod", klog.KObj(pod))
		return 0, nil
	}
	if klog.V(6).Enabled() {
		klog.InfoS("Estimate node usage for scoring", "pod", klog.KObj(pod), "node", nodeMetric.Name,
			"estimated", klog.Format(p.vectorizer.ToList(estimated)),
			"estimatedExistingPods", klog.KObjSlice(estimatedPods.UnsortedList()))
	}
	score := loadAwareSchedulingScorer(p.scoreWeights, estimated, allocatable)
	return score, nil
}

func (p *Plugin) getEstimatedOfExisting(nodeMetric *nodeMetric, nodeUsage ResourceVector, prod bool) (estimated ResourceVector, estimatedPods sets.Set[NamespacedName]) {
	estimated = p.vectorizer.EmptyVec()
	if prod {
		estimated.Add(nodeMetric.prodUsage)
		estimated.Add(nodeMetric.prodDelta)
		estimatedPods = nodeMetric.prodDeltaPods
	} else if nodeUsage != nil {
		estimated.Add(nodeUsage)
		estimated.Add(nodeMetric.nodeDelta)
		estimatedPods = nodeMetric.nodeDeltaPods
	} else {
		estimated.Add(nodeMetric.nodeEstimated)
		estimatedPods = nodeMetric.nodeEstimatedPods
	}
	return
}

// try to use state cache before EstimatePod
func (p *Plugin) addEstimatedOfIncoming(estimated ResourceVector, cycleState *framework.CycleState, pod *corev1.Pod) error {
	var podEstimated ResourceVector
	if c, err := cycleState.Read(incomingPodEstimatedStateKey); err == nil {
		podEstimated, _ = c.(ResourceVector)
	}
	if podEstimated == nil {
		list, err := p.estimator.EstimatePod(pod)
		if err != nil {
			// use len=0 but not empty vector to indicate error occurred
			cycleState.Write(incomingPodEstimatedStateKey, ResourceVector{})
			return err
		}
		podEstimated = p.vectorizer.ToFactorVec(list)
		cycleState.Write(incomingPodEstimatedStateKey, podEstimated)
	} else if len(podEstimated) == 0 {
		return fmt.Errorf("error occurred in former estimation from cycleState with key %q", incomingPodEstimatedStateKey)
	}
	estimated.Add(podEstimated)
	return nil
}

func (p *Plugin) filterNodeUsage(nodeName string, pod *corev1.Pod, usageThresholds, estimatedUsed, allocatable ResourceVector, isAgg bool) *framework.Status {
	for i, value := range usageThresholds {
		if value == 0 {
			continue
		}
		total := allocatable[i]
		if total == 0 {
			continue
		}
		estimated := estimatedUsed[i]
		usage := int64(math.Round(float64(estimated) / float64(total) * 100))
		if usage <= value {
			continue
		}

		reason := ErrReasonUsageExceedThreshold
		if isAgg {
			reason = ErrReasonAggregatedUsageExceedThreshold
		}
		resourceName := p.vectorizer[i]
		if klog.V(5).Enabled() {
			klog.InfoS("Node is unschedulable since usage exceeds threshold", "pod", klog.KObj(pod), "node", nodeName,
				"resource", resourceName, "usage", usage, "threshold", value,
				"estimated", getResourceQuantity(resourceName, estimated),
				"total", getResourceQuantity(resourceName, total))
		}
		return framework.NewStatus(framework.Unschedulable, fmt.Sprintf(reason, resourceName))
	}
	return nil
}

func loadAwareSchedulingScorer(resToWeightMap, used, allocatable ResourceVector) int64 {
	var nodeScore, weightSum int64
	for i, weight := range resToWeightMap {
		nodeScore += leastUsedScore(used[i], allocatable[i]) * weight
		weightSum += weight
	}
	if weightSum <= 0 {
		return 0
	}
	return nodeScore / weightSum
}

func leastUsedScore(used, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if used > capacity {
		return 0
	}

	return ((capacity - used) * framework.MaxNodeScore) / capacity
}

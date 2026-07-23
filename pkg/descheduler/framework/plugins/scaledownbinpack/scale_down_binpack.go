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

package scaledownbinpack

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	podutil "github.com/koordinator-sh/koordinator/pkg/descheduler/pod"
)

const (
	ScaleDownBinPackName = "ScaleDownBinPack"
)

var _ framework.BalancePlugin = &ScaleDownBinPack{}

// ScaleDownBinPack ranks target pods for scale-down by maximizing node consolidation.
// It assigns a global evacuation rank to each eligible target pod and stores the
// ranked result for downstream consumers.
type ScaleDownBinPack struct {
	handle    framework.Handle
	podFilter framework.FilterFunc
	args      *deschedulerconfig.ScaleDownBinPackArgs

	// rankedPods holds the result of the most recent ranking pass.
	rankedPods []RankedPod
}

// NewScaleDownBinPack builds the plugin from its arguments.
func NewScaleDownBinPack(_ context.Context, args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*deschedulerconfig.ScaleDownBinPackArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type ScaleDownBinPackArgs, got %T", args)
	}
	if err := validation.ValidateScaleDownBinPackArgs(nil, pluginArgs); err != nil {
		return nil, err
	}

	podSelectorFn, err := filterPods(pluginArgs.PodSelectors)
	if err != nil {
		return nil, fmt.Errorf("error initializing pod selector filter: %v", err)
	}

	var excludedNamespaces sets.String
	var includedNamespaces sets.String
	if pluginArgs.EvictableNamespaces != nil {
		excludedNamespaces = sets.NewString(pluginArgs.EvictableNamespaces.Exclude...)
		includedNamespaces = sets.NewString(pluginArgs.EvictableNamespaces.Include...)
	}

	podFilter, err := podutil.NewOptions().
		WithFilter(podutil.WrapFilterFuncs(handle.Evictor().Filter, podSelectorFn)).
		WithoutNamespaces(excludedNamespaces).
		WithNamespaces(includedNamespaces).
		BuildFilterFunc()
	if err != nil {
		return nil, fmt.Errorf("error initializing pod filter function: %v", err)
	}

	return &ScaleDownBinPack{
		handle:    handle,
		args:      pluginArgs,
		podFilter: podFilter,
	}, nil
}

// Name returns the plugin name.
func (pl *ScaleDownBinPack) Name() string {
	return ScaleDownBinPackName
}

// Balance evaluates the cluster state and computes a globally ranked list of
// target pods for scale-down. The ranks are stored internally; no evictions or
// annotation patches are performed.
func (pl *ScaleDownBinPack) Balance(ctx context.Context, nodes []*corev1.Node) *framework.Status {
	if pl.args.Paused {
		klog.Infof("ScaleDownBinPack is paused and will do nothing.")
		return nil
	}

	selectedNodes, err := pl.filterNodes(nodes)
	if err != nil {
		return &framework.Status{Err: err}
	}
	if len(selectedNodes) == 0 {
		klog.V(4).InfoS("No nodes selected for ScaleDownBinPack")
		return nil
	}

	getPodsFunc := pl.handle.GetPodsAssignedToNodeFunc()

	var (
		eligibleTargetPods []*corev1.Pod
		skippedTargetPods  []*corev1.Pod
		nonTargetPods      []*corev1.Pod
		candidateNodes     []*corev1.Node
	)

	for _, node := range selectedNodes {
		allPods, err := getPodsFunc(node.Name, nil)
		if err != nil {
			klog.ErrorS(err, "Failed to get pods for node", "node", node.Name)
			continue
		}

		var eligible, skipped, nonTarget []*corev1.Pod
		for _, pod := range allPods {
			// Skip completed pods.
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			if pl.podFilter(pod) {
				// The composed filter includes the evictor check, pod selector,
				// and namespace constraints. Pods that pass are eligible targets.
				eligible = append(eligible, pod)
			} else {
				// Non-matching pods are either non-target workloads or target
				// pods that cannot be legally evicted (skipped targets).
				if pl.isTargetPod(pod) {
					skipped = append(skipped, pod)
				} else {
					nonTarget = append(nonTarget, pod)
				}
			}
		}

		if len(eligible) == 0 {
			// No evictable target pods on this node; skip it.
			continue
		}

		eligibleTargetPods = append(eligibleTargetPods, eligible...)
		skippedTargetPods = append(skippedTargetPods, skipped...)
		nonTargetPods = append(nonTargetPods, nonTarget...)
		candidateNodes = append(candidateNodes, node)
	}

	if len(eligibleTargetPods) == 0 {
		klog.V(4).InfoS("No eligible target pods found for ScaleDownBinPack")
		return nil
	}

	ranked := RankPods(candidateNodes, eligibleTargetPods, skippedTargetPods, nonTargetPods, pl.args.ResourceWeights)
	pl.rankedPods = ranked

	klog.V(4).InfoS("ScaleDownBinPack ranking complete",
		"eligiblePods", len(eligibleTargetPods),
		"skippedPods", len(skippedTargetPods),
		"nonTargetPods", len(nonTargetPods),
		"candidateNodes", len(candidateNodes),
		"rankedPods", len(ranked),
	)

	switch pl.args.Strategy {
	case deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly:
		return pl.patchDeletionCosts(ctx, ranked, skippedTargetPods)
	case deschedulerconfig.ScaleDownBinPackStrategyEvictDirectly:
		return pl.evictRankedPods(ctx, ranked)
	default:
		// Default to CalculateOnly if unset or unknown
		return pl.patchDeletionCosts(ctx, ranked, skippedTargetPods)
	}
}

// GetRankedPods returns the most recent ranking result.
func (pl *ScaleDownBinPack) GetRankedPods() []RankedPod {
	return pl.rankedPods
}

// filterNodes selects nodes that match the configured NodeSelector.
func (pl *ScaleDownBinPack) filterNodes(nodes []*corev1.Node) ([]*corev1.Node, error) {
	if pl.args.NodeSelector == nil {
		return nodes, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(pl.args.NodeSelector)
	if err != nil {
		return nil, err
	}
	var selected []*corev1.Node
	for _, node := range nodes {
		if selector.Matches(labels.Set(node.Labels)) {
			selected = append(selected, node)
		}
	}
	return selected, nil
}

// isTargetPod checks whether the pod matches any of the configured PodSelectors,
// ignoring the evictor filter. This distinguishes skipped target pods (matched by
// selector but blocked by eviction constraints) from truly non-target workloads.
func (pl *ScaleDownBinPack) isTargetPod(pod *corev1.Pod) bool {
	if len(pl.args.PodSelectors) == 0 {
		// With no selectors configured, all pods are considered targets.
		return true
	}
	for _, ps := range pl.args.PodSelectors {
		if ps.Selector == nil {
			return true
		}
		selector, err := metav1.LabelSelectorAsSelector(ps.Selector)
		if err != nil {
			continue
		}
		if selector.Matches(labels.Set(pod.Labels)) {
			return true
		}
	}
	return false
}

// filterPods builds a FilterFunc that matches pods against the configured selectors.
func filterPods(podSelectors []deschedulerconfig.ScaleDownBinPackPodSelector) (framework.FilterFunc, error) {
	var selectors []labels.Selector
	for _, v := range podSelectors {
		if v.Selector != nil {
			selector, err := metav1.LabelSelectorAsSelector(v.Selector)
			if err != nil {
				return nil, fmt.Errorf("invalid labelSelector %s, %w", v.Name, err)
			}
			selectors = append(selectors, selector)
		}
	}

	return func(pod *corev1.Pod) bool {
		if len(selectors) == 0 {
			return true
		}
		for _, v := range selectors {
			if v.Matches(labels.Set(pod.Labels)) {
				return true
			}
		}
		return false
	}, nil
}

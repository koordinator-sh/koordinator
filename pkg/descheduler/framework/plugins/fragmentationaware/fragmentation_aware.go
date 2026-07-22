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

package fragmentationaware

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
	nodeutil "github.com/koordinator-sh/koordinator/pkg/descheduler/node"
	podutil "github.com/koordinator-sh/koordinator/pkg/descheduler/pod"
)

const (
	FragmentationAwareName = "FragmentationAware"
)

var _ framework.BalancePlugin = &FragmentationAware{}

type FragmentationAware struct {
	handle    framework.Handle
	podFilter framework.FilterFunc
	args      *deschedulerconfig.FragmentationAwareArgs
}

func NewFragmentationAware(ctx context.Context, args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*deschedulerconfig.FragmentationAwareArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type FragmentationAwareArgs, got %T", args)
	}

	if err := validation.ValidateFragmentationAwareArgs(nil, pluginArgs); err != nil {
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

	return &FragmentationAware{
		handle:    handle,
		args:      pluginArgs,
		podFilter: podFilter,
	}, nil
}

func filterPods(podSelectors []deschedulerconfig.FragmentationAwarePodSelector) (framework.FilterFunc, error) {
	var selectors []labels.Selector
	for _, v := range podSelectors {
		if v.Selector != nil {
			selector, err := metav1.LabelSelectorAsSelector(v.Selector)
			if err != nil {
				return nil, fmt.Errorf("invalid labelSelector %w", err)
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

func (pl *FragmentationAware) Name() string {
	return FragmentationAwareName
}

func (pl *FragmentationAware) Balance(ctx context.Context, nodes []*corev1.Node) *framework.Status {
	if pl.args.Paused {
		klog.Infof("FragmentationAware is paused and will do nothing.")
		return nil
	}

	ctx = framework.PluginNameWithContext(ctx, pl.Name())
	candidateNodes, err := pl.filterNodesByNodeSelector(nodes)
	if err != nil {
		return &framework.Status{Err: err}
	}

	for _, node := range candidateNodes {
		allPods, err := podutil.ListPodsOnANode(node.Name, pl.handle.GetPodsAssignedToNodeFunc(), nil)
		if err != nil {
			klog.ErrorS(err, "Failed to get pods assigned to node", "node", node.Name)
			continue
		}

		evictablePods, err := podutil.ListPodsOnANode(node.Name, pl.handle.GetPodsAssignedToNodeFunc(), pl.podFilter)
		if err != nil {
			klog.ErrorS(err, "Failed to get evictable pods assigned to node", "node", node.Name)
			continue
		}

		stdBefore := scoreNodeImbalance(node, allPods, pl.args.Resources)

		if stdBefore <= pl.args.ImbalanceThreshold {
			continue
		}

		bestCandidate := pl.chooseBestEvictionCandidate(node, allPods, evictablePods, candidateNodes, stdBefore)
		if bestCandidate == nil {
			continue
		}

		if !pl.handle.Evictor().PreEvictionFilter(bestCandidate.pod) {
			continue
		}

		pl.evictBestCandidate(ctx, bestCandidate)
	}

	return nil
}

func (pl *FragmentationAware) filterNodesByNodeSelector(nodes []*corev1.Node) ([]*corev1.Node, error) {
	if pl.args.NodeSelector == nil {
		return nodes, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(pl.args.NodeSelector)
	if err != nil {
		return nil, err
	}
	var filtered []*corev1.Node
	for _, node := range nodes {
		if selector.Matches(labels.Set(node.Labels)) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

type evictionCandidate struct {
	pod       *corev1.Pod
	gain      float64
	stdBefore float64
	stdAfter  float64
}

func (pl *FragmentationAware) chooseBestEvictionCandidate(node *corev1.Node, allPods []*corev1.Pod, evictablePods []*corev1.Pod, candidateNodes []*corev1.Node, stdBefore float64) *evictionCandidate {
	var bestCandidate *evictionCandidate

	for _, pod := range evictablePods {
		if pl.args.NodeFit {
			if !nodeutil.PodFitsAnyOtherNode(pl.handle.GetPodsAssignedToNodeFunc(), pod, candidateNodes) {
				continue
			}
		}

		var podsAfter []*corev1.Pod
		for _, p := range allPods {
			if p.UID != pod.UID {
				podsAfter = append(podsAfter, p)
			}
		}

		stdAfter := scoreNodeImbalance(node, podsAfter, pl.args.Resources)
		gain := stdBefore - stdAfter

		if gain <= pl.args.MinImprovementThreshold {
			continue
		}

		if bestCandidate == nil || gain > bestCandidate.gain {
			bestCandidate = &evictionCandidate{
				pod:       pod,
				gain:      gain,
				stdBefore: stdBefore,
				stdAfter:  stdAfter,
			}
		}
	}

	return bestCandidate
}

func (pl *FragmentationAware) evictBestCandidate(ctx context.Context, candidate *evictionCandidate) {
	reason := fmt.Sprintf("fragmentation reduction: stdBefore=%.4f stdAfter=%.4f gain=%.4f", candidate.stdBefore, candidate.stdAfter, candidate.gain)
	if pl.args.DryRun {
		klog.InfoS("Evict pod in dry run mode", "pod", klog.KObj(candidate.pod), "reason", reason)
		return
	}
	pl.handle.Evictor().Evict(ctx, candidate.pod, framework.EvictOptions{
		PluginName: pl.Name(),
		Reason:     reason,
	})
}

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

package custompriority

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	nodeutil "github.com/koordinator-sh/koordinator/pkg/descheduler/node"
	podutil "github.com/koordinator-sh/koordinator/pkg/descheduler/pod"
)

const (
	PluginCustomPriorityName = "CustomPriority"
)

var _ framework.BalancePlugin = &CustomPriority{}

// CustomPriority evicts pods from high priority (expensive) resources to low priority (cheap) resources
// based on user-defined priority order and resource availability.
type CustomPriority struct {
	handle    framework.Handle
	podFilter framework.FilterFunc
	args      *deschedulerconfig.CustomPriorityArgs
}

// NewCustomPriority builds plugin from its arguments while passing a handle
func NewCustomPriority(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	customPriorityArgs, ok := args.(*deschedulerconfig.CustomPriorityArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type CustomPriorityArgs, got %T", args)
	}

	if err := validateCustomPriorityArgs(customPriorityArgs); err != nil {
		return nil, err
	}

	podSelectorFn, err := filterPods(customPriorityArgs.PodSelectors)
	if err != nil {
		return nil, fmt.Errorf("error initializing pod selector filter: %v", err)
	}

	var excludedNamespaces sets.String
	var includedNamespaces sets.String
	if customPriorityArgs.EvictableNamespaces != nil {
		excludedNamespaces = sets.NewString(customPriorityArgs.EvictableNamespaces.Exclude...)
		includedNamespaces = sets.NewString(customPriorityArgs.EvictableNamespaces.Include...)
	}

	podFilter, err := podutil.NewOptions().
		WithFilter(podutil.WrapFilterFuncs(handle.Evictor().Filter, podSelectorFn)).
		WithoutNamespaces(excludedNamespaces).
		WithNamespaces(includedNamespaces).
		BuildFilterFunc()
	if err != nil {
		return nil, fmt.Errorf("error initializing pod filter function: %v", err)
	}

	return &CustomPriority{
		handle:    handle,
		args:      customPriorityArgs,
		podFilter: podFilter,
	}, nil
}

// Name retrieves the plugin name
func (pl *CustomPriority) Name() string {
	return PluginCustomPriorityName
}

// Balance extension point implementation for the plugin
func (pl *CustomPriority) Balance(ctx context.Context, nodes []*corev1.Node) *framework.Status {
	if pl.args.Paused {
		klog.Infof("CustomPriority is paused and will do nothing.")
		return nil
	}

	if len(pl.args.EvictionOrder) < 2 {
		klog.V(4).InfoS("CustomPriority requires at least 2 resource priority levels to work")
		return nil
	}

	// Filter nodes based on NodeSelector
	selectedNodes, err := pl.filterNodes(nodes)
	if err != nil {
		return &framework.Status{Err: err}
	}

	if len(selectedNodes) == 0 {
		klog.V(4).InfoS("No nodes selected for CustomPriority")
		return nil
	}

	// Classify nodes by priority
	priorityNodes := pl.classifyNodesByPriority(selectedNodes)
	if len(priorityNodes) < 2 {
		klog.V(4).InfoS("CustomPriority requires at least 2 priority levels with nodes")
		return nil
	}

	// Process eviction from high priority to low priority
	status := pl.processEvictions(ctx, priorityNodes)
	if status != nil && status.Err != nil {
		klog.ErrorS(status.Err, "Failed to process evictions")
	}

	return status
}

// filterNodes filters nodes based on NodeSelector
// todo: make as a common util
func (pl *CustomPriority) filterNodes(nodes []*corev1.Node) ([]*corev1.Node, error) {
	if pl.args.NodeSelector == nil {
		return nodes, nil
	}

	selector, err := metav1.LabelSelectorAsSelector(pl.args.NodeSelector)
	if err != nil {
		return nil, err
	}

	var filteredNodes []*corev1.Node
	for _, node := range nodes {
		if selector.Matches(labels.Set(node.Labels)) {
			filteredNodes = append(filteredNodes, node)
		}
	}

	return filteredNodes, nil
}

// classifyNodesByPriority groups nodes by their priority level
func (pl *CustomPriority) classifyNodesByPriority(nodes []*corev1.Node) map[string][]*corev1.Node {
	priorityNodes := make(map[string][]*corev1.Node)

	for _, node := range nodes {
		for _, priority := range pl.args.EvictionOrder {
			if priority.NodeSelector == nil {
				continue
			}

			selector, err := metav1.LabelSelectorAsSelector(priority.NodeSelector)
			if err != nil {
				klog.V(4).InfoS("Invalid node selector for priority", "priority", priority.Name, "error", err)
				continue
			}

			if selector.Matches(labels.Set(node.Labels)) {
				priorityNodes[priority.Name] = append(priorityNodes[priority.Name], node)
				break
			}
		}
	}

	return priorityNodes
}

// processEvictions processes evictions from high priority to low priority
func (pl *CustomPriority) processEvictions(ctx context.Context, priorityNodes map[string][]*corev1.Node) *framework.Status {
	// Sort priorities by eviction order (high priority first)
	var sortedPriorities []string
	for _, priority := range pl.args.EvictionOrder {
		if nodes, exists := priorityNodes[priority.Name]; exists && len(nodes) > 0 {
			sortedPriorities = append(sortedPriorities, priority.Name)
		}
	}

	if len(sortedPriorities) < 2 {
		return nil
	}

	// Process evictions respecting the configured mode
	if pl.args.Mode == deschedulerconfig.CustomPriorityEvictModeDrainNode {
		for i := 0; i < len(sortedPriorities)-1; i++ {
			sourcePriority := sortedPriorities[i]
			targetPriorities := sortedPriorities[i+1:]

			sourceNodes := priorityNodes[sourcePriority]
			if len(sourceNodes) == 0 {
				continue
			}

			var allTargetNodes []*corev1.Node
			for _, targetPriority := range targetPriorities {
				if nodes, exists := priorityNodes[targetPriority]; exists {
					allTargetNodes = append(allTargetNodes, nodes...)
				}
			}
			if len(allTargetNodes) == 0 {
				continue
			}

			if err := pl.evictByDrainingNodes(ctx, sourcePriority, sourceNodes, allTargetNodes); err != nil {
				return &framework.Status{Err: err}
			}
		}
	} else {
		// BestEffort (default)
		for i := 0; i < len(sortedPriorities)-1; i++ {
			// todo：代码应该复用
			sourcePriority := sortedPriorities[i]
			targetPriorities := sortedPriorities[i+1:]

			sourceNodes := priorityNodes[sourcePriority]
			if len(sourceNodes) == 0 {
				continue
			}

			var allTargetNodes []*corev1.Node
			for _, targetPriority := range targetPriorities {
				if nodes, exists := priorityNodes[targetPriority]; exists {
					allTargetNodes = append(allTargetNodes, nodes...)
				}
			}
			if len(allTargetNodes) == 0 {
				continue
			}

			if err := pl.evictFromPriorityToTargets(ctx, sourcePriority, sourceNodes, allTargetNodes); err != nil {
				return &framework.Status{Err: err}
			}
		}
	}

	return nil
}

// evictFromPriorityToTargets evicts pods from source priority nodes to target priority nodes
func (pl *CustomPriority) evictFromPriorityToTargets(ctx context.Context, sourcePriority string, sourceNodes []*corev1.Node, targetNodes []*corev1.Node) error {
	klog.V(4).InfoS("Processing evictions", "sourcePriority", sourcePriority, "sourceNodes", len(sourceNodes), "targetNodes", len(targetNodes))

	for _, sourceNode := range sourceNodes {
		// Get pods on the source node
		pods, err := pl.handle.GetPodsAssignedToNodeFunc()(sourceNode.Name, pl.podFilter)
		if err != nil {
			klog.ErrorS(err, "Failed to get pods on node", "node", sourceNode.Name)
			continue
		}

		if len(pods) == 0 {
			continue
		}

		// Sort pods to improve fit rate: smaller requests first
		// todo：看一下这个有没有可以复用的地方
		pl.sortPodsByRequestsAscending(pods)

		// Check if pods can be evicted to target nodes
		evictablePods := pl.findEvictablePods(pl.handle.GetPodsAssignedToNodeFunc(), pods, targetNodes)
		if len(evictablePods) == 0 {
			continue
		}

		// Evict pods
		for _, pod := range evictablePods {
			if pl.args.DryRun {
				klog.InfoS("Would evict pod in dry run mode", "pod", klog.KObj(pod), "node", sourceNode.Name, "sourcePriority", sourcePriority)
				continue
			}

			evictionOptions := framework.EvictOptions{
				Reason: fmt.Sprintf("evicting from %s priority resource to lower priority resource", sourcePriority),
			}

			if !pl.handle.Evictor().Evict(ctx, pod, evictionOptions) {
				klog.ErrorS(fmt.Errorf("failed to evict pod"), "Pod eviction failed", "pod", klog.KObj(pod), "node", sourceNode.Name)
				continue
			}

			klog.InfoS("Successfully evicted pod", "pod", klog.KObj(pod), "node", sourceNode.Name, "sourcePriority", sourcePriority)
		}
	}

	return nil
}

// evictByDrainingNodes drains whole source nodes when all candidate pods can be placed onto target nodes.
func (pl *CustomPriority) evictByDrainingNodes(ctx context.Context, sourcePriority string, sourceNodes []*corev1.Node, targetNodes []*corev1.Node) error {
	klog.V(4).InfoS("Processing drain-node evictions", "sourcePriority", sourcePriority, "sourceNodes", len(sourceNodes), "targetNodes", len(targetNodes))

	// Precompute virtual remaining resources for each target node
	// CPU (milli), Memory (bytes), Pods(count)
	// todo: 这个其实需要做的再高明一些，有很多其他限制条件会导致Pod无法驱逐
	type resMap = map[corev1.ResourceName]*resource.Quantity
	virtualRemaining := make(map[string]resMap, len(targetNodes))
	for _, tn := range targetNodes {
		rm, err := pl.nodeRemainingRequests(tn)
		if err != nil {
			klog.ErrorS(err, "Failed to compute remaining resources for target node", "node", tn.Name)
			continue
		}
		virtualRemaining[tn.Name] = rm
	}

	for _, src := range sourceNodes {
		pods, err := pl.handle.GetPodsAssignedToNodeFunc()(src.Name, pl.podFilter)
		if err != nil {
			klog.ErrorS(err, "Failed to get pods on node", "node", src.Name)
			continue
		}
		if len(pods) == 0 {
			continue
		}

		// Sort to improve fit rate
		pl.sortPodsByRequestsAscending(pods)

		// Try to map all pods to target nodes with virtual capacity reservation
		assignment := make(map[types.NamespacedName]string)
		// make a working copy of virtual remaining for this source node attempt
		working := make(map[string]resMap, len(virtualRemaining))
		for n, rm := range virtualRemaining {
			copied := make(resMap)
			for rn, q := range rm {
				qq := q.DeepCopy()
				copied[rn] = &qq
			}
			working[n] = copied
		}

		allPlaced := true
		for _, pod := range pods {
			if !pl.podFilter(pod) {
				continue
			}
			podKey := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
			placed := false
			reqs := pl.getPodRequests(pod)
			for _, tn := range targetNodes {
				// Non-resource checks and actual-remaining checks
				if pl.args.NodeFit && len(nodeutil.NodeFit(pl.handle.GetPodsAssignedToNodeFunc(), pod, tn)) != 0 {
					continue
				}
				// Virtual remaining checks to avoid overbooking in this batch
				if pl.requestsFit(reqs, working[tn.Name]) {
					pl.subRequests(working[tn.Name], reqs)
					assignment[podKey] = tn.Name
					placed = true
					break
				}
			}
			if !placed {
				allPlaced = false
				break
			}
		}

		if !allPlaced {
			klog.V(4).InfoS("Skip draining node because not all pods can be placed", "node", src.Name)
			continue
		}

		// Optionally cordon the source node
		// todo: cordon的时机不对，但凡是前面没做检查、后面做检查的了的，都有可能先cordon节点后驱逐Pod
		if pl.args.AutoCordon {
			if err := pl.cordonNode(ctx, src); err != nil {
				klog.ErrorS(err, "Failed to cordon node", "node", src.Name)
			}
		}

		// Evict all assigned pods
		for _, pod := range pods {
			if !pl.podFilter(pod) {
				continue
			}
			if pl.args.DryRun {
				klog.InfoS("Would evict pod in drain-node mode (dry run)", "pod", klog.KObj(pod), "node", src.Name, "sourcePriority", sourcePriority)
				continue
			}
			evictionOptions := framework.EvictOptions{
				Reason: fmt.Sprintf("drain node %s (sourcePriority=%s)", src.Name, sourcePriority),
			}
			if !pl.handle.Evictor().Evict(ctx, pod, evictionOptions) {
				klog.ErrorS(fmt.Errorf("failed to evict pod"), "Pod eviction failed", "pod", klog.KObj(pod), "node", src.Name)

				if pl.args.AutoCordon {
					klog.InfoS("uncordon node because pod eviction failed (drain-node)", "pod", klog.KObj(pod), "node", src.Name)
					if err := pl.uncordonNode(ctx, src); err != nil {
						klog.ErrorS(err, "Failed to uncordon node", "node", src.Name)
					}
				}

				break
			}
			klog.InfoS("Successfully evicted pod (drain-node)", "pod", klog.KObj(pod), "node", src.Name, "sourcePriority", sourcePriority)
		}

		// Commit virtual reservations into baseline for subsequent nodes
		virtualRemaining = working
	}

	return nil
}

func (pl *CustomPriority) cordonNode(ctx context.Context, node *corev1.Node) error {
	client := pl.handle.ClientSet()
	latest, err := client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if latest.Spec.Unschedulable {
		return nil
	}
	latestCopy := latest.DeepCopy()
	latestCopy.Spec.Unschedulable = true
	_, err = client.CoreV1().Nodes().Update(ctx, latestCopy, metav1.UpdateOptions{})
	return err
}

func (pl *CustomPriority) uncordonNode(ctx context.Context, node *corev1.Node) error {
	client := pl.handle.ClientSet()
	latest, err := client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !latest.Spec.Unschedulable {
		return nil
	}
	latestCopy := latest.DeepCopy()
	latestCopy.Spec.Unschedulable = false
	_, err = client.CoreV1().Nodes().Update(ctx, latestCopy, metav1.UpdateOptions{})
	return err
}

func (pl *CustomPriority) nodeRemainingRequests(node *corev1.Node) (map[corev1.ResourceName]*resource.Quantity, error) {
	podsOnNode, err := podutil.ListPodsOnANode(node.Name, pl.handle.GetPodsAssignedToNodeFunc(), nil)
	if err != nil {
		return nil, err
	}
	names := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods}
	used := nodeutil.NodeUtilization(podsOnNode, names)
	remaining := map[corev1.ResourceName]*resource.Quantity{
		corev1.ResourceCPU:    resource.NewMilliQuantity(node.Status.Allocatable.Cpu().MilliValue()-used[corev1.ResourceCPU].MilliValue(), resource.DecimalSI),
		corev1.ResourceMemory: resource.NewQuantity(node.Status.Allocatable.Memory().Value()-used[corev1.ResourceMemory].Value(), resource.BinarySI),
		corev1.ResourcePods:   resource.NewQuantity(node.Status.Allocatable.Pods().Value()-used[corev1.ResourcePods].Value(), resource.DecimalSI),
	}
	return remaining, nil
}

func (pl *CustomPriority) requestsFit(reqs map[corev1.ResourceName]*resource.Quantity, remaining map[corev1.ResourceName]*resource.Quantity) bool {
	for rn, q := range reqs {
		rem, ok := remaining[rn]
		if !ok {
			continue
		}
		if q.MilliValue() > rem.MilliValue() {
			return false
		}
	}
	return true
}

func (pl *CustomPriority) subRequests(remaining map[corev1.ResourceName]*resource.Quantity, reqs map[corev1.ResourceName]*resource.Quantity) {
	for rn, q := range reqs {
		if rem, ok := remaining[rn]; ok {
			if rn == corev1.ResourceCPU {
				rem.Sub(*resource.NewMilliQuantity(q.MilliValue(), resource.DecimalSI))
			} else {
				rem.Sub(*resource.NewQuantity(q.Value(), rem.Format))
			}
		}
	}
}

// findEvictablePods finds pods that can be evicted to target nodes
func (pl *CustomPriority) findEvictablePods(nodeIndexer podutil.GetPodsAssignedToNodeFunc, pods []*corev1.Pod, targetNodes []*corev1.Node) []*corev1.Pod {
	var evictablePods []*corev1.Pod

	// simple early-stop to avoid long scans when nothing fits
	const maxConsecutiveMisses = 5
	consecutiveMisses := 0

	for _, pod := range pods {
		if !pl.podFilter(pod) {
			continue
		}

		// Check if pod can fit on any target node
		if pl.args.NodeFit {
			if !nodeutil.PodFitsAnyNode(nodeIndexer, pod, targetNodes) {
				consecutiveMisses++
				if consecutiveMisses >= maxConsecutiveMisses {
					break
				}
				continue
			}
			consecutiveMisses = 0
		}

		evictablePods = append(evictablePods, pod)
	}

	return evictablePods
}

// getPodRequests gets the resource requests of the pod
func (pl *CustomPriority) getPodRequests(pod *corev1.Pod) map[corev1.ResourceName]*resource.Quantity {
	requests := make(map[corev1.ResourceName]*resource.Quantity)

	for _, container := range pod.Spec.Containers {
		for resourceName, request := range container.Resources.Requests {
			if existing, exists := requests[resourceName]; exists {
				existing.Add(request)
			} else {
				// DeepCopy() returns a value, we need a pointer
				copied := request.DeepCopy()
				requests[resourceName] = &copied
			}
		}
	}

	return requests
}

// sortPodsByRequestsAscending sorts pods by CPU (milli) then Memory (bytes) requests ascending
func (pl *CustomPriority) sortPodsByRequestsAscending(pods []*corev1.Pod) {
	sort.SliceStable(pods, func(i, j int) bool {
		ri := pl.getPodRequests(pods[i])
		rj := pl.getPodRequests(pods[j])

		var ci, cj int64
		if q := ri[corev1.ResourceCPU]; q != nil {
			ci = q.MilliValue()
		}
		if q := rj[corev1.ResourceCPU]; q != nil {
			cj = q.MilliValue()
		}
		if ci != cj {
			return ci < cj
		}

		var mi, mj int64
		if q := ri[corev1.ResourceMemory]; q != nil {
			mi = q.Value()
		}
		if q := rj[corev1.ResourceMemory]; q != nil {
			mj = q.Value()
		}
		if mi != mj {
			return mi < mj
		}

		// deterministic fallback
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
}

// validateCustomPriorityArgs validates the plugin arguments
func validateCustomPriorityArgs(args *deschedulerconfig.CustomPriorityArgs) error {
	if len(args.EvictionOrder) < 2 {
		return fmt.Errorf("CustomPriority requires at least 2 resource priority levels")
	}

	// Validate that each priority has a unique name
	priorityNames := make(map[string]bool)
	for _, priority := range args.EvictionOrder {
		if priority.Name == "" {
			return fmt.Errorf("priority name cannot be empty")
		}
		if priorityNames[priority.Name] {
			return fmt.Errorf("duplicate priority name: %s", priority.Name)
		}
		priorityNames[priority.Name] = true

		if priority.NodeSelector == nil {
			return fmt.Errorf("priority %s must have a nodeSelector", priority.Name)
		}
	}

	return nil
}

// filterPods creates a filter function for pods based on selectors
func filterPods(podSelectors []deschedulerconfig.CustomPriorityPodSelector) (framework.FilterFunc, error) {
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

package podtype

import (
	"context"
	"fmt"
	"math"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	Name = "PodType"

	// stateKey is the key in CycleState to store pod type distribution
	stateKey = Name

	// PodTypeAnnotationKey is the annotation key for pod type
	PodTypeAnnotationKey = "koordinator.sh/pod-type"

	// PodType values
	PodTypeCPUIntensive     = "cpu-intensive"
	PodTypeMemoryIntensive  = "memory-intensive"
	PodTypeIOIntensive      = "io-intensive"
	PodTypeNetworkIntensive = "network-intensive"

	// ErrReasonPodTypeNotFound is the reason for pod type not found
	ErrReasonPodTypeNotFound = "pod type not found"
)

var (
	_ framework.PreFilterPlugin = &Plugin{}
	_ framework.ScorePlugin     = &Plugin{}
	_ framework.ReservePlugin   = &Plugin{}
)

type Plugin struct {
	handle framework.Handle
	cache  *PodTypeCache
	args   *config.PodTypeArgs
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.PodTypeArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type PodTypeArgs, got %T", args)
	}
	if pluginArgs.EnablePodType != nil && !*pluginArgs.EnablePodType {
		// plugin disabled by configuration; return nil so scheduler won't create it.
		klog.V(4).InfoS("podtype plugin disabled by PodTypeArgs")
		return nil, nil
	}

	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("want handle to be of type frameworkext.ExtendedHandle, got %T", handle)
	}

	cache := NewPodTypeCache(extendedHandle)

	return &Plugin{
		handle: handle,
		cache:  cache,
		args:   pluginArgs,
	}, nil
}

func (p *Plugin) Name() string { return Name }

func (p *Plugin) EventsToRegister() []framework.ClusterEvent {
	return []framework.ClusterEvent{
		{
			Resource:   framework.Pod,
			ActionType: framework.Add | framework.Update | framework.Delete,
		},
	}
}

// PodTypePreFilterState typed object stored in cycleState
type PodTypePreFilterState struct {
	MergedCounts map[string]map[string]int
}

// Clone implements framework.StateData.Clone
func (s *PodTypePreFilterState) Clone() framework.StateData {
	if s == nil {
		return nil
	}
	copied := make(map[string]map[string]int, len(s.MergedCounts))
	for n, cmap := range s.MergedCounts {
		copied[n] = make(map[string]int, len(cmap))
		for pt, v := range cmap {
			copied[n][pt] = v
		}
	}
	return &PodTypePreFilterState{MergedCounts: copied}
}

// helper to read typed state and return proper status on error
func getPreFilterState(state *framework.CycleState) (*PodTypePreFilterState, *framework.Status) {
	obj, err := state.Read(stateKey)
	if err != nil {
		// read error -> skip plugin rather than failing scheduling
		klog.V(4).InfoS("podtype: cycleState read error", "err", err)
		return nil, framework.NewStatus(framework.Success)
	}
	if obj == nil {
		klog.V(4).InfoS("podtype: cycleState not found")
		return nil, framework.NewStatus(framework.Success)
	}
	stateObj, ok := obj.(*PodTypePreFilterState)
	if !ok {
		klog.V(4).InfoS("podtype: invalid cycle state type")
		return nil, framework.NewStatus(framework.Success)
	}
	return stateObj, nil
}

func (p *Plugin) PreFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	// debug
	start := time.Now()
	klog.InfoS("podtype: entering PreFilter", "pod", klog.KObj(pod))
	defer func() {
		klog.InfoS("podtype: leaving PreFilter", "pod", klog.KObj(pod), "duration", time.Since(start))
	}()
	// We prefer to make this plugin best-effort: if pod lacks type -> skip plugin.
	// If internal error occurs (e.g. failed to write state), we skip rather than failing scheduling.

	// If pod has no type and owner has no type -> skip
	podType := getPodTypeFromPod(pod)
	if podType == "" {
		podType = getPodTypeFromOwners(pod, p.cache)
	}
	if podType == "" {
		klog.V(4).InfoS("podtype preFilter: pod has no type annotation, skipping plugin", "pod", klog.KObj(pod))
		return nil, framework.NewStatus(framework.Skip, ErrReasonPodTypeNotFound)
	}
	// debug
	klog.V(4).InfoS("got podtype in PreFilter", "pod", klog.KObj(pod), "podType", podType)

	// Get merged counts (confirmed + reserved)
	mergedCounts := p.cache.GetMergedCounts()
	// debug
	confirmed := p.cache.GetConfirmedCounts()
	reserved := p.cache.GetReservedCounts()
	klog.V(4).InfoS("got podtype counts in PreFilter", "mergedCounts", mergedCounts, "confirmedCounts", confirmed, "reservedCounts", reserved)

	// Wrap mergedCounts into a typed struct for cycleState
	stateObj := &PodTypePreFilterState{MergedCounts: mergedCounts}

	state.Write(stateKey, stateObj)
	klog.V(4).InfoS("podtype: wrote cycle state", "pod", klog.KObj(pod), "stateKey", stateKey)
	return nil, nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (p *Plugin) Score(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	// debug
	start := time.Now()
	klog.InfoS("podtype: entering Score", "pod", klog.KObj(pod))
	defer func() {
		klog.InfoS("podtype: leaving Score", "pod", klog.KObj(pod), "duration", time.Since(start))
	}()
	// Read typed state
	stateObj, status := getPreFilterState(state)
	if status != nil {
		return 0, status
	}
	mergedCounts := stateObj.MergedCounts
	// Get pod type (pod annotation preferred; if missing, try owner)
	podType := getPodTypeFromPod(pod)
	if podType == "" {
		podType = getPodTypeFromOwners(pod, p.cache)
	}
	if podType == "" {
		klog.V(4).InfoS("podtype: pod type not found, skipping scoring", "pod", klog.KObj(pod))
		return 0, framework.NewStatus(framework.Skip, ErrReasonPodTypeNotFound)
	}
	klog.V(4).InfoS("got podtype in Score", "pod", klog.KObj(pod), "podType", podType)

	// Score based on pod type
	var (
		score int64
		st    *framework.Status
	)
	switch podType {
	case PodTypeCPUIntensive:
		score, st = p.scoreCPUMemoryIntensive(ctx, pod, nodeName, mergedCounts, corev1.ResourceCPU)
	case PodTypeMemoryIntensive:
		score, st = p.scoreCPUMemoryIntensive(ctx, pod, nodeName, mergedCounts, corev1.ResourceMemory)
	case PodTypeIOIntensive, PodTypeNetworkIntensive:
		score, st = p.scoreIONetIntensive(nodeName, podType, mergedCounts)
	default:
		klog.V(4).InfoS("podtype: unknown pod type, skipping scoring", "pod", klog.KObj(pod), "podType", podType)
		return 0, framework.NewStatus(framework.Skip, "unknown pod type")
	}
	// if plugin-specific status indicates skipping / error, return it
	if st != nil && st.Code() != framework.Success {
		return 0, st
	}

	//  log every Score call (helpful for debugging)
	klog.V(4).InfoS("podtype scoring result",
		"pod", klog.KObj(pod),
		"podType", podType,
		"node", nodeName,
		"score", score)

	return score, nil
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (p *Plugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	// debug
	start := time.Now()
	klog.InfoS("podtype: entering Reserve", "pod", klog.KObj(pod))
	defer func() {
		klog.InfoS("podtype: leaving Reserve", "pod", klog.KObj(pod), "duration", time.Since(start))
	}()
	// Try to determine pod type for reservation; if not found, we skip reserve
	podType := getPodTypeFromPod(pod)
	if podType == "" {
		podType = getPodTypeFromOwners(pod, p.cache)
	}
	if podType == "" {
		klog.V(4).InfoS("podtype: pod type not found; skipping reserve", "pod", klog.KObj(pod))
		return nil
	}

	// Record reservation with podType
	p.cache.Reserve(nodeName, podType, pod.UID)
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	// Let cache rollback reservedCounts using podUID -> reservation mapping.
	p.cache.Unreserve(pod.UID)
}

func (p *Plugin) scoreCPUMemoryIntensive(ctx context.Context, pod *corev1.Pod, nodeName string, mergedCounts map[string]map[string]int, resource corev1.ResourceName) (int64, *framework.Status) {
	// Get node info
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil || nodeInfo == nil {
		klog.V(4).InfoS("node info not found; skipping podtype scoring", "node", nodeName, "err", err)
		return 0, framework.NewStatus(framework.Skip, "node info not found")
	}
	node := nodeInfo.Node()
	if node == nil {
		klog.V(4).InfoS("node object nil; skipping podtype scoring", "node", nodeName)
		return 0, framework.NewStatus(framework.Skip, "node object nil")
	}
	nodeCap := getNodeResourceCapacity(node, resource)
	if nodeCap <= 0 {
		klog.V(4).InfoS("node resource capacity not found or zero; skipping podtype scoring", "node", nodeName, "resource", resource)
		return 0, framework.NewStatus(framework.Skip, "node resource capacity not found")
	}

	// Determine pod type string
	var podType string
	if resource == corev1.ResourceCPU {
		podType = PodTypeCPUIntensive
	} else {
		podType = PodTypeMemoryIntensive
	}

	// Use Plugin method that can consult owner mapping
	existingReq := p.calculateResourceRequest(nodeInfo.Pods, resource, podType)
	podReq := getPodResourceRequest(pod, resource)
	totalReq := existingReq + podReq

	normalized := float64(totalReq) / float64(nodeCap)
	if normalized > 1.0 {
		normalized = 1.0
	}
	score := int64(math.Round(100 * (1 - normalized*normalized)))
	if score < 0 {
		score = 0
	}
	return score, nil
}

func (p *Plugin) scoreIONetIntensive(nodeName string, podType string, mergedCounts map[string]map[string]int) (int64, *framework.Status) {
	// Get count of same type pods
	count := 0
	if counts, ok := mergedCounts[nodeName]; ok {
		count = counts[podType]
	}

	// Calculate score (linear decrease)
	score := 100 - int64(count)*5
	if score < 0 {
		score = 0
	}
	return score, nil
}

// calculateResourceRequest uses podInfo list and resolves pod type by checking pod annotation first, then ownerRef UID via cache (so it supports owner->type mapping)
func (p *Plugin) calculateResourceRequest(podInfos []*framework.PodInfo, resource corev1.ResourceName, targetPodType string) int64 {
	var total int64
	for _, pi := range podInfos {
		if pi == nil || pi.Pod == nil {
			continue
		}
		// only count pods bound to nodes (to avoid pods that are Pending)
		if pi.Pod.Spec.NodeName == "" || pi.Pod.DeletionTimestamp != nil {
			continue
		}
		podType := getPodTypeFromPod(pi.Pod)
		if podType == "" {
			podType = getPodTypeFromOwners(pi.Pod, p.cache)
		}
		if podType != targetPodType {
			continue
		}
		total += getPodResourceRequest(pi.Pod, resource)
	}
	return total
}

func getNodeResourceCapacity(node *corev1.Node, resource corev1.ResourceName) int64 {
	if node == nil || node.Status.Capacity == nil {
		return 0
	}

	if capacity, ok := node.Status.Capacity[resource]; ok {
		if resource == corev1.ResourceCPU {
			return capacity.MilliValue()
		}
		return capacity.Value()
	}
	return 0
}

func getPodResourceRequest(pod *corev1.Pod, resource corev1.ResourceName) int64 {
	var total int64
	for _, c := range pod.Spec.Containers {
		if c.Resources.Requests == nil {
			continue
		}
		if rq, ok := c.Resources.Requests[resource]; ok {
			if resource == corev1.ResourceCPU {
				total += rq.MilliValue()
			} else {
				total += rq.Value()
			}
		}
	}
	return total
}

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

package deviceshare

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/schedulingphase"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "DeviceShare"

	// stateKey is the key in CycleState to pre-computed data.
	stateKey = Name
)

var (
	_ fwktype.EnqueueExtensions = &Plugin{}

	_ fwktype.PreFilterPlugin = &Plugin{}
	_ fwktype.FilterPlugin    = &Plugin{}
	_ fwktype.PreScorePlugin  = &Plugin{}
	_ fwktype.ScorePlugin     = &Plugin{}
	_ fwktype.ScoreExtensions = &Plugin{}
	_ fwktype.ReservePlugin   = &Plugin{}
	_ fwktype.PreBindPlugin   = &Plugin{}

	_ frameworkext.ResizePodPlugin                       = &Plugin{}
	_ frameworkext.ReservationRestorePlugin              = &Plugin{}
	_ frameworkext.ReservationPreAllocationRestorePlugin = &Plugin{}
	_ frameworkext.ReservationFilterPlugin               = &Plugin{}
	_ frameworkext.ReservationScorePlugin                = &Plugin{}
	_ frameworkext.ReservationScoreExtensions            = &Plugin{}
	_ frameworkext.ReservationPreBindPlugin              = &Plugin{}
)

type Plugin struct {
	disableDeviceNUMATopologyAlignment         bool
	handle                                     frameworkext.ExtendedHandle
	nodeDeviceCache                            *nodeDeviceCache
	gpuSharedResourceTemplatesCache            *gpuSharedResourceTemplatesCache
	gpuSharedResourceTemplatesMatchedResources []corev1.ResourceName
	gpuShareUnsupportedModels                  map[string]sets.Set[string]
	scorer                                     *resourceAllocationScorer
	enableQueueHint                            bool
}

type preFilterState struct {
	skip                              bool
	podRequests                       map[schedulingv1alpha1.DeviceType]corev1.ResourceList
	hints                             apiext.DeviceAllocateHints
	hintSelectors                     map[schedulingv1alpha1.DeviceType][2]labels.Selector
	hasSelectors                      bool
	jointAllocate                     *apiext.DeviceJointAllocate
	primaryDeviceType                 schedulingv1alpha1.DeviceType
	podFitsSecondaryDeviceWellPlanned bool
	gpuRequirements                   *GPURequirements
	allocationResult                  apiext.DeviceAllocations
	preemptibleDevices                map[string]map[schedulingv1alpha1.DeviceType]deviceResources
	preemptibleInRRs                  map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources

	isReservationRequired bool

	// designatedAllocation is parsed from the Pod Annotation during the PreFilter phase. In this case, we should assume that the Node has already been selected. That is, all Score-related plug-ins will not be executed. Instead, we only need to call Filter to confirm whether the designatedAllocation is still valid and use it as the allocation result during actual allocation.
	designatedAllocation apiext.DeviceAllocations
	designatedVF         map[schedulingv1alpha1.DeviceType]map[int32]sets.Set[string]
}

type GPURequirements struct {
	numberOfGPUs                        int
	requestsPerGPU                      corev1.ResourceList
	gpuShared                           bool
	honorGPUPartition                   bool
	restrictedGPUPartition              bool
	rindBusBandwidth                    *resource.Quantity
	requiredTopologyScopeLevel          int
	requiredTopologyScope               apiext.DeviceTopologyScope
	enforceGPUSharedResourceTemplate    bool
	candidateGPUSharedResourceTemplates map[string]apiext.GPUSharedResourceTemplates
}

func (s *preFilterState) Clone() fwktype.StateData {
	ns := &preFilterState{
		skip:                              s.skip,
		podRequests:                       s.podRequests,
		hints:                             s.hints,
		hasSelectors:                      s.hasSelectors,
		gpuRequirements:                   s.gpuRequirements,
		hintSelectors:                     s.hintSelectors,
		jointAllocate:                     s.jointAllocate,
		primaryDeviceType:                 s.primaryDeviceType,
		podFitsSecondaryDeviceWellPlanned: s.podFitsSecondaryDeviceWellPlanned,
		allocationResult:                  s.allocationResult,
		isReservationRequired:             s.isReservationRequired,
	}

	preemptibleDevices := map[string]map[schedulingv1alpha1.DeviceType]deviceResources{}
	for nodeName, returnedDevices := range s.preemptibleDevices {
		devices := preemptibleDevices[nodeName]
		if devices == nil {
			devices = map[schedulingv1alpha1.DeviceType]deviceResources{}
			preemptibleDevices[nodeName] = devices
		}
		for k, v := range returnedDevices {
			devices[k] = v.DeepCopy()
		}
	}
	ns.preemptibleDevices = preemptibleDevices

	preemptibleInRRs := map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{}
	for nodeName, rrs := range s.preemptibleInRRs {
		rrInNode := preemptibleInRRs[nodeName]
		if rrInNode == nil {
			rrInNode = map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{}
			preemptibleInRRs[nodeName] = rrInNode
		}
		for reservationUID, reserved := range rrs {
			rrInNode[reservationUID] = copyDeviceResources(reserved)
		}
	}
	ns.preemptibleInRRs = preemptibleInRRs

	return ns
}

func (p *Plugin) Name() string {
	return Name
}

func getPreFilterState(cycleState fwktype.CycleState) (*preFilterState, *fwktype.Status) {
	value, err := cycleState.Read(stateKey)
	if err != nil {
		return nil, fwktype.AsStatus(err)
	}
	state := value.(*preFilterState)
	return state, nil
}

func (p *Plugin) EventsToRegister(_ context.Context) ([]fwktype.ClusterEventWithHint, error) {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("devices.%v.%v", schedulingv1alpha1.GroupVersion.Version, schedulingv1alpha1.GroupVersion.Group)
	events := []fwktype.ClusterEventWithHint{
		{Event: fwktype.ClusterEvent{Resource: fwktype.Pod, ActionType: fwktype.Delete}},
		{Event: fwktype.ClusterEvent{Resource: fwktype.EventResource(gvk), ActionType: fwktype.Add | fwktype.Update | fwktype.Delete}},
	}

	if p.enableQueueHint {
		events = []fwktype.ClusterEventWithHint{
			{Event: fwktype.ClusterEvent{Resource: fwktype.Pod, ActionType: fwktype.Delete}, QueueingHintFn: p.isSchedulableAfterPodDeletion},
			{Event: fwktype.ClusterEvent{Resource: fwktype.EventResource(gvk), ActionType: fwktype.Add | fwktype.Update | fwktype.Delete}, QueueingHintFn: p.isSchedulableAfterDeviceChanged},
		}
	}
	return events, nil
}

func (p *Plugin) PreFilter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodes []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	state, status := preparePod(pod, p.gpuSharedResourceTemplatesCache, p.gpuSharedResourceTemplatesMatchedResources)
	if !status.IsSuccess() {
		return nil, status
	}
	hintForDevice := false
	if schedulingHint := hinter.GetSchedulingHintState(cycleState); schedulingHint != nil {
		if _, ok := schedulingHint.Extensions[Name]; ok {
			hintForDevice = true
		}
	}
	if !hintForDevice {
		state.designatedAllocation = nil
	}
	cycleState.Write(stateKey, state)
	if state.skip {
		return nil, fwktype.NewStatus(fwktype.Skip)
	}
	return nil, nil
}

func (p *Plugin) PreFilterExtensions() fwktype.PreFilterExtensions {
	return p
}

func (p *Plugin) AddPod(ctx context.Context, cycleState fwktype.CycleState, podToSchedule *corev1.Pod, podInfoToAdd fwktype.PodInfo, nodeInfo fwktype.NodeInfo) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return fwktype.NewStatus(fwktype.Error, "node not found")
	}
	nd := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nd == nil {
		return nil
	}

	nd.lock.RLock()
	defer nd.lock.RUnlock()

	podAllocated := nd.getUsed(podInfoToAdd.GetPod().Namespace, podInfoToAdd.GetPod().Name)
	if len(podAllocated) == 0 {
		return nil
	}

	var rInfo *frameworkext.ReservationInfo
	if rCache := p.handle.GetReservationCache(); rCache != nil {
		rInfo = rCache.GetReservationInfoByPod(podInfoToAdd.GetPod(), node.Name)
	}
	if rInfo == nil {
		nominator := p.handle.GetReservationNominator()
		if nominator != nil {
			rInfo = nominator.GetNominatedReservation(podInfoToAdd.GetPod(), node.Name)
		}
	}

	if rInfo == nil || len(nd.getUsed(rInfo.Pod.Namespace, rInfo.Pod.Name)) == 0 {
		preemptibleDevices := state.preemptibleDevices[node.Name]
		if preemptibleDevices == nil {
			preemptibleDevices = map[schedulingv1alpha1.DeviceType]deviceResources{}
			state.preemptibleDevices[node.Name] = preemptibleDevices
		}
		state.preemptibleDevices[node.Name] = subtractAllocated(preemptibleDevices, podAllocated, false)
	} else {
		preemptibleInRRs := state.preemptibleInRRs[node.Name]
		if preemptibleInRRs == nil {
			preemptibleInRRs = map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{}
			state.preemptibleInRRs[node.Name] = preemptibleInRRs
		}
		preemptible := preemptibleInRRs[rInfo.UID()]
		if preemptible == nil {
			preemptible = map[schedulingv1alpha1.DeviceType]deviceResources{}
			preemptibleInRRs[rInfo.UID()] = preemptible
		}
		preemptible = subtractAllocated(preemptible, podAllocated, false)
		preemptibleInRRs[rInfo.UID()] = preemptible
	}

	return nil
}

func (p *Plugin) RemovePod(ctx context.Context, cycleState fwktype.CycleState, podToSchedule *corev1.Pod, podInfoToRemove fwktype.PodInfo, nodeInfo fwktype.NodeInfo) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return fwktype.NewStatus(fwktype.Error, "node not found")
	}
	nd := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nd == nil {
		return nil
	}

	nd.lock.RLock()
	defer nd.lock.RUnlock()

	podAllocated := nd.getUsed(podInfoToRemove.GetPod().Namespace, podInfoToRemove.GetPod().Name)
	if len(podAllocated) == 0 {
		return nil
	}

	var rInfo *frameworkext.ReservationInfo
	if rCache := p.handle.GetReservationCache(); rCache != nil {
		rInfo = rCache.GetReservationInfoByPod(podInfoToRemove.GetPod(), node.Name)
	}
	if rInfo == nil {
		nominator := p.handle.GetReservationNominator()
		if nominator != nil {
			rInfo = nominator.GetNominatedReservation(podInfoToRemove.GetPod(), node.Name)
		}
	}
	if rInfo == nil || len(nd.getUsed(rInfo.Pod.Namespace, rInfo.Pod.Name)) == 0 {
		preemptibleDevices := state.preemptibleDevices[node.Name]
		if preemptibleDevices == nil {
			preemptibleDevices = map[schedulingv1alpha1.DeviceType]deviceResources{}
			state.preemptibleDevices[node.Name] = preemptibleDevices
		}
		state.preemptibleDevices[node.Name] = appendAllocated(preemptibleDevices, podAllocated)
	} else {
		preemptibleInRRs := state.preemptibleInRRs[node.Name]
		if preemptibleInRRs == nil {
			preemptibleInRRs = map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{}
			state.preemptibleInRRs[node.Name] = preemptibleInRRs
		}
		preemptible := preemptibleInRRs[rInfo.UID()]
		if preemptible == nil {
			preemptible = map[schedulingv1alpha1.DeviceType]deviceResources{}
			preemptibleInRRs[rInfo.UID()] = preemptible
		}
		preemptibleInRRs[rInfo.UID()] = appendAllocated(preemptible, podAllocated)
	}

	return nil
}

func (p *Plugin) Filter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return fwktype.NewStatus(fwktype.Error, "node not found")
	}

	if state.gpuRequirements != nil && state.gpuRequirements.gpuShared &&
		pod.Labels != nil && pod.Labels[apiext.LabelGPUIsolationProvider] != "" &&
		pod.Labels[apiext.LabelGPUIsolationProvider] != node.Labels[apiext.LabelGPUIsolationProvider] {
		return fwktype.NewStatus(fwktype.Error, "GPUIsolationProviderHAMICore not found on the node")
	}
	if state.gpuRequirements != nil && state.gpuRequirements.gpuShared {
		vendor, model := node.Labels[apiext.LabelGPUVendor], node.Labels[apiext.LabelGPUModel]
		if p.gpuShareUnsupportedModels[vendor].Has(model) {
			return fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, fmt.Sprintf("model %q of vendor %q does not support GPU Share", model, vendor))
		}
	}
	store := topologymanager.GetStore(cycleState)
	_, ok := store.GetAffinity(node.Name)
	if ok {
		// 当 Pod 在该节点上的 NUMA Affinity 已经算好，表明：Pod 在该节点上开启了 NUMA，且以算好的 NUMA Affinity 资源可分配，所以后续 Filter 逻辑可以忽略
		return nil
	}

	if state.designatedAllocation != nil {
		if state.allocationResult == nil {
			status = p.allocate(ctx, cycleState, pod, nodeInfo.Node())
			if !status.IsSuccess() {
				return status
			}
			state.allocationResult = nil
		}
		return nil
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(node.Name)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[node.Name])

	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nodeDeviceInfo,
		node:       node,
		pod:        pod,
	}

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()
	allocateResult, status := p.tryAllocateFromReusable(allocator, state, restoreState, restoreState.matched, pod, node, preemptible, state.isReservationRequired)
	if !status.IsSuccess() {
		return status
	}
	if len(allocateResult) > 0 {
		return nil
	}

	// TODO 这里应该表示从节点剩余资源分，但是这里看起来不是这个意思
	preemptible = appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
	_, status = allocator.Allocate(nil, nil, nil, preemptible)
	if status.IsSuccess() {
		return nil
	}
	return status
}

func (p *Plugin) FilterReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeInfo fwktype.NodeInfo) *fwktype.Status {
	return nil
}

func (p *Plugin) FilterNominateReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeName string) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return fwktype.AsStatus(err)
	}
	node := nodeInfo.Node()
	if node == nil {
		return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("getting nil node %q from Snapshot", nodeName))
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	var affinity topologymanager.NUMATopologyHint
	if !p.disableDeviceNUMATopologyAlignment {
		store := topologymanager.GetStore(cycleState)
		affinity, _ = store.GetAffinity(node.Name)
	}

	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nodeDeviceInfo,
		node:       node,
		pod:        pod,
		numaNodes:  affinity.NUMANodeAffinity,
	}

	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[nodeName])

	allocIndex := -1
	// if pre-allocation, we filter the reserve pod with the pre-allocatable pod
	if restoreState.preAllocationRInfo != nil {
		for i, v := range restoreState.matched {
			if v.preAllocatable.GetUID() == pod.GetUID() {
				allocIndex = i
				break
			}
		}
		if allocIndex == -1 {
			klog.V(5).Infof("nominated pre-allocatable %v doesn't reserve any device resource, pod %s, node %s", klog.KObj(pod), klog.KObj(pod), nodeName)
			return nil
		}

		nodeDeviceInfo.lock.RLock()
		defer nodeDeviceInfo.lock.RUnlock()
		_, status = p.tryAllocateFromReusable(allocator, state, restoreState, restoreState.matched[allocIndex:allocIndex+1], reservationInfo.GetReservePod(), node, preemptible, true)
		return status
	}

	for i, v := range restoreState.matched {
		if v.rInfo.UID() == reservationInfo.UID() {
			allocIndex = i
			break
		}
	}
	if allocIndex == -1 {
		klog.V(5).Infof("nominated reservation %v doesn't reserve any device resource, pod %s, node %s", klog.KObj(reservationInfo.Reservation), klog.KObj(pod), nodeName)
		return nil
	}

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()

	_, status = p.tryAllocateFromReusable(allocator, state, restoreState, restoreState.matched[allocIndex:allocIndex+1], pod, node, preemptible, true)
	return status
}

func (p *Plugin) Reserve(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	defer func() {
		// ReservationRestoreState is O(n) complexity of node number of the cluster.
		// cleanReservationRestoreState clears ReservationRestoreState in the stateData to reduce memory cost before entering
		// the binding cycle.
		cleanReservationRestoreState(cycleState)
	}()
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	if state.allocationResult == nil {
		nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
		if err != nil {
			return fwktype.AsStatus(err)
		}
		node := nodeInfo.Node()
		if node == nil {
			return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("getting nil node %q from Snapshot", nodeName))
		}
		status = p.allocate(ctx, cycleState, pod, node)
		if !status.IsSuccess() {
			return status
		}
	}
	if state.allocationResult == nil {
		return nil
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return nil
	}
	logAllocationContext(pod, nodeName, nodeDeviceInfo, state.designatedAllocation, state.allocationResult)
	nodeDeviceInfo.lock.Lock()
	defer nodeDeviceInfo.lock.Unlock()
	nodeDeviceInfo.updateCacheUsed(state.allocationResult, pod, true)
	return nil
}

func logAllocationContext(pod *corev1.Pod, nodeName string, nodeDeviceInfo *nodeDevice, designatedAllocation, allocationResult apiext.DeviceAllocations) string {
	podKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
	nodeDeviceSummary := nodeDeviceInfo.getNodeDeviceSummary()
	rawNodeDeviceSummary, err := json.Marshal(nodeDeviceSummary)
	if err != nil {
		klog.Errorf("pod %s on node %s, failed to marshal nodeDeviceSummary, err: %s", podKey, nodeName, err)
	}
	rawDesignatedAllocation, err := json.Marshal(designatedAllocation)
	if err != nil {
		klog.Errorf("pod %s on node %s, failed to marshal designatedAllocation, err: %s", podKey, nodeName, err)
	}
	rawAllocationResult, err := json.Marshal(allocationResult)
	if err != nil {
		klog.Errorf("pod %s on node %s, failed to marshal allocationResult, err: %s", podKey, nodeName, err)
	}
	msg := fmt.Sprintf("[DeviceShare] pod %s on node %s, nodeDeviceSummary: %s, designatedAllocationResult: %s, allocationResult: %s", podKey, nodeName, string(rawNodeDeviceSummary), string(rawDesignatedAllocation), string(rawAllocationResult))
	klog.V(4).Infoln(msg)
	return msg
}

func (p *Plugin) allocate(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, node *corev1.Node) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	var affinity topologymanager.NUMATopologyHint
	if !p.disableDeviceNUMATopologyAlignment {
		store := topologymanager.GetStore(cycleState)
		affinity, _ = store.GetAffinity(node.Name)
	}

	allocator := &AutopilotAllocator{
		state:              state,
		nodeDevice:         nodeDeviceInfo,
		phaseBeingExecuted: schedulingphase.GetExtensionPointBeingExecuted(cycleState),
		node:               node,
		pod:                pod,
		scorer:             p.scorer,
		numaNodes:          affinity.NUMANodeAffinity,
	}

	// TODO: de-duplicate logic done by the Filter phase and move head the pre-process of the resource options
	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(node.Name)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[node.Name])

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()

	result, status := p.allocateWithNominated(allocator, state, restoreState, node, pod, preemptible)
	if !status.IsSuccess() {
		return status
	}
	if len(result) == 0 {
		preemptible = appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
		var requiredDeviceResource map[schedulingv1alpha1.DeviceType]deviceResources
		if len(state.designatedAllocation) > 0 {
			err := fillGPUTotalMem(state.designatedAllocation, nodeDeviceInfo)
			if err != nil {
				return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("fillGPUTotalMem failed: %v, node: %v", err, node.Name))
			}
			requiredDeviceResource = make(map[schedulingv1alpha1.DeviceType]deviceResources, len(state.designatedAllocation))
			for deviceType, minorResources := range state.designatedAllocation {
				requiredDeviceResource[deviceType] = make(deviceResources, len(minorResources))
				for _, minorResource := range minorResources {
					requiredDeviceResource[deviceType][int(minorResource.Minor)] = minorResource.Resources
				}
			}
		}
		result, status = allocator.Allocate(nil, nil, requiredDeviceResource, preemptible)
		if !status.IsSuccess() {
			return status
		}
	}
	err := fillGPUTotalMem(result, nodeDeviceInfo)
	if err != nil {
		return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("fillGPUTotalMem failed: %v, node: %v", err, node.Name))
	}
	state.allocationResult = result
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return
	}
	if state.skip {
		return
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return
	}

	nodeDeviceInfo.lock.Lock()
	defer nodeDeviceInfo.lock.Unlock()

	nodeDeviceInfo.updateCacheUsed(state.allocationResult, pod, false)
	state.allocationResult = nil
}

func (p *Plugin) ResizePod(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	if !reservationutil.IsReservePod(pod) {
		return nil
	}
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	if state.allocationResult == nil {
		nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
		if err != nil {
			return fwktype.AsStatus(err)
		}
		node := nodeInfo.Node()
		if node == nil {
			return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("getting nil node %q from Snapshot", nodeName))
		}
		status = p.allocate(ctx, cycleState, pod, node)
		if !status.IsSuccess() {
			return status
		}
	}

	var allocated corev1.ResourceList
	for _, allocations := range state.allocationResult {
		for _, v := range allocations {
			allocated = quotav1.Add(allocated, v.Resources)
		}
	}
	reservationutil.UpdateReservePodWithAllocatable(pod, nil, allocated)
	return nil
}

func (p *Plugin) PreBindPreFlight(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	return nil
}

func (p *Plugin) PreBind(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	return p.preBindObject(ctx, cycleState, pod, nodeName)
}

func (p *Plugin) PreBindReservation(ctx context.Context, cycleState fwktype.CycleState, reservation *schedulingv1alpha1.Reservation, nodeName string) *fwktype.Status {
	status := p.preBindObject(ctx, cycleState, reservation, nodeName)
	if !status.IsSuccess() {
		return status
	}
	if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
		return updateReservationAllocatable(cycleState, reservation)
	}
	return nil
}

func (p *Plugin) preBindObject(ctx context.Context, cycleState fwktype.CycleState, object metav1.Object, nodeName string) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	// indicates we already skip our device allocation logic, leave it to kubelet
	if state.allocationResult == nil {
		return nil
	}

	err := p.fillID(state.allocationResult, nodeName)
	if err != nil {
		return fwktype.AsStatus(err)
	}

	if err := apiext.SetDeviceAllocations(object, state.allocationResult); err != nil {
		return fwktype.NewStatus(fwktype.Error, err.Error())
	}

	if k8sfeature.DefaultMutableFeatureGate.Enabled(features.DevicePluginAdaption) {
		if err := p.adaptForDevicePlugin(ctx, object, state.allocationResult, nodeName); err != nil {
			return fwktype.AsStatus(err)
		}
	}
	return nil
}

func (p *Plugin) fillID(allocationResult apiext.DeviceAllocations, nodeName string) error {
	extendedHandle, ok := p.handle.(frameworkext.ExtendedHandle)
	if !ok {
		return fmt.Errorf("expect handle to be type frameworkext.ExtendedHandle, got %T", p.handle)
	}
	deviceLister := extendedHandle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Devices().Lister()
	device, err := deviceLister.Get(nodeName)
	if err != nil {
		klog.ErrorS(err, "Failed to get Device", "node", nodeName)
		return err
	}
	for deviceType, allocations := range allocationResult {
		for i, allocation := range allocations {
			for _, info := range device.Spec.Devices {
				if info.Minor != nil && *info.Minor == allocation.Minor && info.Type == deviceType {
					allocationResult[deviceType][i].ID = info.UUID
					break
				}
			}
		}
	}
	return nil
}

func updateReservationAllocatable(cycleState fwktype.CycleState, reservation *schedulingv1alpha1.Reservation) *fwktype.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	var allocated corev1.ResourceList
	for _, allocations := range state.allocationResult {
		for _, v := range allocations {
			allocated = quotav1.Add(allocated, v.Resources)
		}
	}
	if err := reservationutil.UpdateReservationResizeAllocatable(reservation, allocated); err != nil {
		return fwktype.AsStatus(err)
	}
	return nil
}

func (p *Plugin) getNodeDeviceSummary(nodeName string) (*NodeDeviceSummary, bool) {
	return p.nodeDeviceCache.getNodeDeviceSummary(nodeName)
}

func (p *Plugin) getAllNodeDeviceSummary() map[string]*NodeDeviceSummary {
	return p.nodeDeviceCache.getAllNodeDeviceSummary()
}

func New(ctx context.Context, obj runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
	args, ok := obj.(*schedulerconfig.DeviceShareArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type DeviceShareArgs, got %T", obj)
	}
	if err := validation.ValidateDeviceShareArgs(nil, args); err != nil {
		return nil, err
	}
	if args.ScoringStrategy == nil {
		return nil, fmt.Errorf("scoring strategy not specified")
	}
	strategy := args.ScoringStrategy.Type
	scorePlugin, exists := deviceResourceStrategyTypeMap[strategy]
	if !exists {
		return nil, fmt.Errorf("scoring strategy %s is not supported", strategy)
	}

	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("expect handle to be type frameworkext.ExtendedHandle, got %T", handle)
	}

	deviceCache := newNodeDeviceCache()
	registerDeviceEventHandler(deviceCache, extendedHandle.KoordinatorSharedInformerFactory())
	registerPodEventHandler(deviceCache, handle.SharedInformerFactory(), extendedHandle.KoordinatorSharedInformerFactory())
	extendedHandle.RegisterForgetPodHandler(deviceCache.deletePod)
	// Register the node informer synchronously during New so that the registration
	// is visible to the framework's WaitForHandlersSync. If we registered it inside
	// the gcNodeDevice goroutine, the framework might start scheduling before the
	// handler registration is collected. Using a no-op handler because gcNodeDevice
	// only reads from the node lister.
	nodeInformer := handle.SharedInformerFactory().Core().V1().Nodes().Informer()
	if _, err := frameworkexthelper.ForceSyncFromInformer(ctx.Done(), handle.SharedInformerFactory(), nodeInformer, nil); err != nil {
		return nil, err
	}
	go deviceCache.gcNodeDevice(ctx, handle.SharedInformerFactory(), defaultGCPeriod)

	gpuSharedResourceTemplatesCache := newGPUSharedResourceTemplatesCache()
	registerGPUSharedResourceTemplatesConfigMapEventHandler(gpuSharedResourceTemplatesCache,
		args.GPUSharedResourceTemplatesConfig.ConfigMapNamespace, args.GPUSharedResourceTemplatesConfig.ConfigMapName,
		handle.SharedInformerFactory())

	registerNodeEventHandler(handle.SharedInformerFactory())

	gpuShareUnsupportedModels := make(map[string]sets.Set[string])
	for _, m := range args.GPUShareUnsupportedModels {
		if _, ok := gpuShareUnsupportedModels[m.Vendor]; !ok {
			gpuShareUnsupportedModels[m.Vendor] = sets.New[string]()
		}
		gpuShareUnsupportedModels[m.Vendor].Insert(m.Model)
	}

	return &Plugin{
		handle:                          extendedHandle,
		nodeDeviceCache:                 deviceCache,
		gpuSharedResourceTemplatesCache: gpuSharedResourceTemplatesCache,
		gpuSharedResourceTemplatesMatchedResources: args.GPUSharedResourceTemplatesConfig.MatchedResources,
		gpuShareUnsupportedModels:                  gpuShareUnsupportedModels,
		scorer:                                     scorePlugin(args),
		disableDeviceNUMATopologyAlignment:         args.DisableDeviceNUMATopologyAlignment,
		enableQueueHint:                            args.EnableQueueHint,
	}, nil
}

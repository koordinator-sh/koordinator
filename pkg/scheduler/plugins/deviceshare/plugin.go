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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/schedulingphase"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "DeviceShare"

	// stateKey is the key in CycleState to pre-computed data.
	stateKey = Name
)

var (
	_ framework.EnqueueExtensions = &Plugin{}

	_ framework.PreFilterPlugin = &Plugin{}
	_ framework.FilterPlugin    = &Plugin{}
	_ framework.PreScorePlugin  = &Plugin{}
	_ framework.ScorePlugin     = &Plugin{}
	_ framework.ScoreExtensions = &Plugin{}
	_ framework.ReservePlugin   = &Plugin{}
	_ framework.PreBindPlugin   = &Plugin{}

	_ frameworkext.ResizePodPlugin            = &Plugin{}
	_ frameworkext.ReservationRestorePlugin   = &Plugin{}
	_ frameworkext.ReservationFilterPlugin    = &Plugin{}
	_ frameworkext.ReservationScorePlugin     = &Plugin{}
	_ frameworkext.ReservationScoreExtensions = &Plugin{}
	_ frameworkext.ReservationPreBindPlugin   = &Plugin{}
)

type Plugin struct {
	disableDeviceNUMATopologyAlignment bool
	handle                             frameworkext.ExtendedHandle
	nodeDeviceCache                    *nodeDeviceCache
	scorer                             *resourceAllocationScorer
}

type preFilterState struct {
	skip               bool
	podRequests        map[schedulingv1alpha1.DeviceType]corev1.ResourceList
	hints              apiext.DeviceAllocateHints
	hintSelectors      map[schedulingv1alpha1.DeviceType][2]labels.Selector
	jointAllocate      *apiext.DeviceJointAllocate
	primaryDeviceType  schedulingv1alpha1.DeviceType
	gpuRequirements    *GPURequirements
	allocationResult   apiext.DeviceAllocations
	preemptibleDevices map[string]map[schedulingv1alpha1.DeviceType]deviceResources
	preemptibleInRRs   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources

	hasReservationAffinity bool
}

type GPURequirements struct {
	numberOfGPUs               int
	requestsPerGPU             corev1.ResourceList
	gpuShared                  bool
	honorGPUPartition          bool
	restrictedGPUPartition     bool
	rindBusBandwidth           *resource.Quantity
	requiredTopologyScopeLevel int
	requiredTopologyScope      apiext.DeviceTopologyScope
}

func (s *preFilterState) Clone() framework.StateData {
	ns := &preFilterState{
		skip:                   s.skip,
		podRequests:            s.podRequests,
		hints:                  s.hints,
		gpuRequirements:        s.gpuRequirements,
		hintSelectors:          s.hintSelectors,
		jointAllocate:          s.jointAllocate,
		primaryDeviceType:      s.primaryDeviceType,
		allocationResult:       s.allocationResult,
		hasReservationAffinity: s.hasReservationAffinity,
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

func getPreFilterState(cycleState *framework.CycleState) (*preFilterState, *framework.Status) {
	value, err := cycleState.Read(stateKey)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	state := value.(*preFilterState)
	return state, nil
}

func (p *Plugin) EventsToRegister() []framework.ClusterEventWithHint {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("devices.%v.%v", schedulingv1alpha1.GroupVersion.Version, schedulingv1alpha1.GroupVersion.Group)
	return []framework.ClusterEventWithHint{
		{Event: framework.ClusterEvent{Resource: framework.GVK(gvk), ActionType: framework.Add | framework.Update | framework.Delete}},
	}
}

func (p *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	state, status := preparePod(pod)
	if !status.IsSuccess() {
		return nil, status
	}
	cycleState.Write(stateKey, state)
	if state.skip {
		return nil, framework.NewStatus(framework.Skip)
	}
	return nil, nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return p
}

func (p *Plugin) AddPod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, podInfoToAdd *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	node := nodeInfo.Node()
	nd := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nd == nil {
		return nil
	}

	nd.lock.RLock()
	defer nd.lock.RUnlock()

	podAllocated := nd.getUsed(podInfoToAdd.Pod.Namespace, podInfoToAdd.Pod.Name)
	if len(podAllocated) == 0 {
		return nil
	}

	rInfo := reservation.GetReservationCache().GetReservationInfoByPod(podInfoToAdd.Pod, node.Name)
	if rInfo == nil {
		nominator := p.handle.GetReservationNominator()
		if nominator != nil {
			rInfo = nominator.GetNominatedReservation(podInfoToAdd.Pod, node.Name)
		}
	}
	if rInfo == nil {
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

func (p *Plugin) RemovePod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, podInfoToRemove *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	node := nodeInfo.Node()
	nd := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nd == nil {
		return nil
	}

	nd.lock.RLock()
	defer nd.lock.RUnlock()

	podAllocated := nd.getUsed(podInfoToRemove.Pod.Namespace, podInfoToRemove.Pod.Name)
	if len(podAllocated) == 0 {
		return nil
	}

	rInfo := reservation.GetReservationCache().GetReservationInfoByPod(podInfoToRemove.Pod, node.Name)
	if rInfo == nil {
		nominator := p.handle.GetReservationNominator()
		if nominator != nil {
			rInfo = nominator.GetNominatedReservation(podInfoToRemove.Pod, node.Name)
		}
	}
	if rInfo == nil {
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

func (p *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	store := topologymanager.GetStore(cycleState)
	_, ok := store.GetAffinity(node.Name)
	if ok {
		// 当 Pod 在该节点上的 NUMA Affinity 已经算好，表明：Pod 在该节点上开启了 NUMA，且以算好的 NUMA Affinity 资源可分配，所以后续 Filter 逻辑可以忽略
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
	allocateResult, status := p.tryAllocateFromReservation(allocator, state, restoreState, restoreState.matched, pod, node, preemptible, state.hasReservationAffinity)
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

func (p *Plugin) FilterReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return framework.AsStatus(err)
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)

	allocIndex := -1
	for i, v := range restoreState.matched {
		if v.rInfo.UID() == reservationInfo.UID() {
			allocIndex = i
			break
		}
	}
	if allocIndex == -1 {
		return framework.AsStatus(fmt.Errorf("impossible, there is no relevant Reservation information in deviceShare"))
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	var affinity topologymanager.NUMATopologyHint
	if !p.disableDeviceNUMATopologyAlignment {
		store := topologymanager.GetStore(cycleState)
		affinity, _ = store.GetAffinity(nodeInfo.Node().Name)
	}

	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nodeDeviceInfo,
		node:       nodeInfo.Node(),
		pod:        pod,
		numaNodes:  affinity.NUMANodeAffinity,
	}

	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[nodeName])

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()

	_, status = p.tryAllocateFromReservation(allocator, state, restoreState, restoreState.matched[allocIndex:allocIndex+1], pod, nodeInfo.Node(), preemptible, true)
	return status
}

func (p *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
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

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return framework.AsStatus(err)
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	var affinity topologymanager.NUMATopologyHint
	if !p.disableDeviceNUMATopologyAlignment {
		store := topologymanager.GetStore(cycleState)
		affinity, _ = store.GetAffinity(nodeInfo.Node().Name)
	}

	allocator := &AutopilotAllocator{
		state:              state,
		nodeDevice:         nodeDeviceInfo,
		phaseBeingExecuted: schedulingphase.GetExtensionPointBeingExecuted(cycleState),
		node:               nodeInfo.Node(),
		pod:                pod,
		scorer:             p.scorer,
		numaNodes:          affinity.NUMANodeAffinity,
	}

	// TODO: de-duplicate logic done by the Filter phase and move head the pre-process of the resource options
	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[nodeName])

	nodeDeviceInfo.lock.Lock()
	defer nodeDeviceInfo.lock.Unlock()

	result, status := p.allocateWithNominatedReservation(
		allocator, cycleState, state, restoreState, nodeInfo.Node(), pod, preemptible)
	if !status.IsSuccess() {
		return status
	}
	if len(result) == 0 {
		preemptible = appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
		result, status = allocator.Allocate(nil, nil, nil, preemptible)
		if !status.IsSuccess() {
			return status
		}
	}
	err = fillGPUTotalMem(result, nodeDeviceInfo)
	if err != nil {
		return framework.NewStatus(framework.Error, fmt.Sprintf("fillGPUTotalMem failed: %v, node: %v", err, nodeName))
	}
	nodeDeviceInfo.updateCacheUsed(result, pod, true)
	state.allocationResult = result
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) {
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

func (p *Plugin) ResizePod(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
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

	var allocated corev1.ResourceList
	for _, allocations := range state.allocationResult {
		for _, v := range allocations {
			allocated = quotav1.Add(allocated, v.Resources)
		}
	}
	reservationutil.UpdateReservePodWithAllocatable(pod, nil, allocated)
	return nil
}

func (p *Plugin) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	return p.preBindObject(ctx, cycleState, pod, nodeName)
}

func (p *Plugin) PreBindReservation(ctx context.Context, cycleState *framework.CycleState, reservation *schedulingv1alpha1.Reservation, nodeName string) *framework.Status {
	status := p.preBindObject(ctx, cycleState, reservation, nodeName)
	if !status.IsSuccess() {
		return status
	}
	if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
		return updateReservationAllocatable(cycleState, reservation)
	}
	return nil
}

func (p *Plugin) preBindObject(ctx context.Context, cycleState *framework.CycleState, object metav1.Object, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	err := p.fillID(state.allocationResult, nodeName)
	if err != nil {
		return framework.AsStatus(err)
	}

	if err := apiext.SetDeviceAllocations(object, state.allocationResult); err != nil {
		return framework.NewStatus(framework.Error, err.Error())
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
		/*if deviceType == schedulingv1alpha1.GPU {
			// because gpu minor is well known, ID isn't needed
			continue
		}*/
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

func updateReservationAllocatable(cycleState *framework.CycleState, reservation *schedulingv1alpha1.Reservation) *framework.Status {
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
		return framework.AsStatus(err)
	}
	return nil
}

func (p *Plugin) getNodeDeviceSummary(nodeName string) (*NodeDeviceSummary, bool) {
	return p.nodeDeviceCache.getNodeDeviceSummary(nodeName)
}

func (p *Plugin) getAllNodeDeviceSummary() map[string]*NodeDeviceSummary {
	return p.nodeDeviceCache.getAllNodeDeviceSummary()
}

func New(obj runtime.Object, handle framework.Handle) (framework.Plugin, error) {
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
	go deviceCache.gcNodeDevice(context.TODO(), handle.SharedInformerFactory(), defaultGCPeriod)

	return &Plugin{
		handle:                             extendedHandle,
		nodeDeviceCache:                    deviceCache,
		scorer:                             scorePlugin(args),
		disableDeviceNUMATopologyAlignment: args.DisableDeviceNUMATopologyAlignment,
	}, nil
}

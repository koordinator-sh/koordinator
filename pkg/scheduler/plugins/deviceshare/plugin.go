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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "DeviceShare"

	// stateKey is the key in CycleState to pre-computed data.
	stateKey = Name

	// ErrMissingDevice when node does not have Device.
	ErrMissingDevice = "node(s) missing Device"

	// ErrInsufficientDevices when node can't satisfy Pod's requested resource.
	ErrInsufficientDevices = "Insufficient Devices"
)

var (
	_ framework.EnqueueExtensions = &Plugin{}

	_ framework.PreFilterPlugin = &Plugin{}
	_ framework.FilterPlugin    = &Plugin{}
	_ framework.ScorePlugin     = &Plugin{}
	_ framework.ScoreExtensions = &Plugin{}
	_ framework.ReservePlugin   = &Plugin{}
	_ framework.PreBindPlugin   = &Plugin{}

	_ frameworkext.ResizePodPlugin          = &Plugin{}
	_ frameworkext.ReservationRestorePlugin = &Plugin{}
	_ frameworkext.ReservationFilterPlugin  = &Plugin{}
	_ frameworkext.ReservationScorePlugin   = &Plugin{}
	_ frameworkext.ReservationPreBindPlugin = &Plugin{}
)

type Plugin struct {
	handle          framework.Handle
	nodeDeviceCache *nodeDeviceCache
	allocator       Allocator
	scorer          *resourceAllocationScorer
}

type preFilterState struct {
	skip               bool
	allocationResult   apiext.DeviceAllocations
	podRequests        corev1.ResourceList
	preemptibleDevices map[string]map[schedulingv1alpha1.DeviceType]deviceResources
	preemptibleInRRs   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources
}

func (s *preFilterState) Clone() framework.StateData {
	ns := &preFilterState{
		skip:             s.skip,
		allocationResult: s.allocationResult,
		podRequests:      s.podRequests,
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

func (p *Plugin) EventsToRegister() []framework.ClusterEvent {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("devices.%v.%v", schedulingv1alpha1.GroupVersion.Version, schedulingv1alpha1.GroupVersion.Group)
	return []framework.ClusterEvent{
		{Resource: framework.GVK(gvk), ActionType: framework.Add | framework.Update | framework.Delete},
	}
}

func (p *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	state := &preFilterState{
		skip:               true,
		preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
		preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
	}

	var status *framework.Status
	state.skip, state.podRequests, status = PreparePod(pod)
	if !status.IsSuccess() {
		return nil, status
	}
	cycleState.Write(stateKey, state)
	return nil, nil
}

func PreparePod(pod *corev1.Pod) (skip bool, requests corev1.ResourceList, status *framework.Status) {
	podRequests, _ := resource.PodRequestsAndLimits(pod)
	podRequests = quotav1.RemoveZeros(podRequests)

	skip = true
	requests = corev1.ResourceList{}

	for _, supportedResourceNames := range DeviceResourceNames {
		deviceRequest := quotav1.Mask(podRequests, supportedResourceNames)
		if quotav1.IsZero(deviceRequest) {
			continue
		}
		combination, err := ValidateDeviceRequest(deviceRequest)
		if err != nil {
			return false, nil, framework.NewStatus(framework.Error, err.Error())
		}
		requests = quotav1.Add(requests, ConvertDeviceRequest(deviceRequest, combination))
		skip = false
	}
	return
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return p
}

func (p *Plugin) AddPod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, podInfoToAdd *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	if reservationutil.IsReservePod(podInfoToAdd.Pod) {
		return nil
	}

	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	nd := p.nodeDeviceCache.getNodeDevice(podInfoToAdd.Pod.Spec.NodeName, false)
	if nd == nil {
		return nil
	}

	nd.lock.RLock()
	defer nd.lock.RUnlock()

	podAllocated := nd.getUsed(podInfoToAdd.Pod.Namespace, podInfoToAdd.Pod.Name)
	if len(podAllocated) == 0 {
		return nil
	}

	boundReservation, err := apiext.GetReservationAllocated(podInfoToAdd.Pod)
	if err != nil {
		return framework.AsStatus(err)
	}
	if boundReservation == nil || boundReservation.UID == "" {
		preemptible := subtractAllocated(state.preemptibleDevices[podInfoToAdd.Pod.Spec.NodeName], podAllocated)
		if len(preemptible) == 0 {
			delete(state.preemptibleDevices, podInfoToAdd.Pod.Spec.NodeName)
		}
	} else {
		preemptibleInRRs := state.preemptibleInRRs[podInfoToAdd.Pod.Spec.NodeName]
		preemptible := preemptibleInRRs[boundReservation.UID]
		preemptible = subtractAllocated(preemptible, podAllocated)
		if len(preemptible) == 0 {
			delete(preemptibleInRRs, boundReservation.UID)
		}
		if len(preemptibleInRRs) == 0 {
			delete(state.preemptibleInRRs, podInfoToAdd.Pod.Spec.NodeName)
		}
	}

	return nil
}

func (p *Plugin) RemovePod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, podInfoToRemove *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	if reservationutil.IsReservePod(podInfoToRemove.Pod) {
		return nil
	}

	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	nd := p.nodeDeviceCache.getNodeDevice(podInfoToRemove.Pod.Spec.NodeName, false)
	if nd == nil {
		return nil
	}

	nd.lock.RLock()
	defer nd.lock.RUnlock()

	podAllocated := nd.getUsed(podInfoToRemove.Pod.Namespace, podInfoToRemove.Pod.Name)
	if len(podAllocated) == 0 {
		return nil
	}

	boundReservation, err := apiext.GetReservationAllocated(podInfoToRemove.Pod)
	if err != nil {
		return framework.AsStatus(err)
	}
	if boundReservation == nil || boundReservation.UID == "" {
		preemptibleDevices := state.preemptibleDevices[podInfoToRemove.Pod.Spec.NodeName]
		state.preemptibleDevices[podInfoToRemove.Pod.Spec.NodeName] = appendAllocated(preemptibleDevices, podAllocated)
	} else {
		preemptibleInRRs := state.preemptibleInRRs[podInfoToRemove.Pod.Spec.NodeName]
		if preemptibleInRRs == nil {
			preemptibleInRRs = map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{}
			state.preemptibleInRRs[podInfoToRemove.Pod.Spec.NodeName] = preemptibleInRRs
		}
		preemptible := preemptibleInRRs[boundReservation.UID]
		preemptibleInRRs[boundReservation.UID] = appendAllocated(preemptible, podAllocated)
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

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(node.Name, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(node.Name)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[node.Name])

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()
	allocateResult, status := p.tryAllocateFromReservation(state, restoreState, restoreState.matched, nodeDeviceInfo, node.Name, pod, preemptible, false, nil)
	if !status.IsSuccess() {
		return status
	}
	if len(allocateResult) > 0 {
		return nil
	}

	preemptible = appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
	allocateResult, err := p.allocator.Allocate(node.Name, pod, state.podRequests, nodeDeviceInfo, nil, nil, nil, preemptible, nil)
	if len(allocateResult) > 0 && err == nil {
		return nil
	}
	return framework.NewStatus(framework.Unschedulable, ErrInsufficientDevices)
}

func (p *Plugin) FilterReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
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

	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[nodeName])

	nodeDeviceInfo.lock.RLock()
	defer nodeDeviceInfo.lock.RUnlock()

	allocateResult, status := p.tryAllocateFromReservation(state, restoreState, restoreState.matched[allocIndex:allocIndex+1], nodeDeviceInfo, nodeName, pod, preemptible, true, nil)
	if !status.IsSuccess() {
		return status
	}
	if len(allocateResult) == 0 {
		return framework.NewStatus(framework.Unschedulable, ErrInsufficientDevices)
	}
	return nil
}

func (p *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	nodeDeviceInfo := p.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDeviceInfo == nil {
		return nil
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)
	preemptible := appendAllocated(nil, restoreState.mergedUnmatchedUsed, state.preemptibleDevices[nodeName])

	nodeDeviceInfo.lock.Lock()
	defer nodeDeviceInfo.lock.Unlock()

	result, status := p.allocateWithNominatedReservation(
		cycleState, state, restoreState, nodeDeviceInfo, nodeName, pod, preemptible, p.scorer)
	if !status.IsSuccess() {
		return status
	}
	var err error
	if len(result) == 0 {
		preemptible = appendAllocated(preemptible, restoreState.mergedMatchedAllocatable)
		result, err = p.allocator.Allocate(nodeName, pod, state.podRequests, nodeDeviceInfo, nil, nil, nil, preemptible, p.scorer)
	}
	if err != nil || len(result) == 0 {
		return framework.NewStatus(framework.Unschedulable, ErrInsufficientDevices)
	}
	p.allocator.Reserve(pod, nodeDeviceInfo, result)
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

	p.allocator.Unreserve(pod, nodeDeviceInfo, state.allocationResult)
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

	if err := apiext.SetDeviceAllocations(object, state.allocationResult); err != nil {
		return framework.NewStatus(framework.Error, err.Error())
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

	allocatorOpts := AllocatorOptions{
		SharedInformerFactory:      extendedHandle.SharedInformerFactory(),
		KoordSharedInformerFactory: extendedHandle.KoordinatorSharedInformerFactory(),
	}
	allocator := NewAllocator(args.Allocator, allocatorOpts)

	return &Plugin{
		handle:          handle,
		nodeDeviceCache: deviceCache,
		allocator:       allocator,
		scorer:          scorePlugin(args),
	}, nil
}

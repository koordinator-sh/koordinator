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

package reservation

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	clientschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation/controller"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	Name     = "Reservation"
	stateKey = Name

	// ErrReasonReservationAffinity is the reason for Pod's reservation affinity/selector not matching.
	ErrReasonReservationAffinity = "node(s) no reservations match reservation affinity"
	// ErrReasonNodeNotMatchReservation is the reason for node not matching which the reserve pod specifies.
	ErrReasonNodeNotMatchReservation = "node(s) didn't match the nodeName specified by reservation"
	// ErrReasonReservationAllocatePolicyConflict is the reason for the AllocatePolicy of the Reservation conflicts with other Reservations on the node
	ErrReasonReservationAllocatePolicyConflict = "node(s) reservation allocate policy conflict"
	// ErrReasonReservationInactive is the reason for the reservation is failed/succeeded and should not be used.
	ErrReasonReservationInactive = "reservation is not active"
	// ErrReasonNoReservationsMeetRequirements is the reason for no reservation(s) to meet the requirements.
	ErrReasonNoReservationsMeetRequirements = "node(s) no reservation(s) to meet the requirements"
	// ErrReasonPreemptionFailed is the reason for preemption failed
	ErrReasonPreemptionFailed = "node(s) preemption failed due to insufficient resources"
)

var (
	_ framework.EnqueueExtensions = &Plugin{}

	_ framework.PreFilterPlugin  = &Plugin{}
	_ framework.FilterPlugin     = &Plugin{}
	_ framework.PostFilterPlugin = &Plugin{}
	_ framework.ScorePlugin      = &Plugin{}
	_ framework.ReservePlugin    = &Plugin{}
	_ framework.PreBindPlugin    = &Plugin{}
	_ framework.BindPlugin       = &Plugin{}

	_ frameworkext.ControllerProvider      = &Plugin{}
	_ frameworkext.PreFilterTransformer    = &Plugin{}
	_ frameworkext.ReservationNominator    = &Plugin{}
	_ frameworkext.ReservationFilterPlugin = &Plugin{}
	_ frameworkext.ReservationScorePlugin  = &Plugin{}
)

type Plugin struct {
	handle                       frameworkext.ExtendedHandle
	args                         *config.ReservationArgs
	rLister                      listerschedulingv1alpha1.ReservationLister
	client                       clientschedulingv1alpha1.SchedulingV1alpha1Interface
	reservationCache             *reservationCache
	nominator                    *nominator
	preemptionMgr                *PreemptionMgr
	enableLazyReservationRestore bool
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.ReservationArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type ReservationArgs, got %T", args)
	}
	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("want handle to be of type frameworkext.ExtendedHandle, got %T", handle)
	}

	sharedInformerFactory := handle.SharedInformerFactory()
	podLister := handle.SharedInformerFactory().Core().V1().Pods().Lister()
	koordSharedInformerFactory := extendedHandle.KoordinatorSharedInformerFactory()
	reservationLister := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()
	cache := newReservationCache(reservationLister)
	nm := newNominator(podLister, reservationLister)
	registerReservationEventHandler(cache, koordSharedInformerFactory, nm)
	registerPodEventHandler(cache, nm, sharedInformerFactory)

	// TODO(joseph): Considering the amount of changed code,
	// temporarily use global variable to store ReservationCache instance,
	// and then refactor to separate ReservationCache later.
	SetReservationCache(cache)

	p := &Plugin{
		handle:                       extendedHandle,
		args:                         pluginArgs,
		rLister:                      reservationLister,
		client:                       extendedHandle.KoordinatorClientSet().SchedulingV1alpha1(),
		reservationCache:             cache,
		nominator:                    nm,
		enableLazyReservationRestore: k8sfeature.DefaultFeatureGate.Enabled(features.LazyReservationRestore),
	}

	if pluginArgs.EnablePreemption {
		preemptionMgr, err := newPreemptionMgr(pluginArgs, extendedHandle, podLister, reservationLister)
		if err != nil {
			return nil, fmt.Errorf("failed to new preemption, err: %w", err)
		}
		p.preemptionMgr = preemptionMgr
	}

	return p, nil
}

func (pl *Plugin) Name() string { return Name }

func (pl *Plugin) NewControllers() ([]frameworkext.Controller, error) {
	reservationController := controller.New(
		pl.handle.SharedInformerFactory(),
		pl.handle.KoordinatorSharedInformerFactory(),
		pl.handle.KoordinatorClientSet(),
		1)
	return []frameworkext.Controller{reservationController}, nil
}

func (pl *Plugin) EventsToRegister() []framework.ClusterEventWithHint {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("reservations.%v.%v", schedulingv1alpha1.GroupVersion.Version, schedulingv1alpha1.GroupVersion.Group)
	return []framework.ClusterEventWithHint{
		{Event: framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Delete}},
		{Event: framework.ClusterEvent{Resource: framework.GVK(gvk), ActionType: framework.Add | framework.Update | framework.Delete}},
	}
}

var _ framework.StateData = &stateData{}

type stateData struct {
	// scheduling cycle data
	schedulingStateData

	// all cycle data
	// NOTE: The part of data is kept during both the scheduling cycle and the binding cycle. In case too many
	// binding goroutines reference the data causing OOM issues, the memory overhead of this part should be
	// small and its space complexity should be no more than O(1) for pods, reservations and nodes.
	assumed *frameworkext.ReservationInfo
}

// schedulingStateData is the data only kept in the scheduling cycle. It could be cleaned up
// before entering the binding cycle to reduce memory cost.
type schedulingStateData struct {
	preemptLock          sync.RWMutex
	hasAffinity          bool
	reservationName      string
	podRequests          corev1.ResourceList
	podRequestsResources *framework.Resource
	preemptible          map[string]corev1.ResourceList
	preemptibleInRRs     map[string]map[types.UID]corev1.ResourceList

	nodeReservationStates    map[string]*nodeReservationState
	nodeReservationDiagnosis map[string]*nodeDiagnosisState
	preferredNode            string
}

type nodeReservationState struct {
	nodeName string
	// matchedOrIgnored represents all matched or ignored reservations for the scheduling pod.
	matchedOrIgnored []*frameworkext.ReservationInfo

	// podRequested represents all Pods(including matched reservation) requested resources
	// but excluding the already allocated from unmatched reservations
	podRequested *framework.Resource
	// rAllocated represents the allocated resources of matched reservations
	rAllocated *framework.Resource

	unmatched     []*frameworkext.ReservationInfo
	preRestored   bool // restore in PreFilter or Filter
	finalRestored bool // restore in Filter
}

type nodeDiagnosisState struct {
	nodeName string

	ignored int // resource reservations are ignored due to the pod label

	ownerMatched             int // owner matched
	nameMatched              int // owner matched and reservation name matched
	nameUnmatched            int // owner matched but BeforePreFilter unmatched due to reservation name
	isUnschedulableUnmatched int // owner matched but BeforePreFilter unmatched due to unschedulable
	affinityUnmatched        int // owner matched but BeforePreFilter unmatched due to affinity
	notExactMatched          int // owner matched but BeforePreFilter unmatched due to not exact match
	taintsUnmatched          int // owner matched but BeforePreFilter unmatched due to reservation taints
	taintsUnmatchedReasons   map[string]int
}

func (s *stateData) Clone() framework.StateData {
	ns := &stateData{
		schedulingStateData: schedulingStateData{
			hasAffinity:              s.hasAffinity,
			reservationName:          s.reservationName,
			podRequests:              s.podRequests,
			podRequestsResources:     s.podRequestsResources,
			nodeReservationStates:    s.nodeReservationStates,
			nodeReservationDiagnosis: s.nodeReservationDiagnosis,
			preferredNode:            s.preferredNode,
		},
		assumed: s.assumed,
	}

	s.preemptLock.RLock()
	defer s.preemptLock.RUnlock()

	preemptible := map[string]corev1.ResourceList{}
	for nodeName, returned := range s.preemptible {
		preemptible[nodeName] = returned.DeepCopy()
	}
	ns.preemptible = preemptible

	preemptibleInRRs := map[string]map[types.UID]corev1.ResourceList{}
	for nodeName, rrs := range s.preemptibleInRRs {
		rrInNode := preemptibleInRRs[nodeName]
		if rrInNode == nil {
			rrInNode = map[types.UID]corev1.ResourceList{}
			preemptibleInRRs[nodeName] = rrInNode
		}
		for reservationUID, returned := range rrs {
			rrInNode[reservationUID] = returned.DeepCopy()
		}
	}
	ns.preemptibleInRRs = preemptibleInRRs

	return ns
}

// CleanSchedulingData clears the scheduling cycle data in the stateData to reduce memory cost before entering
// the binding cycle.
func (s *stateData) CleanSchedulingData() {
	s.schedulingStateData = schedulingStateData{}
}

func getStateData(cycleState *framework.CycleState) *stateData {
	v, err := cycleState.Read(stateKey)
	if err != nil {
		return &stateData{}
	}
	s, ok := v.(*stateData)
	if !ok || s == nil {
		return &stateData{}
	}
	return s
}

// PreFilter checks if the pod is a reserve pod. If it is, update cycle state to annotate reservation scheduling.
// Also do validations in this phase.
func (pl *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		// validate reserve pod and reservation
		klog.V(4).InfoS("Attempting to pre-filter reserve pod", "pod", klog.KObj(pod))
		rName := reservationutil.GetReservationNameFromReservePod(pod)
		r, err := pl.rLister.Get(rName)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(3).InfoS("skip the pre-filter for reservation since the object is not found", "pod", klog.KObj(pod), "reservation", rName)
			}
			return nil, framework.NewStatus(framework.Error, "cannot get reservation, err: "+err.Error())
		}
		err = reservationutil.ValidateReservation(r)
		if err != nil {
			return nil, framework.NewStatus(framework.Error, err.Error())
		}
		return nil, nil
	}

	var preResult *framework.PreFilterResult

	state := getStateData(cycleState)
	if state.hasAffinity {
		if len(state.nodeReservationStates) == 0 {
			return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAffinity)
		}
		preResult = &framework.PreFilterResult{
			NodeNames: sets.Set[string]{},
		}
		for nodeName := range state.nodeReservationStates {
			preResult.NodeNames.Insert(nodeName)
		}
	} else if len(state.nodeReservationStates) <= 0 { // nor available reservation neither a reserve pod
		return nil, framework.NewStatus(framework.Skip)
	}

	return preResult, nil
}

func (pl *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return pl
}

func (pl *Plugin) AddPod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, podInfoToAdd *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	podRequests := resourceapi.PodRequests(podInfoToAdd.Pod, resourceapi.PodResourcesOptions{})

	podRequests[corev1.ResourcePods] = *resource.NewQuantity(1, resource.DecimalSI) // count pods resources
	state := getStateData(cycleState)
	node := nodeInfo.Node()
	rInfo := pl.reservationCache.GetReservationInfoByPod(podInfoToAdd.Pod, node.Name)
	if rInfo == nil {
		rInfo = pl.GetNominatedReservation(podInfoToAdd.Pod, node.Name)
	}

	state.preemptLock.Lock()
	defer state.preemptLock.Unlock()
	if rInfo == nil {
		preemptible := state.preemptible[node.Name]
		state.preemptible[node.Name] = quotav1.Subtract(preemptible, podRequests)
	} else {
		preemptibleInRRs := state.preemptibleInRRs[node.Name]
		if preemptibleInRRs == nil {
			preemptibleInRRs = map[types.UID]corev1.ResourceList{}
			state.preemptibleInRRs[node.Name] = preemptibleInRRs
		}
		preemptible := preemptibleInRRs[rInfo.UID()]
		preemptibleInRRs[rInfo.UID()] = quotav1.Subtract(preemptible, podRequests)
	}

	return nil
}

func (pl *Plugin) RemovePod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, podInfoToRemove *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	podRequests := resourceapi.PodRequests(podInfoToRemove.Pod, resourceapi.PodResourcesOptions{})

	podRequests[corev1.ResourcePods] = *resource.NewQuantity(1, resource.DecimalSI) // count pods resources
	state := getStateData(cycleState)
	node := nodeInfo.Node()
	rInfo := pl.reservationCache.GetReservationInfoByPod(podInfoToRemove.Pod, node.Name)
	if rInfo == nil {
		rInfo = pl.GetNominatedReservation(podInfoToRemove.Pod, node.Name)
	}

	state.preemptLock.Lock()
	defer state.preemptLock.Unlock()
	if rInfo == nil {
		preemptible := state.preemptible[node.Name]
		state.preemptible[node.Name] = quotav1.Add(preemptible, podRequests)
	} else {
		preemptibleInRRs := state.preemptibleInRRs[node.Name]
		if preemptibleInRRs == nil {
			preemptibleInRRs = map[types.UID]corev1.ResourceList{}
			state.preemptibleInRRs[node.Name] = preemptibleInRRs
		}
		preemptible := preemptibleInRRs[rInfo.UID()]
		preemptibleInRRs[rInfo.UID()] = quotav1.Add(preemptible, podRequests)
	}

	return nil
}

// Filter only processes pods either the pod is a reserve pod or a pod can allocate reserved resources on the node.
func (pl *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	if reservationutil.IsReservePod(pod) || apiext.IsReservationOperatingMode(pod) {
		var allocatePolicy schedulingv1alpha1.ReservationAllocatePolicy
		if reservationutil.IsReservePod(pod) {
			// if the reservation specifies a nodeName initially, check if the nodeName matches
			rNodeName := reservationutil.GetReservePodNodeName(pod)
			if len(rNodeName) > 0 && rNodeName != nodeInfo.Node().Name {
				return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonNodeNotMatchReservation)
			}

			rName := reservationutil.GetReservationNameFromReservePod(pod)
			reservation, err := pl.rLister.Get(rName)
			if err != nil {
				return framework.NewStatus(framework.Error, "reservation not found")
			}
			allocatePolicy = reservation.Spec.AllocatePolicy
		} else if apiext.IsReservationOperatingMode(pod) {
			allocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyAligned
		}

		status := pl.reservationCache.forEachAvailableReservationOnNode(node.Name, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
			// ReservationAllocatePolicyDefault cannot coexist with other allocate policies
			if (allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyDefault ||
				rInfo.GetAllocatePolicy() == schedulingv1alpha1.ReservationAllocatePolicyDefault) &&
				allocatePolicy != rInfo.GetAllocatePolicy() {
				return false, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAllocatePolicyConflict)
			}
			return true, nil
		})
		if !status.IsSuccess() {
			return status
		}
	}

	state := getStateData(cycleState)
	nodeRState := state.nodeReservationStates[node.Name]
	if nodeRState == nil {
		nodeRState = &nodeReservationState{}
	}

	if len(nodeRState.matchedOrIgnored) <= 0 && state.hasAffinity {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAffinity)
	}

	if reservationutil.IsReservePod(pod) {
		// TODO: handle pre-allocation cases
		return nil
	}

	matchedReservations := nodeRState.matchedOrIgnored
	if len(matchedReservations) == 0 {
		status := func() *framework.Status {
			state.preemptLock.RLock()
			defer state.preemptLock.RUnlock()

			if len(state.preemptible[node.Name]) > 0 || len(state.preemptibleInRRs[node.Name]) > 0 {
				preemptible := state.preemptible[node.Name]
				preemptibleResource := framework.NewResource(preemptible)
				insufficientResources := fitsNode(state.podRequestsResources, nodeInfo, nodeRState, nil, preemptibleResource)
				if len(insufficientResources) != 0 {
					return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonPreemptionFailed)
				}
			}
			return nil
		}()
		if !status.IsSuccess() {
			return status
		}

		return nil
	}

	return pl.filterWithReservations(ctx, cycleState, pod, nodeInfo, matchedReservations, state.hasAffinity)
}

func (pl *Plugin) filterWithReservations(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo, matchedReservations []*frameworkext.ReservationInfo, requiredFromReservation bool) *framework.Status {
	if !requiredFromReservation { // no need to filter out resources
		return nil
	}

	extender, ok := pl.handle.(frameworkext.FrameworkExtender)
	if !ok {
		return framework.AsStatus(fmt.Errorf("not implemented frameworkext.FrameworkExtender"))
	}

	node := nodeInfo.Node()
	state := getStateData(cycleState)
	nodeRState := state.nodeReservationStates[node.Name]
	podRequestsResourceNames := quotav1.ResourceNames(state.podRequests)

	allInsufficientResourcesByNode := sets.NewString()
	var allInsufficientResourceReasonsByReservation []string
	for _, rInfo := range matchedReservations {
		resourceNames := quotav1.Intersection(rInfo.ResourceNames, podRequestsResourceNames)
		// NOTE: The reservation may not consider the irrelevant pods that have no matched resource names since it makes
		// no sense in most cases but introduces a performance overhead. However, we allow pods to allocate reserved
		// resources to accomplish their reservation affinities.
		if len(resourceNames) == 0 && !state.hasAffinity {
			continue
		}
		// When the pod specifies a reservation name, we record the admission reasons.
		if len(state.reservationName) > 0 && state.reservationName != rInfo.GetName() {
			continue
		}
		requireDetailReasons := len(state.reservationName) > 0 && state.reservationName == rInfo.GetName()

		state.preemptLock.RLock()
		preemptibleInRR := state.preemptibleInRRs[node.Name][rInfo.UID()]
		preemptible := framework.NewResource(preemptibleInRR)
		preemptible.Add(state.preemptible[node.Name])
		insufficientResourcesByNode := fitsNode(state.podRequestsResources, nodeInfo, nodeRState, rInfo, preemptible)
		state.preemptLock.RUnlock()

		nodeFits := len(insufficientResourcesByNode) <= 0
		allInsufficientResourcesByNode.Insert(insufficientResourcesByNode...)

		reservationFits := false
		allocatePolicy := rInfo.GetAllocatePolicy()
		if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyDefault ||
			allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyAligned {
			reservationFits = nodeFits
		} else if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
			insufficientResourceReasonsByReservation := fitsReservation(state.podRequests, rInfo, preemptibleInRR, requireDetailReasons)

			reservationFits = len(insufficientResourceReasonsByReservation) <= 0
			allInsufficientResourceReasonsByReservation = append(allInsufficientResourceReasonsByReservation, insufficientResourceReasonsByReservation...)
		}

		// Before nominating a reservation in PreScore or Reserve, check the reservation by multiple plugins to make
		// the Filter phase give a more accurate result. It is extensible to support more policies.
		status := extender.RunReservationFilterPlugins(ctx, cycleState, pod, rInfo, nodeInfo)
		if !status.IsSuccess() {
			allInsufficientResourceReasonsByReservation = append(allInsufficientResourceReasonsByReservation, status.Reasons()...)
			continue
		}

		if nodeFits && reservationFits {
			return nil
		}
	}

	// The Pod requirement must be allocated from Reservation, but currently no Reservation meets the requirement.
	// We will keep all failure reasons.
	failureReasons := make([]string, 0, len(allInsufficientResourcesByNode)+len(allInsufficientResourceReasonsByReservation)+1)
	for insufficientResourceByNode := range allInsufficientResourcesByNode {
		failureReasons = append(failureReasons, fmt.Sprintf("Insufficient %s by node", insufficientResourceByNode))
	}
	failureReasons = append(failureReasons, allInsufficientResourceReasonsByReservation...)

	if len(failureReasons) == 0 {
		failureReasons = append(failureReasons, ErrReasonNoReservationsMeetRequirements)
	}

	return framework.NewStatus(framework.Unschedulable, failureReasons...)
}

var dummyResource = framework.NewResource(nil)

// fitsNode checks if node have enough resources to host the pod.
func fitsNode(podRequest *framework.Resource, nodeInfo *framework.NodeInfo, nodeRState *nodeReservationState, rInfo *frameworkext.ReservationInfo, preemptible *framework.Resource) []string {
	var insufficientResources []string

	allowedPodNumber := nodeInfo.Allocatable.AllowedPodNumber
	if len(nodeInfo.Pods)-len(nodeRState.matchedOrIgnored)+1 > allowedPodNumber {
		insufficientResources = append(insufficientResources, string(corev1.ResourcePods))
	}

	if podRequest.MilliCPU == 0 &&
		podRequest.Memory == 0 &&
		podRequest.EphemeralStorage == 0 &&
		len(podRequest.ScalarResources) == 0 {
		return insufficientResources
	}

	var rRemained *framework.Resource
	if rInfo != nil {
		// Reservation available = Allocatable - Allocated - InnerReserved
		resources := quotav1.Subtract(quotav1.Subtract(rInfo.Allocatable, rInfo.Allocated), rInfo.Reserved)
		rRemained = framework.NewResource(resources)
	} else {
		rRemained = dummyResource
	}
	allRAllocated := nodeRState.rAllocated
	if allRAllocated == nil {
		allRAllocated = dummyResource
	}
	podRequested := nodeRState.podRequested
	if podRequested == nil {
		podRequested = dummyResource
	}
	if preemptible == nil {
		preemptible = dummyResource
	}

	if podRequest.MilliCPU > (nodeInfo.Allocatable.MilliCPU - (podRequested.MilliCPU - rRemained.MilliCPU - allRAllocated.MilliCPU - preemptible.MilliCPU)) {
		insufficientResources = append(insufficientResources, string(corev1.ResourceCPU))
	}
	if podRequest.Memory > (nodeInfo.Allocatable.Memory - (podRequested.Memory - rRemained.Memory - allRAllocated.Memory - preemptible.Memory)) {
		insufficientResources = append(insufficientResources, string(corev1.ResourceMemory))
	}
	if podRequest.EphemeralStorage > (nodeInfo.Allocatable.EphemeralStorage - (podRequested.EphemeralStorage - rRemained.EphemeralStorage - allRAllocated.EphemeralStorage - preemptible.EphemeralStorage)) {
		insufficientResources = append(insufficientResources, string(corev1.ResourceEphemeralStorage))
	}

	for rName, rQuant := range podRequest.ScalarResources {
		if rQuant > (nodeInfo.Allocatable.ScalarResources[rName] - (podRequested.ScalarResources[rName] - rRemained.ScalarResources[rName] - allRAllocated.ScalarResources[rName] - preemptible.ScalarResources[rName])) {
			insufficientResources = append(insufficientResources, string(rName))
		}
	}

	return insufficientResources
}

func fitsReservation(podRequest corev1.ResourceList, rInfo *frameworkext.ReservationInfo, preemptibleInRR corev1.ResourceList, isDetailed bool) []string {
	allocated := rInfo.Allocated
	if len(preemptibleInRR) > 0 {
		allocated = quotav1.SubtractWithNonNegativeResult(allocated, preemptibleInRR)
	}
	allocatable := rInfo.Allocatable
	allocated = quotav1.Mask(allocated, rInfo.ResourceNames)
	reserved := quotav1.Mask(rInfo.Reserved, rInfo.ResourceNames)
	requests := quotav1.Mask(podRequest, rInfo.ResourceNames)

	var insufficientResourceReasons []string

	// check "pods" resource in the reservation when reserved explicitly
	if maxPods, found := allocatable[corev1.ResourcePods]; found {
		allocatedPods := rInfo.GetAllocatedPods()
		if preemptiblePodsInRR, found := preemptibleInRR[corev1.ResourcePods]; found {
			allocatedPods -= int(preemptiblePodsInRR.Value()) // assert no overflow
		}
		if int64(allocatedPods)+1 > maxPods.Value() {
			if !isDetailed {
				insufficientResourceReasons = append(insufficientResourceReasons,
					reservationutil.NewReservationReason("Too many pods"))
			} else { // print a reason with resource amounts if needed
				insufficientResourceReasons = append(insufficientResourceReasons,
					reservationutil.NewReservationReason("Too many pods, requested: 1, used: %d, capacity: %d",
						allocatedPods, maxPods.Value()))
			}
		}
	}

	for resourceName, requested := range requests {
		if requested.IsZero() {
			continue
		}

		capacity, found := allocatable[resourceName]
		if !found {
			capacity = *resource.NewQuantity(0, resource.DecimalSI)
		}
		used, found := allocated[resourceName]
		if !found {
			used = *resource.NewQuantity(0, resource.DecimalSI)
		}
		reservedQ, found := reserved[resourceName]
		if found {
			// NOTE: capacity excludes the reserved resource
			capacity.Sub(reservedQ)
		}
		remained := capacity.DeepCopy()
		remained.Sub(used)

		if requested.Cmp(remained) <= 0 {
			continue
		}

		if !isDetailed { // just give the resource name
			insufficientResourceReasons = append(insufficientResourceReasons,
				reservationutil.NewReservationReason("Insufficient "+string(resourceName)))
		} else if resourceName == corev1.ResourceCPU { // print a reason with resource amounts if needed
			insufficientResourceReasons = append(insufficientResourceReasons,
				reservationutil.NewReservationReason("Insufficient %s, requested: %d, used: %d, capacity: %d",
					resourceName, requested.MilliValue(), used.MilliValue(), capacity.MilliValue()))
		} else { // print a reason with resource amounts if needed
			insufficientResourceReasons = append(insufficientResourceReasons,
				reservationutil.NewReservationReason("Insufficient %s, requested: %d, used: %d, capacity: %d",
					resourceName, requested.Value(), used.Value(), capacity.Value()))
		}
	}

	return insufficientResourceReasons
}

func (pl *Plugin) PostFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	var result *framework.PostFilterResult
	var reasons []string

	// If Reservation Preemption is enabled, try preemption before aggregating failure reasons.
	if pl.preemptionMgr != nil {
		preemptionResult, preemptionStatus := pl.preemptionMgr.PostFilter(ctx, cycleState, pod, filteredNodeStatusMap)
		if preemptionStatus.IsSuccess() ||
			preemptionStatus.Code() == framework.UnschedulableAndUnresolvable ||
			!preemptionStatus.IsUnschedulable() {
			return preemptionResult, preemptionStatus
		}
		if preemptionResult != nil && preemptionResult.Mode() != framework.ModeNoop {
			result = preemptionResult
		}

		reasons = append(reasons, preemptionStatus.Reasons()...)
	}

	state := getStateData(cycleState)
	postFilterReasons := pl.makePostFilterReasons(state, filteredNodeStatusMap)
	reasons = append(reasons, postFilterReasons...)
	return result, framework.NewStatus(framework.Unschedulable, reasons...)
}

func (pl *Plugin) makePostFilterReasons(state *stateData, filteredNodeStatusMap framework.NodeToStatusMap) []string {
	var (
		ownerMatched             = 0
		nameMatched              = 0
		affinityUnmatched        = 0
		isUnSchedulableUnmatched = 0
		notExactMatched          = 0
		nameUnmatched            = 0
		taintsUnmatchedReasons   = map[string]int{}
	)
	// failure reasons and counts for the nodes which have not been handled by the Reservation's Filter
	reasonsByNode := map[string]int{}

	for nodeName, diagnosisState := range state.nodeReservationDiagnosis {
		// summarize node diagnosis states
		ownerMatched += diagnosisState.ownerMatched
		nameMatched += diagnosisState.nameMatched
		isUnSchedulableUnmatched += diagnosisState.isUnschedulableUnmatched
		affinityUnmatched += diagnosisState.affinityUnmatched
		notExactMatched += diagnosisState.notExactMatched
		nameUnmatched += diagnosisState.nameUnmatched
		for taintKey, nodeCount := range diagnosisState.taintsUnmatchedReasons {
			taintsUnmatchedReasons[taintKey] += nodeCount
		}

		// calculate the remaining unmatched which is owner-matched and Reservation BeforePreFilter matched
		remainUnmatched := diagnosisState.ownerMatched - diagnosisState.nameUnmatched - diagnosisState.isUnschedulableUnmatched - diagnosisState.affinityUnmatched - diagnosisState.notExactMatched - diagnosisState.taintsUnmatched
		if remainUnmatched <= 0 { // no need to check other reasons
			continue
		}
		// count the failure reasons which is neither counted by the PreFilterTransformer nor by the Reservation Filter.
		nodeReasons := map[string]int{}
		if failureStatus, ok := filteredNodeStatusMap[nodeName]; ok {
			for _, reason := range failureStatus.Reasons() {
				if reservationutil.IsReservationReason(reason) { // reservation-level reasons are not counted
					remainUnmatched--
					continue
				}
				nodeReasons[reason]++
			}
		}
		if remainUnmatched <= 0 { // capped with zero
			continue
		}
		for reason, count := range nodeReasons {
			reasonsByNode[reason] += remainUnmatched * count
		}
	}
	// if the pod specifies an affinity, we always show the owner matched reason
	if ownerMatched <= 0 && !state.hasAffinity {
		return nil
	}

	// Make the error messages: The framework does not aggregate the same reasons for the PostFilter, so we need
	// to prepare the exact messages by ourselves.
	var reasons []string
	var b strings.Builder
	if nameMatched > 0 {
		b.WriteString(strconv.Itoa(nameMatched))
		b.WriteString(" Reservation(s) exactly matches the requested reservation name")
		reasons = append(reasons, b.String())
		b.Reset()
	}
	if nameUnmatched > 0 {
		b.WriteString(strconv.Itoa(nameUnmatched))
		b.WriteString(" Reservation(s) didn't match the requested reservation name")
		reasons = append(reasons, b.String())
		b.Reset()
	}
	if affinityUnmatched > 0 {
		b.WriteString(strconv.Itoa(affinityUnmatched))
		b.WriteString(" Reservation(s) didn't match affinity rules")
		reasons = append(reasons, b.String())
		b.Reset()
	}
	if isUnSchedulableUnmatched > 0 {
		b.WriteString(strconv.Itoa(isUnSchedulableUnmatched))
		b.WriteString(" Reservation(s) is unschedulable")
		reasons = append(reasons, b.String())
		b.Reset()
	}
	if notExactMatched > 0 {
		b.WriteString(strconv.Itoa(notExactMatched))
		b.WriteString(" Reservation(s) is not exact matched")
		reasons = append(reasons, b.String())
		b.Reset()
	}
	for taintKey, count := range taintsUnmatchedReasons {
		b.WriteString(strconv.Itoa(count))
		b.WriteString(" Reservation(s) had untolerated taint ")
		b.WriteString(taintKey)
		reasons = append(reasons, b.String())
		b.Reset()
	}
	for nodeReason, count := range reasonsByNode { // node reason Filter failed
		b.WriteString(strconv.Itoa(count))
		b.WriteString(" Reservation(s) for node reason that ")
		b.WriteString(nodeReason)
		reasons = append(reasons, b.String())
		b.Reset()
	}
	b.WriteString(strconv.Itoa(ownerMatched))
	b.WriteString(" Reservation(s) matched owner total")
	reasons = append(reasons, b.String())
	return reasons
}

func (pl *Plugin) FilterReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	return nil
}

func (pl *Plugin) FilterNominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeName string) *framework.Status {
	// TODO(joseph): We can consider optimizing these codes. It seems that there is no need to exist at present.
	state := getStateData(cycleState)

	nodeRState := state.nodeReservationStates[nodeName]
	if nodeRState == nil {
		nodeRState = &nodeReservationState{}
	}

	var rInfo *frameworkext.ReservationInfo
	for _, v := range nodeRState.matchedOrIgnored {
		if v.UID() == reservationInfo.UID() {
			rInfo = v
			break
		}
	}

	if rInfo == nil {
		return framework.AsStatus(fmt.Errorf("impossible, there is no relevant Reservation information"))
	}

	if rInfo.IsAllocateOnce() && rInfo.GetAllocatedPods() > 0 {
		return framework.NewStatus(framework.Unschedulable, "reservation has allocateOnce enabled and has already been allocated")
	}

	nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "missing node")
	}

	return pl.filterWithReservations(ctx, cycleState, pod, nodeInfo, []*frameworkext.ReservationInfo{rInfo}, true)
}

func (pl *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if reservationutil.IsReservePod(pod) {
		rName := reservationutil.GetReservationNameFromReservePod(pod)
		assumedReservation, err := pl.rLister.Get(rName)
		if err != nil {
			return framework.AsStatus(err)
		}
		assumedReservation = assumedReservation.DeepCopy()
		assumedReservation.Status.NodeName = nodeName
		pl.reservationCache.assumeReservation(assumedReservation)
		return nil
	}

	state := getStateData(cycleState)

	if apiext.IsReservationIgnored(pod) {
		// clean scheduling cycle to avoid unnecessary memory cost before entering the binding
		state.CleanSchedulingData()
		klog.V(4).InfoS("Reserve pod to node and reservations are ignored",
			"pod", klog.KObj(pod), "node", nodeName)
		return nil
	}

	nominatedReservation := pl.handle.GetReservationNominator().GetNominatedReservation(pod, nodeName)
	if nominatedReservation == nil {
		// The scheduleOne skip scores and reservation nomination if there is only one node available.
		var status *framework.Status
		nominatedReservation, status = pl.handle.GetReservationNominator().NominateReservation(ctx, cycleState, pod, nodeName)
		if !status.IsSuccess() {
			return status
		}
		if nominatedReservation == nil {
			if state.hasAffinity {
				return framework.NewStatus(framework.Unschedulable, ErrReasonReservationAffinity)
			}
			klog.V(5).Infof("Skip reserve with reservation since there are no matched reservations, pod %v, node: %v", klog.KObj(pod), nodeName)
			// clean scheduling cycle to avoid unnecessary memory cost before entering the binding
			state.CleanSchedulingData()
			return nil
		}
		pl.handle.GetReservationNominator().AddNominatedReservation(pod, nodeName, nominatedReservation)
	}

	err := pl.reservationCache.assumePod(nominatedReservation.UID(), pod)
	if err != nil {
		klog.ErrorS(err, "Failed to assume pod in reservationCache", "pod", klog.KObj(pod), "reservation", klog.KObj(nominatedReservation))
		return framework.AsStatus(err)
	}
	state.assumed = nominatedReservation.Clone()
	// clean scheduling cycle to avoid unnecessary memory cost before entering the binding
	state.CleanSchedulingData()
	klog.V(4).InfoS("Reserve pod to node with reservations", "pod", klog.KObj(pod), "node", nodeName, "assumed", klog.KObj(nominatedReservation))
	return nil
}

func (pl *Plugin) Unreserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) {
	if reservationutil.IsReservePod(pod) {
		rName := reservationutil.GetReservationNameFromReservePod(pod)
		assumedReservation, err := pl.rLister.Get(rName)
		if err != nil {
			klog.ErrorS(err, "Failed to get reservation in Unreserve phase", "reservation", rName, "nodeName", nodeName)
			return
		}
		assumedReservation = assumedReservation.DeepCopy()
		assumedReservation.Status.NodeName = nodeName
		pl.reservationCache.forgetReservation(assumedReservation)
		return
	}

	if apiext.IsReservationIgnored(pod) {
		klog.V(5).InfoS("Unreserve pod to node and reservations are ignored",
			"pod", klog.KObj(pod), "node", nodeName)
		return
	}

	state := getStateData(cycleState)
	if state.assumed != nil {
		klog.V(4).InfoS("Attempting to unreserve pod to node with reservations", "pod", klog.KObj(pod), "node", nodeName, "assumed", klog.KObj(state.assumed))
		pl.reservationCache.forgetPod(state.assumed.UID(), pod)
	} else {
		klog.V(5).InfoS("Skip the Reservation Unreserve, no assumed reservation", "pod", klog.KObj(pod), "node", nodeName)
	}
	return
}

func (pl *Plugin) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if reservationutil.IsReservePod(pod) ||
		apiext.IsReservationIgnored(pod) {
		return nil
	}

	state := getStateData(cycleState)
	if state.assumed == nil {
		if state.hasAffinity {
			return framework.NewStatus(framework.Unschedulable, ErrReasonReservationAffinity)
		}
		klog.V(5).Infof("Skip the Reservation PreBind since no reservation allocated for the pod %s on node %s", klog.KObj(pod), nodeName)
		return nil
	}

	reservation := state.assumed
	klog.V(4).Infof("Attempting to pre-bind pod %v to node %v with reservation %v", klog.KObj(pod), nodeName, klog.KObj(reservation))

	apiext.SetReservationAllocated(pod, reservation.GetObject())
	return nil
}

// Bind fake binds reserve pod and mark corresponding reservation as Available.
// NOTE: This Bind plugin should get called before DefaultBinder; plugin order should be configured.
func (pl *Plugin) Bind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if !reservationutil.IsReservePod(pod) {
		return framework.NewStatus(framework.Skip)
	}

	rName := reservationutil.GetReservationNameFromReservePod(pod)
	klog.V(4).InfoS("Attempting to bind reservation to node", "pod", klog.KObj(pod), "reservation", rName, "node", nodeName)

	var reservation *schedulingv1alpha1.Reservation
	err := util.RetryOnConflictOrTooManyRequests(func() error {
		var err error
		reservation, err = pl.rLister.Get(rName)
		if err != nil {
			return err
		}

		// check if the reservation has been inactive
		if reservationutil.IsReservationFailed(reservation) {
			return fmt.Errorf(ErrReasonReservationInactive)
		}

		// mark reservation as available
		reservation = reservation.DeepCopy()
		if err = reservationutil.SetReservationAvailable(reservation, nodeName); err != nil {
			return err
		}
		_, err = pl.client.Reservations().UpdateStatus(context.TODO(), reservation, metav1.UpdateOptions{})
		if err != nil {
			klog.V(4).ErrorS(err, "failed to update reservation", "reservation", klog.KObj(reservation))
		}
		return err
	})
	if err != nil {
		klog.Errorf("Failed to update bind Reservation %s, err: %v", rName, err)
		return framework.AsStatus(err)
	}

	pl.handle.EventRecorder().Eventf(reservation, nil, corev1.EventTypeNormal, "Scheduled", "Binding", "Successfully assigned %v to %v", rName, nodeName)
	return nil
}

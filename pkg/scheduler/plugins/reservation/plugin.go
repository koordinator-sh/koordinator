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
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	clientschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
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
	// ErrReasonReservationInsufficientResources is the reason for the reservation's resources are insufficient.
	ErrReasonReservationInsufficientResources = "node(s) reservations insufficient resources"
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
	handle           frameworkext.ExtendedHandle
	args             *config.ReservationArgs
	rLister          listerschedulingv1alpha1.ReservationLister
	client           clientschedulingv1alpha1.SchedulingV1alpha1Interface
	reservationCache *reservationCache
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
	koordSharedInformerFactory := extendedHandle.KoordinatorSharedInformerFactory()
	reservationLister := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()
	cache := newReservationCache(reservationLister)
	registerReservationEventHandler(cache, koordSharedInformerFactory)
	registerPodEventHandler(cache, sharedInformerFactory)

	// TODO(joseph): Considering the amount of changed code,
	// temporarily use global variable to store ReservationCache instance,
	// and then refactor to separate ReservationCache later.
	SetReservationCache(cache)

	p := &Plugin{
		handle:           extendedHandle,
		args:             pluginArgs,
		rLister:          reservationLister,
		client:           extendedHandle.KoordinatorClientSet().SchedulingV1alpha1(),
		reservationCache: cache,
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

func (pl *Plugin) EventsToRegister() []framework.ClusterEvent {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("reservations.%v.%v", schedulingv1alpha1.GroupVersion.Version, schedulingv1alpha1.GroupVersion.Group)
	return []framework.ClusterEvent{
		{Resource: framework.GVK(gvk), ActionType: framework.Add | framework.Update | framework.Delete},
	}
}

var _ framework.StateData = &stateData{}

type stateData struct {
	hasAffinity          bool
	podRequests          corev1.ResourceList
	podRequestsResources *framework.Resource

	nodeReservationStates map[string]nodeReservationState
	preferredNode         string
	assumed               *frameworkext.ReservationInfo
}

type nodeReservationState struct {
	nodeName string
	matched  []*frameworkext.ReservationInfo
	// podRequested represents all Pods(including matched reservation) requested resources
	// but excluding the already allocated from unmatched reservations
	podRequested *framework.Resource
	// rAllocated represents the allocated resources of matched reservations
	rAllocated *framework.Resource
	// totalAligned represents the count of Reservation that specified Aligned policy
	totalAligned int
	// totalRestricted represents the count of Reservation that specified Restricted policy
	totalRestricted int
}

func (s *stateData) Clone() framework.StateData {
	return s
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
func (pl *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	if reservationutil.IsReservePod(pod) {
		// validate reserve pod and reservation
		klog.V(4).InfoS("Attempting to pre-filter reserve pod", "pod", klog.KObj(pod))
		rName := reservationutil.GetReservationNameFromReservePod(pod)
		r, err := pl.rLister.Get(rName)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(3).InfoS("skip the pre-filter for reservation since the object is not found", "pod", klog.KObj(pod), "reservation", rName)
			}
			return framework.NewStatus(framework.Error, "cannot get reservation, err: "+err.Error())
		}
		err = reservationutil.ValidateReservation(r)
		if err != nil {
			return framework.NewStatus(framework.Error, err.Error())
		}
		return nil
	}

	state := getStateData(cycleState)
	if state.hasAffinity && len(state.nodeReservationStates) == 0 {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAffinity)
	}

	klog.V(5).InfoS("Attempting to pre-filter pod for reservation state", "pod", klog.KObj(pod))
	return nil
}

func (pl *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
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

		rInfos := pl.reservationCache.listAvailableReservationInfosOnNode(node.Name)
		for _, v := range rInfos {
			// ReservationAllocatePolicyDefault cannot coexist with other allocate policies
			if (allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyDefault ||
				v.GetAllocatePolicy() == schedulingv1alpha1.ReservationAllocatePolicyDefault) &&
				allocatePolicy != v.GetAllocatePolicy() {
				return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAllocatePolicyConflict)
			}
		}
	}

	if !reservationutil.IsReservePod(pod) {
		return pl.filterWithReservations(ctx, cycleState, pod, nodeInfo)
	}

	// TODO: handle pre-allocation cases
	return nil
}

func (pl *Plugin) filterWithReservations(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	state := getStateData(cycleState)
	nodeRState := state.nodeReservationStates[node.Name]
	matchedReservations := nodeRState.matched
	if len(matchedReservations) == 0 {
		if state.hasAffinity {
			return framework.NewStatus(framework.Unschedulable, ErrReasonReservationAffinity)
		}
		return nil
	}

	var insufficientAligned, insufficientRestricted int
	for _, rInfo := range matchedReservations {
		allocatePolicy := rInfo.GetAllocatePolicy()
		if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyDefault {
			continue
		}
		nodeFits := fitsNode(state.podRequestsResources, nodeInfo, &nodeRState, rInfo)
		if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyAligned {
			if nodeFits {
				return nil
			}
			insufficientAligned++
		} else if allocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
			rRemained := quotav1.SubtractWithNonNegativeResult(rInfo.Allocatable, rInfo.Allocated)
			fits, _ := quotav1.LessThanOrEqual(state.podRequests, rRemained)
			if fits && nodeFits {
				return nil
			}
			insufficientRestricted++
		}
	}
	if (nodeRState.totalAligned > 0 && nodeRState.totalAligned == insufficientAligned) ||
		(nodeRState.totalRestricted > 0 && nodeRState.totalRestricted == insufficientRestricted) {
		return framework.NewStatus(framework.Unschedulable, ErrReasonReservationInsufficientResources)
	}
	return nil
}

// fitsNode checks if node have enough resources to host the pod.
func fitsNode(podRequest *framework.Resource, nodeInfo *framework.NodeInfo, nodeRState *nodeReservationState, rInfo *frameworkext.ReservationInfo) bool {
	allowedPodNumber := nodeInfo.Allocatable.AllowedPodNumber
	if len(nodeInfo.Pods)-len(nodeRState.matched)+1 > allowedPodNumber {
		return false
	}

	if podRequest.MilliCPU == 0 &&
		podRequest.Memory == 0 &&
		podRequest.EphemeralStorage == 0 &&
		len(podRequest.ScalarResources) == 0 {
		return false
	}

	rRemained := framework.NewResource(quotav1.SubtractWithNonNegativeResult(rInfo.Allocatable, rInfo.Allocated))
	allRAllocated := nodeRState.rAllocated
	podRequested := nodeRState.podRequested

	if podRequest.MilliCPU > (nodeInfo.Allocatable.MilliCPU - (podRequested.MilliCPU - rRemained.MilliCPU - allRAllocated.MilliCPU)) {
		return false
	}
	if podRequest.Memory > (nodeInfo.Allocatable.Memory - (podRequested.Memory - rRemained.Memory - allRAllocated.Memory)) {
		return false
	}
	if podRequest.EphemeralStorage > (nodeInfo.Allocatable.EphemeralStorage - (podRequested.EphemeralStorage - rRemained.EphemeralStorage - allRAllocated.EphemeralStorage)) {
		return false
	}

	for rName, rQuant := range podRequest.ScalarResources {
		if rQuant > (nodeInfo.Allocatable.ScalarResources[rName] - (podRequested.ScalarResources[rName] - rRemained.ScalarResources[rName] - allRAllocated.ScalarResources[rName])) {
			return false
		}
	}

	return true
}

func (pl *Plugin) PostFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, _ framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		// return err to stop default preemption
		return nil, framework.NewStatus(framework.Error)
	}

	allNodes, err := pl.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	pl.handle.Parallelizer().Until(ctx, len(allNodes), func(piece int) {
		nodeInfo := allNodes[piece]
		node := nodeInfo.Node()
		if node == nil {
			return
		}
		reservationInfos := pl.reservationCache.listAvailableReservationInfosOnNode(node.Name)
		for _, rInfo := range reservationInfos {
			// Pods whose operating mode is Reservation can still be preempted.
			if apiext.IsReservationOperatingMode(rInfo.GetReservePod()) {
				continue
			}
			if rInfo.GetPriority() >= corev1helpers.PodPriority(pod) {
				continue
			}
			for _, podInfo := range nodeInfo.Pods {
				if podInfo.Pod.UID == rInfo.UID() {
					podInfo.Pod = podInfo.Pod.DeepCopy()
					podInfo.Pod.Spec.Priority = pointer.Int32(math.MaxInt32)
					break
				}
			}
		}
	})
	return nil, framework.NewStatus(framework.Unschedulable)
}

func (pl *Plugin) FilterReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeName string) *framework.Status {
	state := getStateData(cycleState)
	nodeRState := state.nodeReservationStates[nodeName]

	var rInfo *frameworkext.ReservationInfo
	for _, v := range nodeRState.matched {
		if v.UID() == reservationInfo.UID() {
			rInfo = v
			break
		}
	}

	if rInfo == nil {
		return framework.AsStatus(fmt.Errorf("impossible, there is no relevant Reservation information"))
	}

	if rInfo.IsAllocateOnce() && len(rInfo.AssignedPods) > 0 {
		return framework.AsStatus(fmt.Errorf("reservation has allocateOnce enabled and has already been allocated"))
	}

	podRequests, _ := resourceapi.PodRequestsAndLimits(pod)
	resourceNames := quotav1.Intersection(rInfo.ResourceNames, quotav1.ResourceNames(podRequests))
	if len(resourceNames) == 0 {
		return framework.AsStatus(fmt.Errorf("no intersection resources"))
	}

	remainedResource := quotav1.SubtractWithNonNegativeResult(rInfo.Allocatable, rInfo.Allocated)
	if quotav1.IsZero(quotav1.Mask(remainedResource, resourceNames)) {
		return framework.AsStatus(fmt.Errorf("insufficient resources in reservation"))
	}
	return nil
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

	nominatedReservation := frameworkext.GetNominatedReservation(cycleState)
	if nominatedReservation == nil {
		klog.V(5).Infof("Skip reserve with reservation since there are no matched reservations, pod %v, node: %v", klog.KObj(pod), nodeName)
		return nil
	}

	err := pl.reservationCache.assumePod(nominatedReservation.UID(), pod)
	if err != nil {
		klog.ErrorS(err, "Failed to assume pod in reservationCache", "pod", klog.KObj(pod), "reservation", klog.KObj(nominatedReservation))
		return framework.AsStatus(err)
	}

	state := getStateData(cycleState)
	state.assumed = nominatedReservation.Clone()
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
	if reservationutil.IsReservePod(pod) {
		return nil
	}

	state := getStateData(cycleState)
	if state.assumed == nil {
		klog.V(5).Infof("Skip the Reservation PreBind since no reservation allocated for the pod %d o node %s", klog.KObj(pod), nodeName)
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
		reservationutil.SetReservationAvailable(reservation, nodeName)
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

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
	"sort"

	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config"
	clientschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	Name = "Reservation" // the plugin name

	preFilterStateKey = "PreFilter" + Name // what nodes the scheduling pod match any reservation at

	// ErrReasonNodeNotMatchReservation is the reason for node not matching which the reserve pod specifies.
	ErrReasonNodeNotMatchReservation = "node(s) didn't match the nodeName specified by reservation"
	// ErrReasonReservationNotFound is the reason for the reservation is not found and should not be used.
	ErrReasonReservationNotFound = "reservation is not found"
	// ErrReasonReservationFailed is the reason for the reservation is failed/expired and should not be used.
	ErrReasonReservationFailed = "reservation is failed"
	// ErrReasonReservationFailedToReserve is the reason for the assumed reservation does not match the pod any more.
	ErrReasonReservationFailedToReserve = "reservation failed to reserve and does not match any more"
	// SkipReasonNotReservation is the reason for pod does not match any reservation.
	SkipReasonNotReservation = "pod does not match any reservation"
)

var (
	_ framework.PreFilterPlugin  = &Plugin{}
	_ framework.FilterPlugin     = &Plugin{}
	_ framework.PostFilterPlugin = &Plugin{}
	_ framework.ScorePlugin      = &Plugin{}
	_ framework.ReservePlugin    = &Plugin{}
	_ framework.PreBindPlugin    = &Plugin{}
	_ framework.BindPlugin       = &Plugin{}
)

type Plugin struct {
	handle           frameworkext.ExtendedHandle
	args             *config.ReservationArgs
	informer         cache.SharedIndexInformer
	lister           listerschedulingv1alpha1.ReservationLister
	client           clientschedulingv1alpha1.SchedulingV1alpha1Interface // for updates
	reservationCache *reservationCache
}

var pluginEnabled atomic.Bool

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.ReservationArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type ReservationArgs, got %T", args)
	}
	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("want handle to be of type frameworkext.ExtendedHandle, got %T", handle)
	}

	reservationInterface := extendedHandle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations()
	reservationInformer := reservationInterface.Informer()
	// index reservation with status.nodeName; avoid duplicate add
	if reservationInformer.GetIndexer().GetIndexers()[NodeNameIndex] == nil {
		err := reservationInformer.AddIndexers(cache.Indexers{NodeNameIndex: StatusNodeNameIndexFunc})
		if err != nil {
			return nil, fmt.Errorf("failed to add indexer, err: %s", err)
		}
	} else {
		klog.V(3).InfoS("indexer has been added", "index", NodeNameIndex)
	}

	p := &Plugin{
		handle:           extendedHandle,
		args:             pluginArgs,
		informer:         reservationInformer,
		lister:           reservationInterface.Lister(),
		client:           extendedHandle.KoordinatorClientSet().SchedulingV1alpha1(),
		reservationCache: newReservationCache(),
	}

	// handle reservation event in cache; here only scheduled and expired reservations are considered.
	reservationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.reservationCache.HandleOnAdd,
		UpdateFunc: p.reservationCache.HandleOnUpdate,
		DeleteFunc: p.reservationCache.HandleOnDelete,
	})
	// handle reservations on deleted nodes
	extendedHandle.SharedInformerFactory().Core().V1().Nodes().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			switch t := obj.(type) {
			case *corev1.Node:
				p.expireReservationOnNode(t)
				return
			case cache.DeletedFinalStateUnknown:
				node, ok := t.Obj.(*corev1.Node)
				if ok && node != nil {
					p.expireReservationOnNode(node)
					return
				}
			}
			klog.V(3).InfoS("reservation's node informer delete func parse obj failed", "obj", obj)
		},
	})
	// handle deleting pods which allocate reservation resources
	extendedHandle.SharedInformerFactory().Core().V1().Pods().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			switch t := obj.(type) {
			case *corev1.Pod:
				p.syncPodDeleted(t)
				return
			case cache.DeletedFinalStateUnknown:
				pod, ok := t.Obj.(*corev1.Pod)
				if ok && pod != nil {
					p.syncPodDeleted(pod)
					return
				}
			}
			klog.V(3).InfoS("reservation's pod informer delete func parse obj failed", "obj", obj)
		},
	})

	// check reservations' expiration
	go wait.Until(p.gcReservations, defaultGCCheckInterval, nil)

	pluginEnabled.Store(true)
	klog.V(3).InfoS("reservation plugin enabled")

	return p, nil
}

func (p *Plugin) Name() string { return Name }

// PreFilter checks if the pod is a reserve pod. If it is, update cycle state to annotate reservation scheduling.
// Also do validations in this phase.
func (p *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	// if the pod is a reserve pod
	if IsReservePod(pod) {
		// validate reserve pod and reservation
		klog.V(4).InfoS("Attempting to pre-filter reserve pod", "pod", klog.KObj(pod))
		rName := GetReservationNameFromReservePod(pod)
		r, err := p.lister.Get(rName)
		if errors.IsNotFound(err) {
			klog.V(3).InfoS("skip the pre-filter for reservation since the object is not found",
				"pod", klog.KObj(pod), "reservation", rName)
			return nil
		} else if err != nil {
			return framework.NewStatus(framework.Error, "cannot get reservation, err: "+err.Error())
		}
		err = ValidateReservation(r)
		if err != nil {
			return framework.NewStatus(framework.Error, err.Error())
		}
		return nil
	}

	klog.V(5).InfoS("Attempting to pre-filter pod for reservation state", "pod", klog.KObj(pod))
	return nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// Filter only processes pods either the pod is a reserve pod or a pod can allocate reserved resources on the node.
func (p *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	// the pod is a reserve pod
	if IsReservePod(pod) {
		klog.V(4).InfoS("Attempting to filter reserve pod", "pod", klog.KObj(pod), "node", node.Name)
		// if the reservation specifies a nodeName initially, check if the nodeName matches
		rNodeName := GetReservePodNodeName(pod)
		if len(rNodeName) > 0 && rNodeName != nodeInfo.Node().Name {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonNodeNotMatchReservation)
		}
		// TODO: handle pre-allocation cases

		return nil
	}

	return nil
}

func (p *Plugin) PostFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	// if a reserve pod is unschedulable, update the reservation status
	if IsReservePod(pod) {
		rName := GetReservationNameFromReservePod(pod)
		klog.V(4).InfoS("Attempting to post-filter reserve pod",
			"pod", klog.KObj(pod), "reservation", rName)
		err := retryOnConflictOrTooManyRequests(func() error {
			r, err1 := p.lister.Get(rName)
			if errors.IsNotFound(err1) {
				klog.V(4).InfoS("skip the post-filter for reservation since the object is not found",
					"pod", klog.KObj(pod), "reservation", rName)
				return nil
			} else if err1 != nil {
				klog.V(3).InfoS("failed to get reservation",
					"pod", klog.KObj(pod), "reservation", rName, "err", err1)
				return nil
			}

			curR := r.DeepCopy()
			setReservationUnschedulable(curR, getUnschedulableMessage(filteredNodeStatusMap))
			_, err1 = p.client.Reservations().UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
			if err1 != nil {
				klog.V(4).InfoS("failed to update reservation status for post-filter",
					"reservation", klog.KObj(curR), "err", err1)
			}
			return err1
		})
		if err != nil {
			klog.Warningf("failed to post-filter reservation %s, pod %s, err: %v", rName, klog.KObj(pod), err)
		}
	} // otherwise, skip

	return nil, framework.NewStatus(framework.Unschedulable)
}

func (p *Plugin) Score(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	// if pod is a reserve pod, ignored
	if IsReservePod(pod) {
		return framework.MinNodeScore, nil
	}

	state := getPreFilterState(cycleState)
	if state == nil { // expected matchedCache is prepared
		klog.V(5).InfoS("skip the Reservation Score", "pod", klog.KObj(pod), "node", nodeName)
		return framework.MinNodeScore, nil
	}
	if state.skip {
		return framework.MinNodeScore, nil
	}

	rOnNode := state.matchedCache.GetOnNode(nodeName)
	klog.V(5).InfoS("Attempting to score pod for reservation state", "pod", klog.KObj(pod),
		"node", nodeName, "matched count", len(rOnNode))
	if len(rOnNode) <= 0 {
		return framework.MinNodeScore, nil
	}

	// prefer the nodes which have reservation matched
	// TODO: enable fine-grained scoring algorithm for reservations
	return framework.MaxNodeScore, nil
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (p *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	// if the pod is a reserve pod
	if IsReservePod(pod) {
		return nil
	}

	// if the pod match a reservation
	state := getPreFilterState(cycleState)
	if state == nil || state.skip { // expected matchedCache is prepared
		klog.V(5).InfoS("skip the Reservation Reserve", "pod", klog.KObj(pod), "node", nodeName)
		return nil
	}
	rOnNode := state.matchedCache.GetOnNode(nodeName)
	if len(rOnNode) <= 0 { // the pod is suggested to bind on a node with no reservation
		return nil
	}

	// select one reservation for the pod to allocate
	// sort: here we use MostAllocated (simply set all weights as 1.0)
	for i := range rOnNode {
		rOnNode[i].ScoreForPod(pod)
	}
	sort.Slice(rOnNode, func(i, j int) bool {
		return rOnNode[i].Score >= rOnNode[j].Score
	})

	// NOTE: matchedCache may be stale, try next reservation when current one does not match any more
	// TBD: currently Reserve got a failure if any reservation is selected but all failed to reserve
	for _, rInfo := range rOnNode {
		target := rInfo.Reservation

		// check the allocation info from cache
		cached := p.reservationCache.GetActive(target)
		r := cached.GetReservation()
		if r == nil { // reservation not found in active cache, abort
			if p.reservationCache.IsFailed(target) { // in case reservation is marked as failed
				klog.V(5).InfoS("skip reserve current reservation since it is marked as expired",
					"pod", klog.KObj(pod), "reservation", klog.KObj(target))
				continue
			}
			// the object is just missed in cache, maybe the reservation is deleted or updated unexpectedly
			klog.V(5).InfoS("skip reserve current reservation since it is not found in active cache",
				"pod", klog.KObj(pod), "reservation", klog.KObj(target))
			continue
		}
		if !matchReservation(pod, cached) {
			klog.V(5).InfoS("failed to reserve reservation since the reservation does not match the pod",
				"pod", klog.KObj(pod), "reservation", klog.KObj(target), "reason", dumpMatchReservationReason(pod, cached))
			continue
		}

		reserved := r.DeepCopy()
		setReservationAllocated(reserved, pod)
		// update assumed status in cache
		p.reservationCache.AddToActive(reserved)

		// update assume state
		state.assumed = reserved
		cycleState.Write(preFilterStateKey, state)
		klog.V(4).InfoS("Attempting to reserve pod to node with reservations", "pod", klog.KObj(pod),
			"node", nodeName, "matched count", len(rOnNode), "assumed", klog.KObj(r))
		return nil
	}

	klog.V(3).InfoS("failed to reserve pod with reservations, no reservation matched any more",
		"pod", klog.KObj(pod), "node", nodeName, "tried count", len(rOnNode))
	return framework.NewStatus(framework.Error, ErrReasonReservationFailedToReserve)
}

func (p *Plugin) Unreserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) {
	// if the pod is a reserve pod
	if IsReservePod(pod) {
		return
	}

	state := getPreFilterState(cycleState)
	if state == nil || state.skip { // expected matchedCache is prepared
		klog.V(5).InfoS("skip the Reservation Unreserve", "pod", klog.KObj(pod), "node", nodeName)
		return
	}
	klog.V(4).InfoS("Attempting to unreserve pod to node with reservations", "pod", klog.KObj(pod),
		"node", nodeName, "assumed", klog.KObj(state.assumed))

	target := state.assumed
	if target == nil {
		klog.V(5).InfoS("skip the Reservation Unreserve, no assumed reservation",
			"pod", klog.KObj(pod), "node", nodeName)
		return
	}

	// clean assume state
	state.assumed = nil
	cycleState.Write(preFilterStateKey, state)

	// remove the allocation info in cache
	rInfo := p.reservationCache.GetActive(target)
	r := rInfo.GetReservation()
	if r == nil { // reservation not found in active cache, abort
		if p.reservationCache.IsFailed(target) { // in case reservation is marked as failed
			klog.V(5).InfoS("skip unreserve reservations since it is marked as expired",
				"pod", klog.KObj(pod), "reservation", klog.KObj(target))
			return
		}
		// the object is just missed in cache, maybe the reservation is deleted or updated unexpectedly
		klog.V(5).InfoS("skip unreserve reservations since it is not found in active cache",
			"pod", klog.KObj(pod), "reservation", klog.KObj(target))
		return
	}

	reserved := r.DeepCopy()
	err := removeReservationAllocated(reserved, pod)
	if err == nil {
		// update assumed status in cache
		p.reservationCache.AddToActive(reserved)
	} else {
		klog.V(5).InfoS("skip unreserve reservation since the reservation does not match the pod",
			"pod", klog.KObj(pod), "reservation", klog.KObj(target), "reason", dumpMatchReservationReason(pod, rInfo))
	}
}

func (p *Plugin) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	// if the pod is a reserve pod
	if IsReservePod(pod) {
		return nil
	}

	// if the pod match a reservation
	state := getPreFilterState(cycleState)
	if state == nil || state.skip { // expected matchedCache is prepared
		klog.V(5).InfoS("skip the Reservation PreBind", "pod", klog.KObj(pod), "node", nodeName)
		return nil
	}
	if state.assumed == nil {
		klog.V(5).InfoS("skip the Reservation PreBind since no reservation allocated for the pod",
			"pod", klog.KObj(pod), "node", nodeName)
		return nil
	}

	target := state.assumed
	klog.V(4).InfoS("Attempting to pre-bind pod to node with reservations", "pod", klog.KObj(pod),
		"node", nodeName, "assumed reservation", klog.KObj(target))

	// update: update current owner and allocated resources info for assumed reservation
	err := retryOnConflictOrTooManyRequests(func() error {
		curR, err1 := p.lister.Get(target.Name)
		if errors.IsNotFound(err1) {
			klog.Warningf("reservation %v not found, abort the update", klog.KObj(target))
			return nil
		} else if err1 != nil {
			klog.Warningf("failed to get reservation %v, err: %v", klog.KObj(target), err1)
			return err1
		}

		if !IsReservationScheduled(curR) {
			klog.Warningf("failed to allocate resources on a non-scheduled reservation %v, phase %v",
				klog.KObj(curR), curR.Status.Phase)
			// TBD: may try with the next candidate instead of return error
			return fmt.Errorf(ErrReasonReservationFailed)
		}

		curR = curR.DeepCopy()
		setReservationAllocated(curR, pod)
		_, err1 = p.client.Reservations().UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
		if err1 != nil {
			klog.Warningf("failed to update reservation status for pod allocation, reservation %v, pod %v, err: %v",
				klog.KObj(curR), klog.KObj(pod), err1)
		}

		return err1
	})
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	// update reservation allocation of the pod
	newPod := pod.DeepCopy()
	apiext.SetReservationAllocated(newPod, target)
	patchBytes, err := generatePodPatch(pod, newPod)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	err = retryOnConflictOrTooManyRequests(func() error {
		_, err1 := p.handle.ClientSet().CoreV1().Pods(pod.Namespace).
			Patch(ctx, pod.Name, apimachinerytypes.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		if err1 != nil {
			klog.Error("failed to patch Pod %s/%s, patch %v, err: %v",
				pod.Namespace, pod.Name, string(patchBytes), err1)
		}
		return err1
	})
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	return nil
}

// Bind fake binds reserve pod and mark corresponding reservation as Available.
// NOTE: This Bind plugin should get called before DefaultBinder; plugin order should be configured.
func (p *Plugin) Bind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	// 1. if the pod is a reserve pod
	if IsReservePod(pod) {
		rName := GetReservationNameFromReservePod(pod)
		klog.V(4).InfoS("Attempting to fake bind reserve pod to node",
			"pod", klog.KObj(pod), "reservation", rName, "node", nodeName)
		err := retryOnConflictOrTooManyRequests(func() error {
			// get latest version of the reservation
			r, err1 := p.lister.Get(rName)
			if errors.IsNotFound(err1) {
				klog.V(3).InfoS("reservation not found, abort the update", "pod", klog.KObj(pod))
				return fmt.Errorf(ErrReasonReservationNotFound)
			} else if err1 != nil {
				klog.Warningf("failed to get reservation %v, pod %v, err: %v", rName, klog.KObj(pod), err1)
				return err1
			}

			// check if the reservation has been failed
			if IsReservationFailed(r) || p.reservationCache.IsFailed(r) {
				return fmt.Errorf(ErrReasonReservationFailed)
			}

			// mark reservation as available
			r = r.DeepCopy()
			setReservationAvailable(r, nodeName)
			_, err1 = p.client.Reservations().UpdateStatus(context.TODO(), r, metav1.UpdateOptions{})
			// TBD: just set status.nodeName to avoid multiple updates
			if err1 != nil {
				klog.Warningf("failed to update reservation %v, err: %v", klog.KObj(r), err1)
			}
			return err1
		})
		if err != nil {
			return framework.NewStatus(framework.Error, "failed to bind reservation, err: "+err.Error())
		}
		return nil
	}

	return framework.NewStatus(framework.Skip, SkipReasonNotReservation)
}

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
	listercorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
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
	// ErrReasonReservationNotMatchStale is the reason for the assumed reservation does not match the pod any more.
	ErrReasonReservationNotMatchStale = "reservation is stale and does not match any more"
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
	rLister          listerschedulingv1alpha1.ReservationLister
	podLister        listercorev1.PodLister
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
		rLister:          reservationInterface.Lister(),
		podLister:        extendedHandle.SharedInformerFactory().Core().V1().Pods().Lister(),
		client:           extendedHandle.KoordinatorClientSet().SchedulingV1alpha1(),
		reservationCache: newReservationCache(),
	}

	// handle reservation event in cache; here only scheduled and expired reservations are considered.
	reservationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.handleOnAdd,
		UpdateFunc: p.handleOnUpdate,
		DeleteFunc: p.handleOnDelete,
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
	// FIXME: the handler does not recognize if the pod belongs to current scheduler, so the reconciliation could be
	//  duplicated if multiple koord-scheduler runs in same cluster. Now we should keep the reconciliation idempotent.
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
	// check reservation cache expiration
	go wait.Until(p.reservationCache.Run, defaultCacheCheckInterval, nil)

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
		r, err := p.rLister.Get(rName)
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
			r, err1 := p.rLister.Get(rName)
			if errors.IsNotFound(err1) {
				klog.V(4).InfoS("skip the post-filter for reservation since the object is not found",
					"pod", klog.KObj(pod), "reservation", rName)
				return nil
			} else if err1 != nil {
				klog.V(3).InfoS("failed to get reservation",
					"pod", klog.KObj(pod), "reservation", rName, "err", err1)
				return err1
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
		target := rInfo.GetReservation()
		// use the cached reservation, in case the version in cycle state is too old/incorrect or mutated by other pods
		rInfo = p.reservationCache.GetInCache(target)
		if rInfo == nil {
			// check if the reservation is marked as expired
			if p.reservationCache.IsFailed(target) { // in case reservation is marked as failed
				klog.V(5).InfoS("skip reserve current reservation since it is marked as expired",
					"pod", klog.KObj(pod), "reservation", klog.KObj(target))
			} else {
				klog.V(4).InfoS("failed to reserve current reservation since it is not found in cache",
					"pod", klog.KObj(pod), "reservation", klog.KObj(target))
			}
			continue
		}

		// avoid concurrency conflict inside the scheduler (i.e. scheduling cycle vs. binding cycle)
		if !matchReservation(pod, rInfo) {
			klog.V(5).InfoS("failed to reserve reservation since the reservation does not match the pod",
				"pod", klog.KObj(pod), "reservation", klog.KObj(target), "reason", dumpMatchReservationReason(pod, rInfo))
			continue
		}

		reserved := target.DeepCopy()
		setReservationAllocated(reserved, pod)
		// update assumed status in cache
		p.reservationCache.Assume(reserved)

		// update assume state
		state.assumed = reserved
		cycleState.Write(preFilterStateKey, state)
		klog.V(4).InfoS("Attempting to reserve pod to node with reservations", "pod", klog.KObj(pod),
			"node", nodeName, "matched count", len(rOnNode), "assumed", klog.KObj(reserved))
		return nil
	}

	klog.V(3).InfoS("failed to reserve pod with reservations, no reservation matched any more",
		"pod", klog.KObj(pod), "node", nodeName, "tried count", len(rOnNode))
	return framework.NewStatus(framework.Error, ErrReasonReservationNotMatchStale)
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

	// update assume cache
	unreserved := target.DeepCopy()
	err := removeReservationAllocated(unreserved, pod)
	if err == nil {
		p.reservationCache.Unassume(unreserved, true)
	} else {
		klog.V(4).InfoS("Unreserve failed to unassume reservation in cache, current owner not matched",
			"pod", klog.KObj(pod), "reservation", klog.KObj(target), "err", err)
	}

	if !state.preBind { // the pod failed at Reserve, does not reach PreBind
		return
	}

	// update reservation and pod
	err = retryOnConflictOrTooManyRequests(func() error {
		// get the latest reservation
		curR, err1 := p.rLister.Get(target.Name)
		if errors.IsNotFound(err1) {
			klog.V(4).InfoS("abort the update, reservation not found", "reservation", klog.KObj(target))
			return nil
		} else if err1 != nil {
			return err1
		}
		if !IsReservationScheduled(curR) {
			klog.V(5).InfoS("skip unreserve resources on a non-scheduled reservation",
				"reservation", klog.KObj(curR), "phase", curR.Status.Phase)
			return nil
		}

		// update reservation status
		curR = curR.DeepCopy()
		err1 = removeReservationAllocated(curR, pod)
		// normally there is no need to update reservation status if failed in Reserve and PreBind
		if err1 != nil {
			klog.V(5).InfoS("skip unreserve reservation since the reservation does not match the pod",
				"reservation", klog.KObj(curR), "pod", klog.KObj(pod), "err", err1)
			return nil
		}

		_, err1 = p.client.Reservations().UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
		if err1 != nil {
			klog.V(4).InfoS("failed to update reservation status for unreserve",
				"reservation", klog.KObj(curR), "pod", klog.KObj(pod), "err", err1)
			return err1
		}

		return nil
	})
	if err != nil {
		klog.ErrorS(err, "Unreserve failed to update reservation status",
			"reservation", klog.KObj(target), "pod", klog.KObj(pod), "node", nodeName)
	}

	// update pod annotation
	newPod := pod.DeepCopy()
	needPatch, _ := apiext.RemoveReservationAllocated(newPod, target)
	if !needPatch {
		return
	}
	patchBytes, err := generatePodPatch(pod, newPod)
	if err != nil {
		klog.V(4).InfoS("failed to generate patch for pod unreserve",
			"pod", klog.KObj(pod), "patch", patchBytes, "err", err)
		return
	}
	_, err = p.handle.ClientSet().CoreV1().Pods(pod.Namespace).
		Patch(ctx, pod.Name, apimachinerytypes.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		klog.V(4).InfoS("failed to patch pod for unreserve",
			"pod", klog.KObj(pod), "patch", patchBytes, "err", err)
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
		// here we just use the latest version, assert the reservation status is correct eventually
		curR, err1 := p.rLister.Get(target.Name)
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
			return fmt.Errorf(ErrReasonReservationFailed)
		}
		// double-check if the latest version does not match the pod any more
		if !matchReservation(pod, newReservationInfo(curR)) {
			klog.V(5).InfoS("failed to allocate reservation since the reservation does not match the pod",
				"pod", klog.KObj(pod), "reservation", klog.KObj(target), "reason", dumpMatchReservationReason(pod, newReservationInfo(curR)))
			return fmt.Errorf(ErrReasonReservationNotMatchStale)
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

	// assume accepted
	p.reservationCache.Unassume(target, false)
	// set the pre-bind flag, unreserve should try to resume
	state.preBind = true

	// update reservation allocation of the pod
	// NOTE: the pod annotation can be stale, we should use reservation status as the ground-truth
	newPod := pod.DeepCopy()
	apiext.SetReservationAllocated(newPod, target)
	patchBytes, err := generatePodPatch(pod, newPod)
	if err != nil {
		klog.V(4).InfoS("failed to generate patch for pod PreBind",
			"pod", klog.KObj(pod), "patch", patchBytes, "err", err)
		return nil
	}
	err = retryOnConflictOrTooManyRequests(func() error {
		_, err1 := p.handle.ClientSet().CoreV1().Pods(pod.Namespace).
			Patch(ctx, pod.Name, apimachinerytypes.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		return err1
	})
	if err != nil {
		klog.V(4).InfoS("failed to patch pod for PreBind allocating reservation",
			"pod", klog.KObj(pod), "patch", patchBytes, "err", err)
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
			r, err1 := p.rLister.Get(rName)
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

func (p *Plugin) handleOnAdd(obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.V(3).Infof("reservation cache add failed to parse, obj %T", obj)
		return
	}
	if IsReservationActive(r) {
		p.reservationCache.AddToActive(r)
	} else if IsReservationFailed(r) {
		p.reservationCache.AddToFailed(r)
	}
	klog.V(5).InfoS("reservation cache add", "reservation", klog.KObj(r))
}

func (p *Plugin) handleOnUpdate(oldObj, newObj interface{}) {
	oldR, oldOK := oldObj.(*schedulingv1alpha1.Reservation)
	newR, newOK := newObj.(*schedulingv1alpha1.Reservation)
	if !oldOK || !newOK {
		klog.V(3).InfoS("reservation cache update failed to parse, old %T, new %T", oldObj, newObj)
		return
	}
	if oldR == nil || newR == nil {
		klog.V(4).InfoS("reservation cache update get nil object", "old", oldObj, "new", newObj)
		return
	}

	// in case delete and created of two reservations with same namespaced name are merged into update
	if oldR.UID != newR.UID {
		klog.V(4).InfoS("reservation cache update get merged update event",
			"reservation", klog.KObj(newR), "oldUID", oldR.UID, "newUID", newR.UID)
		p.handleOnDelete(oldObj)
		p.handleOnAdd(newObj)
		return
	}

	if IsReservationActive(newR) {
		p.reservationCache.AddToActive(newR)
	} else if IsReservationFailed(newR) {
		p.reservationCache.AddToFailed(newR)
	}
	klog.V(5).InfoS("reservation cache update", "reservation", klog.KObj(newR))
}

func (p *Plugin) handleOnDelete(obj interface{}) {
	var r *schedulingv1alpha1.Reservation
	switch t := obj.(type) {
	case *schedulingv1alpha1.Reservation:
		r = t
	case cache.DeletedFinalStateUnknown:
		deletedReservation, ok := t.Obj.(*schedulingv1alpha1.Reservation)
		if ok {
			r = deletedReservation
		}
	}
	if r == nil {
		klog.V(4).InfoS("reservation cache delete failed to parse, obj %T", obj)
		return
	}
	p.reservationCache.Delete(r)
	klog.V(5).InfoS("reservation cache delete", "reservation", klog.KObj(r))
}

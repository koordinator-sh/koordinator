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
	"sync"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type nominator struct {
	podLister corelisters.PodLister
	rLister   listerschedulingv1alpha1.ReservationLister
	// nominatedPodToNode is map keyed by a Pod UID to the node name where it is nominated to reserve.
	nominatedPodToNode map[types.UID]map[string]types.UID
	// nominatedPreAllocatable is map keyed by a Reservation UID to the node name where there are
	// pre-allocatable pods being nominated. Supports both single and multiple pods.
	nominatedPreAllocatable map[types.UID]map[string][]*corev1.Pod
	// nominatedReservePod is map keyed by nodeName to the nominated reservation's PodInfo for preemption.
	nominatedReservePod       map[string][]*framework.PodInfo
	nominatedReservePodToNode map[types.UID]string
	// nominatedReserveIdentity records what the stored PodInfo was built from,
	// so a read can tell whether it still describes the current reservation.
	nominatedReserveIdentity map[types.UID]reserveIdentity
	lock                     sync.RWMutex
}

// reserveIdentity captures everything NewReservePod derives from a Reservation:
// the spec through metadata.generation (the CRD has the status subresource, so
// the generation bumps exactly on spec updates) plus the labels and annotations
// it copies onto the reserve pod. An unchanged identity therefore means a
// rebuilt reserve pod would be identical to the stored one, and a changed one
// means the nomination describes a reservation that no longer exists in that
// shape - which is also when reservationEventHandler.OnUpdate deletes it.
//
// resourceVersion is deliberately not part of this: it advances on every write,
// including the Unschedulable status this scheduler records after each failed
// attempt, so it would report a change on updates that cannot invalidate a
// nomination.
type reserveIdentity struct {
	generation  int64
	labels      map[string]string
	annotations map[string]string
}

func identityOf(r *schedulingv1alpha1.Reservation) reserveIdentity {
	return reserveIdentity{
		generation:  r.Generation,
		labels:      r.Labels,
		annotations: r.Annotations,
	}
}

func (id reserveIdentity) matches(other reserveIdentity) bool {
	return id.generation == other.generation &&
		apiequality.Semantic.DeepEqual(id.labels, other.labels) &&
		apiequality.Semantic.DeepEqual(id.annotations, other.annotations)
}

// isReservationWaitingForScheduling reports whether a reservation is still
// waiting for a node, which is the only state a reserve pod nomination is
// meaningful in.
//
// The phase is checked as an allowlist rather than by excluding the terminal
// ones, so an unknown or partially written phase fails closed. Excluding via
// IsReservationActive would also let Available or Waiting through whenever
// status.nodeName happened to be empty, since that predicate requires both.
//
// A reservation that already carries status.nodeName while still Pending does
// count as waiting, deliberately: between the node being chosen and the reserve
// pod being assumed into the scheduler cache, this nomination is the only thing
// accounting for it. The read path narrows that to the node it is going to.
//
// A terminating reservation is excluded even while it lingers behind a
// finalizer: ReservationInfo.IsUnschedulable already treats it that way, and its
// reserve pod will never be scheduled.
func isReservationWaitingForScheduling(r *schedulingv1alpha1.Reservation) bool {
	if r == nil || !r.DeletionTimestamp.IsZero() {
		return false
	}
	phase := r.Status.Phase
	return phase == "" || phase == schedulingv1alpha1.ReservationPending
}

// sameSchedulingShape reports whether two reserve pods would be scheduled
// identically. It compares what NewReservePod derives from a Reservation, and
// deliberately ignores status, which the framework mutates on the copy it
// carries through a scheduling cycle.
// sameSchedulingShape reports whether two reserve pods describe the same
// scheduling problem.
//
// It can only compare what NewReservePod puts into the synthetic pod: the
// template spec, and the reservation's labels and annotations. A reserve pod
// cycle also reads fields straight off the Reservation that never reach the
// pod - Spec.Owners and PreAllocationPolicy select the pre-allocatable
// candidates, whose nodes narrow the feasible set. A cycle spanning an update
// that touched only those fields is therefore indistinguishable here and its
// node is accepted. That gap predates this nominator - the previous code
// accepted every late node decision unconditionally - and closing it needs the
// generation the cycle actually used to travel with it, which is tracked in
// #3158.
func sameSchedulingShape(a, b *corev1.Pod) bool {
	return a.Namespace == b.Namespace &&
		apiequality.Semantic.DeepEqual(a.Spec, b.Spec) &&
		apiequality.Semantic.DeepEqual(a.Labels, b.Labels) &&
		apiequality.Semantic.DeepEqual(a.Annotations, b.Annotations)
}

func newNominator(podLister corelisters.PodLister, rLister listerschedulingv1alpha1.ReservationLister) *nominator {
	return &nominator{
		podLister:                 podLister,
		rLister:                   rLister,
		nominatedPodToNode:        map[types.UID]map[string]types.UID{},
		nominatedPreAllocatable:   map[types.UID]map[string][]*corev1.Pod{},
		nominatedReservePod:       map[string][]*framework.PodInfo{},
		nominatedReservePodToNode: map[types.UID]string{},
		nominatedReserveIdentity:  map[types.UID]reserveIdentity{},
	}
}

func (nm *nominator) AddNominatedReservation(pod *corev1.Pod, nodeName string, rInfo *frameworkext.ReservationInfo) {
	if rInfo == nil {
		return
	}
	nm.lock.Lock()
	defer nm.lock.Unlock()

	// nominate it only if the pod is unscheduled and reservation is active
	rName := rInfo.GetName()
	if nm.podLister != nil {
		p, err := nm.podLister.Pods(pod.Namespace).Get(pod.Name)
		if err != nil {
			klog.V(4).InfoS("Pod doesn't exist in podLister, aborted nominating it to the reservation",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
		if p.Spec.NodeName != "" {
			klog.V(4).InfoS("Pod is already scheduled to a node, aborted nominating it to the reservation",
				"current node", p.Spec.NodeName, "node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
	}
	if nm.rLister != nil {
		r, err := nm.rLister.Get(rName)
		if err != nil {
			klog.V(4).InfoS("reservation doesn't exist in rLister, aborted nominating pod to it",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
		if !reservationutil.IsReservationActive(r) { // cannot nominate to an inactive reservation
			klog.V(4).InfoS("reservation is inactive, aborted nominating pod to it",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
	}

	nodeToReservation := nm.nominatedPodToNode[pod.UID]
	if nodeToReservation == nil {
		nodeToReservation = map[string]types.UID{}
		nm.nominatedPodToNode[pod.UID] = nodeToReservation
	}
	nodeToReservation[nodeName] = rInfo.UID()
}

func (nm *nominator) AddNominatedReservePod(pi *framework.PodInfo, nodeName string) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	rName := reservationutil.GetReservationNameFromReservePod(pi.Pod)
	if len(rName) <= 0 { // not a reserve pod
		klog.V(4).InfoS("reservation nominator aborts nominating pod which is not a reserve pod",
			"pod", klog.KObj(pi.Pod), "reservation", rName, "nominatedNodeName", nodeName)
		return
	}
	if nodeName == "" {
		klog.V(4).InfoS("reservation nominated node is removed",
			"pod", klog.KObj(pi.Pod), "reservation", rName, "nominatedNodeName", nodeName)
		nm.deleteReservePod(pi.Pod)
		return
	}

	// Nothing is removed until the nomination has been accepted. The caller can
	// be an older scheduling cycle finishing late - addNominatedReservation
	// reuses the NominatedNodeName from the cycle that failed - so a rejected
	// call must leave a newer cycle's nomination alone.
	stored, identity := pi, reserveIdentity{}
	if nm.rLister != nil {
		r, err := nm.rLister.Get(rName)
		if err != nil {
			klog.V(4).InfoS("reservation doesn't exist in rLister, aborted adding it to the nominator",
				"pod", klog.KObj(pi.Pod), "reservation", rName)
			nm.deleteReservePod(pi.Pod)
			return
		}
		if r.UID != pi.Pod.UID {
			// The reservation was replaced by a same-named one. Drop what the
			// replaced one held; the new one nominates on its own cycle.
			klog.V(4).InfoS("reservation was replaced, aborted adding the stale reserve pod to the nominator",
				"pod", klog.KObj(pi.Pod), "reservation", rName, "currentUID", r.UID)
			nm.deleteReservePod(pi.Pod)
			return
		}
		if !isReservationWaitingForScheduling(r) {
			klog.V(4).InfoS("reservation is no longer waiting to be scheduled, aborted adding it to the nominator",
				"pod", klog.KObj(pi.Pod), "reservation", rName, "phase", r.Status.Phase)
			nm.deleteReservePod(pi.Pod)
			return
		}
		currentReservePod := reservationutil.NewReservePod(r)
		if !sameSchedulingShape(pi.Pod, currentReservePod) {
			// The node was chosen for a shape the reservation no longer has, so
			// it says nothing about where the current one belongs. Keep whatever
			// is stored: it may come from a cycle that ran on the current shape.
			klog.V(4).InfoS("reserve pod was scheduled against an older revision, aborted nominating its node",
				"pod", klog.KObj(pi.Pod), "reservation", rName, "nominatedNodeName", nodeName)
			return
		}
		// Store the reserve pod built from the object the identity is taken
		// from, so the two can never describe different revisions.
		fresh, err := framework.NewPodInfo(currentReservePod)
		if err != nil {
			klog.ErrorS(err, "failed to build the reserve pod info, aborted nominating it",
				"pod", klog.KObj(pi.Pod), "reservation", rName)
			return
		}
		stored, identity = fresh, identityOf(r)
	}

	// Accepted: replace any entry this reservation already had. deleteReservePod
	// is keyed by reservation UID, so this only ever touches its own.
	nm.deleteReservePod(pi.Pod)
	nm.nominatedReservePodToNode[pi.Pod.UID] = nodeName
	if nm.rLister != nil {
		// Recorded only when a lister established which revision this is. With
		// no lister there is no provenance to compare against later, and the
		// read path cannot validate either, so the entry is left unlabelled.
		nm.nominatedReserveIdentity[pi.Pod.UID] = identity
	}
	nm.nominatedReservePod[nodeName] = append(nm.nominatedReservePod[nodeName], stored)
}

func (nm *nominator) AddNominatedPreAllocation(rInfo *frameworkext.ReservationInfo, nodeName string, pod *corev1.Pod) {
	if rInfo == nil {
		return
	}
	nm.lock.Lock()
	defer nm.lock.Unlock()

	// nominate it only if the pod is unscheduled and reservation is active
	rName := rInfo.GetName()
	if nm.podLister != nil {
		p, err := nm.podLister.Pods(pod.Namespace).Get(pod.Name)
		if err != nil {
			klog.V(4).InfoS("Pod doesn't exist in podLister, aborted nominating it to the reservation",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
		if p.Spec.NodeName != nodeName {
			klog.V(4).InfoS("Pod is not scheduled to the node, aborted nominating for reservation pre-allocation",
				"current node", p.Spec.NodeName, "node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
	}
	if nm.rLister != nil {
		r, err := nm.rLister.Get(rName)
		if err != nil {
			klog.V(4).InfoS("reservation doesn't exist in rLister, aborted nominating for reservation pre-allocation",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
		if phase := r.Status.Phase; len(phase) > 0 && phase != schedulingv1alpha1.ReservationPending {
			klog.V(4).InfoS("reservation is scheduled or terminated, aborted nominating for reservation pre-allocation",
				"phase", r.Status.Phase, "node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
		if r.Status.NodeName != "" {
			klog.V(4).InfoS("reservation is assigned, aborted nominating for reservation pre-allocation",
				"current node", r.Status.NodeName, "node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
	}

	nodeToPreAllocatable := nm.nominatedPreAllocatable[rInfo.UID()]
	if nodeToPreAllocatable == nil {
		nodeToPreAllocatable = map[string][]*corev1.Pod{}
		nm.nominatedPreAllocatable[rInfo.UID()] = nodeToPreAllocatable
	}
	nodeToPreAllocatable[nodeName] = []*corev1.Pod{pod}
}

func (nm *nominator) NominatedReservePodForNode(nodeName string) []*framework.PodInfo {
	type nomination struct {
		podInfo  *framework.PodInfo
		identity reserveIdentity
	}
	stored := func() []nomination {
		nm.lock.RLock()
		defer nm.lock.RUnlock()
		nominations := make([]nomination, 0, len(nm.nominatedReservePod[nodeName]))
		for _, pi := range nm.nominatedReservePod[nodeName] {
			nominations = append(nominations, nomination{
				podInfo:  pi,
				identity: nm.nominatedReserveIdentity[pi.Pod.UID],
			})
		}
		return nominations
	}()
	// The critical section above is only a slice clone: revalidating a
	// nomination reads the lister, and BeforeFilter calls this once per pod per
	// node, so holding the read lock across that work would stall concurrent
	// nominator writes (and a blocked writer also blocks later readers).
	//
	// Entries are revalidated against the reservation's current state instead of
	// returned blindly from the snapshot. The informer store is updated before
	// any event handler runs, so this read-through keeps a waiter requeued by a
	// Reservation event from being blocked by a nomination the store already
	// invalidated, even when the listener that maintains this nominator has not
	// processed the same event yet. The caller may mutate the results.
	reservePods := make([]*framework.PodInfo, 0, len(stored))
	for _, n := range stored {
		if podInfo, ok := nm.revalidateNominatedPodInfo(n.podInfo, n.identity, nodeName); ok {
			reservePods = append(reservePods, podInfo)
		}
	}
	return reservePods
}

// revalidateNominatedPodInfo reports whether a stored nomination still stands.
// It is dropped once the lister shows that the reservation is gone, was
// replaced by a same-named object, is no longer waiting to be scheduled
// (assigned, terminated, or terminating behind a finalizer), or no longer has
// the shape the nomination was built from. Keeping any of those would let a phantom reserve pod occupy the node
// until this nominator's own listener catches up, which is exactly the window
// in which a requeued waiter re-evaluates.
//
// The last case is why the identity is compared field by field. A spec, label or
// annotation change can move the reserve pod's placement constraints, its
// requests, its priority or even its schedulerName, so the nominated node need
// not be a valid choice for it any more - reservationEventHandler.OnUpdate
// deletes the nomination for exactly these changes, and this read converges on
// the same answer rather than re-applying the new shape to the old node. When
// the identity does hold, the stored PodInfo is by construction what a rebuild
// would produce, so nothing is rebuilt on this per-pod-per-node path.
//
// rLister is immutable after construction, so no lock is needed here.
func (nm *nominator) revalidateNominatedPodInfo(pi *framework.PodInfo, identity reserveIdentity, nodeName string) (*framework.PodInfo, bool) {
	if nm.rLister == nil {
		return pi.DeepCopy(), true
	}
	r, err := nm.rLister.Get(reservationutil.GetReservationNameFromReservePod(pi.Pod))
	if err != nil || r.UID != pi.Pod.UID ||
		!isReservationWaitingForScheduling(r) ||
		!identityOf(r).matches(identity) {
		return nil, false
	}
	// A nomination holds a candidate node. isReservationWaitingForScheduling
	// deliberately still accepts a reservation that already carries
	// status.nodeName but has not become active yet, because the nomination is
	// the only accounting for it during that window - but only on the node it
	// is actually going to. Any other node it was holding is stale.
	if assigned := reservationutil.GetReservationNodeName(r); assigned != "" && assigned != nodeName {
		return nil, false
	}
	return pi.DeepCopy(), true
}

// DeleteReservePodIfStale applies the read path's validity test now instead of
// waiting for the next read, so a node stops being held as soon as the update
// that invalidated it is processed.
//
// It reconciles against the informer store rather than against the old object
// of the event that triggered it. Those differ: this handler and the scheduling
// cycle run on goroutines with no ordering between them, so by the time an
// update is handled here the store may be further ahead, and a cycle may
// already have recorded a nomination for what the store holds now. Comparing
// with the event's old identity would delete that nomination whenever the two
// happen to be equal - and they can be, because a CRD with the status
// subresource does not advance metadata.generation for metadata-only writes, so
// labels or annotations going A -> B -> A produce two identical identities.
//
// Deleting too much is the failure that matters here: NominatedReservePodForNode
// validates the entries it finds, it never recreates a missing one.
//
// Pre-allocation nominations are deliberately left alone. They carry no
// identity to judge them by (AddNominatedPreAllocation records only the
// candidate), and a cycle in progress writes them before reading them back, so
// clearing them here would take state from a cycle this update says nothing
// about. Their staleness is tracked in #3158.
func (nm *nominator) DeleteReservePodIfStale(pod *corev1.Pod) {
	// The lister read below is deliberately inside the lock, unlike the one in
	// NominatedReservePodForNode. What matters here is that no concurrent Add
	// can record a newer nomination between reading the stored identity and
	// deleting it - reading the store first and locking afterwards would bring
	// back exactly the race this function exists to avoid. The store itself can
	// move either way; that is what makes the read path the backstop. A lister
	// Get is an indexer map lookup, and this is not the per-pod-per-node path.
	nm.lock.Lock()
	defer nm.lock.Unlock()

	if _, nominated := nm.nominatedReservePodToNode[pod.UID]; !nominated {
		return
	}
	if nm.rLister == nil {
		// Nothing can establish what the entry was built from, and the read path
		// cannot validate it either, so this is its only cleanup.
		nm.deleteNominatedReservePodOnly(pod)
		return
	}
	stored, ok := nm.nominatedReserveIdentity[pod.UID]
	r, err := nm.rLister.Get(reservationutil.GetReservationNameFromReservePod(pod))
	if !ok || err != nil || r.UID != pod.UID ||
		!isReservationWaitingForScheduling(r) ||
		!identityOf(r).matches(stored) {
		nm.deleteNominatedReservePodOnly(pod)
	}
}

func (nm *nominator) DeleteReservePod(pod *corev1.Pod) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	nm.deleteReservePod(pod)
}

func (nm *nominator) deleteReservePod(pod *corev1.Pod) {
	// delete pre-allocation for the reservation if exists
	delete(nm.nominatedPreAllocatable, pod.UID)
	nm.deleteNominatedReservePodOnly(pod)
}

// deleteNominatedReservePodOnly drops the reserve pod's own nomination and
// leaves the reservation's pre-allocation candidates in place, for callers that
// have judged the former stale and have no basis to judge the latter.
func (nm *nominator) deleteNominatedReservePodOnly(pod *corev1.Pod) {
	delete(nm.nominatedReserveIdentity, pod.UID)

	nnn, ok := nm.nominatedReservePodToNode[pod.UID]
	if !ok {
		return
	}
	for i, np := range nm.nominatedReservePod[nnn] {
		if np.Pod.UID == pod.UID {
			nm.nominatedReservePod[nnn] = append(nm.nominatedReservePod[nnn][:i], nm.nominatedReservePod[nnn][i+1:]...)
			if len(nm.nominatedReservePod[nnn]) == 0 {
				delete(nm.nominatedReservePod, nnn)
			}
			break
		}
	}
	delete(nm.nominatedReservePodToNode, pod.UID)
}

// RemoveNominatedReservation removes the nominated reservation of a pod from the nominator.
func (nm *nominator) RemoveNominatedReservation(pod *corev1.Pod) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	delete(nm.nominatedPodToNode, pod.UID)
}

func (nm *nominator) RemoveNominatedPreAllocation(pod *corev1.Pod) {
	nm.lock.Lock()
	defer nm.lock.Unlock()
	delete(nm.nominatedPreAllocatable, pod.UID)
}

// DeleteNominatedReservePodOrReservation is used to delete the nominated reserve pod or
// the nominated reservation for the pod.
func (nm *nominator) DeleteNominatedReservePodOrReservation(pod *corev1.Pod) {
	if reservationutil.IsReservePod(pod) {
		nm.DeleteReservePod(pod)
	} else {
		nm.RemoveNominatedReservation(pod)
	}
}

func (nm *nominator) GetNominatedReservation(pod *corev1.Pod, nodeName string) types.UID {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	return nm.nominatedPodToNode[pod.UID][nodeName]
}

func (nm *nominator) GetNominatedPreAllocation(rInfo *frameworkext.ReservationInfo, nodeName string) *corev1.Pod {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	pods := nm.nominatedPreAllocatable[rInfo.UID()][nodeName]
	if len(pods) > 0 {
		return pods[0]
	}
	return nil
}

func (nm *nominator) AddNominatedPreAllocations(rInfo *frameworkext.ReservationInfo, nodeName string, pods []*corev1.Pod) {
	if rInfo == nil || len(pods) == 0 {
		return
	}
	nm.lock.Lock()
	defer nm.lock.Unlock()

	rName := rInfo.GetName()
	if nm.rLister != nil {
		r, err := nm.rLister.Get(rName)
		if err != nil {
			klog.V(4).InfoS("reservation doesn't exist in rLister, aborted nominating for reservation pre-allocations",
				"node", nodeName, "podsCount", len(pods), "reservation", rName)
			return
		}
		if phase := r.Status.Phase; len(phase) > 0 && phase != schedulingv1alpha1.ReservationPending {
			klog.V(4).InfoS("reservation is scheduled or terminated, aborted nominating for reservation pre-allocations",
				"phase", r.Status.Phase, "node", nodeName, "podsCount", len(pods), "reservation", rName)
			return
		}
		if r.Status.NodeName != "" {
			klog.V(4).InfoS("reservation is assigned, aborted nominating for reservation pre-allocations",
				"current node", r.Status.NodeName, "node", nodeName, "podsCount", len(pods), "reservation", rName)
			return
		}
	}

	// Validate all pods are assigned to the node
	if nm.podLister != nil {
		for _, pod := range pods {
			p, err := nm.podLister.Pods(pod.Namespace).Get(pod.Name)
			if err != nil {
				klog.V(4).InfoS("Pod doesn't exist in podLister, aborted nominating it for reservation pre-allocations",
					"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
				return
			}
			if p.Spec.NodeName != nodeName {
				klog.V(4).InfoS("Pod is not scheduled to the node, aborted nominating for reservation pre-allocations",
					"current node", p.Spec.NodeName, "node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
				return
			}
		}
	}

	nodeToPreAllocatable := nm.nominatedPreAllocatable[rInfo.UID()]
	if nodeToPreAllocatable == nil {
		nodeToPreAllocatable = map[string][]*corev1.Pod{}
		nm.nominatedPreAllocatable[rInfo.UID()] = nodeToPreAllocatable
	}
	nodeToPreAllocatable[nodeName] = pods

	if klog.V(5).Enabled() {
		podNames := make([]string, len(pods))
		for i, pod := range pods {
			podNames[i] = klog.KObj(pod).String()
		}
		klog.InfoS("Nominated pre-allocatable pods for reservation",
			"reservation", rName, "node", nodeName, "pods", podNames)
	}
}

func (nm *nominator) GetNominatedPreAllocations(rInfo *frameworkext.ReservationInfo, nodeName string) []*corev1.Pod {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	return nm.nominatedPreAllocatable[rInfo.UID()][nodeName]
}

// TODO(joseph): Should move the function into frameworkext package as default nominator

func (pl *Plugin) NominateReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) (*frameworkext.ReservationInfo, *fwktype.Status) {
	if reservationutil.IsReservePod(pod) {
		return nil, nil
	}

	state := getStateData(cycleState)

	var reservationInfos []*frameworkext.ReservationInfo
	if nodeRState := state.nodeReservationStates[nodeName]; nodeRState != nil {
		reservationInfos = nodeRState.matchedOrIgnored
	}

	if len(reservationInfos) == 0 {
		return nil, nil
	}

	if len(reservationInfos) == 1 && state.hasAffinity {
		return reservationInfos[0], nil
	}

	rInfo := pl.GetNominatedReservation(pod, nodeName)
	if rInfo != nil {
		return rInfo, nil
	}

	extender, ok := pl.handle.(frameworkext.FrameworkExtender)
	if !ok {
		return nil, fwktype.AsStatus(fmt.Errorf("not implemented frameworkext.FrameworkExtender"))
	}

	reservations := make([]*frameworkext.ReservationInfo, 0, len(reservationInfos))
	for i := range reservationInfos {
		status := extender.RunNominateReservationFilterPlugins(ctx, cycleState, pod, reservationInfos[i], nodeName)
		if !status.IsSuccess() {
			continue
		}
		reservations = append(reservations, reservationInfos[i])
	}
	if len(reservations) == 0 {
		return nil, nil
	}

	if len(reservations) == 1 {
		return reservations[0], nil
	}

	nominated, _ := findMostPreferredReservationByOrder(reservations)
	if nominated != nil {
		return nominated, nil
	}

	reservationScoreList, err := prioritizeReservations(ctx, extender, cycleState, pod, reservations, nodeName)
	if err != nil {
		return nil, fwktype.AsStatus(err)
	}
	sort.Slice(reservationScoreList, func(i, j int) bool {
		return reservationScoreList[i].Score > reservationScoreList[j].Score
	})

	nominated = nil
	for _, v := range reservations {
		if v.UID() == reservationScoreList[0].UID {
			nominated = v
			break
		}
	}
	if nominated == nil {
		return nil, fwktype.AsStatus(fmt.Errorf("missing the most suitable reservation %v(%v)",
			klog.KRef(reservationScoreList[0].Namespace, reservationScoreList[0].Name), reservationScoreList[0].UID))
	}
	return nominated, nil
}

func (pl *Plugin) AddNominatedReservation(pod *corev1.Pod, nodeName string, rInfo *frameworkext.ReservationInfo) {
	pl.nominator.AddNominatedReservation(pod, nodeName, rInfo)
}

// RemoveNominatedReservations is used to delete the nominated reserve pod.
// DEPRECATED: use DeleteNominatedReservePodOrReservation instead.
func (pl *Plugin) RemoveNominatedReservations(pod *corev1.Pod) {
	pl.nominator.RemoveNominatedReservation(pod)
}

func (pl *Plugin) AddNominatedReservePod(pod *corev1.Pod, nodeName string) {
	if nodeName == "" {
		// An empty node means the reserve pod lost its nomination, which has to
		// be honored even when the pod cannot be parsed - otherwise a reserve
		// pod whose affinity became invalid keeps holding the node it was
		// nominated to before that change.
		pl.nominator.DeleteReservePod(pod)
		return
	}
	podInfo, err := framework.NewPodInfo(pod)
	if err != nil {
		// A partially parsed PodInfo would understate the reserve pod's
		// constraints for every pod that later reads this nomination, so skip
		// it rather than nominate something incomplete.
		klog.ErrorS(err, "Failed to build the reserve pod info, skipped nominating it",
			"pod", klog.KObj(pod), "node", nodeName)
		return
	}
	pl.nominator.AddNominatedReservePod(podInfo, nodeName)
}

// DeleteNominatedReservePodOrReservation is used to delete the nominated reserve pod or
// the nominated reservation for the pod.
func (pl *Plugin) DeleteNominatedReservePodOrReservation(pod *corev1.Pod) {
	pl.nominator.DeleteNominatedReservePodOrReservation(pod)
}

// DeleteNominatedReservePod is used to delete the nominated reserve pod.
// DEPRECATED: use DeleteNominatedReservePodOrReservation instead.
func (pl *Plugin) DeleteNominatedReservePod(pod *corev1.Pod) {
	pl.nominator.DeleteReservePod(pod)
}

func (pl *Plugin) NominatedReservePodForNode(nodeName string) []*framework.PodInfo {
	return pl.nominator.NominatedReservePodForNode(nodeName)
}

func (pl *Plugin) GetNominatedReservation(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	reservationID := pl.nominator.GetNominatedReservation(pod, nodeName)
	if reservationID == "" {
		return nil
	}
	return pl.reservationCache.getReservationInfoByUID(reservationID)
}

func (pl *Plugin) NominatePreAllocation(ctx context.Context, cycleState fwktype.CycleState, rInfo *frameworkext.ReservationInfo, nodeName string) (*corev1.Pod, *fwktype.Status) {
	if !rInfo.IsPreAllocation() {
		return nil, nil
	}

	state := getStateData(cycleState)

	var preAllocatablePods []*corev1.Pod
	if nodeRState := state.nodeReservationStates[nodeName]; nodeRState != nil {
		preAllocatablePods = nodeRState.selectedPreAllocatablePods
	}

	if len(preAllocatablePods) == 0 {
		return nil, nil
	}

	if len(preAllocatablePods) == 1 && state.isPreAllocationRequired {
		return preAllocatablePods[0], nil
	}

	pod := pl.GetNominatedPreAllocation(rInfo, nodeName)
	if pod != nil {
		return pod, nil
	}

	extender, ok := pl.handle.(frameworkext.FrameworkExtender)
	if !ok {
		return nil, fwktype.AsStatus(fmt.Errorf("not implemented frameworkext.FrameworkExtender"))
	}

	candidates := make([]*corev1.Pod, 0, len(preAllocatablePods))
	for i := range preAllocatablePods {
		status := extender.RunNominateReservationFilterPlugins(ctx, cycleState, preAllocatablePods[i], rInfo, nodeName)
		if !status.IsSuccess() {
			continue
		}
		candidates = append(candidates, preAllocatablePods[i])
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}

	reservationScoreList, err := prioritizePreAllocatablePods(ctx, extender, cycleState, rInfo, candidates, nodeName)
	if err != nil {
		return nil, fwktype.AsStatus(err)
	}
	sort.Slice(reservationScoreList, func(i, j int) bool {
		return reservationScoreList[i].Score > reservationScoreList[j].Score
	})

	var nominated *corev1.Pod
	for _, v := range preAllocatablePods {
		if v.GetUID() == reservationScoreList[0].UID {
			nominated = v
			break
		}
	}
	if nominated == nil {
		return nil, fwktype.AsStatus(fmt.Errorf("missing the most suitable pre-allocatable pod %v(%v)",
			klog.KRef(reservationScoreList[0].Namespace, reservationScoreList[0].Name), reservationScoreList[0].UID))
	}
	return nominated, nil
}

func (pl *Plugin) AddNominatedPreAllocation(rInfo *frameworkext.ReservationInfo, nodeName string, pod *corev1.Pod) {
	pl.nominator.AddNominatedPreAllocation(rInfo, nodeName, pod)
}

func (pl *Plugin) GetNominatedPreAllocation(rInfo *frameworkext.ReservationInfo, nodeName string) *corev1.Pod {
	return pl.nominator.GetNominatedPreAllocation(rInfo, nodeName)
}

func (pl *Plugin) AddNominatedPreAllocations(rInfo *frameworkext.ReservationInfo, nodeName string, pods []*corev1.Pod) {
	pl.nominator.AddNominatedPreAllocations(rInfo, nodeName, pods)
}

func (pl *Plugin) GetNominatedPreAllocations(rInfo *frameworkext.ReservationInfo, nodeName string) []*corev1.Pod {
	return pl.nominator.GetNominatedPreAllocations(rInfo, nodeName)
}

// NominatePreAllocations nominates multiple pre-allocatable pods for a reservation.
// It accumulates resources from pods until all dimensions are satisfied.
func (pl *Plugin) NominatePreAllocations(cycleState fwktype.CycleState, rInfo *frameworkext.ReservationInfo, nodeName string) ([]*corev1.Pod, *fwktype.Status) {
	if !rInfo.IsPreAllocation() {
		return nil, nil
	}
	state := getStateData(cycleState)

	var preAllocatablePods []*corev1.Pod
	if nodeRState := state.nodeReservationStates[nodeName]; nodeRState != nil {
		preAllocatablePods = nodeRState.selectedPreAllocatablePods
	}
	return preAllocatablePods, nil
}

func prioritizeReservations(
	ctx context.Context,
	fwk frameworkext.FrameworkExtender,
	state fwktype.CycleState,
	pod *corev1.Pod,
	reservations []*frameworkext.ReservationInfo,
	nodeName string,
) (frameworkext.ReservationScoreList, error) {
	scoresMap, scoreStatus := fwk.RunReservationScorePlugins(ctx, state, pod, reservations, nodeName)
	if !scoreStatus.IsSuccess() {
		return nil, scoreStatus.AsError()
	}

	if klog.V(5).Enabled() {
		for plugin, reservationScoreList := range scoresMap {
			for _, score := range reservationScoreList {
				klog.InfoS("Plugin scored reservation for pod", "pod", klog.KObj(pod), "plugin", plugin, "reservation", klog.KRef(score.Namespace, score.Name), "score", score.Score)
			}
		}
	}

	// Summarize all scores.
	result := make(frameworkext.ReservationScoreList, 0, len(reservations))
	for i := range reservations {
		rs := frameworkext.ReservationScore{
			Name:      reservations[i].GetName(),
			Namespace: reservations[i].GetNamespace(),
			UID:       reservations[i].UID(),
			Score:     0,
		}
		result = append(result, rs)
		for j := range scoresMap {
			result[i].Score += scoresMap[j][i].Score
		}
	}

	if klog.V(5).Enabled() {
		for i := range result {
			klog.InfoS("Calculated reservation's final score for pod", "pod", klog.KObj(pod), "reservation", klog.KRef(result[i].Namespace, result[i].Name), "score", result[i].Score)
		}
	}
	return result, nil
}

func prioritizePreAllocatablePods(
	ctx context.Context,
	fwk frameworkext.FrameworkExtender,
	state fwktype.CycleState,
	rInfo *frameworkext.ReservationInfo,
	pods []*corev1.Pod,
	nodeName string,
) (frameworkext.ReservationScoreList, error) {
	scoresMap, scoreStatus := fwk.RunReservationPreAllocationScorePlugins(ctx, state, rInfo, pods, nodeName)
	if !scoreStatus.IsSuccess() {
		return nil, scoreStatus.AsError()
	}

	if klog.V(5).Enabled() {
		for plugin, reservationScoreList := range scoresMap {
			for _, score := range reservationScoreList {
				klog.InfoS("Plugin scored pre-allocatable pod for reservation", "reservation", rInfo.GetName(), "plugin", plugin, "pod", klog.KRef(score.Namespace, score.Name), "score", score.Score)
			}
		}
	}

	// Summarize all scores.
	result := make(frameworkext.ReservationScoreList, 0, len(pods))
	for i := range pods {
		pod := pods[i]
		rs := frameworkext.ReservationScore{
			Name:      pod.GetName(),
			Namespace: pod.GetNamespace(),
			UID:       pod.GetUID(),
			Score:     0,
		}
		result = append(result, rs)
		for j := range scoresMap {
			result[i].Score += scoresMap[j][i].Score
		}
	}

	if klog.V(5).Enabled() {
		for i := range result {
			klog.InfoS("Calculated pre-allocatable pod's final score for reservation", "reservation", rInfo.GetName(), "pod", klog.KRef(result[i].Namespace, result[i].Name), "score", result[i].Score)
		}
	}
	return result, nil
}

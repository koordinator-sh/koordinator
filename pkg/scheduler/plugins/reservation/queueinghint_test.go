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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientcache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestPlugin_EventsToRegister(t *testing.T) {
	tests := []struct {
		name            string
		enableQueueHint bool
		expectHintFn    bool
	}{
		{
			name:            "no hint functions when queue hint is disabled",
			enableQueueHint: false,
			expectHintFn:    false,
		},
		{
			name:            "hint functions are set when queue hint is enabled",
			enableQueueHint: true,
			expectHintFn:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
				args.EnableQueueHint = tt.enableQueueHint
			})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			events, err := pl.EventsToRegister(context.TODO())
			assert.NoError(t, err)
			assert.Equal(t, 2, len(events), "should register exactly Pod and Reservation events")

			expectedGVK := fmt.Sprintf("reservations.%v.%v",
				schedulingv1alpha1.GroupVersion.Version,
				schedulingv1alpha1.GroupVersion.Group)

			var podEvent, reservationEvent *fwktype.ClusterEventWithHint
			for i := range events {
				switch events[i].Event.Resource {
				case fwktype.Pod:
					podEvent = &events[i]
				case fwktype.EventResource(expectedGVK):
					reservationEvent = &events[i]
				}
			}
			assert.NotNil(t, podEvent, "Pod Delete event should be registered")
			assert.NotNil(t, reservationEvent, "Reservation Add|Update|Delete event should be registered")

			// Action type is preserved regardless of the flag.
			assert.Equal(t, fwktype.Delete, podEvent.Event.ActionType)
			assert.Equal(t, fwktype.Add|fwktype.Update|fwktype.Delete, reservationEvent.Event.ActionType)

			if !tt.expectHintFn {
				assert.Nil(t, podEvent.QueueingHintFn)
				assert.Nil(t, reservationEvent.QueueingHintFn)
				return
			}
			require.NotNil(t, podEvent.QueueingHintFn)
			require.NotNil(t, reservationEvent.QueueingHintFn)

			// "Non-nil" alone would still pass if the two callbacks were
			// swapped. The pod hint answers Queue unconditionally, so only the
			// Reservation registration can tell them apart: a Pending Add is
			// QueueSkip from the reservation callback and Queue from the pod
			// one. The unstructured payload is what the dynamic informer
			// actually delivers.
			logger := klog.Background()
			waiter := makeWaitingPodNoReservation("waiter")

			hint, err := podEvent.QueueingHintFn(logger, waiter, makeWaitingPodNoReservation("deleted-unbound"), nil)
			assert.NoError(t, err)
			assert.Equal(t, fwktype.Queue, hint, "Pod/Delete requeues unconditionally")

			pendingAdd, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "r-wiring", UID: "r-wiring"},
				Status:     schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationPending},
			})
			require.NoError(t, err)
			hint, err = reservationEvent.QueueingHintFn(logger, waiter, nil, &unstructured.Unstructured{Object: pendingAdd})
			assert.NoError(t, err)
			assert.Equal(t, fwktype.QueueSkip, hint,
				"Reservation Add|Update|Delete must be wired to isSchedulableAfterReservationChange")
		})
	}
}

func makeWaitingPodUsingReservation(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: `{"reservationSelector":{"app":"demo"}}`,
			},
		},
	}
}

func makeWaitingPodNoReservation(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
		},
	}
}

func makeReservePod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
			Annotations: map[string]string{
				reservationutil.AnnotationReservePod: "true",
			},
		},
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterPodDeletion(t *testing.T) {
	type args struct {
		waitingPod *corev1.Pod
		oldObj     interface{}
	}
	tests := []struct {
		name         string
		args         args
		expectedHint fwktype.QueueingHint
	}{
		{
			name: "oldObj is not a Pod, fall back to Queue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w1"),
				oldObj:     "not-a-pod",
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "nil deleted pod, fall back to Queue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w1n"),
				oldObj:     (*corev1.Pod)(nil),
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "reserve pod deletion frees node capacity, requeue even waiters without affinity",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w2"),
				oldObj:     makeReservePod("deleted-reserve"),
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "waiting pod uses reservation and the deleted pod is a reserve pod, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w3"),
				oldObj:     makeReservePod("deleted-reserve"),
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "waiting pod is itself a reserve pod, deleted reserve pod frees its spot, requeue",
			args: args{
				waitingPod: makeReservePod("waiting-reserve"),
				oldObj:     makeReservePod("deleted-reserve"),
			},
			expectedHint: fwktype.Queue,
		},
		{
			// The scheduler unwraps a tombstone before notifying the queue, so
			// a pod with empty placement fields reaches the hint as a plain
			// *v1.Pod and may still be the stale copy of a pod the cache just
			// removed from a node. It cannot be skipped on those fields.
			name: "stale unwrapped delete with empty placement still requeues",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w4"),
				oldObj:     makeWaitingPodNoReservation("deleted-normal"),
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "any bound pod deletion frees node-level capacity, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-bound"),
				oldObj: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "bound-untracked", Namespace: "default", UID: "bound-untracked"},
					Spec:       corev1.PodSpec{NodeName: "node-x"},
				},
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "bound pod deletion requeues even waiters with no reservation relationship",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-preemptible"),
				oldObj: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "bound-any", Namespace: "default", UID: "bound-any"},
					Spec:       corev1.PodSpec{NodeName: "node-y"},
				},
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "deleted nominated pod releases preemptible accounting, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-nominated"),
				oldObj: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "nominated", Namespace: "default", UID: "nominated"},
					Status:     corev1.PodStatus{NominatedNodeName: "node-z"},
				},
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "tombstone-wrapped bound pod requeues",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-tombstone"),
				oldObj: clientcache.DeletedFinalStateUnknown{
					Key: "default/tombstoned",
					Obj: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: "tombstoned", Namespace: "default", UID: "tombstoned"},
						Spec:       corev1.PodSpec{NodeName: "node-t"},
					},
				},
			},
			expectedHint: fwktype.Queue,
		},
		{
			// The object a tombstone carries is the last one the store held,
			// which can predate the pod's binding. Trusting its empty placement
			// fields would skip a waiter whose rejection the deletion did in
			// fact resolve.
			name: "tombstone carrying a pre-binding copy still requeues",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-stale-tombstone"),
				oldObj: clientcache.DeletedFinalStateUnknown{
					Key: "default/stale-tombstoned",
					Obj: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: "stale-tombstoned", Namespace: "default", UID: "stale-tombstoned"},
					},
				},
			},
			expectedHint: fwktype.Queue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
				args.EnableQueueHint = true
			})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), tt.args.waitingPod, tt.args.oldObj, nil)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}

// TestPlugin_QueueingHint_PodDeletion_CacheStateIndependent documents that a
// bound pod's deletion requeues every waiter regardless of what the
// reservation cache says: the hint deliberately reads no cache state, because
// the informer handler that maintains the cache processes the same delete
// events on another goroutine, so the outcomes below must hold both before
// and after that handler runs.
func TestPlugin_QueueingHint_PodDeletion_CacheStateIndependent(t *testing.T) {
	suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
		args.EnableQueueHint = true
	})
	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	pl := p.(*Plugin)

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-owner-cache", UID: "r-owner-cache"},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "owner-match-demo"},
				},
			}},
		},
	}
	assert.NoError(t, reservationutil.SetReservationAvailable(reservation, "node-1"))
	pl.reservationCache.updateReservation(reservation)
	assignedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "assigned", Namespace: "default", UID: "assigned-uid"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
	assert.NoError(t, pl.reservationCache.assumePod(reservation.UID, assignedPod))

	waiters := []*corev1.Pod{
		makeWaitingPodUsingReservation("affinity-waiter"),
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "owner-matched", Namespace: "default", UID: "owner-matched",
				Labels: map[string]string{"app": "owner-match-demo"},
			},
		},
		makeWaitingPodNoReservation("unrelated-waiter"),
	}
	for _, waiter := range waiters {
		got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), waiter, assignedPod, nil)
		assert.NoError(t, err)
		assert.Equal(t, fwktype.Queue, got, "bound pod deletion requeues waiter %s while the pod is still assigned in the cache", waiter.Name)
	}

	// Regression for the informer-ordering race: the cache handler may
	// process the same delete event before the scheduling queue evaluates
	// this hint. The outcomes must not change.
	pl.reservationCache.deletePod(reservation.UID, assignedPod)
	for _, waiter := range waiters {
		got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), waiter, assignedPod, nil)
		assert.NoError(t, err)
		assert.Equal(t, fwktype.Queue, got, "bound pod deletion requeues waiter %s after the cache handler already ran", waiter.Name)
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterReservationChange(t *testing.T) {
	// IsReservationAvailable requires Status.NodeName to be set and Phase == Available.
	// The hint keys off availability because that is what ReservationInfo.IsMatchable
	// requires when the scheduler looks for a match.
	availableReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-available", UID: "r-available"},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "node-1",
		},
	}
	waitingReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-waiting", UID: "r-waiting"},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationWaiting,
			NodeName: "node-1",
		},
	}
	pendingReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-pending", UID: "r-pending"},
		Status:     schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationPending},
	}

	// ownerMatchedReservation is consumable by any pod whose labels include
	// app=owner-match-demo, even pods without a ReservationAffinity annotation.
	// The QueueingHintFn must still wake those pods when this reservation
	// becomes available; otherwise pods that rely on reservation owner
	// selectors miss scheduling opportunities.
	ownerMatchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-owner", UID: "r-owner"},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "owner-match-demo"},
				},
			}},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "node-1",
		},
	}
	ownerMatchedReservationPending := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-owner-p", UID: "r-owner-p"},
		Spec:       ownerMatchedReservation.Spec,
		Status:     schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationPending},
	}
	ownerMatchedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "owner-matched", Namespace: "default", UID: "owner-matched",
			Labels: map[string]string{"app": "owner-match-demo"},
		},
	}

	// Fixtures for updates that keep the reservation Available but still
	// change what a waiter can get from it. The Reservation CRD enables the
	// status subresource, so metadata.generation bumps exactly on spec
	// updates (e.g. widened owners or a resized template).
	availableGen1 := availableReservation.DeepCopy()
	availableGen1.Generation = 1
	availableGen2 := availableReservation.DeepCopy()
	availableGen2.Generation = 2
	availableRelabeled := availableReservation.DeepCopy()
	availableRelabeled.Labels = map[string]string{"app": "demo"}
	availableReannotated := availableReservation.DeepCopy()
	availableReannotated.Annotations = map[string]string{
		apiext.AnnotationNodeReservation: `{"resources":{"cpu":"1"}}`,
	}
	failedReservation := availableReservation.DeepCopy()
	failedReservation.Status.Phase = schedulingv1alpha1.ReservationFailed
	availableOnNode2 := availableReservation.DeepCopy()
	availableOnNode2.Status.NodeName = "node-2"
	availableNewUID := availableReservation.DeepCopy()
	availableNewUID.UID = "r-available-replaced"
	// For an Available reservation the fit filter sources capacity from
	// status.allocatable, so growing it (VPA resize, scheduler amendment)
	// increases free capacity without any generation or metadata change.
	availableFreeNone := availableReservation.DeepCopy()
	availableFreeNone.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	}
	availableFreeNone.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	}
	availableFreeGrown := availableFreeNone.DeepCopy()
	availableFreeGrown.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("4"),
	}
	availableFreeNetGrown := availableFreeNone.DeepCopy()
	availableFreeNetGrown.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("6"),
	}
	availableFreeNetGrown.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("5"),
	}
	// Shrinking allocatable shrinks the reserve pod held in the scheduler
	// cache and releases node capacity, which can admit waiters unrelated to
	// this reservation.
	availableFreeShrunk := availableFreeNone.DeepCopy()
	availableFreeShrunk.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("1"),
	}
	// A still-pending reservation's spec update can fix why its own reserve
	// pod was rejected; a status-only write (e.g. the scheduler's own
	// unschedulable condition) must not requeue it.
	pendingGen1 := pendingReservation.DeepCopy()
	pendingGen1.Generation = 1
	pendingGen2 := pendingReservation.DeepCopy()
	pendingGen2.Generation = 2
	pendingRelabeled := pendingGen1.DeepCopy()
	pendingRelabeled.Labels = map[string]string{"tier": "gold"}
	// A finalizer keeps the object Pending with everything else unchanged, so
	// only deletionTimestamp distinguishes it.
	pendingTerminating := pendingGen1.DeepCopy()
	terminatingAt := metav1.Now()
	pendingTerminating.DeletionTimestamp = &terminatingAt
	// Same-phase nodeName mutations: the handlers apply these as
	// delete-then-add of the assumed reserve pod (Waiting is an active,
	// assigned state), freeing or moving node capacity.
	waitingOnNode2 := waitingReservation.DeepCopy()
	waitingOnNode2.Status.NodeName = "node-2"
	waitingUnassigned := waitingReservation.DeepCopy()
	waitingUnassigned.Status.NodeName = ""
	pendingOnNode1 := pendingReservation.DeepCopy()
	pendingOnNode1.Status.NodeName = "node-1"
	availableAllocatedReleased := availableFreeNone.DeepCopy()
	availableAllocatedReleased.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("1"),
	}
	// A resource present in allocated but not in allocatable. Its
	// disappearance changes status.allocated, so it is still an accounting
	// change.
	availableExtraAllocated := availableFreeNone.DeepCopy()
	availableExtraAllocated.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("2"),
		corev1.ResourceMemory: resource.MustParse("1Gi"),
	}
	ownReservePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-r-pending", Namespace: "default", UID: "reserve-r-pending",
			Annotations: map[string]string{
				reservationutil.AnnotationReservePod:      "true",
				reservationutil.AnnotationReservationName: pendingReservation.Name,
			},
		},
	}
	availableAllocatedHigh := availableReservation.DeepCopy()
	availableAllocatedHigh.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("4"),
	}
	availableAllocatedLow := availableReservation.DeepCopy()
	availableAllocatedLow.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	}
	availableAllocatedZero := availableReservation.DeepCopy()
	// Two owners whose tracked requests are both zero, so releasing one frees a
	// pod slot without moving any allocated quantity.
	availableTwoOwners := availableReservation.DeepCopy()
	availableTwoOwners.Status.Allocated = corev1.ResourceList{}
	availableTwoOwners.Status.CurrentOwners = []corev1.ObjectReference{
		{Kind: "Pod", Namespace: "default", Name: "owner-a", UID: "owner-a"},
		{Kind: "Pod", Namespace: "default", Name: "owner-b", UID: "owner-b"},
	}
	availableOneOwner := availableTwoOwners.DeepCopy()
	availableOneOwner.Status.CurrentOwners = availableTwoOwners.Status.CurrentOwners[:1]
	// Owners widened while Available: the old spec targeted another app, the
	// new spec targets the waiter's app, and the spec change bumped the
	// generation.
	ownerWidenedOld := ownerMatchedReservation.DeepCopy()
	ownerWidenedOld.Generation = 1
	ownerWidenedOld.Spec.Owners = []schedulingv1alpha1.ReservationOwner{{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "another-app"},
		},
	}}
	ownerWidenedNew := ownerMatchedReservation.DeepCopy()
	ownerWidenedNew.Generation = 2

	type args struct {
		waitingPod *corev1.Pod
		oldObj     interface{}
		newObj     interface{}
	}
	tests := []struct {
		name         string
		args         args
		expectedHint fwktype.QueueingHint
	}{
		{
			name: "obj is not a Reservation, fall back to Queue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w1"),
				oldObj:     nil,
				newObj:     "not-a-reservation",
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "waiting pod is unrelated to reservations, skip all reservation changes",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w2"),
				oldObj:     nil,
				newObj:     availableReservation,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			// A reserve pod is a waiter this plugin can reject too, and
			// podUsesReservation treats it as able to consume a reservation:
			// its own reservation may be the one that just became available,
			// and it competes for the same node-level capacity either way.
			name: "Add an available reservation, requeue a waiting reserve pod",
			args: args{
				waitingPod: makeReservePod("w-reserve-add"),
				oldObj:     nil,
				newObj:     availableReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Add an available reservation, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w3"),
				oldObj:     nil,
				newObj:     availableReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Add a not-yet-available reservation (pending), skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w4"),
				oldObj:     nil,
				newObj:     pendingReservation,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "Add a Waiting reservation is not yet matchable, skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w4w"),
				oldObj:     nil,
				newObj:     waitingReservation,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "Update from pending to available, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w5"),
				oldObj:     pendingReservation,
				newObj:     availableReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Update from Waiting to Available is the matchability transition, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w5w"),
				oldObj:     waitingReservation,
				newObj:     availableReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Update from Pending to Waiting clears the reserve pod nomination, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w5p"),
				oldObj:     pendingReservation,
				newObj:     waitingReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "transition into Available requeues even waiters unrelated to this reservation",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-into-avail"),
				oldObj:     pendingReservation,
				newObj:     availableReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Update while both are available with no meaningful change, skip to avoid queue noise",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w6"),
				oldObj:     availableReservation,
				newObj:     availableReservation,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "Delete gives waiting pods another chance to re-evaluate",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w7"),
				oldObj:     availableReservation,
				newObj:     nil,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Update from Available to non-Available frees the reserve pod's node resources, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-lose"),
				oldObj:     availableReservation,
				newObj:     pendingReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Update from Available to Failed requeues even waiters unrelated to this reservation",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-failed"),
				oldObj:     availableReservation,
				newObj:     failedReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Delete requeues even waiters unrelated to this reservation",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-del-unrelated"),
				oldObj:     availableReservation,
				newObj:     nil,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Available reservation migrated to another node, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-migrate"),
				oldObj:     availableReservation,
				newObj:     availableOnNode2,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Available reservation replaced under the same name (UID changed), requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-uid"),
				oldObj:     availableReservation,
				newObj:     availableNewUID,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "status.allocatable grew while Available, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-allocatable"),
				oldObj:     availableFreeNone,
				newObj:     availableFreeGrown,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "allocatable and allocated both changed while Available, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-netfree"),
				oldObj:     availableFreeNone,
				newObj:     availableFreeNetGrown,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "status.allocatable shrank, node capacity released, requeue even unrelated waiters",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-shrunk"),
				oldObj:     availableFreeNone,
				newObj:     availableFreeShrunk,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "pending reservation spec updated, its own reserve pod gets another chance",
			args: args{
				waitingPod: ownReservePod,
				oldObj:     pendingGen1,
				newObj:     pendingGen2,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "pending reservation status-only update must not requeue its reserve pod (would loop)",
			args: args{
				waitingPod: ownReservePod,
				oldObj:     pendingGen1,
				newObj:     pendingGen1,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "pending reservation spec update resizes its nominated reserve pod, requeue all waiters",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-pending-spec"),
				oldObj:     pendingGen1,
				newObj:     pendingGen2,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "pending reservation entering deletion releases its nomination, requeue",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-terminating"),
				oldObj:     pendingGen1,
				newObj:     pendingTerminating,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "pending reservation label change requeues waiters without any reservation relationship",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-pending-label"),
				oldObj:     pendingGen1,
				newObj:     pendingRelabeled,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "a release is not gated on consumer relevance, requeue an unrelated waiter",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-cross-release"),
				oldObj:     availableFreeNone,
				newObj:     availableAllocatedReleased,
			},
			expectedHint: fwktype.Queue,
		},
		{
			// A spec change is not local to this reservation's consumers: the
			// template requests decide ReservationInfo.ResourceNames, which
			// masks Allocated, which fitsNode subtracts from the node's
			// requested total for every pod on the node. Requeue regardless of
			// whether the waiter could ever claim this reservation.
			name: "Available-state spec change requeues a waiter that can neither claim nor own the reservation",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-unrelated-spec"),
				oldObj:     availableGen1,
				newObj:     availableGen2,
			},
			expectedHint: fwktype.Queue,
		},
		{
			// An Available reservation's reserve pod sits in the scheduler
			// cache carrying these labels, so other pods' inter-pod affinity
			// and topology spread are evaluated against them.
			name: "Available-state label change requeues a waiter with no reservation relationship",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-unrelated-label"),
				// Same pair as the matched-waiter case above, so only the
				// waiter's relationship to the reservation differs - and the
				// generation is untouched, so this exercises the label path
				// rather than the spec path.
				oldObj: availableReservation,
				newObj: availableRelabeled,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "an over-allocated resource key disappearing is a release, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-vanished"),
				oldObj:     availableExtraAllocated,
				newObj:     availableFreeNone,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "a waiting reserve pod is woken by another reservation's spec update",
			args: args{
				waitingPod: makeReservePod("waiting-reserve-other"),
				oldObj:     availableGen1,
				newObj:     availableGen2,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Waiting reservation migrated to another node, requeue",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-waiting-migrate"),
				oldObj:     waitingReservation,
				newObj:     waitingOnNode2,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Waiting reservation assignment rolled back, requeue",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-waiting-rollback"),
				oldObj:     waitingReservation,
				newObj:     waitingUnassigned,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Pending reservation gained a nodeName, requeue",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-pending-node"),
				oldObj:     pendingReservation,
				newObj:     pendingOnNode1,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "owner-matched pod without affinity wakes when its owner reservation becomes Available",
			args: args{
				waitingPod: ownerMatchedPod,
				oldObj:     ownerMatchedReservationPending,
				newObj:     ownerMatchedReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "owner-matched pod without affinity wakes when an Available reservation it can match is added",
			args: args{
				waitingPod: ownerMatchedPod,
				oldObj:     nil,
				newObj:     ownerMatchedReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "owner-matched pod stays skipped when the affected reservation does not match its labels",
			args: args{
				waitingPod: ownerMatchedPod,
				oldObj:     nil,
				newObj:     availableReservation, // empty owners, does not target this pod
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "Available reservation spec updated (generation bumped), requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-gen"),
				oldObj:     availableGen1,
				newObj:     availableGen2,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "owners widened while Available to now target the waiter, requeue",
			args: args{
				waitingPod: ownerMatchedPod,
				oldObj:     ownerWidenedOld,
				newObj:     ownerWidenedNew,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "labels changed while Available, reservation affinity may now select it, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-label"),
				oldObj:     availableReservation,
				newObj:     availableRelabeled,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "annotations changed while Available, reserved/restricted derivations may change, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-ann"),
				oldObj:     availableReservation,
				newObj:     availableReannotated,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "allocated capacity released while Available, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-freed"),
				oldObj:     availableAllocatedHigh,
				newObj:     availableAllocatedLow,
			},
			expectedHint: fwktype.Queue,
		},
		{
			// Growth is not a reason to skip. Associating an already-bound pod
			// with a reservation raises Allocated without changing that pod's
			// requests, and restoreUnmatchedReservations then subtracts the
			// larger amount from the node's requested total, which can admit a
			// waiter that has no relationship to this reservation at all.
			name: "allocated capacity grew while Available, requeue an unrelated waiter",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-grew-unrelated"),
				oldObj:     availableAllocatedLow,
				newObj:     availableAllocatedHigh,
			},
			expectedHint: fwktype.Queue,
		},
		{
			// A pod slot is a separate limit from the allocated quantities:
			// fitsReservation rejects on len(AssignedPods)+1 > allocatable
			// pods. The controller compares CurrentOwners and Allocated
			// separately for the same reason.
			name: "current owners changed while the allocated quantities did not, requeue",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-owner-slot"),
				oldObj:     availableTwoOwners,
				newObj:     availableOneOwner,
			},
			expectedHint: fwktype.Queue,
		},
		{
			// A relist delivers OnAdd for an object the local store does not
			// have, so an Add can carry a reservation that has been allocated
			// for a while. Its accounting reaches this plugin for the first
			// time here.
			name: "add of an already allocated reservation requeues an unrelated waiter",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-late-add"),
				oldObj:     nil,
				newObj:     availableAllocatedHigh,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "add of an unallocated reservation still skips an unrelated waiter",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w-plain-add"),
				oldObj:     nil,
				newObj:     availableAllocatedZero,
			},
			expectedHint: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
				args.EnableQueueHint = true
			})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterReservationChange(klog.Background(), tt.args.waitingPod, tt.args.oldObj, tt.args.newObj)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}

// The QueueingHintFns run once per event per waiter this plugin rejected, so
// their per-call cost bounds event processing. Only the Add branch reaches
// reservationOwnerMatches, which builds a selector per Spec.Owners entry;
// every Update short-circuits before it.
func BenchmarkIsSchedulableAfterReservationChange_ReservationAddOwnerOnlyWaiter(b *testing.B) {
	pl := &Plugin{}
	owners := make([]schedulingv1alpha1.ReservationOwner, 0, 8)
	for i := range 8 {
		owners = append(owners, schedulingv1alpha1.ReservationOwner{
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{fmt.Sprintf("app-%d", i): "demo"}},
		})
	}
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-bench", UID: "r-bench", Generation: 1},
		Spec:       schedulingv1alpha1.ReservationSpec{Owners: owners},
		Status:     schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationAvailable, NodeName: "node-1"},
	}
	// No reservation-affinity annotation, so podUsesReservation is false, and
	// the matching selector is the last one, so all of them are built.
	waiter := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "w", Namespace: "default", UID: "w", Labels: map[string]string{"app-7": "demo"},
	}}
	logger := klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, nil, r)
		if err != nil || hint != fwktype.Queue {
			b.Fatalf("owner-matching path not exercised: hint=%v, err=%v", hint, err)
		}
	}
}

// The spec-update fast path the same waiter takes, which returns on the
// generation check before any owner selector is built.
func BenchmarkIsSchedulableAfterReservationChange_GenerationBump(b *testing.B) {
	pl := &Plugin{}
	owners := make([]schedulingv1alpha1.ReservationOwner, 0, 8)
	for i := range 8 {
		owners = append(owners, schedulingv1alpha1.ReservationOwner{
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{fmt.Sprintf("app-%d", i): "demo"}},
		})
	}
	oldR := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-bench-gen", UID: "r-bench-gen", Generation: 1},
		Spec:       schedulingv1alpha1.ReservationSpec{Owners: owners},
		Status:     schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationAvailable, NodeName: "node-1"},
	}
	newR := oldR.DeepCopy()
	newR.Generation = 2
	waiter := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "w", Namespace: "default", UID: "w", Labels: map[string]string{"app-7": "demo"},
	}}
	logger := klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, oldR, newR)
		if err != nil || hint != fwktype.Queue {
			b.Fatalf("unexpected hint result: hint=%v, err=%v", hint, err)
		}
	}
}

// The common no-op case: an Available reservation's status heartbeat with an
// owner-only waiter, ending in QueueSkip.
func BenchmarkIsSchedulableAfterReservationChange_StatusHeartbeat(b *testing.B) {
	pl := &Plugin{}
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-bench-hb", UID: "r-bench-hb", Generation: 1},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
			}},
		},
		Status: schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationAvailable, NodeName: "node-1"},
	}
	waiter := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "w-hb", Namespace: "default", UID: "w-hb"}}
	logger := klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, r, r)
		if err != nil || hint != fwktype.QueueSkip {
			b.Fatalf("status-heartbeat path not exercised: hint=%v, err=%v", hint, err)
		}
	}
}

// TestReservationOwnerMatches covers the fallbacks of the owner-matching
// helper the QueueingHintFns rely on. Unparsable owners must report "no
// match", mirroring the cached ReservationInfo used by Filter, whose
// MatchOwners returns false when the same parse failed.
func TestReservationOwnerMatches(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "p", Namespace: "default", UID: "p", Labels: map[string]string{"app": "demo"},
	}}
	matching := &schedulingv1alpha1.Reservation{
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
			}},
		},
	}
	nonMatching := &schedulingv1alpha1.Reservation{
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "other"}},
			}},
		},
	}
	unparsable := &schedulingv1alpha1.Reservation{
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: "NotAnOperator"}},
				},
			}},
		},
	}

	assert.False(t, reservationOwnerMatches(pod, nil), "nil reservation cannot claim any pod")
	assert.True(t, reservationOwnerMatches(pod, matching))
	assert.False(t, reservationOwnerMatches(pod, nonMatching))
	assert.False(t, reservationOwnerMatches(pod, unparsable),
		"unparsable owners must not claim the pod, matching ReservationInfo.MatchOwners")
}

// The scheduler resolves this plugin's event resource through the dynamic
// informer, so real Reservation events arrive as *unstructured.Unstructured.
// Every case is driven through the registered callback in both shapes and must
// answer identically; without the decoder the unstructured half falls into the
// type-error fallback and answers Queue for all of them, so the QueueSkip cases
// below are the ones that actually pin the behaviour.
func TestPlugin_QueueingHint_ReservationChange_UnstructuredParity(t *testing.T) {
	toUnstructured := func(t *testing.T, r *schedulingv1alpha1.Reservation) interface{} {
		t.Helper()
		if r == nil {
			return nil
		}
		object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(r)
		require.NoError(t, err)
		return &unstructured.Unstructured{Object: object}
	}

	newReservation := func(name string, phase schedulingv1alpha1.ReservationPhase) *schedulingv1alpha1.Reservation {
		return &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name), Generation: 1},
			Spec: schedulingv1alpha1.ReservationSpec{
				Owners: []schedulingv1alpha1.ReservationOwner{{
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "owned"}},
				}},
			},
			Status: schedulingv1alpha1.ReservationStatus{Phase: phase, NodeName: "node-1"},
		}
	}
	ownerMatched := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "owner-matched", Namespace: "default", UID: "owner-matched",
		Labels: map[string]string{"app": "owned"},
	}}
	unrelated := makeWaitingPodNoReservation("unrelated")

	pendingOld := newReservation("r-pending", schedulingv1alpha1.ReservationPending)
	pendingNew := pendingOld.DeepCopy()
	pendingNew.Status.Conditions = []schedulingv1alpha1.ReservationCondition{{
		Reason: schedulingv1alpha1.ReasonReservationUnschedulable, LastProbeTime: metav1.Now(),
	}}

	availableOld := newReservation("r-available", schedulingv1alpha1.ReservationAvailable)
	availableNew := availableOld.DeepCopy()
	availableNew.Status.Conditions = []schedulingv1alpha1.ReservationCondition{{
		Reason: schedulingv1alpha1.ReasonReservationAvailable, LastProbeTime: metav1.Now(),
	}}

	replacedOld := newReservation("r-replaced", schedulingv1alpha1.ReservationPending)
	replacedNew := replacedOld.DeepCopy()
	replacedNew.UID = "r-replaced-2"

	phaseOld := newReservation("r-phase", schedulingv1alpha1.ReservationPending)
	phaseNew := phaseOld.DeepCopy()
	phaseNew.Status.Phase = schedulingv1alpha1.ReservationAvailable

	deletingOld := newReservation("r-deleting", schedulingv1alpha1.ReservationPending)
	deletingNew := deletingOld.DeepCopy()
	now := metav1.Now()
	deletingNew.DeletionTimestamp = &now

	allocGrewOld := newReservation("r-alloc", schedulingv1alpha1.ReservationAvailable)
	allocGrewOld.Status.Allocated = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}
	allocGrewNew := allocGrewOld.DeepCopy()
	allocGrewNew.Status.Allocated = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}

	ownersOld := newReservation("r-owners", schedulingv1alpha1.ReservationAvailable)
	ownersOld.Status.CurrentOwners = []corev1.ObjectReference{
		{Kind: "Pod", Namespace: "default", Name: "o-a", UID: "o-a"},
		{Kind: "Pod", Namespace: "default", Name: "o-b", UID: "o-b"},
	}
	ownersNew := ownersOld.DeepCopy()
	ownersNew.Status.CurrentOwners = ownersOld.Status.CurrentOwners[:1]

	tests := []struct {
		name   string
		waiter *corev1.Pod
		oldR   *schedulingv1alpha1.Reservation
		newR   *schedulingv1alpha1.Reservation
		want   fwktype.QueueingHint
	}{
		{"pending status-only update", unrelated, pendingOld, pendingNew, fwktype.QueueSkip},
		{"available heartbeat", unrelated, availableOld, availableNew, fwktype.QueueSkip},
		{"available add, unrelated waiter", unrelated, nil, availableOld, fwktype.QueueSkip},
		{"available add, owner-only waiter", ownerMatched, nil, availableOld, fwktype.Queue},
		{"uid replacement", unrelated, replacedOld, replacedNew, fwktype.Queue},
		{"phase transition", unrelated, phaseOld, phaseNew, fwktype.Queue},
		{"entering deletion", unrelated, deletingOld, deletingNew, fwktype.Queue},
		{"delete", unrelated, availableOld, nil, fwktype.Queue},
		{"allocation growth, unrelated waiter", unrelated, allocGrewOld, allocGrewNew, fwktype.Queue},
		{"allocation release, unrelated waiter", unrelated, allocGrewNew, allocGrewOld, fwktype.Queue},
		{"owners released, quantities unchanged", unrelated, ownersOld, ownersNew, fwktype.Queue},
		{"add of an already allocated reservation", unrelated, nil, allocGrewNew, fwktype.Queue},
	}

	suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
		args.EnableQueueHint = true
	})
	p, err := suit.pluginFactory()
	require.NoError(t, err)
	pl := p.(*Plugin)
	events, err := pl.EventsToRegister(context.TODO())
	require.NoError(t, err)
	var hintFn fwktype.QueueingHintFn
	for i := range events {
		if events[i].Event.Resource != fwktype.Pod {
			hintFn = events[i].QueueingHintFn
		}
	}
	require.NotNil(t, hintFn)
	logger := klog.Background()

	for _, tt := range tests {
		t.Run(tt.name+"/typed", func(t *testing.T) {
			var oldObj, newObj interface{}
			if tt.oldR != nil {
				oldObj = tt.oldR
			}
			if tt.newR != nil {
				newObj = tt.newR
			}
			got, err := hintFn(logger, tt.waiter, oldObj, newObj)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
		t.Run(tt.name+"/unstructured", func(t *testing.T) {
			got, err := hintFn(logger, tt.waiter, toUnstructured(t, tt.oldR), toUnstructured(t, tt.newR))
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("unstructured tombstone requeues", func(t *testing.T) {
		got, err := hintFn(logger, unrelated,
			clientcache.DeletedFinalStateUnknown{Key: "r-available", Obj: toUnstructured(t, availableOld)}, nil)
		assert.NoError(t, err)
		assert.Equal(t, fwktype.Queue, got)
	})

	t.Run("undecodable payload requeues", func(t *testing.T) {
		got, err := hintFn(logger, unrelated, nil, &unstructured.Unstructured{
			Object: map[string]interface{}{"status": map[string]interface{}{"phase": 42}},
		})
		assert.NoError(t, err)
		assert.Equal(t, fwktype.Queue, got)
	})
}

// The typed benchmarks above measure the predicate alone. These two add the
// decoding the dynamic informer actually forces on every event: the fixtures
// are converted outside the timer, so what is measured is the
// FromUnstructured the callback itself performs.
func BenchmarkIsSchedulableAfterReservationChange_UnstructuredHeartbeat(b *testing.B) {
	pl := &Plugin{}
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-bench-u-hb", UID: "r-bench-u-hb", Generation: 1},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
			}},
		},
		Status: schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationAvailable, NodeName: "node-1"},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(r)
	if err != nil {
		b.Fatal(err)
	}
	event := &unstructured.Unstructured{Object: object}
	waiter := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "w-u-hb", Namespace: "default", UID: "w-u-hb"}}
	logger := klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, event, event)
		if err != nil || hint != fwktype.QueueSkip {
			b.Fatalf("unstructured heartbeat path not exercised: hint=%v, err=%v", hint, err)
		}
	}
}

func BenchmarkIsSchedulableAfterReservationChange_UnstructuredAllocatedAdd(b *testing.B) {
	pl := &Plugin{}
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-bench-u-add", UID: "r-bench-u-add", Generation: 1},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:     schedulingv1alpha1.ReservationAvailable,
			NodeName:  "node-1",
			Allocated: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
		},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(r)
	if err != nil {
		b.Fatal(err)
	}
	event := &unstructured.Unstructured{Object: object}
	waiter := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "w-u-add", Namespace: "default", UID: "w-u-add"}}
	logger := klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, nil, event)
		if err != nil || hint != fwktype.Queue {
			b.Fatalf("unstructured allocated-add path not exercised: hint=%v, err=%v", hint, err)
		}
	}
}

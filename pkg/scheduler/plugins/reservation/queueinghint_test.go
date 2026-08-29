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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

			if tt.expectHintFn {
				assert.NotNil(t, podEvent.QueueingHintFn)
				assert.NotNil(t, reservationEvent.QueueingHintFn)
			} else {
				assert.Nil(t, podEvent.QueueingHintFn)
				assert.Nil(t, reservationEvent.QueueingHintFn)
			}
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
			name: "deleted pod never bound, held no node resources, skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w4"),
				oldObj:     makeWaitingPodNoReservation("deleted-normal"),
			},
			expectedHint: fwktype.QueueSkip,
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
	// An allocated resource that is absent from allocatable carries negative
	// free capacity; its disappearance is still a release.
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
			name: "status.allocatable grew while Available, free capacity increased, requeue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-allocatable"),
				oldObj:     availableFreeNone,
				newObj:     availableFreeGrown,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "allocatable and allocated both changed but net free capacity grew, requeue",
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
			name: "allocated capacity only grew while Available, cannot help the waiter, skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-grew"),
				oldObj:     availableAllocatedLow,
				newObj:     availableAllocatedHigh,
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
// their per-call cost bounds the scheduler's event-processing throughput.
// The owner-only path is the most expensive one: it parses the owner
// matchers of both the old and the new object.
func BenchmarkIsSchedulableAfterReservationChange_OwnerOnlyWaiter(b *testing.B) {
	pl := &Plugin{}
	owners := make([]schedulingv1alpha1.ReservationOwner, 0, 8)
	for i := 0; i < 8; i++ {
		owners = append(owners, schedulingv1alpha1.ReservationOwner{
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{fmt.Sprintf("app-%d", i): "demo"}},
		})
	}
	oldR := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-bench", UID: "r-bench", Generation: 1},
		Spec:       schedulingv1alpha1.ReservationSpec{Owners: owners},
		Status:     schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationAvailable, NodeName: "node-1"},
	}
	newR := oldR.DeepCopy()
	newR.Generation = 2
	waiter := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "w", Namespace: "default", UID: "w", Labels: map[string]string{"app-7": "demo"},
	}}
	logger := klog.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pl.isSchedulableAfterReservationChange(logger, waiter, oldR, newR)
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pl.isSchedulableAfterReservationChange(logger, waiter, r, r)
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

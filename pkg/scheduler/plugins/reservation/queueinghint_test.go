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
			name: "waiting pod is unrelated to reservation, skip",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w2"),
				oldObj:     makeReservePod("deleted-reserve"),
			},
			expectedHint: fwktype.QueueSkip,
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
			name: "waiting pod uses reservation, deleted pod is unrelated, skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w4"),
				oldObj:     makeWaitingPodNoReservation("deleted-normal"),
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "waiting pod uses reservation, deleted pod bound to a node but not tracked by cache, skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-cache-miss"),
				oldObj: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "bound-untracked", Namespace: "default", UID: "bound-untracked"},
					Spec:       corev1.PodSpec{NodeName: "node-x"},
				},
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

			got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), tt.args.waitingPod, tt.args.oldObj, nil)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}

// TestPlugin_QueueingHint_PodDeletion_AssignedViaCache covers the branch
// where the deleted pod is a regular (non-reserve) pod that was bound to
// a reservation tracked by reservationCache. Pod deletion in that state
// releases capacity from the reservation and must re-queue waiting pods
// that use reservations.
func TestPlugin_QueueingHint_PodDeletion_AssignedViaCache(t *testing.T) {
	suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
		args.EnableQueueHint = true
	})
	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	pl := p.(*Plugin)

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-cache", UID: "r-cache"},
	}
	assert.NoError(t, reservationutil.SetReservationAvailable(reservation, "node-1"))
	pl.reservationCache.updateReservation(reservation)

	assignedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "assigned", Namespace: "default", UID: "assigned-uid"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
	assert.NoError(t, pl.reservationCache.assumePod(reservation.UID, assignedPod))

	waiting := makeWaitingPodUsingReservation("waiting")
	got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), waiting, assignedPod, nil)
	assert.NoError(t, err)
	assert.Equal(t, fwktype.Queue, got)
}

// TestPlugin_QueueingHint_PodDeletion_OwnerMatchedWaiter covers waiters that
// carry no reservation-affinity annotation and can consume a reservation only
// via its owner selectors. Deleting a pod assigned to such a reservation
// releases capacity the waiter may now fit into, so the hint must requeue it.
func TestPlugin_QueueingHint_PodDeletion_OwnerMatchedWaiter(t *testing.T) {
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

	ownerMatchedWaiter := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "owner-matched", Namespace: "default", UID: "owner-matched",
			Labels: map[string]string{"app": "owner-match-demo"},
		},
	}
	got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), ownerMatchedWaiter, assignedPod, nil)
	assert.NoError(t, err)
	assert.Equal(t, fwktype.Queue, got, "owner-matched waiter must be requeued when reservation capacity is released")

	unrelatedWaiter := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "unrelated", Namespace: "default", UID: "unrelated",
			Labels: map[string]string{"app": "something-else"},
		},
	}
	got, err = pl.isSchedulableAfterPodDeletion(klog.Background(), unrelatedWaiter, assignedPod, nil)
	assert.NoError(t, err)
	assert.Equal(t, fwktype.QueueSkip, got, "waiter that matches neither affinity nor owners must stay skipped")
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
			name: "Update from Pending to Waiting is still not matchable, skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w5p"),
				oldObj:     pendingReservation,
				newObj:     waitingReservation,
			},
			expectedHint: fwktype.QueueSkip,
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
			name: "Update from Available to non-Available loses matchability for affinity waiters, skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w-lose"),
				oldObj:     availableReservation,
				newObj:     pendingReservation,
			},
			expectedHint: fwktype.QueueSkip,
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

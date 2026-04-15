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

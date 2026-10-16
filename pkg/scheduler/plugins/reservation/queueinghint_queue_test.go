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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const queueTestProfile = "koord-scheduler"

// reservationUpdateEvent is the ClusterEvent EventsToRegister attaches the
// hint to, resolved the same way it is there.
func reservationUpdateEvent() fwktype.ClusterEvent {
	gvk := fmt.Sprintf("reservations.%v.%v", schedulingv1alpha1.GroupVersion.Version, schedulingv1alpha1.GroupVersion.Group)
	return fwktype.ClusterEvent{Resource: fwktype.EventResource(gvk), ActionType: fwktype.Update}
}

// newQueueWithRejectedPods builds a real scheduling queue whose only hint is
// this plugin's Reservation/Update callback, then parks n pods in the
// unschedulable pool as rejected by this plugin, which is the state the queue
// consults the hint from.
func newQueueWithRejectedPods(t testing.TB, pl *Plugin, n int) *queue.PriorityQueue {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := klog.Background()
	hints := queue.QueueingHintMapPerProfile{queueTestProfile: {
		reservationUpdateEvent(): {{PluginName: Name, QueueingHintFn: pl.isSchedulableAfterReservationChange}},
	}}
	q := queue.NewTestQueue(ctx, (&queuesort.PrioritySort{}).Less, queue.WithQueueingHintMapPerProfile(hints))
	t.Cleanup(q.Close)
	for i := range n {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("w-%d", i), Namespace: "default", UID: types.UID(fmt.Sprintf("w-%d", i))},
			Spec:       corev1.PodSpec{SchedulerName: queueTestProfile},
		}
		q.Add(logger, pod)
		popped, err := q.Pop(logger)
		require.NoError(t, err)
		popped.UnschedulablePlugins = sets.New(Name)
		require.NoError(t, q.AddUnschedulableIfNotPresent(logger, popped, q.SchedulingCycle()))
	}
	require.Len(t, q.UnschedulablePods(), n)
	return q
}

// The queue runs the hint once per rejected pod with the same two event
// objects. This drives that loop for real and checks both verdicts and that a
// single decode served the whole event.
func TestQueueingHintThroughSchedulingQueue(t *testing.T) {
	logger := klog.Background()
	old, cur := costHeartbeat(benchLabels, benchAnnotations, benchOwners)

	t.Run("a heartbeat leaves every rejected pod where it is", func(t *testing.T) {
		pl := &Plugin{}
		q := newQueueWithRejectedPods(t, pl, 200)
		eo, en := toUnstructured(t, old), toUnstructured(t, cur)
		q.MoveAllToActiveOrBackoffQueue(logger, reservationUpdateEvent(), eo, en, nil)
		assert.Len(t, q.UnschedulablePods(), 200)
		assert.Empty(t, q.PodsInBackoffQ())
		assert.Same(t, en, pl.hintDecoded.newU, "the event objects are what the cache holds")
	})

	t.Run("a generation bump requeues every rejected pod", func(t *testing.T) {
		pl := &Plugin{}
		q := newQueueWithRejectedPods(t, pl, 200)
		bumped := cur.DeepCopy()
		bumped.Generation++
		bumped.ResourceVersion = "102"
		q.MoveAllToActiveOrBackoffQueue(logger, reservationUpdateEvent(), toUnstructured(t, old), toUnstructured(t, bumped), nil)
		assert.Empty(t, q.UnschedulablePods())
		assert.Len(t, q.PodsInBackoffQ(), 200)
	})

	t.Run("a delete requeues without decoding", func(t *testing.T) {
		pl := &Plugin{}
		q := newQueueWithRejectedPods(t, pl, 50)
		deleteEvent := reservationUpdateEvent()
		deleteEvent.ActionType = fwktype.Delete
		hints := queue.QueueingHintMapPerProfile{queueTestProfile: {
			deleteEvent: {{PluginName: Name, QueueingHintFn: pl.isSchedulableAfterReservationChange}},
		}}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		q2 := queue.NewTestQueue(ctx, (&queuesort.PrioritySort{}).Less, queue.WithQueueingHintMapPerProfile(hints))
		t.Cleanup(q2.Close)
		for _, p := range q.UnschedulablePods() {
			q2.Add(logger, p)
			popped, err := q2.Pop(logger)
			require.NoError(t, err)
			popped.UnschedulablePlugins = sets.New(Name)
			require.NoError(t, q2.AddUnschedulableIfNotPresent(logger, popped, q2.SchedulingCycle()))
		}
		q2.MoveAllToActiveOrBackoffQueue(logger, deleteEvent, toUnstructured(t, old), nil, nil)
		assert.Empty(t, q2.UnschedulablePods())
		assert.Nil(t, pl.hintDecoded.newU, "nothing was decoded for a delete")
	})
}

// One heartbeat through the real queue against 1000 rejected pods: the hint
// plus the queue's own per-pod work, which is the cost saintube asked about.
func BenchmarkQueueingHintThroughSchedulingQueue_OneEvent1000Pods(b *testing.B) {
	logger := klog.Background()
	pl := &Plugin{}
	q := newQueueWithRejectedPods(b, pl, 1000)
	events := benchEvents(b, 64)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		e := events[i%len(events)]
		i++
		q.MoveAllToActiveOrBackoffQueue(logger, reservationUpdateEvent(), e[0], e[1], nil)
	}
	if got := len(q.UnschedulablePods()); got != 1000 {
		b.Fatalf("heartbeats must not move pods, %d left", got)
	}
}

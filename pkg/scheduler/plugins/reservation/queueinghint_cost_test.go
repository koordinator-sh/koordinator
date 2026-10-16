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
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// costReservation is an Available reservation of the size the hint has to
// compare in practice: labels, JSON annotations, assigned owners and four
// resource names on both allocatable and allocated.
func costReservation(labels, annotations, owners int) *schedulingv1alpha1.Reservation {
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r", UID: "r-uid", Generation: 3, ResourceVersion: "100",
			Labels: map[string]string{}, Annotations: map[string]string{}},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationAvailable, NodeName: "node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("32"), corev1.ResourceMemory: resource.MustParse("128Gi"),
				"nvidia.com/gpu": resource.MustParse("8"), "koordinator.sh/gpu-memory": resource.MustParse("640Gi"),
			},
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("12"), corev1.ResourceMemory: resource.MustParse("48Gi"),
				"nvidia.com/gpu": resource.MustParse("3"), "koordinator.sh/gpu-memory": resource.MustParse("240Gi"),
			},
		},
	}
	for i := range labels {
		r.Labels[fmt.Sprintf("label-%d", i)] = fmt.Sprintf("value-%d", i)
	}
	for i := range annotations {
		r.Annotations[fmt.Sprintf("koordinator.sh/annotation-%d", i)] = fmt.Sprintf(`{"k":%d,"v":"payload-%d"}`, i, i)
	}
	for i := range owners {
		r.Status.CurrentOwners = append(r.Status.CurrentOwners, corev1.ObjectReference{
			Kind: "Pod", Namespace: "default", Name: fmt.Sprintf("pod-%d", i), UID: types.UID(fmt.Sprintf("uid-%d", i)),
		})
	}
	return r
}

// costHeartbeat is the update this scheduler writes after every failed
// attempt: a new resourceVersion and a condition, nothing a Filter reads.
// Old and new are distinct objects, as they are from the informer.
func costHeartbeat(labels, annotations, owners int) (*schedulingv1alpha1.Reservation, *schedulingv1alpha1.Reservation) {
	old := costReservation(labels, annotations, owners)
	cur := old.DeepCopy()
	cur.ResourceVersion = "101"
	cur.Status.Conditions = []schedulingv1alpha1.ReservationCondition{{
		Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusFalse,
		LastProbeTime: metav1.Now(),
	}}
	return old, cur
}

func toUnstructured(t testing.TB, r *schedulingv1alpha1.Reservation) *unstructured.Unstructured {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(r)
	require.NoError(t, err)
	return &unstructured.Unstructured{Object: m}
}

// The hint compares labels, annotations, owners and quantities with typed
// helpers instead of apiequality.Semantic.DeepEqual. These pin that the two
// agree on every edge that matters, so the swap cannot change a verdict.
func TestQueueingHintTypedComparesMatchSemantic(t *testing.T) {
	t.Run("maps.Equal on labels and annotations", func(t *testing.T) {
		for _, c := range []struct{ a, b map[string]string }{
			{nil, nil}, {nil, map[string]string{}}, {map[string]string{}, nil},
			{map[string]string{"a": "1"}, map[string]string{"a": "1"}},
			{map[string]string{"a": "1"}, map[string]string{"a": "2"}},
			{map[string]string{"a": "1"}, map[string]string{"b": "1"}},
			{map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}},
			{map[string]string{"a": ""}, map[string]string{}},
		} {
			assert.Equal(t, apiequality.Semantic.DeepEqual(c.a, c.b), maps.Equal(c.a, c.b), "%v vs %v", c.a, c.b)
		}
	})
	t.Run("slices.Equal on CurrentOwners", func(t *testing.T) {
		x := corev1.ObjectReference{Kind: "Pod", Name: "a", UID: "u1"}
		y := corev1.ObjectReference{Kind: "Pod", Name: "b", UID: "u2"}
		for _, c := range []struct{ a, b []corev1.ObjectReference }{
			{nil, nil}, {nil, []corev1.ObjectReference{}}, {[]corev1.ObjectReference{}, nil},
			{[]corev1.ObjectReference{x, y}, []corev1.ObjectReference{x, y}},
			{[]corev1.ObjectReference{x, y}, []corev1.ObjectReference{y, x}},
			{[]corev1.ObjectReference{x, y}, []corev1.ObjectReference{x}},
			{[]corev1.ObjectReference{x}, []corev1.ObjectReference{{Kind: "Pod", Name: "a", UID: "other"}}},
		} {
			assert.Equal(t, apiequality.Semantic.DeepEqual(c.a, c.b), slices.Equal(c.a, c.b), "%v vs %v", c.a, c.b)
		}
	})
	t.Run("quotav1.Equals on ResourceList", func(t *testing.T) {
		q := resource.MustParse
		for _, c := range []struct{ a, b corev1.ResourceList }{
			{nil, nil}, {nil, corev1.ResourceList{}},
			{corev1.ResourceList{"cpu": q("1")}, corev1.ResourceList{"cpu": q("1")}},
			{corev1.ResourceList{"cpu": q("1000m")}, corev1.ResourceList{"cpu": q("1")}},
			{corev1.ResourceList{"cpu": q("1")}, corev1.ResourceList{"cpu": q("2")}},
			{corev1.ResourceList{"cpu": q("1")}, corev1.ResourceList{"memory": q("1")}},
			{corev1.ResourceList{"cpu": q("1")}, corev1.ResourceList{"cpu": q("1"), "memory": q("1Gi")}},
			{corev1.ResourceList{"cpu": q("0")}, corev1.ResourceList{}},
			{corev1.ResourceList{"cpu": {}}, corev1.ResourceList{"cpu": q("0")}},
		} {
			assert.Equal(t, apiequality.Semantic.DeepEqual(c.a, c.b), quotav1.Equals(c.a, c.b), "%v vs %v", c.a, c.b)
		}
	})
}

func TestQueueingHintDecodeCache(t *testing.T) {
	logger := klog.Background()
	waiter := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default"}}

	t.Run("a later revision is not served from an earlier decode", func(t *testing.T) {
		pl := &Plugin{}
		old, cur := costHeartbeat(3, 2, 2)
		eo, en := toUnstructured(t, old), toUnstructured(t, cur)
		hint, _ := pl.isSchedulableAfterReservationChange(logger, waiter, eo, en)
		require.Equal(t, fwktype.QueueSkip, hint)

		bumped := cur.DeepCopy()
		bumped.Generation++
		bumped.ResourceVersion = "102"
		hint, _ = pl.isSchedulableAfterReservationChange(logger, waiter, eo, toUnstructured(t, bumped))
		assert.Equal(t, fwktype.Queue, hint)
	})

	// The informer never rewrites an object in place, but the key must not
	// depend on that: the same address with a new resourceVersion is a miss.
	t.Run("the key includes resourceVersion", func(t *testing.T) {
		pl := &Plugin{}
		old, cur := costHeartbeat(1, 1, 1)
		eo, en := toUnstructured(t, old), toUnstructured(t, cur)
		hint, _ := pl.isSchedulableAfterReservationChange(logger, waiter, eo, en)
		require.Equal(t, fwktype.QueueSkip, hint)

		bumped := cur.DeepCopy()
		bumped.Generation++
		bumped.ResourceVersion = "102"
		en.Object = toUnstructured(t, bumped).Object
		hint, _ = pl.isSchedulableAfterReservationChange(logger, waiter, eo, en)
		assert.Equal(t, fwktype.Queue, hint)
	})

	t.Run("a decode error is not cached", func(t *testing.T) {
		pl := &Plugin{}
		bad := &unstructured.Unstructured{Object: map[string]interface{}{"spec": "not-an-object", "metadata": map[string]interface{}{"resourceVersion": "1"}}}
		hint, _ := pl.isSchedulableAfterReservationChange(logger, waiter, bad, toUnstructured(t, costReservation(0, 0, 0)))
		assert.Equal(t, fwktype.Queue, hint)
		assert.Nil(t, pl.hintDecoded.newR)
	})

	t.Run("an object without a resourceVersion is not cached", func(t *testing.T) {
		pl := &Plugin{}
		old, cur := costHeartbeat(0, 0, 0)
		cur.ResourceVersion = ""
		hint, _ := pl.isSchedulableAfterReservationChange(logger, waiter, toUnstructured(t, old), toUnstructured(t, cur))
		assert.Equal(t, fwktype.QueueSkip, hint)
		assert.Nil(t, pl.hintDecoded.newR)
	})

	t.Run("concurrent callers", func(t *testing.T) {
		pl := &Plugin{}
		old, cur := costHeartbeat(5, 5, 5)
		eo, en := toUnstructured(t, old), toUnstructured(t, cur)
		bumped := cur.DeepCopy()
		bumped.Generation++
		bumped.ResourceVersion = "102"
		en2 := toUnstructured(t, bumped)
		var wg sync.WaitGroup
		for i := range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 100 {
					if i%2 == 0 {
						h, _ := pl.isSchedulableAfterReservationChange(logger, waiter, eo, en)
						assert.Equal(t, fwktype.QueueSkip, h)
					} else {
						h, _ := pl.isSchedulableAfterReservationChange(logger, waiter, eo, en2)
						assert.Equal(t, fwktype.Queue, h)
					}
				}
			}()
		}
		wg.Wait()
	})
}

// Benchmarks use distinct old and new objects and a realistic size; a shared
// pointer short-circuits DeepEqual and an empty object hides the map work.
const benchLabels, benchAnnotations, benchOwners = 10, 5, 10

func benchWaiter() *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default", UID: "w"}}
}

func BenchmarkIsSchedulableAfterReservationChange_GenerationBump(b *testing.B) {
	pl := &Plugin{}
	old, cur := costHeartbeat(benchLabels, benchAnnotations, benchOwners)
	cur.Generation++
	waiter, logger := benchWaiter(), klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		if hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, old, cur); err != nil || hint != fwktype.Queue {
			b.Fatalf("hint=%v err=%v", hint, err)
		}
	}
}

// The typed compare block on its own: what every call after the first for an
// event costs once the decode is cached.
func BenchmarkIsSchedulableAfterReservationChange_StatusHeartbeat(b *testing.B) {
	pl := &Plugin{}
	old, cur := costHeartbeat(benchLabels, benchAnnotations, benchOwners)
	waiter, logger := benchWaiter(), klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		if hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, old, cur); err != nil || hint != fwktype.QueueSkip {
			b.Fatalf("hint=%v err=%v", hint, err)
		}
	}
}

// benchEvents builds n heartbeat pairs with distinct resourceVersions, so a
// benchmark that cycles through them misses the decode cache on every call
// without paying for ToUnstructured inside the timed loop.
func benchEvents(b *testing.B, n int) [][2]*unstructured.Unstructured {
	events := make([][2]*unstructured.Unstructured, n)
	for i := range events {
		old, cur := costHeartbeat(benchLabels, benchAnnotations, benchOwners)
		old.ResourceVersion, cur.ResourceVersion = fmt.Sprintf("%d", 2*i), fmt.Sprintf("%d", 2*i+1)
		events[i] = [2]*unstructured.Unstructured{toUnstructured(b, old), toUnstructured(b, cur)}
	}
	return events
}

// The first call for an event: FromUnstructured on both objects.
func BenchmarkIsSchedulableAfterReservationChange_UnstructuredHeartbeatDecode(b *testing.B) {
	pl := &Plugin{}
	events := benchEvents(b, 256)
	waiter, logger := benchWaiter(), klog.Background()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		e := events[i%len(events)]
		i++
		if hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, e[0], e[1]); err != nil || hint != fwktype.QueueSkip {
			b.Fatalf("hint=%v err=%v", hint, err)
		}
	}
}

// Every later call for the same event: the cache lookup plus the compare.
func BenchmarkIsSchedulableAfterReservationChange_UnstructuredHeartbeatCached(b *testing.B) {
	pl := &Plugin{}
	old, cur := costHeartbeat(benchLabels, benchAnnotations, benchOwners)
	eo, en := toUnstructured(b, old), toUnstructured(b, cur)
	waiter, logger := benchWaiter(), klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		if hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, eo, en); err != nil || hint != fwktype.QueueSkip {
			b.Fatalf("hint=%v err=%v", hint, err)
		}
	}
}

func BenchmarkIsSchedulableAfterReservationChange_UnstructuredAdd(b *testing.B) {
	pl := &Plugin{}
	r := costReservation(benchLabels, benchAnnotations, benchOwners)
	waiter, logger := benchWaiter(), klog.Background()
	b.ReportAllocs()
	for b.Loop() {
		if hint, err := pl.isSchedulableAfterReservationChange(logger, waiter, nil, toUnstructured(b, r)); err != nil || hint != fwktype.Queue {
			b.Fatalf("hint=%v err=%v", hint, err)
		}
	}
}

// One heartbeat against 1000 pods this plugin rejected, which is how the
// scheduling queue drives the hint: the same event objects on every call.
func BenchmarkIsSchedulableAfterReservationChange_OneEvent1000Pods(b *testing.B) {
	pl := &Plugin{}
	waiters := make([]*corev1.Pod, 1000)
	for i := range waiters {
		waiters[i] = &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("w-%d", i), Namespace: "default"}}
	}
	events := benchEvents(b, 64)
	logger := klog.Background()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		e := events[i%len(events)]
		i++
		for _, w := range waiters {
			if hint, _ := pl.isSchedulableAfterReservationChange(logger, w, e[0], e[1]); hint != fwktype.QueueSkip {
				b.Fatal("expected QueueSkip")
			}
		}
	}
}

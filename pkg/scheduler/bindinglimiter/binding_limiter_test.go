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

package bindinglimiter

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// fakeClass is the EquivalenceClass under test. Real workload classes declare membership their own
// way; this one groups the pods carrying the fake equivalence class labels.
var fakeClass = frameworkext.NewFakeEquivalenceClass()

// makeClassPod builds a pod of the fake class. An empty key yields a marked pod the class does not
// handle.
func makeClassPod(name, key string) *corev1.Pod {
	return fakeClass.MakePod(name, key)
}

func TestBindingLimiterHandles(t *testing.T) {
	reserveClassPod := makeClassPod("reserve", "key-a")
	reserveClassPod.Annotations = map[string]string{reservationutil.AnnotationReservePod: "true"}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "nil pod is not handled",
			pod:  nil,
			want: false,
		},
		{
			name: "pod outside the class is not handled",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ordinary", UID: "ordinary"}},
			want: false,
		},
		{
			name: "class pod with a key is handled",
			pod:  makeClassPod("member", "key-a"),
			want: true,
		},
		{
			name: "class pod without a key is not handled",
			pod:  makeClassPod("member-no-key", ""),
			want: false,
		},
		{
			name: "reserve pod is never handled even when it carries class labels",
			pod:  reserveClassPod,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewBindingLimiter(1, fakeClass)
			assert.Equal(t, tt.want, l.Handles(tt.pod))
		})
	}
}

func TestBindingLimiterDefaultsOnInvalidCapacity(t *testing.T) {
	l := NewBindingLimiter(0, fakeClass)
	assert.Equal(t, DefaultMaxConcurrentBindings, cap(l.slots))
}

func TestBindingLimiterAcquireReleaseHoldsSingleSlot(t *testing.T) {
	l := NewBindingLimiter(2, fakeClass)
	pod := makeClassPod("a", "key-a")

	require.NoError(t, l.Acquire(context.Background(), pod))
	assert.Len(t, l.slots, 1, "one slot must be taken after Acquire")
	assert.Len(t, l.held, 1, "the pod must be tracked as holding a slot")

	l.Release(pod)
	assert.Len(t, l.slots, 0, "the slot must be returned after Release")
	assert.Len(t, l.held, 0, "the pod must no longer be tracked")
}

func TestBindingLimiterNilPodIsNoOp(t *testing.T) {
	l := NewBindingLimiter(1, fakeClass)
	require.NoError(t, l.Acquire(context.Background(), nil))
	assert.Len(t, l.slots, 0, "Acquire(nil) must not take a slot")
	// Release(nil) must not touch the semaphore or panic.
	l.Release(nil)
	assert.Len(t, l.slots, 0)
}

func TestBindingLimiterContextCancellationDoesNotHoldLease(t *testing.T) {
	l := NewBindingLimiter(1, fakeClass)
	held := makeClassPod("held", "key-a")
	require.NoError(t, l.Acquire(context.Background(), held))
	assert.Len(t, l.slots, 1)

	// The single slot is taken, so an Acquire with an already-cancelled context must fail without
	// taking a slot or recording the pod as holding one.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blocked := makeClassPod("blocked", "key-a")
	err := l.Acquire(ctx, blocked)
	require.ErrorIs(t, err, context.Canceled)
	assert.Len(t, l.slots, 1, "a cancelled Acquire must not take a slot")
	assert.Len(t, l.held, 1, "a cancelled Acquire must not record the pod")
	l.mu.Lock()
	_, tracked := l.held[blocked.UID]
	l.mu.Unlock()
	assert.False(t, tracked, "the cancelled pod must not hold a lease")

	// Releasing the original holder frees the slot for reuse.
	l.Release(held)
	assert.Len(t, l.slots, 0)
	require.NoError(t, l.Acquire(context.Background(), blocked))
	assert.Len(t, l.slots, 1)
	l.Release(blocked)
	assert.Len(t, l.slots, 0)
}

func TestBindingLimiterCancelledContextWithFreeSlot(t *testing.T) {
	l := NewBindingLimiter(1, fakeClass)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		pod := makeClassPod(fmt.Sprintf("cancelled-%d", i), "key-a")
		err := l.Acquire(ctx, pod)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, l.slots)
		l.Release(pod)
	}
}

func TestBindingLimiterDuplicateAcquireReleaseSameUID(t *testing.T) {
	l := NewBindingLimiter(1, fakeClass)
	pod := makeClassPod("dup", "key-a")

	// A duplicate Acquire for the same UID must be a no-op and never take a second slot, so it does
	// not block against the capacity-one semaphore.
	require.NoError(t, l.Acquire(context.Background(), pod))
	require.NoError(t, l.Acquire(context.Background(), pod))
	assert.Len(t, l.slots, 1, "duplicate Acquire must not take a second slot")
	assert.Len(t, l.held, 1)

	// The first Release returns the slot; the duplicate Release is a no-op and must not underflow the
	// semaphore.
	l.Release(pod)
	l.Release(pod)
	assert.Len(t, l.slots, 0, "duplicate Release must not underflow the semaphore")
	assert.Len(t, l.held, 0)
}

func TestBindingLimiterCapacityBlocksAndUnblocks(t *testing.T) {
	l := NewBindingLimiter(1, fakeClass)
	first := makeClassPod("first", "key-a")
	second := makeClassPod("second", "key-a")

	require.NoError(t, l.Acquire(context.Background(), first))

	done := make(chan error, 1)
	go func() {
		done <- l.Acquire(context.Background(), second)
	}()

	// The second Acquire must block while the only slot is taken.
	select {
	case <-done:
		t.Fatal("Acquire should block while the semaphore is full")
	case <-time.After(100 * time.Millisecond):
	}

	// Releasing the holder must unblock the waiter.
	l.Release(first)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire should unblock after Release")
	}
	assert.Len(t, l.slots, 1, "the unblocked Acquire now holds the slot")
	assert.Len(t, l.held, 1)

	l.Release(second)
	assert.Len(t, l.slots, 0)
	assert.Len(t, l.held, 0)
}

// TestBindingLimiterConcurrentDistinctPods stresses the limiter with more concurrent binders than
// capacity to prove that Acquire/Release never leaks a slot or underflows the semaphore. Run with
// -race to catch data races on the held set.
func TestBindingLimiterConcurrentDistinctPods(t *testing.T) {
	const capacity = 4
	const pods = 64
	l := NewBindingLimiter(capacity, fakeClass)

	var wg sync.WaitGroup
	errCh := make(chan error, pods)
	for i := 0; i < pods; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pod := makeClassPod(fmt.Sprintf("p-%d", i), "key-a")
			pod.UID = types.UID(fmt.Sprintf("uid-%d", i))
			if err := l.Acquire(context.Background(), pod); err != nil {
				errCh <- err
				return
			}
			l.Release(pod)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	assert.Len(t, l.slots, 0, "no slot must leak after all binders finish")
	assert.Len(t, l.held, 0, "no pod must remain tracked after all binders finish")
}

// TestBindingLimiterConcurrentSameUID exercises the branch where multiple concurrent Acquire calls
// for the same UID each take a slot but only one records the lease and the others return the extra
// slot, so exactly one slot ends up held.
func TestBindingLimiterConcurrentSameUID(t *testing.T) {
	const capacity = 8
	const goroutines = 8
	l := NewBindingLimiter(capacity, fakeClass)
	pod := makeClassPod("same", "key-a")

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := l.Acquire(context.Background(), pod); err != nil {
				errCh <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	assert.Len(t, l.slots, 1, "concurrent Acquire of the same UID must hold exactly one slot")
	assert.Len(t, l.held, 1)

	l.Release(pod)
	assert.Len(t, l.slots, 0)
	assert.Len(t, l.held, 0)
}

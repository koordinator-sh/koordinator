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

package sandbox

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

	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestBindingLimiterHandles(t *testing.T) {
	reserveSandboxPod := makeSandboxPod("reserve", "hash-a")
	reserveSandboxPod.Annotations = map[string]string{reservationutil.AnnotationReservePod: "true"}

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
			name: "ordinary pod is not handled",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ordinary", UID: "ordinary"}},
			want: false,
		},
		{
			name: "sandbox pod with template hash is handled",
			pod:  makeSandboxPod("sandbox", "hash-a"),
			want: true,
		},
		{
			name: "sandbox pod without template hash is not handled",
			pod:  makeSandboxPod("sandbox-no-hash", ""),
			want: false,
		},
		{
			name: "reserve sandbox pod is not handled",
			pod:  reserveSandboxPod,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newBindingLimiter(1)
			assert.Equal(t, tt.want, l.Handles(tt.pod))
		})
	}
}

func TestBindingLimiterAcquireReleaseHoldsSingleSlot(t *testing.T) {
	l := newBindingLimiter(2)
	pod := makeSandboxPod("a", "hash-a")

	require.NoError(t, l.Acquire(context.Background(), pod))
	assert.Len(t, l.slots, 1, "one slot must be taken after Acquire")
	assert.Len(t, l.held, 1, "the pod must be tracked as holding a slot")

	l.Release(pod)
	assert.Len(t, l.slots, 0, "the slot must be returned after Release")
	assert.Len(t, l.held, 0, "the pod must no longer be tracked")
}

func TestBindingLimiterNilPodIsNoOp(t *testing.T) {
	l := newBindingLimiter(1)
	require.NoError(t, l.Acquire(context.Background(), nil))
	assert.Len(t, l.slots, 0, "Acquire(nil) must not take a slot")
	// Release(nil) must not touch the semaphore or panic.
	l.Release(nil)
	assert.Len(t, l.slots, 0)
}

func TestBindingLimiterContextCancellationDoesNotHoldLease(t *testing.T) {
	l := newBindingLimiter(1)
	held := makeSandboxPod("held", "hash-a")
	require.NoError(t, l.Acquire(context.Background(), held))
	assert.Len(t, l.slots, 1)

	// The single slot is taken, so an Acquire with an already-cancelled context must fail without
	// taking a slot or recording the pod as holding one.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blocked := makeSandboxPod("blocked", "hash-a")
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
	l := newBindingLimiter(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		pod := makeSandboxPod(fmt.Sprintf("cancelled-%d", i), "hash-a")
		err := l.Acquire(ctx, pod)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, l.slots)
		l.Release(pod)
	}
}

func TestBindingLimiterDuplicateAcquireReleaseSameUID(t *testing.T) {
	l := newBindingLimiter(1)
	pod := makeSandboxPod("dup", "hash-a")

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
	l := newBindingLimiter(1)
	first := makeSandboxPod("first", "hash-a")
	second := makeSandboxPod("second", "hash-a")

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
	l := newBindingLimiter(capacity)

	var wg sync.WaitGroup
	for i := 0; i < pods; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pod := makeSandboxPod(fmt.Sprintf("p-%d", i), "hash-a")
			pod.UID = types.UID(fmt.Sprintf("uid-%d", i))
			require.NoError(t, l.Acquire(context.Background(), pod))
			l.Release(pod)
		}(i)
	}
	wg.Wait()

	assert.Len(t, l.slots, 0, "no slot must leak after all binders finish")
	assert.Len(t, l.held, 0, "no pod must remain tracked after all binders finish")
}

// TestBindingLimiterConcurrentSameUID exercises the branch where multiple concurrent Acquire calls
// for the same UID each take a slot but only one records the lease and the others return the extra
// slot, so exactly one slot ends up held.
func TestBindingLimiterConcurrentSameUID(t *testing.T) {
	const capacity = 8
	const goroutines = 8
	l := newBindingLimiter(capacity)
	pod := makeSandboxPod("same", "hash-a")

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			require.NoError(t, l.Acquire(context.Background(), pod))
		}()
	}
	close(start)
	wg.Wait()

	assert.Len(t, l.slots, 1, "concurrent Acquire of the same UID must hold exactly one slot")
	assert.Len(t, l.held, 1)

	l.Release(pod)
	assert.Len(t, l.slots, 0)
	assert.Len(t, l.held, 0)
}

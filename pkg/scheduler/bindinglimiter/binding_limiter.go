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

// Package bindinglimiter bounds the number of concurrent binding cycles a scheduler runs, so a
// burst of pods cannot turn into a bind storm against the API server. It is independent of any
// workload class: the caller supplies the frameworkext.EquivalenceClass whose pods are bounded.
package bindinglimiter

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	schedulerframeworkext "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var _ schedulerframeworkext.BindingLimiter = &BindingLimiter{}

// DefaultMaxConcurrentBindings is the binding concurrency bound used when no explicit limit is
// configured.
const DefaultMaxConcurrentBindings = 1024

// BindingLimiter bounds the number of concurrent binding cycles for the pods of one
// EquivalenceClass. A single instance is registered as a frameworkext.BindingLimiter on every
// scheduler profile, so all profiles share one semaphore. Slots are keyed by Pod UID and tracked in
// a mutex-protected held set so acquire/release are idempotent across the binding lifecycle.
//
// Concurrency model: distinct UIDs are acquired and released fully concurrently. For any single UID
// the scheduler drives one binding cycle in one goroutine, so Acquire (RunPreBindPlugins) strictly
// precedes Release (RunPostBindPlugins / RunReservePluginsUnreserve); the limiter never sees a
// concurrent Acquire and Release for the same UID. The held set is only ever mutated under mu; the
// semaphore channel is only touched outside mu, so a full semaphore never blocks a release.
type BindingLimiter struct {
	slots chan struct{}
	class schedulerframeworkext.EquivalenceClass

	mu   sync.Mutex
	held map[types.UID]struct{}
}

// NewBindingLimiter returns a limiter admitting at most maxConcurrentBindings binding cycles at a
// time for the pods that class.Handles reports. Reservation reserve pods are never bounded: they
// are bookkeeping stand-ins for a Reservation, not workload being placed. A non-positive limit falls
// back to DefaultMaxConcurrentBindings so the semaphore can never be created unbuffered.
func NewBindingLimiter(maxConcurrentBindings int, class schedulerframeworkext.EquivalenceClass) *BindingLimiter {
	if maxConcurrentBindings <= 0 {
		klog.Warningf("maxConcurrentBindings must be > 0 for BindingLimiter, use default: %d", DefaultMaxConcurrentBindings)
		maxConcurrentBindings = DefaultMaxConcurrentBindings
	}
	return &BindingLimiter{
		slots: make(chan struct{}, maxConcurrentBindings),
		class: class,
		held:  map[types.UID]struct{}{},
	}
}

// Handles reports whether the pod's binding concurrency is bounded by this limiter.
func (l *BindingLimiter) Handles(pod *corev1.Pod) bool {
	if pod == nil || reservationutil.IsReservePod(pod) {
		return false
	}
	return l.class.Handles(pod)
}

// Acquire blocks until a slot is available for the pod or ctx is done. It is a no-op when the pod
// already holds a slot, and returns ctx.Err() on cancellation without taking a slot.
func (l *BindingLimiter) Acquire(ctx context.Context, pod *corev1.Pod) error {
	if pod == nil {
		return nil
	}
	uid := pod.UID
	l.mu.Lock()
	if _, ok := l.held[uid]; ok {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	// The channel cannot be held under the lock: blocking on a full semaphore would serialize all
	// acquires and deadlock against concurrent releases.
	start := time.Now()
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Cancellation and an available slot can become ready together.
	if err := ctx.Err(); err != nil {
		<-l.slots
		return err
	}
	koordmetrics.RecordBindingSlotWaitDuration(pod.Spec.SchedulerName, time.Since(start))

	l.mu.Lock()
	if _, ok := l.held[uid]; ok {
		// A concurrent Acquire for the same UID already recorded the slot; return the extra one.
		l.mu.Unlock()
		<-l.slots
		return nil
	}
	l.held[uid] = struct{}{}
	l.mu.Unlock()
	return nil
}

// Release returns the slot held for the pod. It is a no-op when the pod holds no slot, so the
// overlapping release points of the binding lifecycle (PreBind failure, Unreserve, PostBind) are
// harmless.
func (l *BindingLimiter) Release(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	uid := pod.UID
	l.mu.Lock()
	if _, ok := l.held[uid]; !ok {
		l.mu.Unlock()
		return
	}
	delete(l.held, uid)
	l.mu.Unlock()
	<-l.slots
}

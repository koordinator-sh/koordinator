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

package framework

import (
	"context"
	"fmt"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// insufficientQuotaSubstring matches the FailedScheduling event message the
// ElasticQuota plugin emits when a pod is blocked by quota limits. Substring
// match because the recorded message is prefixed with "0/N nodes are
// available: ..." so an exact match would never work, and the suffix includes
// the quota name which varies per run.
// Note: the plugin also emits "Insufficient non-preemptible quotas, ..." from
// the same PreFilter path, which does NOT contain this substring and is
// therefore not counted here. This scenario does not set non-preemptible pods,
// so the current match is complete. A future scenario that does would need to
// extend this check.
const insufficientQuotaSubstring = "Insufficient quotas"

// FailureWatcher observes FailedScheduling events for a known set of pods
// and tracks how many distinct pods hit at least one scheduling failure,
// plus the total event count (a pod can fail-and-retry multiple times
// before it eventually lands or the run ends).
//
// Unlike Watcher, FailureWatcher has no natural end condition — scheduling
// failures can keep happening for the run's entire duration. The caller is
// expected to cancel the context passed to Start once the main pod watcher
// has returned, rather than waiting for FailureWatcher to finish on its own.
type FailureWatcher struct {
	client    kubernetes.Interface
	namespace string
	podNames  map[string]struct{}

	// ready is closed once the event watch stream is established.
	ready chan struct{}

	mu               sync.Mutex
	failedPods       map[string]int    // pod name → FailedScheduling event count (any reason)
	quotaBlockedPods map[string]struct{} // pod name set — membership only; only len() is used
}

// NewFailureWatcher creates a FailureWatcher scoped to namespace, tracking
// only events whose InvolvedObject.Name is in podNames. Events use
// InvolvedObject rather than a run-id label (Events don't carry the pod's
// labels), so the caller must supply the exact pod name set for this run.
func NewFailureWatcher(client kubernetes.Interface, namespace string, podNames []string) *FailureWatcher {
	set := make(map[string]struct{}, len(podNames))
	for _, name := range podNames {
		set[name] = struct{}{}
	}
	return &FailureWatcher{
		client:           client,
		namespace:        namespace,
		podNames:         set,
		ready:            make(chan struct{}),
		failedPods:       make(map[string]int),
		quotaBlockedPods: make(map[string]struct{}),
	}
}

// Ready returns a channel closed once the watch stream is established.
func (f *FailureWatcher) Ready() <-chan struct{} {
	return f.ready
}

// Start begins watching for FailedScheduling events. Blocks until ctx is
// cancelled or the watch channel closes. The caller must cancel ctx explicitly
// once scheduling is complete (see FailureWatcher doc comment).
func (f *FailureWatcher) Start(ctx context.Context) error {
	fieldSel := fields.Set{
		"involvedObject.kind": "Pod",
		"reason":              "FailedScheduling",
	}.AsSelector().String()

	watcher, err := f.client.CoreV1().Events(f.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fieldSel,
	})
	if err != nil {
		close(f.ready)
		return fmt.Errorf("failed to start FailedScheduling event watcher: %w", err)
	}
	defer watcher.Stop()
	close(f.ready)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("failure event watch channel closed unexpectedly")
			}
			if event.Type == watch.Error {
				return fmt.Errorf("failure event watch error: %v", event.Object)
			}

			ev, ok := event.Object.(*corev1.Event)
			if !ok {
				continue
			}
			if _, tracked := f.podNames[ev.InvolvedObject.Name]; !tracked {
				// Not one of this run's pods — field selector scopes by
				// reason/kind but not by run, so filter here.
				continue
			}

			f.mu.Lock()
			f.failedPods[ev.InvolvedObject.Name]++
			if isQuotaBlockedEvent(ev.Message) {
				f.quotaBlockedPods[ev.InvolvedObject.Name] = struct{}{}
			}
			f.mu.Unlock()
		}
	}
}

// Stats returns the number of distinct pods that received at least one
// FailedScheduling event, and the total event count across all pods.
// Safe to call after cancelling Start's context.
func (f *FailureWatcher) Stats() (failedPodCount, totalEvents int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, count := range f.failedPods {
		totalEvents += count
	}
	return len(f.failedPods), totalEvents
}

// QuotaBlockedPodCount returns the number of distinct pods that received at
// least one FailedScheduling event whose message contained
// insufficientQuotaSubstring. Safe to call after cancelling Start's context.
func (f *FailureWatcher) QuotaBlockedPodCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.quotaBlockedPods)
}

// isQuotaBlockedEvent reports whether the FailedScheduling event message
// indicates the pod was throttled by an ElasticQuota limit. Extracted as a
// named function so it can be unit-tested independently of the watch stream.
func isQuotaBlockedEvent(message string) bool {
	return strings.Contains(message, insufficientQuotaSubstring)
}

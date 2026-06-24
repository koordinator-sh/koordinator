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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// Watcher observes PodScheduled conditions and records per-pod latencies.
type Watcher struct {
	client    kubernetes.Interface
	namespace string
	runID     string
	podCount  int

	mu        sync.Mutex
	latencies []PodLatency
	// seen prevents double-counting the same pod. The watch stream can
	// deliver multiple Modified events for the same pod after it is bound;
	// without this guard scheduledCount would exceed podCount.
	seen map[string]struct{}
}

// NewWatcher creates a Watcher for pods labelled with runID in namespace.
func NewWatcher(client kubernetes.Interface, namespace, runID string, podCount int) *Watcher {
	return &Watcher{
		client:    client,
		namespace: namespace,
		runID:     runID,
		podCount:  podCount,
		seen:      make(map[string]struct{}),
	}
}

// Start begins watching for PodScheduled=True events.
// Blocks until all pods are scheduled, ctx is cancelled, or the channel closes.
// Call this before the pod burst begins.
func (w *Watcher) Start(ctx context.Context) error {
	labelSel := fmt.Sprintf("benchmark.koordinator.sh/run-id=%s", w.runID)

	watcher, err := w.client.CoreV1().Pods(w.namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: labelSel,
	})
	if err != nil {
		return fmt.Errorf("failed to start pod watcher: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed unexpectedly")
			}
			if event.Type != watch.Modified && event.Type != watch.Added {
				continue
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			for _, cond := range pod.Status.Conditions {
				if cond.Type != corev1.PodScheduled || cond.Status != corev1.ConditionTrue {
					continue
				}

				w.mu.Lock()
				if _, alreadySeen := w.seen[pod.Name]; alreadySeen {
					w.mu.Unlock()
					break
				}
				w.seen[pod.Name] = struct{}{}

				// Latency is measured from pod creation to scheduler decision.
				latency := cond.LastTransitionTime.Time.Sub(pod.CreationTimestamp.Time)
				w.latencies = append(w.latencies, PodLatency{
					PodName: pod.Name,
					Latency: latency,
				})
				done := len(w.latencies) >= w.podCount
				w.mu.Unlock()

				if done {
					return nil
				}
				break
			}
		}
	}
}

// Latencies returns all recorded per-pod latencies. Call after Start returns.
func (w *Watcher) Latencies() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make([]time.Duration, len(w.latencies))
	for i, l := range w.latencies {
		result[i] = l.Latency
	}
	return result
}

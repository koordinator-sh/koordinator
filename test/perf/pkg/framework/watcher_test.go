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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

// TestWatcher_StopsAtExpectedScheduled verifies that Watcher.Start returns as
// soon as expectedScheduled pods are observed, not when all podCount pods
// schedule — and that duplicate events for the same pod are not double-counted.
func TestWatcher_StopsAtExpectedScheduled(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	fakeWatch := watch.NewFake()
	fakeClient.PrependWatchReactor("pods", clienttesting.DefaultWatchReactor(fakeWatch, nil))

	// podCount=5, expectedScheduled=2: Start must return after pod-1 and pod-2.
	w := NewWatcher(fakeClient, "elasticquota-benchmark", "run-1", 5, 2)
	errCh := make(chan error, 1)
	go func() { errCh <- w.Start(context.Background()) }()
	<-w.Ready()

	scheduledPod := func(name string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				CreationTimestamp: metav1.Now(),
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				}},
			},
		}
	}

	fakeWatch.Add(scheduledPod("pod-1"))
	fakeWatch.Modify(scheduledPod("pod-1")) // duplicate — must not double-count
	fakeWatch.Add(scheduledPod("pod-2"))

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start() returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start() did not return after expectedScheduled was reached")
	}

	if got := len(w.Latencies()); got != 2 {
		t.Errorf("len(Latencies()) = %d, want 2", got)
	}
}

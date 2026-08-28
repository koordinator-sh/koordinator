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
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestIsQuotaBlockedEvent(t *testing.T) {
	cases := []struct {
		message string
		want    bool
	}{
		// Real format from Plugin.PreFilter / checkQuotaRecursive:
		// "Insufficient quotas, quotaName: %v, runtime: %v, used: %v, pod's request: %v, exceedDimensions: %v"
		{
			"Insufficient quotas, quotaName: elasticquota-benchmark, runtime: map[cpu:150], " +
				"used: map[cpu:100m], pod's request: map[cpu:500m], exceedDimensions: [cpu]",
			true,
		},
		{"Insufficient quotas", true}, // bare prefix also matches (substring check)
		{"0/100 nodes are available: insufficient cpu", false},
		{"Preemption is not helpful for scheduling", false},
		// non-preemptible variant does NOT match — intentional, see failure_watcher.go comment
		{"Insufficient non-preemptible quotas, quotaName: elasticquota-benchmark", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isQuotaBlockedEvent(c.message); got != c.want {
			t.Errorf("isQuotaBlockedEvent(%q) = %v, want %v", c.message, got, c.want)
		}
	}
}

// TestFailureWatcher_EventRouting exercises Start() end-to-end via a fake
// clientset: distinct-pod counting across repeated events for the same pod,
// a mix of quota/non-quota reasons, and events for pods outside this run
// being filtered out.
func TestFailureWatcher_EventRouting(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	fakeWatcher := watch.NewFake()
	fakeClient.PrependWatchReactor("events", func(action k8stesting.Action) (bool, watch.Interface, error) {
		return true, fakeWatcher, nil
	})

	fw := NewFailureWatcher(fakeClient, "elasticquota-benchmark", []string{"pod-a", "pod-b"})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- fw.Start(ctx) }()
	<-fw.Ready()

	emit := func(podName, message string) {
		fakeWatcher.Add(&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName + "-evt",
				Namespace: "elasticquota-benchmark",
			},
			InvolvedObject: corev1.ObjectReference{
				Name:      podName,
				Namespace: "elasticquota-benchmark",
			},
			Reason:  "FailedScheduling",
			Message: message,
		})
	}

	realMsg := "Insufficient quotas, quotaName: elasticquota-benchmark, runtime: map[cpu:150], " +
		"used: map[cpu:100m], pod's request: map[cpu:500m], exceedDimensions: [cpu]"
	emit("pod-a", realMsg)
	emit("pod-a", realMsg)                                       // repeat — distinct-pod count must not double
	emit("pod-b", "0/100 nodes are available: insufficient cpu") // non-quota FailedScheduling
	emit("pod-outside-run", realMsg)                             // not tracked by this watcher

	time.Sleep(50 * time.Millisecond) // let the goroutine drain the channel
	cancel()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() returned unexpected error: %v", err)
	}

	failedPodCount, failureEventCount := fw.Stats()
	if failedPodCount != 2 {
		t.Errorf("failedPodCount = %d, want 2 (pod-a and pod-b)", failedPodCount)
	}
	if failureEventCount != 3 {
		t.Errorf("failureEventCount = %d, want 3 (pod-a×2, pod-b×1)", failureEventCount)
	}
	if got := fw.QuotaBlockedPodCount(); got != 1 {
		t.Errorf("QuotaBlockedPodCount() = %d, want 1 (only pod-a)", got)
	}

	// FailedPodNames must return the same pod-name set as Stats' distinct-pod
	// count — used by accountedForCount to build a true |scheduled ∪ failed|
	// union rather than a sum that double-counts a pod that failed then scheduled.
	names := fw.FailedPodNames()
	if len(names) != 2 {
		t.Errorf("len(FailedPodNames()) = %d, want 2", len(names))
	}
	for _, want := range []string{"pod-a", "pod-b"} {
		if _, ok := names[want]; !ok {
			t.Errorf("FailedPodNames() missing %q", want)
		}
	}
	if _, ok := names["pod-outside-run"]; ok {
		t.Errorf("FailedPodNames() contains pod-outside-run — filtering failed")
	}
}

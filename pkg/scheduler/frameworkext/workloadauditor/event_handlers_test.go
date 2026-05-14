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

package workloadauditor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientfeatures "k8s.io/client-go/features"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
)

type eventHandlerMutableGates interface {
	clientfeatures.Gates
	Set(key clientfeatures.Feature, value bool) error
}

func init() {
	schedulermetrics.Register()
}

// withWatchListClient sets the WatchListClient feature gate for the duration of
// the current test and restores the previous value via t.Cleanup. This avoids
// mutating a process-wide client-go feature gate from init(), which would leak
// across tests and packages and cause hard-to-debug flakes.
func withWatchListClient(t testing.TB, value bool) {
	fg, ok := clientfeatures.FeatureGates().(eventHandlerMutableGates)
	if !ok {
		t.Skip("client-go feature gates are not mutable in this environment")
		return
	}
	prev := fg.Enabled(clientfeatures.WatchListClient)
	if err := fg.Set(clientfeatures.WatchListClient, value); err != nil {
		t.Fatalf("failed to set WatchListClient feature gate: %v", err)
	}
	t.Cleanup(func() {
		_ = fg.Set(clientfeatures.WatchListClient, prev)
	})
}

// mockWorkloadAuditor is a lightweight WorkloadAuditor used in event handler tests.
type mockWorkloadAuditor struct {
	enabled bool
	added   []*corev1.Pod
	deleted []*corev1.Pod
}

func newMockAuditor(enabled bool) *mockWorkloadAuditor {
	return &mockWorkloadAuditor{enabled: enabled}
}

func (m *mockWorkloadAuditor) Enabled() bool { return m.enabled }
func (m *mockWorkloadAuditor) AddPod(pod *corev1.Pod) {
	m.added = append(m.added, pod)
}
func (m *mockWorkloadAuditor) DeletePod(pod *corev1.Pod) {
	m.deleted = append(m.deleted, pod)
}
func (m *mockWorkloadAuditor) RecordPod(_ *corev1.Pod, _ RecordType, _ string)                 {}
func (m *mockWorkloadAuditor) RecordPodGating(_ *corev1.Pod, _ bool)                           {}
func (m *mockWorkloadAuditor) RecordAttemptPod(_ *corev1.Pod)                                  {}
func (m *mockWorkloadAuditor) RecordPodScheduleResult(_ *corev1.Pod, _ RecordType, _ string)   {}
func (m *mockWorkloadAuditor) AddGangGroup(_ string)                                           {}
func (m *mockWorkloadAuditor) DeleteGangGroup(_ string)                                        {}
func (m *mockWorkloadAuditor) RecordGangGroup(_ string, _ *corev1.Pod, _ RecordType, _ string) {}
func (m *mockWorkloadAuditor) RecordGangGating(_ string, _ *corev1.Pod, _ bool)                {}
func (m *mockWorkloadAuditor) RecordGangScheduleResult(_ string, _ RecordType, _ string)       {}
func (m *mockWorkloadAuditor) RecordDiagnosis(_ *corev1.Pod, _ string, _ RecordType, _ string) {}

// TestAddEventHandler_HandlerRegistrationAndSync verifies that AddEventHandler registers
// the pod handler via ForceSyncFromInformer (so a registration is collected) and that
// OnAdd events are delivered to the workloadAuditor after the informer starts.
func TestAddEventHandler_HandlerRegistrationAndSync(t *testing.T) {
	// Disable WatchListClient for this test (fake client compatibility) and
	// restore the previous value on cleanup to avoid leaking across tests.
	withWatchListClient(t, false)
	// Reset registrations to isolate from other tests.
	frameworkexthelper.ResetRegistrations()
	defer frameworkexthelper.ResetRegistrations()

	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordInformerFactory := koordinatorinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	// Construct a minimal Scheduler with a Profiles map so that HandlesSchedulerName works.
	sched := &scheduler.Scheduler{
		Profiles: profile.Map{
			corev1.DefaultSchedulerName: (framework.Framework)(nil),
		},
	}
	auditor := newMockAuditor(true)

	// Pre-create an unscheduled pod with default scheduler name; the filter inside
	// AddEventHandler accepts pods where NodeName=="" and schedulerName is handled.
	pendingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pending-pod",
			Namespace: "default",
			UID:       "uid-pending",
		},
		Spec: corev1.PodSpec{
			SchedulerName: corev1.DefaultSchedulerName,
			// NodeName intentionally empty: this pod passes the filter.
		},
	}
	_, err := fakeClientSet.CoreV1().Pods(pendingPod.Namespace).Create(context.TODO(), pendingPod, metav1.CreateOptions{})
	assert.NoError(t, err)

	// AddEventHandler calls ForceSyncFromInformer internally, which should collect registrations.
	AddEventHandler(sched, auditor, sharedInformerFactory, koordInformerFactory)

	// Verify that at least one registration was collected (the pod handler).
	regs := frameworkexthelper.GetRegistrations()
	assert.NotEmpty(t, regs, "expected at least one handler registration after AddEventHandler")

	// Start informers so the registered handler receives the initial list (OnAdd events).
	stopCh := make(chan struct{})
	defer close(stopCh)
	sharedInformerFactory.Start(stopCh)
	koordInformerFactory.Start(stopCh)
	sharedInformerFactory.WaitForCacheSync(stopCh)
	koordInformerFactory.WaitForCacheSync(stopCh)

	// Wait for all collected handler registrations to finish their initial list sync.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = frameworkexthelper.WaitForHandlersSync(ctx)
	assert.NoError(t, err, "handlers did not sync within timeout")

	// The pending pod (NodeName=="" and handled scheduler) must have been delivered via OnAdd.
	assert.Len(t, auditor.added, 1, "expected pending pod to be added to the auditor")
	if len(auditor.added) == 1 {
		assert.Equal(t, pendingPod.Name, auditor.added[0].Name)
	}
}

// TestAddEventHandler_DisabledAuditor verifies that AddEventHandler is a no-op when
// the auditor is disabled, so no registrations are collected.
func TestAddEventHandler_DisabledAuditor(t *testing.T) {
	withWatchListClient(t, false)
	frameworkexthelper.ResetRegistrations()
	defer frameworkexthelper.ResetRegistrations()

	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordInformerFactory := koordinatorinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	sched := &scheduler.Scheduler{
		Profiles: profile.Map{
			corev1.DefaultSchedulerName: (framework.Framework)(nil),
		},
	}
	// disabled auditor: AddEventHandler should return early without registering handlers.
	auditor := newMockAuditor(false)

	AddEventHandler(sched, auditor, sharedInformerFactory, koordInformerFactory)

	regs := frameworkexthelper.GetRegistrations()
	assert.Empty(t, regs, "expected no registrations when auditor is disabled")
}

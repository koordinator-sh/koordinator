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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2/ktesting"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
)

func newTestWorkflow(t *testing.T, ctx context.Context) *Workflow {
	t.Helper()
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
	}
	fwk, err := schedulertesting.NewFramework(ctx, registeredPlugins, "koord-scheduler",
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
	)
	assert.NoError(t, err)

	w := &Workflow{
		sched: &scheduler.Scheduler{
			Cache: cache.New(ctx, 30*time.Second, nil),
			Profiles: profile.Map{
				"koord-scheduler": fwk,
			},
		},
	}
	return w
}

func TestFrameworkForPod(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkflow(t, ctx)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p1"},
		Spec:       corev1.PodSpec{SchedulerName: "koord-scheduler"},
	}
	fwk, err := w.frameworkForPod(pod)
	assert.NoError(t, err)
	assert.NotNil(t, fwk)
	assert.Equal(t, "koord-scheduler", fwk.ProfileName())

	podUnknown := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p2"},
		Spec:       corev1.PodSpec{SchedulerName: "unknown-scheduler"},
	}
	_, err = w.frameworkForPod(podUnknown)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown-scheduler")
}

func TestSkipPodSchedule(t *testing.T) {
	ctx := context.Background()
	logger, _ := ktesting.NewTestContext(t)

	tests := []struct {
		name     string
		pod      *corev1.Pod
		assume   bool
		expected bool
	}{
		{
			name: "pod being deleted is skipped",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "ns",
					Name:              "p1",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
			},
			expected: true,
		},
		{
			name: "assumed pod is skipped",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p2", UID: "uid-p2"},
				Spec:       corev1.PodSpec{SchedulerName: "koord-scheduler"},
			},
			assume:   true,
			expected: true,
		},
		{
			name: "normal pending pod is not skipped",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p3", UID: "uid-p3"},
				Spec:       corev1.PodSpec{SchedulerName: "koord-scheduler"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newTestWorkflow(t, ctx)
			fwk, err := w.frameworkForPod(&corev1.Pod{Spec: corev1.PodSpec{SchedulerName: "koord-scheduler"}})
			assert.NoError(t, err)

			if tt.assume {
				assumedPod := tt.pod.DeepCopy()
				assumedPod.Spec.NodeName = "node-a"
				assert.NoError(t, w.sched.Cache.AssumePod(logger, assumedPod))
			}

			assert.Equal(t, tt.expected, w.skipPodSchedule(ctx, fwk, tt.pod))
		})
	}
}

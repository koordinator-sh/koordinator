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

package frameworkext

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestErrorHandlerDispatcher(t *testing.T) {
	dispatcher := newErrorHandlerDispatcher()
	enterDefaultHandler := false
	dispatcher.setDefaultHandler(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
		enterDefaultHandler = true
	})

	handler1Processed := false
	afterHandler1Processed := false

	dispatcher.RegisterErrorHandlerFilters(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
		if podInfo.Pod.Name == "handler1" {
			handler1Processed = true
			return true
		}
		return false
	}, func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
		if podInfo.Pod.Name == "handler1" {
			afterHandler1Processed = true
			return true
		}
		return false
	})

	handler2Processed := false
	afterHandler2Processed := false
	dispatcher.RegisterErrorHandlerFilters(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
		if podInfo.Pod.Name == "handler2" {
			handler2Processed = true
			return true
		}
		return false
	}, func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
		if podInfo.Pod.Name == "handler2" {
			afterHandler2Processed = true
			return true
		}
		return false
	})

	podInfo := &framework.QueuedPodInfo{
		PodInfo: &framework.PodInfo{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "none",
				},
			},
		},
	}
	dispatcher.Error(context.TODO(), nil, podInfo, nil, nil, time.Now())
	assert.True(t, enterDefaultHandler)
	enterDefaultHandler = false

	podInfo = &framework.QueuedPodInfo{
		PodInfo: &framework.PodInfo{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "handler2",
				},
			},
		},
	}
	dispatcher.Error(context.TODO(), nil, podInfo, nil, nil, time.Now())
	assert.False(t, handler1Processed)
	assert.False(t, afterHandler1Processed)
	assert.True(t, handler2Processed)
	assert.True(t, afterHandler2Processed)
	assert.False(t, enterDefaultHandler)
	handler2Processed = false
	afterHandler2Processed = false

	podInfo = &framework.QueuedPodInfo{
		PodInfo: &framework.PodInfo{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "handler1",
				},
			},
		},
	}
	dispatcher.Error(context.TODO(), nil, podInfo, nil, nil, time.Now())
	assert.True(t, handler1Processed)
	assert.True(t, afterHandler1Processed)
	assert.False(t, handler2Processed)
	assert.False(t, afterHandler2Processed)
	assert.False(t, enterDefaultHandler)
}

// TestErrorHandlerDispatcherReleasesInFlightPodOnSuppression verifies that when a
// pre-handler filter suppresses the default failure handler, the dispatcher still
// calls SchedulingQueue.Done() to release the pod's in-flight bookkeeping.
// Otherwise the pod stays in inFlightPods forever, disables the pruning of
// inFlightEvents, and all subsequent cluster events accumulate unboundedly,
// leaking scheduler memory (see asi-release-v1.8.0 koord-scheduler OOM).
func TestErrorHandlerDispatcherReleasesInFlightPodOnSuppression(t *testing.T) {
	noOpHandler := func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
	}

	t.Run("suppressed failure releases the in-flight pod", func(t *testing.T) {
		dispatcher := newErrorHandlerDispatcher()
		defaultHandlerCalled := false
		dispatcher.setDefaultHandler(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
			defaultHandlerCalled = true
		})
		queue := &FakeQueue{}
		dispatcher.setSchedulingQueue(queue)
		dispatcher.RegisterErrorHandlerFilters(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
			return true
		}, nil)

		podInfo := &framework.QueuedPodInfo{
			PodInfo: &framework.PodInfo{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "suppressed",
						UID:  types.UID("suppressed-uid"),
					},
				},
			},
		}
		dispatcher.Error(context.TODO(), nil, podInfo, nil, nil, time.Now())
		assert.False(t, defaultHandlerCalled)
		assert.Equal(t, []types.UID{types.UID("suppressed-uid")}, queue.DonePods)
	})

	t.Run("unsuppressed failure leaves Done to the default handler", func(t *testing.T) {
		dispatcher := newErrorHandlerDispatcher()
		defaultHandlerCalled := false
		dispatcher.setDefaultHandler(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
			defaultHandlerCalled = true
		})
		queue := &FakeQueue{}
		dispatcher.setSchedulingQueue(queue)
		dispatcher.RegisterErrorHandlerFilters(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
			return false
		}, nil)

		podInfo := &framework.QueuedPodInfo{
			PodInfo: &framework.PodInfo{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "normal",
						UID:  types.UID("normal-uid"),
					},
				},
			},
		}
		dispatcher.Error(context.TODO(), nil, podInfo, nil, nil, time.Now())
		assert.True(t, defaultHandlerCalled)
		assert.Empty(t, queue.DonePods)
	})

	t.Run("suppression without a queue wired does not panic", func(t *testing.T) {
		dispatcher := newErrorHandlerDispatcher()
		dispatcher.setDefaultHandler(noOpHandler)
		dispatcher.RegisterErrorHandlerFilters(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
			return true
		}, nil)

		podInfo := &framework.QueuedPodInfo{
			PodInfo: &framework.PodInfo{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "suppressed-no-queue",
						UID:  types.UID("suppressed-no-queue-uid"),
					},
				},
			},
		}
		assert.NotPanics(t, func() {
			dispatcher.Error(context.TODO(), nil, podInfo, nil, nil, time.Now())
		})
	})
}

// TestErrorHandlerDispatcherDropsDeletedPod covers the race where the pod is deleted after the
// default handler found it in the informer cache but before it was requeued as unschedulable.
func TestErrorHandlerDispatcherDropsDeletedPod(t *testing.T) {
	newPod := func(name string, reservePod bool) *corev1.Pod {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      name,
				UID:       types.UID(name + "-uid"),
			},
		}
		if reservePod {
			pod.Annotations = map[string]string{reservationutil.AnnotationReservePod: "true"}
		}
		return pod
	}
	tests := []struct {
		name          string
		pod           *corev1.Pod
		inInformer    bool
		wantInUnsched bool
	}{
		{
			name:          "pod deleted during failure handling is removed from the queue",
			pod:           newPod("deleted", false),
			inInformer:    false,
			wantInUnsched: false,
		},
		{
			name:          "pod still in informer cache stays in the queue",
			pod:           newPod("alive", false),
			inInformer:    true,
			wantInUnsched: true,
		},
		{
			name:          "reserve pod is left alone",
			pod:           newPod("reserve", true),
			inInformer:    false,
			wantInUnsched: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				[]schedulertesting.RegisterPluginFunc{
					schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
					schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				},
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)
			if tt.inInformer {
				assert.NoError(t, sharedInformerFactory.Core().V1().Pods().Informer().GetStore().Add(tt.pod))
			}

			queue := &FakeQueue{
				Pods:              map[string]*corev1.Pod{},
				UnschedulablePods: map[string]*corev1.Pod{},
			}
			dispatcher := newErrorHandlerDispatcher()
			dispatcher.setSchedulingQueue(queue)
			// The default handler requeues the pod as if its informer lookup won the race against the delete.
			dispatcher.setDefaultHandler(func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
				assert.NoError(t, queue.AddUnschedulableIfNotPresent(klog.Background(), podInfo, 0))
			})

			podInfo := &framework.QueuedPodInfo{PodInfo: &framework.PodInfo{Pod: tt.pod}}
			dispatcher.Error(context.TODO(), fh, podInfo, fwktype.NewStatus(fwktype.Unschedulable), nil, time.Now())

			key, _ := framework.GetPodKey(tt.pod)
			_, ok := queue.UnschedulablePods[key]
			assert.Equal(t, tt.wantInUnsched, ok)
		})
	}
}

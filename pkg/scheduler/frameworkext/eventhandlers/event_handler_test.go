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

package eventhandlers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	clientfeatures "k8s.io/client-go/features"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

type mutableClientFeatureGates interface {
	clientfeatures.Gates
	Set(key clientfeatures.Feature, value bool) error
}

func init() {
	schedulermetrics.Register()
	// Disable WatchListClient to avoid fake client compatibility issues in tests.
	if fg, ok := clientfeatures.FeatureGates().(mutableClientFeatureGates); ok {
		_ = fg.Set(clientfeatures.WatchListClient, false)
	}
}

func TestAddScheduleEventHandler(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		sched := &scheduler.Scheduler{}
		internalHandler := &frameworkext.FakeScheduler{}
		clientSet := kubefake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
		koordClientSet := koordfake.NewSimpleClientset()
		koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
		AddScheduleEventHandler(sched, internalHandler, informerFactory, koordSharedInformerFactory, nil)
	})
}

// TestAddScheduleEventHandler_CrossSchedulerNominatorRegistration verifies that
// AddScheduleEventHandler only registers the cross-scheduler pod nominator handler
// when BOTH CrossSchedulerNomination feature gate is enabled AND the nominator is
// non-nil. When either condition is false, the cross-scheduler handler path is
// a no-op so no cross-scheduler nominated pod events are delivered.
func TestAddScheduleEventHandler_CrossSchedulerNominatorRegistration(t *testing.T) {
	// Helper: build a pending, unscheduled pod owned by an external scheduler.
	buildOtherSchedulerPod := func(name string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("uid-" + name)},
			Spec: corev1.PodSpec{
				SchedulerName: "other-scheduler",
			},
			Status: corev1.PodStatus{
				NominatedNodeName: "node-1",
			},
		}
	}

	// run registers the handlers with the given enable flag and nominator, starts
	// informers, creates a pod, and returns what the nominator observed.
	run := func(t *testing.T, enable bool, nominator *frameworkext.CrossSchedulerPodNominator) int {
		defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, features.CrossSchedulerNomination, enable)()

		clientSet := kubefake.NewSimpleClientset()
		koordClientSet := koordfake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
		koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
		sched := &scheduler.Scheduler{}
		AddScheduleEventHandler(sched, &frameworkext.FakeScheduler{}, informerFactory, koordSharedInformerFactory, nominator)

		stopCh := make(chan struct{})
		defer close(stopCh)
		informerFactory.Start(stopCh)
		koordSharedInformerFactory.Start(stopCh)
		informerFactory.WaitForCacheSync(stopCh)
		koordSharedInformerFactory.WaitForCacheSync(stopCh)

		// Create an unscheduled pod nominated by "other-scheduler"; if the
		// cross-scheduler handler was registered, the nominator should observe
		// the OnAdd callback and track it.
		pod := buildOtherSchedulerPod("pod-a")
		_, err := clientSet.CoreV1().Pods("default").Create(context.TODO(), pod, metav1.CreateOptions{})
		assert.NoError(t, err)

		// Give the informer a chance to deliver events.
		time.Sleep(200 * time.Millisecond)

		if nominator == nil {
			return 0
		}
		return len(nominator.NominatedPodsForNode("node-1"))
	}

	// Case 1: feature disabled + non-nil nominator -> handler MUST NOT be
	// registered; the nominator must not observe the pod.
	nom := frameworkext.NewCrossSchedulerPodNominator()
	observed := run(t, false, nom)
	assert.Equal(t, 0, observed, "handler must not be registered when feature gate is disabled")

	// Case 2: feature enabled + nil nominator -> still no panic, nothing to observe.
	run(t, true, nil)

	// Case 3: feature enabled + non-nil nominator -> handler registered and
	// pod events flow through ShouldHandle -> OnAdd -> nominatedPods cache.
	nom = frameworkext.NewCrossSchedulerPodNominator()
	observed = run(t, true, nom)
	assert.Equal(t, 1, observed, "handler must be registered and pod event delivered to nominator")
}

func Test_irresponsibleUnscheduledPodEventHandler(t *testing.T) {
	sched := &scheduler.Scheduler{
		Profiles: map[string]framework.Framework{
			corev1.DefaultSchedulerName: nil,
		},
	}
	adapt := frameworkext.NewFakeScheduler()
	handler := irresponsibleUnscheduledPodEventHandler(sched, adapt)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "pod-0",
			UID:             "123",
			ResourceVersion: "1",
		},
		Spec: corev1.PodSpec{},
	}
	// obj is responsible, ignore it
	handler.OnAdd(pod, true)
	assert.Nil(t, adapt.Queue.Pods[string(pod.UID)])
	adapt.Queue.Pods[string(pod.UID)] = pod // mock an enqueue as the default handler
	podCopy := pod.DeepCopy()
	podCopy.ResourceVersion = "2"
	handler.OnUpdate(pod, podCopy)
	adapt.Queue.Pods[string(pod.UID)] = podCopy
	assert.NotNil(t, adapt.Queue.Pods[string(pod.UID)])
	// obj become irresponsible and deleted, dequeue it
	podCopy = pod.DeepCopy()
	podCopy.Spec.SchedulerName = "other-scheduler"
	handler.OnDelete(podCopy)
	// the default handler does not handle irresponsible obj
	assert.Nil(t, adapt.Queue.Pods[string(pod.UID)])
	// re-create an irresponsible obj
	podCopy = pod.DeepCopy()
	podCopy.ResourceVersion = "3"
	podCopy.Spec.SchedulerName = "other-scheduler"
	handler.OnAdd(podCopy, true)
	assert.Nil(t, adapt.Queue.Pods[string(podCopy.UID)])
	// the default handler does not handle irresponsible obj
	assert.Nil(t, adapt.Queue.Pods[string(podCopy.UID)])
	// obj from irresponsible to responsible, keep it
	podCopy1 := podCopy.DeepCopy()
	podCopy1.Spec.SchedulerName = corev1.DefaultSchedulerName
	adapt.Queue.Pods[string(pod.UID)] = podCopy1 // mock an enqueue as the default handler
	handler.OnUpdate(podCopy, podCopy1)
	assert.NotNil(t, adapt.Queue.Pods[string(podCopy1.UID)])
	// obj deleted, dequeue it
	delete(adapt.Queue.Pods, string(podCopy1.UID)) // mock a dequeue as the default handler
	handler.OnDelete(podCopy1)
	assert.Nil(t, adapt.Queue.Pods[string(podCopy1.UID)])
}

func Test_isResponsibleForPod(t *testing.T) {
	type args struct {
		profiles profile.Map
		pod      *corev1.Pod
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "not responsible when profile is empty",
			args: args{
				profiles: profile.Map{},
				pod:      &corev1.Pod{},
			},
			want: false,
		},
		{
			name: "responsible when scheduler name matched the profile",
			args: args{
				profiles: profile.Map{
					"test-scheduler": &fakeFramework{},
				},
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod-sample",
					},
					Spec: corev1.PodSpec{
						SchedulerName: "test-scheduler",
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isResponsibleForPod(tt.args.profiles, tt.args.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

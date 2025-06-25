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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/profile"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func TestAddScheduleEventHandler(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		sched := &scheduler.Scheduler{}
		internalHandler := &frameworkext.FakeScheduler{}
		clientSet := kubefake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
		koordClientSet := koordfake.NewSimpleClientset()
		koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
		AddScheduleEventHandler(sched, internalHandler, informerFactory, koordSharedInformerFactory)
	})
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

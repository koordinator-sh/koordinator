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
	corev1 "k8s.io/api/core/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/profile"

	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// AddScheduleEventHandler adds extra event handlers for the scheduler:
// - Reservation event handlers for the scheduler just like pods'. One special case is that reservations have expiration, which the scheduler should clean up expired ones from the
// cache and queue.
// - Pod and reservation event handlers for multi-scheduler clean up.
func AddScheduleEventHandler(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler, informerFactory informers.SharedInformerFactory, koordSharedInformerFactory koordinatorinformers.SharedInformerFactory) {
	podInformer := informerFactory.Core().V1().Pods().Informer()
	if k8sfeature.DefaultFeatureGate.Enabled(features.DynamicSchedulerCheck) {
		// Clean up irresponsible pods for scheduling queue
		_, err := podInformer.AddEventHandler(irresponsibleUnscheduledPodEventHandler(sched, schedAdapter))
		if err != nil {
			klog.Fatalf("failed to add irresponsible pod handler for SchedulingQueue, err: %s", err)
		}
	}

	reservationInformer := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Informer()
	// unified reservation event handler for both cache and queue
	_, err := reservationInformer.AddEventHandler(reservationEventHandlers(sched, schedAdapter))
	if err != nil {
		klog.Fatalf("failed to add reservation handler, err: %s", err)
	}
}

func irresponsibleUnscheduledPodEventHandler(sched *scheduler.Scheduler, schedAdapter frameworkext.Scheduler) cache.ResourceEventHandler {
	// 1. If the pod does not change its scheduler name, the default handler keeps the scheduling queue and the
	//    nominator correct.
	// 2. If the pod updates to irresponsible from responsible, the default handler deletes the old obj from the queue.
	// 3. If the pod updates to responsible from irresponsible, the default handler adds the old obj to the queue.
	// 4. If an irresponsible deleted after enqueued, the default handler may not handle the obj causing a leak.
	return cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			pod := toPod(obj)
			if pod == nil {
				return
			}
			if isResponsibleForPod(sched.Profiles, pod) {
				return
			}
			klog.V(3).InfoS("Delete event for irresponsible unscheduled pod", "pod", klog.KObj(pod))
			if err := schedAdapter.GetSchedulingQueue().Delete(pod); err != nil {
				klog.Errorf("failed to dequeue irresponsible pod %s, err: %s", klog.KObj(pod), err)
			}
			// FIXME: proactively reject waiting pod if it has handled by a responsible profile
		},
	}
}

func toPod(obj interface{}) *corev1.Pod {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		pod, _ = t.Obj.(*corev1.Pod)
	}
	return pod
}

func isResponsibleForPod(profiles profile.Map, pod *corev1.Pod) bool {
	return profiles.HandlesSchedulerName(pod.Spec.SchedulerName)
}

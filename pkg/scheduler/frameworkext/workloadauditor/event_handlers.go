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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler"

	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func AddEventHandler(sched *scheduler.Scheduler, workloadAuditor WorkloadAuditor, sharedInformerFactory informers.SharedInformerFactory, koordInformerFactory koordinatorinformers.SharedInformerFactory) {
	if !workloadAuditor.Enabled() {
		return
	}
	podEventHandler := &cache.FilteringResourceEventHandler{
		FilterFunc: func(obj any) bool {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return false
				}
				pod, ok = tombstone.Obj.(*corev1.Pod)
				if !ok {
					return false
				}
			}
			return pod.Spec.NodeName == "" && sched.Profiles.HandlesSchedulerName(pod.Spec.SchedulerName)
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					return
				}
				workloadAuditor.AddPod(pod)
			},
			UpdateFunc: func(oldObj, newObj any) {
				oldPod, ok := oldObj.(*corev1.Pod)
				if !ok {
					return
				}
				newPod, ok := newObj.(*corev1.Pod)
				if !ok {
					return
				}
				oldGated := PodIsGated(oldPod)
				newGated := PodIsGated(newPod)
				if oldGated != newGated {
					workloadAuditor.RecordPodGating(newPod, newGated)
				}
			},
			DeleteFunc: func(obj any) {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
					if !ok {
						return
					}
					pod, ok = tombstone.Obj.(*corev1.Pod)
					if !ok {
						return
					}
				}
				workloadAuditor.DeletePod(pod)
			},
		},
	}
	podInformer := sharedInformerFactory.Core().V1().Pods()
	_, _ = frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), sharedInformerFactory, podInformer.Informer(), podEventHandler)
	reservationEventHandler := reservationutil.NewReservationToPodEventHandler(podEventHandler)
	reservationInformer := koordInformerFactory.Scheduling().V1alpha1().Reservations()
	_, _ = frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), koordInformerFactory, reservationInformer.Informer(), reservationEventHandler)
}

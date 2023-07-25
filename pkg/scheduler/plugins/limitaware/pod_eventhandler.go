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

package limitaware

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type podEventHandler struct {
	nodeLimitsCache *Cache
}

func registerPodEventHandler(handle framework.Handle, nodeLimitsCache *Cache) {
	podInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	eventHandler := cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch t := obj.(type) {
			case *corev1.Pod:
				return assignedPod(t)
			case cache.DeletedFinalStateUnknown:
				if pod, ok := t.Obj.(*corev1.Pod); ok {
					return assignedPod(pod)
				}
				return false
			default:
				return false
			}
		},
		Handler: &podEventHandler{
			nodeLimitsCache: nodeLimitsCache,
		},
	}
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), handle.SharedInformerFactory(), podInformer, eventHandler)
	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if ok {
		reservationInformer := extendedHandle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations()
		reservationEventHandler := reservationutil.NewReservationToPodEventHandler(eventHandler, reservationutil.IsObjValidActiveReservation)
		frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), extendedHandle.KoordinatorSharedInformerFactory(), reservationInformer.Informer(), reservationEventHandler)
	}
}

func assignedPod(pod *corev1.Pod) bool {
	return pod.Spec.NodeName != ""
}

func (c *podEventHandler) OnAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	if util.IsPodTerminated(pod) {
		return
	}
	c.nodeLimitsCache.AddPod(pod.Spec.NodeName, pod)
}

func (c *podEventHandler) OnUpdate(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return
	}
	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	if util.IsPodTerminated(newPod) {
		c.nodeLimitsCache.DeletePod(newPod.Spec.NodeName, newPod)
	} else {
		c.nodeLimitsCache.UpdatePod(newPod.Spec.NodeName, oldPod, newPod)
	}
}

func (c *podEventHandler) OnDelete(obj interface{}) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	default:
		break
	}

	if pod == nil {
		return
	}
	c.nodeLimitsCache.DeletePod(pod.Spec.NodeName, pod)
}

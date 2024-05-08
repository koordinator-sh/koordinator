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

package reservation

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type podEventHandler struct {
	cache     *reservationCache
	nominator *nominator
}

func registerPodEventHandler(cache *reservationCache, nominator *nominator, factory informers.SharedInformerFactory) {
	eventHandler := &podEventHandler{
		cache:     cache,
		nominator: nominator,
	}
	informer := factory.Core().V1().Pods().Informer()
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), factory, informer, eventHandler)
}

// assignedPod selects pods that are assigned (scheduled and running).
func assignedPod(pod *corev1.Pod) bool {
	return len(pod.Spec.NodeName) != 0
}

func (h *podEventHandler) OnAdd(obj interface{}, isInInitialList bool) {
	pod, _ := obj.(*corev1.Pod)
	if pod == nil {
		return
	}

	h.updatePod(nil, pod)
}

func (h *podEventHandler) OnUpdate(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return
	}
	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	h.updatePod(oldPod, newPod)
}

func (h *podEventHandler) OnDelete(obj interface{}) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		pod, _ = t.Obj.(*corev1.Pod)
	}
	if pod == nil {
		return
	}
	h.deletePod(pod)
}

func (h *podEventHandler) updatePod(oldPod, newPod *corev1.Pod) {
	if util.IsPodTerminated(newPod) {
		h.deletePod(newPod)
		return
	}

	if !assignedPod(newPod) {
		return
	}

	h.nominator.RemoveNominatedReservation(newPod)
	podInfo, _ := framework.NewPodInfo(newPod)
	h.nominator.DeleteReservePod(podInfo)

	var reservationUID types.UID
	if oldPod != nil {
		reservationAllocated, err := apiext.GetReservationAllocated(oldPod)
		if err == nil && reservationAllocated != nil && reservationAllocated.UID != "" {
			reservationUID = reservationAllocated.UID
		}
	}
	if newPod != nil && reservationUID == "" {
		reservationAllocated, err := apiext.GetReservationAllocated(newPod)
		if err == nil && reservationAllocated != nil && reservationAllocated.UID != "" {
			reservationUID = reservationAllocated.UID
		}
	}

	if reservationUID != "" {
		h.cache.updatePod(reservationUID, oldPod, newPod)
	}

	if newPod != nil && apiext.IsReservationOperatingMode(newPod) {
		if newPod.Spec.NodeName == "" {
			return
		}
		currentOwner, err := apiext.GetReservationCurrentOwner(newPod.Annotations)
		if err != nil {
			klog.ErrorS(err, "Invalid reservation current owner in Pod", "pod", klog.KObj(newPod))
		}
		h.cache.updateReservationOperatingPod(newPod, currentOwner)
	}
}

func (h *podEventHandler) deletePod(pod *corev1.Pod) {
	h.nominator.RemoveNominatedReservation(pod)
	podInfo, _ := framework.NewPodInfo(pod)
	h.nominator.DeleteReservePod(podInfo)

	reservationAllocated, err := apiext.GetReservationAllocated(pod)
	if err == nil && reservationAllocated != nil && reservationAllocated.UID != "" {
		h.cache.deletePod(reservationAllocated.UID, pod)
	}

	if apiext.IsReservationOperatingMode(pod) {
		h.cache.deleteReservationOperatingPod(pod)
	}
}

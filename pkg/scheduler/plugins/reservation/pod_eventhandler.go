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
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type podEventHandler struct {
	cache     *reservationCache
	nominator *nominator
}

func registerPodEventHandler(handle frameworkext.ExtendedHandle, cache *reservationCache, nominator *nominator, factory informers.SharedInformerFactory) {
	eventHandler := &podEventHandler{
		cache:     cache,
		nominator: nominator,
	}
	handle.RegisterForgetPodHandler(eventHandler.deletePod)
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

	h.nominator.DeleteNominatedReservePodOrReservation(newPod)
	var oldRAllocated, newRAllocated *apiext.ReservationAllocated
	if oldPod != nil {
		reservationAllocated, err := apiext.GetReservationAllocated(oldPod)
		if err == nil && reservationAllocated != nil && reservationAllocated.UID != "" {
			oldRAllocated = reservationAllocated
		}
	}
	if newPod != nil {
		reservationAllocated, err := apiext.GetReservationAllocated(newPod)
		if err == nil && reservationAllocated != nil && reservationAllocated.UID != "" {
			newRAllocated = reservationAllocated
		}
	}

	if oldRAllocated != nil || newRAllocated != nil {
		h.cache.updatePod(oldRAllocated.GetUID(), newRAllocated.GetUID(), oldPod, newPod)
		if oldRAllocated == nil {
			klog.V(4).InfoS("add pod for reservation", "pod", klog.KObj(newPod), "reservation", newRAllocated.GetName(), "uid", newRAllocated.GetUID())
		} else if newRAllocated == nil {
			klog.V(4).InfoS("delete pod for reservation", "pod", klog.KObj(oldPod), "reservation", oldRAllocated.GetName(), "uid", oldRAllocated.GetUID())
		} else if oldRAllocated.GetUID() != newRAllocated.GetUID() {
			klog.V(4).InfoS("update pod for different reservation", "pod", klog.KObj(newPod), "oldReservation", oldRAllocated.GetName(), "oldUID", oldRAllocated.GetUID(), "newReservation", newRAllocated.GetName(), "newUID", newRAllocated.GetUID())
		} else {
			klog.V(5).InfoS("update pod for same reservation", "pod", klog.KObj(newPod), "reservation", newRAllocated.GetName(), "uid", newRAllocated.GetUID())
		}
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

	// Update pre-allocatable candidates cache if pod's pre-allocatable status changed
	h.updatePreAllocatableCandidatesCache(oldPod, newPod)
}

func (h *podEventHandler) deletePod(pod *corev1.Pod) {
	h.nominator.DeleteNominatedReservePodOrReservation(pod)

	reservationAllocated, err := apiext.GetReservationAllocated(pod)
	if err == nil && reservationAllocated != nil && reservationAllocated.UID != "" {
		h.cache.deletePod(reservationAllocated.UID, pod)
		klog.V(4).InfoS("delete pod for reservation", "pod", klog.KObj(pod), "reservation", reservationAllocated.GetName(), "uid", reservationAllocated.GetUID())
	}

	if apiext.IsReservationOperatingMode(pod) {
		h.cache.deleteReservationOperatingPod(pod)
	}

	// Remove pod from pre-allocatable candidates cache if it was a candidate
	if isPreAllocatablePod(pod) && pod.Spec.NodeName != "" {
		h.cache.deletePreAllocatableCandidateOnNode(pod.Spec.NodeName, pod.UID)
	}
}

// isPreAllocatablePod checks if a pod is a pre-allocatable candidate
func isPreAllocatablePod(pod *corev1.Pod) bool {
	if pod == nil || pod.Labels == nil {
		return false
	}
	return pod.Labels[apiext.LabelPodPreAllocatable] == "true"
}

// getPreAllocatablePriority retrieves the pre-allocatable priority from pod annotation
func getPreAllocatablePriority(pod *corev1.Pod) int64 {
	if pod == nil || pod.Annotations == nil {
		return 0
	}
	priorityStr, ok := pod.Annotations[apiext.AnnotationPodPreAllocatablePriority]
	if !ok {
		return 0
	}
	priority, err := strconv.ParseInt(priorityStr, 10, 64)
	if err != nil {
		return 0
	}
	return priority
}

// updatePreAllocatableCandidatesCache updates the cached pre-allocatable candidates when pod changes
// Uses incremental updates with btree for efficiency
func (h *podEventHandler) updatePreAllocatableCandidatesCache(oldPod, newPod *corev1.Pod) {
	if newPod == nil || newPod.Spec.NodeName == "" {
		return
	}

	oldIsCandidate := oldPod != nil && isPreAllocatablePod(oldPod)
	newIsCandidate := isPreAllocatablePod(newPod)

	// Case 1: Non-candidate -> Candidate (Add to cache)
	if !oldIsCandidate && newIsCandidate {
		h.cache.addPreAllocatableCandidateOnNode(newPod)
		return
	}

	// Case 2: Candidate -> Non-candidate (Remove from cache)
	if oldIsCandidate && !newIsCandidate {
		h.cache.deletePreAllocatableCandidateOnNode(newPod.Spec.NodeName, newPod.UID)
		return
	}

	// Case 3: Candidate -> Candidate, check if priority or node changed
	if newIsCandidate && oldPod != nil {
		// Check if node changed
		if oldPod.Spec.NodeName != newPod.Spec.NodeName {
			// Node changed: remove from old node, add to new node
			if oldPod.Spec.NodeName != "" {
				h.cache.deletePreAllocatableCandidateOnNode(oldPod.Spec.NodeName, oldPod.UID)
			}
			h.cache.addPreAllocatableCandidateOnNode(newPod)
			return
		}

		// Check if priority changed
		oldPriority := getPreAllocatablePriority(oldPod)
		newPriority := getPreAllocatablePriority(newPod)
		if oldPriority != newPriority {
			// Priority changed: update in btree (delete old + insert new)
			h.cache.updatePreAllocatableCandidatePriority(newPod)
		}
	}
}

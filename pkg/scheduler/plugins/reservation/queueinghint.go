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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func (pl *Plugin) isSchedulableAfterPodDeletion(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) (fwktype.QueueingHint, error) {
	deletedPod, _, err := schedutil.As[*corev1.Pod](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj to Pod in isSchedulableAfterPodDeletion", "oldObj", oldObj)
		return fwktype.Queue, nil
	}

	if deletedPod == nil {
		return fwktype.Queue, nil
	}

	// Any pod deletion might free up resources for reservations or pods using reservations.
	// To be simple, we queue if the pod was assigned to a node.
	if deletedPod.Spec.NodeName != "" {
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func (pl *Plugin) isSchedulableAfterReservationChanged(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) (fwktype.QueueingHint, error) {
	oldRes, newRes, err := schedutil.As[*schedulingv1alpha1.Reservation](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj or newObj to Reservation", "oldObj", oldObj, "newObj", newObj)
		return fwktype.Queue, nil
	}

	if oldRes == nil || newRes == nil {
		return fwktype.Queue, nil
	}

	// 1. If a Reservation becomes Available, it might satisfy a pod's reservation affinity.
	if oldRes.Status.Phase != newRes.Status.Phase && newRes.Status.Phase == schedulingv1alpha1.ReservationAvailable {
		logger.V(5).Info("Queuing pod because reservation became Available", "pod", klog.KObj(pod), "reservation", klog.KObj(newRes))
		return fwktype.Queue, nil
	}

	// 2. If allocated resources decreased, more space might be available.
	if isReservationMoreSchedulable(oldRes, newRes) {
		logger.V(5).Info("Queuing pod because reservation allocated resources decreased", "pod", klog.KObj(pod), "reservation", klog.KObj(newRes))
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func isReservationMoreSchedulable(oldRes, newRes *schedulingv1alpha1.Reservation) bool {
	for resName, newVal := range newRes.Status.Allocated {
		oldVal, ok := oldRes.Status.Allocated[resName]
		if ok && newVal.Cmp(oldVal) < 0 {
			return true
		}
	}
	// Also check if current owners count decreased
	if len(newRes.Status.CurrentOwners) < len(oldRes.Status.CurrentOwners) {
		return true
	}
	return false
}

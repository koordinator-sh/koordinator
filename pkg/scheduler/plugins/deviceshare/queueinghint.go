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

package deviceshare

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func (p *Plugin) isSchedulableAfterPodDeletion(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) (fwktype.QueueingHint, error) {
	deletedPod, _, err := schedutil.As[*corev1.Pod](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj to Pod in isSchedulableAfterPodDeletion", "oldObj", oldObj)
		return fwktype.Queue, nil
	}

	if deletedPod == nil {
		return fwktype.Queue, nil
	}

	// If the unschedulable pod doesn't require device resources, we can skip.
	// But since PreFilter already skips if no device is needed, this pod must be one that needs devices.

	// Check if the deleted pod was using any device resources.
	// We check both annotations (allocated) and requests.
	if hasDeviceAllocated(deletedPod) || hasDeviceRequest(deletedPod) {
		logger.V(5).Info("Queuing pod because a pod with device resources was deleted", "pod", klog.KObj(pod), "deletedPod", klog.KObj(deletedPod))
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func (p *Plugin) isSchedulableAfterDeviceChanged(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) (fwktype.QueueingHint, error) {
	originalDevice, modifiedDevice, err := schedutil.As[*schedulingv1alpha1.Device](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj or newObj to Device", "oldObj", oldObj, "newObj", newObj)
		return fwktype.Queue, nil
	}

	if originalDevice == nil || modifiedDevice == nil {
		return fwktype.Queue, nil
	}

	// For Update event, check if resources increased or metadata changed significantly.
	if isDeviceMoreSchedulable(originalDevice, modifiedDevice) {
		logger.V(5).Info("Queuing pod because device resources became more available", "pod", klog.KObj(pod), "device", klog.KObj(modifiedDevice))
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func isDeviceMoreSchedulable(oldDevice, newDevice *schedulingv1alpha1.Device) bool {
	// Check if any device became healthy
	if countHealthyDevices(newDevice) > countHealthyDevices(oldDevice) {
		return true
	}
	// Check if allocations decreased
	if len(newDevice.Status.Allocations) < len(oldDevice.Status.Allocations) {
		return true
	}
	return false
}

func countHealthyDevices(device *schedulingv1alpha1.Device) int {
	count := 0
	for _, d := range device.Spec.Devices {
		if d.Health {
			count++
		}
	}
	return count
}

func hasDeviceAllocated(pod *corev1.Pod) bool {
	allocations, err := apiext.GetDeviceAllocations(pod.Annotations)
	if err != nil || len(allocations) == 0 {
		return false
	}
	return true
}

func hasDeviceRequest(pod *corev1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		for r := range container.Resources.Requests {
			if isDeviceResource(r) {
				return true
			}
		}
	}
	return false
}

func isDeviceResource(name corev1.ResourceName) bool {
	_, ok := DeviceResourceFlags[name]
	return ok
}

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
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func registerPodEventHandler(deviceCache *nodeDeviceCache, sharedInformerFactory informers.SharedInformerFactory, koordSharedInformerFactory koordinatorinformers.SharedInformerFactory) {
	podInformer := sharedInformerFactory.Core().V1().Pods().Informer()
	eventHandler := cache.ResourceEventHandlerFuncs{
		AddFunc:    deviceCache.onPodAdd,
		UpdateFunc: deviceCache.onPodUpdate,
		DeleteFunc: deviceCache.onPodDelete,
	}
	// make sure Pods are loaded before scheduler starts working
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), sharedInformerFactory, podInformer, eventHandler)
	reservationInformer := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations()
	reservationEventHandler := reservationutil.NewReservationToPodEventHandler(eventHandler, reservationutil.IsObjValidActiveReservation)
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), koordSharedInformerFactory, reservationInformer.Informer(), reservationEventHandler)
}

func (n *nodeDeviceCache) onPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.Errorf("pod cache add failed to parse, obj %T", obj)
		return
	}
	n.updatePod(nil, pod)
}

func (n *nodeDeviceCache) onPodUpdate(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return
	}

	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	n.updatePod(oldPod, pod)
	return
}

func (n *nodeDeviceCache) onPodDelete(obj interface{}) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*corev1.Pod)
		if !ok {
			klog.V(5).Infof("pod cache remove failed to parse, obj %T", obj)
			return
		}
	default:
		return
	}
	n.deletePod(pod)
}

func (n *nodeDeviceCache) updatePod(oldPod *corev1.Pod, pod *corev1.Pod) {
	if pod.Spec.NodeName == "" {
		klog.V(5).InfoS("Pod missed nodeName", "pod", klog.KObj(pod))
		return
	}

	if util.IsPodTerminated(pod) {
		n.deletePod(pod)
		return
	}

	allocations, err := apiext.GetDeviceAllocations(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "failed to get device allocations from new pod", "pod", klog.KObj(pod))
		return
	}
	if len(allocations) != 0 {
		transformDeviceAllocations(allocations)
	}

	var oldAllocations apiext.DeviceAllocations
	if oldPod != nil {
		oldAllocations, err = apiext.GetDeviceAllocations(oldPod.Annotations)
		if err != nil {
			klog.ErrorS(err, "failed to get device allocations from old pod", "pod", klog.KObj(oldPod))
			return
		}
		if len(oldAllocations) != 0 {
			transformDeviceAllocations(oldAllocations)
		}
	}

	if len(oldAllocations) == 0 && len(allocations) == 0 {
		return
	}

	info := n.getNodeDevice(pod.Spec.NodeName, true)
	info.lock.Lock()
	defer info.lock.Unlock()
	if oldPod != nil && len(oldAllocations) > 0 {
		info.updateCacheUsed(oldAllocations, oldPod, false)
		klog.V(5).InfoS("remove old pod from nodeDevice cache on node", "pod", klog.KObj(pod), "node", oldPod.Spec.NodeName)
	}
	if len(allocations) > 0 {
		info.updateCacheUsed(allocations, pod, true)
		klog.V(5).InfoS("update pod in nodeDevice cache on node", "pod", klog.KObj(pod), "node", pod.Spec.NodeName)
	}
}

func (n *nodeDeviceCache) deletePod(pod *corev1.Pod) {
	if pod.Spec.NodeName == "" {
		return
	}

	devicesAllocation, err := apiext.GetDeviceAllocations(pod.Annotations)
	if err != nil {
		klog.Errorf("failed to get device allocation from pod %v, err: %v", klog.KObj(pod), err)
		return
	}
	if len(devicesAllocation) == 0 {
		return
	}
	transformDeviceAllocations(devicesAllocation)

	info := n.getNodeDevice(pod.Spec.NodeName, false)
	if info == nil {
		klog.Errorf("node device cache not found, nodeName: %v, pod: %v", pod.Spec.NodeName, klog.KObj(pod))
		return
	}

	info.lock.Lock()
	defer info.lock.Unlock()

	info.updateCacheUsed(devicesAllocation, pod, false)
	klog.V(5).InfoS("pod has been deleted so remove pod from nodeDevice cache on node", "pod", klog.KObj(pod), "node", pod.Spec.NodeName)
}

func transformDeviceAllocations(deviceAllocations apiext.DeviceAllocations) {
	for _, allocations := range deviceAllocations {
		for _, v := range allocations {
			v.Resources = apiext.TransformDeprecatedDeviceResources(v.Resources)
		}
	}
}

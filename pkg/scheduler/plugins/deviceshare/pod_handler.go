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
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func (n *nodeDeviceCache) onPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.Errorf("pod cache add failed to parse, obj %T", obj)
		return
	}

	info := n.getNodeDevice(pod.Spec.NodeName)
	if info == nil {
		klog.Errorf("node device cache not found, nodeName: %v", pod.Spec.NodeName)
		return
	}

	podRequest, _ := resource.PodRequestsAndLimits(pod)

	deviceExist := false
	for deviceType := range deviceResourceNames {
		if hasDeviceResource(podRequest, deviceType) {
			deviceExist = true
		}
	}

	if !deviceExist {
		klog.V(5).Infof("pod cache add skip non Device pod, podName: %v", pod.Name)
		return
	}

	devicesAllocation, err := apiext.GetDeviceAllocations(pod.Annotations)
	if err != nil {
		klog.Errorf("failed to get device allocation, pod: %v, err: %v", pod.Name, err)
		return
	}

	info.lock.Lock()
	defer info.lock.Unlock()

	info.updateCacheUsed(devicesAllocation, pod, true)
	klog.V(5).InfoS("pod cache added", "pod", klog.KObj(pod))
}

func (n *nodeDeviceCache) onPodUpdate(oldObj, newObj interface{}) {
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

	info := n.getNodeDevice(pod.Spec.NodeName)
	if info == nil {
		klog.Errorf("node device cache not found, nodeName: %v", pod.Spec.NodeName)
		return
	}

	podRequest, _ := resource.PodRequestsAndLimits(pod)

	deviceExist := false
	for deviceType := range deviceResourceNames {
		if hasDeviceResource(podRequest, deviceType) {
			deviceExist = true
		}
	}

	if !deviceExist {
		klog.V(5).Infof("pod cache remove skip non Device pod, podName: %v", pod.Name)
		return
	}

	devicesAllocation, err := apiext.GetDeviceAllocations(pod.Annotations)
	if err != nil {
		klog.Errorf("failed to get %v device allocation, err: %v", pod.Name, err)
		return
	}

	info.lock.Lock()
	defer info.lock.Unlock()

	info.updateCacheUsed(devicesAllocation, pod, false)
	klog.V(5).InfoS("pod cache deleted", "pod", klog.KObj(pod))
}

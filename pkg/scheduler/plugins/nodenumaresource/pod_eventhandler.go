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

package nodenumaresource

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

type podEventHandler struct {
	cpuManager CPUManager
}

func registerPodEventHandler(handle framework.Handle, cpuManager CPUManager) {
	handle.SharedInformerFactory().Core().V1().Pods().Informer().AddEventHandler(&podEventHandler{
		cpuManager: cpuManager,
	})
}

func (c *podEventHandler) OnAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	c.updatePod(nil, pod)
}

func (c *podEventHandler) OnUpdate(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return
	}

	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	c.updatePod(oldPod, pod)
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
	c.deletePod(pod)
}

func (c *podEventHandler) updatePod(oldPod, pod *corev1.Pod) {
	if pod.Spec.NodeName == "" {
		return
	}
	if util.IsPodTerminated(pod) {
		c.deletePod(pod)
		return
	}

	resourceStatus, err := GetResourceStatus(pod.Annotations)
	if err != nil {
		return
	}
	cpuset, err := Parse(resourceStatus.CPUSet)
	if err != nil || cpuset.IsEmpty() {
		return
	}

	resourceSpec, err := GetResourceSpec(pod.Annotations)
	if err != nil {
		return
	}

	c.cpuManager.UpdateAllocatedCPUSet(pod.Spec.NodeName, pod.UID, cpuset, resourceSpec.PreferredCPUExclusivePolicy)
}

func (c *podEventHandler) deletePod(pod *corev1.Pod) {
	if pod.Spec.NodeName == "" {
		return
	}

	resourceStatus, err := GetResourceStatus(pod.Annotations)
	if err != nil {
		return
	}
	cpuset, err := Parse(resourceStatus.CPUSet)
	if err != nil || cpuset.IsEmpty() {
		return
	}

	c.cpuManager.Free(pod.Spec.NodeName, pod.UID)
}

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

package controller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/api/v1/resource"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func (c *Controller) onPodAdd(obj interface{}) {
	pod, _ := obj.(*corev1.Pod)
	if pod == nil {
		return
	}
	needUpdate, rAllocated := c.enqueueReservationIfPodUpdate(nil, pod)
	if !needUpdate {
		return
	}
	c.updatePod(pod, rAllocated)
}

func (c *Controller) onPodUpdate(oldObj, newObj interface{}) {
	oldPod, _ := oldObj.(*corev1.Pod)
	newPod, _ := newObj.(*corev1.Pod)
	if newPod == nil {
		return
	}
	needUpdate, rAllocated := c.enqueueReservationIfPodUpdate(oldPod, newPod)
	if !needUpdate {
		return
	}
	c.updatePod(newPod, rAllocated)
}

func (c *Controller) onPodDelete(obj interface{}) {
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
	needUpdate, _ := c.enqueueReservationIfPodUpdate(pod, nil)
	if !needUpdate {
		return
	}
	c.deletePod(pod)
}

// handle the following updates of the pod:
// 1. reservation ownership changed
// 2. assigned node changed
// 3. resource requests changed
func (c *Controller) enqueueReservationIfPodUpdate(oldPod, newPod *corev1.Pod) (bool, *apiext.ReservationAllocated) {
	if oldPod == nil && newPod == nil {
		return false, nil
	}
	var oldRAllocated, newRAllocated *apiext.ReservationAllocated
	if oldPod != nil {
		oldRAllocated, _ = apiext.GetReservationAllocated(oldPod)
	}
	if newPod != nil {
		newRAllocated, _ = apiext.GetReservationAllocated(newPod)
	}
	if oldRAllocated == nil && newRAllocated == nil {
		return false, newRAllocated
	}
	if oldRAllocated == nil {
		c.enqueueReservation(newRAllocated)
		return true, newRAllocated
	}
	if newRAllocated == nil {
		c.enqueueReservation(oldRAllocated)
		return true, newRAllocated
	}
	if oldRAllocated.UID != newRAllocated.UID || oldRAllocated.Name != newRAllocated.Name {
		c.enqueueReservation(newRAllocated)
		return true, newRAllocated
	}

	// assert pod non-nil
	if oldPod.Spec.NodeName != newPod.Spec.NodeName {
		c.enqueueReservation(newRAllocated)
		return true, newRAllocated
	}

	oldRequests := resource.PodRequests(oldPod, resource.PodResourcesOptions{})
	newRequests := resource.PodRequests(newPod, resource.PodResourcesOptions{})
	if !quotav1.Equals(oldRequests, newRequests) {
		c.enqueueReservation(newRAllocated)
		return true, newRAllocated
	}

	return false, newRAllocated
}

func (c *Controller) enqueueReservation(rAllocated *apiext.ReservationAllocated) {
	if rAllocated == nil || rAllocated.Name == "" {
		return
	}
	c.queue.Add(rAllocated.Name)
}

func (c *Controller) updatePod(pod *corev1.Pod, rAllocated *apiext.ReservationAllocated) {
	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	podsOnNode := c.pods[nodeName]
	if podsOnNode == nil {
		podsOnNode = map[types.UID]*corev1.Pod{}
		c.pods[nodeName] = podsOnNode
	}
	podsOnNode[pod.UID] = pod

	if !c.enableSyncReservationDeletion { // no need to maintain the reservation-to-pod mapping
		return
	}
	if oldR := c.podToR[pod.UID]; oldR != "" {
		delete(c.podToR, pod.UID)
		podsOnOldR := c.rToPod[oldR]
		if len(podsOnOldR) > 0 {
			delete(podsOnOldR, pod.UID)
		}
		if len(podsOnOldR) == 0 {
			delete(c.rToPod, oldR)
		}
	}
	if rAllocated == nil {
		return
	}
	c.podToR[pod.UID] = rAllocated.Name
	podsOnReservation := c.rToPod[rAllocated.Name]
	if podsOnReservation == nil {
		podsOnReservation = map[types.UID]*corev1.Pod{}
		c.rToPod[rAllocated.Name] = podsOnReservation
	}
	podsOnReservation[pod.UID] = pod
}

func (c *Controller) deletePod(pod *corev1.Pod) {
	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	pods := c.pods[nodeName]
	delete(pods, pod.UID)
	if len(pods) == 0 {
		delete(c.pods, nodeName)
	}

	if !c.enableSyncReservationDeletion { // no need to maintain the reservation-to-pod mapping
		return
	}
	if oldR := c.podToR[pod.UID]; oldR != "" {
		podsOnOldR := c.rToPod[oldR]
		if len(podsOnOldR) > 0 {
			delete(podsOnOldR, pod.UID)
		}
		if len(podsOnOldR) == 0 {
			delete(c.rToPod, oldR)
		}
	}
	delete(c.podToR, pod.UID)
}

func (c *Controller) getPodsOnNode(nodeName string) map[types.UID]*corev1.Pod {
	c.lock.RLock()
	defer c.lock.RUnlock()

	pods := c.pods[nodeName]
	if len(pods) == 0 {
		return nil
	}
	m := make(map[types.UID]*corev1.Pod)
	for k, v := range pods {
		m[k] = v
	}
	return m
}

func (c *Controller) getPodsOnReservation(rName string) map[types.UID]*corev1.Pod {
	c.lock.RLock()
	defer c.lock.RUnlock()

	pods := c.rToPod[rName]
	if len(pods) == 0 {
		return nil
	}
	m := make(map[types.UID]*corev1.Pod)
	for k, v := range pods {
		m[k] = v
	}
	return m
}

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
	"k8s.io/client-go/tools/cache"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func (c *Controller) onPodAdd(obj interface{}) {
	pod, _ := obj.(*corev1.Pod)
	if pod == nil {
		return
	}
	rAllocated := c.enqueueIfPodBoundReservation(pod)
	c.updatePod(pod, rAllocated)
}

func (c *Controller) onPodUpdate(oldObj, newObj interface{}) {
	newPod, _ := newObj.(*corev1.Pod)
	if newPod == nil {
		return
	}
	oldPod, _ := oldObj.(*corev1.Pod)
	if oldPod == nil {
		return
	}
	if newPod.Spec.NodeName != "" {
		rAllocated := c.enqueueIfPodBoundReservation(newPod)
		c.updatePod(newPod, rAllocated)
	} else if oldPod.Spec.NodeName != "" { // become unassigned, a special case for multi-scheduler
		_ = c.enqueueIfPodBoundReservation(oldPod)
		c.deletePod(oldPod)
	}
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
	_ = c.enqueueIfPodBoundReservation(pod)
	c.deletePod(pod)
}

// enqueueIfPodBoundReservation will enqueue the reservation and  return the reservation allocated
// if the pod is bound to the reservation
func (c *Controller) enqueueIfPodBoundReservation(pod *corev1.Pod) *apiext.ReservationAllocated {
	if pod.Spec.NodeName == "" {
		return nil
	}

	reservationAllocated, err := apiext.GetReservationAllocated(pod)
	if err != nil || reservationAllocated == nil || reservationAllocated.Name == "" {
		return nil
	}

	c.queue.Add(getReservationKeyByAllocated(reservationAllocated))
	return reservationAllocated
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
	if oldR := c.podToR[pod.UID]; oldR != "" {
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
	c.podToR[pod.UID] = rAllocated.UID
	podsOnReservation := c.rToPod[rAllocated.UID]
	if podsOnReservation == nil {
		podsOnReservation = map[types.UID]*corev1.Pod{}
		c.rToPod[rAllocated.UID] = podsOnReservation
	}
	podsOnReservation[pod.UID] = pod
}

func (c *Controller) deletePod(pod *corev1.Pod) {
	nodeName := pod.Spec.NodeName
	if pod.Spec.NodeName == "" {
		return
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	pods := c.pods[nodeName]
	delete(pods, pod.UID)
	if len(pods) == 0 {
		delete(c.pods, nodeName)
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
	m := make(map[types.UID]*corev1.Pod, len(pods))
	for k, v := range pods {
		m[k] = v
	}
	return m
}

func (c *Controller) getPodsOnReservation(rUID types.UID) map[types.UID]*corev1.Pod {
	c.lock.RLock()
	defer c.lock.RUnlock()
	pods := c.rToPod[rUID]
	if len(pods) == 0 {
		return nil
	}
	m := make(map[types.UID]*corev1.Pod)
	for k, v := range pods {
		m[k] = v
	}
	return m
}

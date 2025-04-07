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
	"fmt"
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulinglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// TODO(joseph): Considering the amount of changed code,
// temporarily use global variable to store ReservationCache instance,
// and then refactor to separate ReservationCache later.
// TODO: move to the frameworkext
var theReservationCache atomic.Value

type ReservationCache interface {
	DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo
	GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo
	GetReservationInfo(uid types.UID) *frameworkext.ReservationInfo
}

func GetReservationCache() ReservationCache {
	cache, _ := theReservationCache.Load().(ReservationCache)
	if cache == nil {
		return nil
	}
	return cache
}

func SetReservationCache(cache ReservationCache) {
	theReservationCache.Store(cache)
}

type FakeReservationCache struct {
	RInfo *frameworkext.ReservationInfo
}

func (f *FakeReservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo {
	if f.RInfo != nil {
		return f.RInfo
	}
	return frameworkext.NewReservationInfo(r)
}

func (f *FakeReservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	return f.RInfo
}

func (f *FakeReservationCache) GetReservationInfo(uid types.UID) *frameworkext.ReservationInfo {
	return f.RInfo
}

type reservationCache struct {
	reservationLister  schedulinglister.ReservationLister
	lock               sync.RWMutex
	reservationInfos   map[types.UID]*frameworkext.ReservationInfo
	reservationsOnNode map[string]map[types.UID]struct{}
	podsToR            map[types.UID]types.UID
}

func newReservationCache(reservationLister schedulinglister.ReservationLister) *reservationCache {
	cache := &reservationCache{
		reservationLister:  reservationLister,
		reservationInfos:   map[types.UID]*frameworkext.ReservationInfo{},
		reservationsOnNode: map[string]map[types.UID]struct{}{},
		podsToR:            map[types.UID]types.UID{},
	}
	return cache
}

func (cache *reservationCache) AssumeReservation(r *schedulingv1alpha1.Reservation) {
	cache.UpdateReservation(r)
}

func (cache *reservationCache) ForgetReservation(r *schedulingv1alpha1.Reservation) {
	cache.DeleteReservation(r)
}

func (cache *reservationCache) UpdateReservation(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo == nil {
		rInfo = frameworkext.NewReservationInfo(newR)
		cache.reservationInfos[newR.UID] = rInfo
	} else {
		rInfo.UpdateReservation(newR)
	}
	if newR.Status.NodeName != "" {
		cache.updateReservationsOnNode(newR.Status.NodeName, newR.UID)
	}
}

func (cache *reservationCache) UpdateReservationIfExists(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo != nil {
		rInfo.UpdateReservation(newR)
		if newR.Status.NodeName != "" {
			cache.updateReservationsOnNode(newR.Status.NodeName, newR.UID)
		}
	}
}

func (cache *reservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[r.UID]
	delete(cache.reservationInfos, r.UID)
	cache.deleteReservationOnNode(r.Status.NodeName, r.UID)
	for podUID := range rInfo.AssignedPods {
		delete(cache.podsToR, podUID)
	}
	return rInfo
}

func (cache *reservationCache) UpdateReservationOperatingPod(newPod *corev1.Pod, currentOwner *corev1.ObjectReference) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newPod.UID]
	if rInfo == nil {
		rInfo = frameworkext.NewReservationInfoFromPod(newPod)
		cache.reservationInfos[newPod.UID] = rInfo
	} else {
		rInfo.UpdatePod(newPod)
	}
	if newPod.Spec.NodeName != "" {
		cache.updateReservationsOnNode(newPod.Spec.NodeName, newPod.UID)
	}
	if currentOwner != nil {
		rInfo.AddAssignedPod(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      currentOwner.Name,
				Namespace: currentOwner.Namespace,
				UID:       currentOwner.UID,
			},
		})
		cache.podsToR[currentOwner.UID] = newPod.UID
	}
}

func (cache *reservationCache) DeleteReservationOperatingPod(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[pod.UID]
	delete(cache.reservationInfos, pod.UID)
	cache.deleteReservationOnNode(pod.Spec.NodeName, pod.UID)
	if rInfo != nil {
		for podUID := range rInfo.AssignedPods {
			delete(cache.podsToR, podUID)
		}
	}
}

func (cache *reservationCache) SetReservationInfo(rInfo *frameworkext.ReservationInfo) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	cache.reservationInfos[rInfo.UID()] = rInfo
	if rInfo.GetNodeName() != "" {
		cache.updateReservationsOnNode(rInfo.GetNodeName(), rInfo.UID())
	}
}

func (cache *reservationCache) AssumePod(reservationUID types.UID, pod *corev1.Pod) error {
	return cache.AddPod(reservationUID, pod)
}

func (cache *reservationCache) ForgetPod(reservationUID types.UID, pod *corev1.Pod) {
	cache.DeletePod(reservationUID, pod)
}

func (cache *reservationCache) AddPod(reservationUID types.UID, pod *corev1.Pod) error {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo == nil {
		return fmt.Errorf("cannot find target reservation")
	}
	if rInfo.IsTerminating() {
		return fmt.Errorf("target reservation is terminating")
	}
	rInfo.AddAssignedPod(pod)
	cache.podsToR[pod.UID] = reservationUID
	return nil
}

func (cache *reservationCache) UpdatePod(reservationUID types.UID, oldPod, newPod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		if oldPod != nil {
			rInfo.RemoveAssignedPod(oldPod)
			delete(cache.podsToR, oldPod.UID)
		}
		if newPod != nil {
			rInfo.AddAssignedPod(newPod)
			cache.podsToR[newPod.UID] = reservationUID
		}
	}
}

func (cache *reservationCache) DeletePod(reservationUID types.UID, pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		rInfo.RemoveAssignedPod(pod)
		delete(cache.podsToR, pod.UID)
	}
}

func (cache *reservationCache) GetReservationInfoByUID(uid types.UID) *frameworkext.ReservationInfo {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rInfo := cache.reservationInfos[uid]
	if rInfo != nil {
		return rInfo.Clone()
	}
	return nil
}

func (cache *reservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	target := cache.GetReservationInfoByPodUID(pod.UID)
	if target != nil {
		return target
	}

	cache.ForEachAvailableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
		if _, ok := rInfo.AssignedPods[pod.UID]; ok {
			target = rInfo
			return false, nil
		}
		return true, nil
	})
	return target
}

func (cache *reservationCache) GetReservationInfo(uid types.UID) *frameworkext.ReservationInfo {
	return cache.GetReservationInfoByUID(uid)
}

func (cache *reservationCache) GetReservationInfoByPodUID(podUID types.UID) *frameworkext.ReservationInfo {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	reservationUID, ok := cache.podsToR[podUID]
	if !ok {
		return nil
	}
	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		return rInfo.Clone()
	}
	return nil
}

func (cache *reservationCache) ForEachAvailableReservationOnNode(nodeName string, fn func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status)) *framework.Status {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rOnNode := cache.reservationsOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	for uid := range rOnNode {
		rInfo := cache.reservationInfos[uid]
		if rInfo != nil && rInfo.IsAvailable() {
			beContinue, status := fn(rInfo)
			if !status.IsSuccess() {
				return status
			}
			if !beContinue {
				return nil
			}
		}
	}
	return nil
}

func (cache *reservationCache) ListAvailableReservationInfosOnNode(nodeName string) []*frameworkext.ReservationInfo {
	var result []*frameworkext.ReservationInfo
	cache.ForEachAvailableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
		result = append(result, rInfo.Clone())
		return true, nil
	})
	return result
}

func (cache *reservationCache) ListAllNodes() []string {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	if len(cache.reservationsOnNode) == 0 {
		return nil
	}
	nodes := make([]string, 0, len(cache.reservationsOnNode))
	for k := range cache.reservationsOnNode {
		nodes = append(nodes, k)
	}
	return nodes
}

func (cache *reservationCache) updateReservationsOnNode(nodeName string, uid types.UID) {
	if nodeName == "" {
		return
	}

	reservations := cache.reservationsOnNode[nodeName]
	if reservations == nil {
		reservations = map[types.UID]struct{}{}
		cache.reservationsOnNode[nodeName] = reservations
	}
	reservations[uid] = struct{}{}
}

func (cache *reservationCache) deleteReservationOnNode(nodeName string, uid types.UID) {
	if nodeName == "" {
		return
	}
	reservations := cache.reservationsOnNode[nodeName]
	delete(reservations, uid)
	if len(reservations) == 0 {
		delete(cache.reservationsOnNode, nodeName)
	}
}

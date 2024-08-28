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
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulinglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// TODO(joseph): Considering the amount of changed code,
// temporarily use global variable to store ReservationCache instance,
// and then refactor to separate ReservationCache later.
var theReservationCache atomic.Value

type ReservationCache interface {
	DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo
	// this reservation info is only
	GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo
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

type reservationCache struct {
	reservationLister   schedulinglister.ReservationLister
	lock                sync.RWMutex
	reservationsOnNode  map[string]map[types.UID]struct{}
	reservationInfos    map[types.UID]*reservationInfoListItem
	headReservationInfo *reservationInfoListItem
	snapshot            *Snapshot
}

type reservationInfoListItem struct {
	info *frameworkext.ReservationInfo
	next *reservationInfoListItem
	prev *reservationInfoListItem
}

// newNodeInfoListItem initializes a new nodeInfoListItem.
func newReservationInfoListItem(rInfo *frameworkext.ReservationInfo) *reservationInfoListItem {
	return &reservationInfoListItem{
		info: rInfo,
	}
}

func (cache *reservationCache) moveReservationInfoToHead(uid types.UID) {
	rInfoItem, ok := cache.reservationInfos[uid]
	if !ok {
		klog.ErrorS(nil, "No  info with given name found in the cache", "node", klog.KRef("", string(uid)))
		return
	}
	// if the node info list item is already at the head, we are done.
	if rInfoItem == cache.headReservationInfo {
		return
	}

	if rInfoItem.prev != nil {
		rInfoItem.prev.next = rInfoItem.next
	}
	if rInfoItem.next != nil {
		rInfoItem.next.prev = rInfoItem.prev
	}
	if cache.headReservationInfo != nil {
		cache.headReservationInfo.prev = rInfoItem
	}
	rInfoItem.next = cache.headReservationInfo
	rInfoItem.prev = nil
	cache.headReservationInfo = rInfoItem
}

// removeReservationInfoFromList removes a NodeInfo from the "cache.nodes" doubly
// linked list.
// We assume cache lock is already acquired.
func (cache *reservationCache) removeReservationInfoFromList(uid types.UID) {
	rInfo, ok := cache.reservationInfos[uid]
	if !ok {
		klog.ErrorS(nil, "No node info with given name found in the cache", "node", klog.KRef("", string(uid)))
		return
	}

	if rInfo.prev != nil {
		rInfo.prev.next = rInfo.next
	}
	if rInfo.next != nil {
		rInfo.next.prev = rInfo.prev
	}
	// if the removed item was at the head, we must update the head.
	if rInfo == cache.headReservationInfo {
		cache.headReservationInfo = rInfo.next
	}
	delete(cache.reservationInfos, uid)
}

func newReservationCache(reservationLister schedulinglister.ReservationLister) *reservationCache {
	cache := &reservationCache{
		reservationLister:  reservationLister,
		reservationInfos:   map[types.UID]*reservationInfoListItem{},
		reservationsOnNode: map[string]map[types.UID]struct{}{},
		snapshot:           NewEmptySnapshot(),
	}
	return cache
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

func (cache *reservationCache) assumeReservation(r *schedulingv1alpha1.Reservation) {
	cache.updateReservation(r)
}

func (cache *reservationCache) forgetReservation(r *schedulingv1alpha1.Reservation) {
	cache.DeleteReservation(r)
}

func (cache *reservationCache) updateReservation(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo == nil {
		rInfo = newReservationInfoListItem(frameworkext.NewReservationInfo(newR))
		cache.reservationInfos[newR.UID] = rInfo
	} else {
		rInfo.info.UpdateReservation(newR)
	}
	cache.moveReservationInfoToHead(newR.UID)
	if newR.Status.NodeName != "" {
		cache.updateReservationsOnNode(newR.Status.NodeName, newR.UID)
	}
}

func (cache *reservationCache) updateReservationIfExists(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo != nil {
		rInfo.info.UpdateReservation(newR)
		cache.moveReservationInfoToHead(newR.UID)
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
	cache.removeReservationInfoFromList(r.UID)
	cache.deleteReservationOnNode(r.Status.NodeName, r.UID)
	return rInfo.info
}

func (cache *reservationCache) updateReservationOperatingPod(newPod *corev1.Pod, currentOwner *corev1.ObjectReference) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newPod.UID]
	if rInfo == nil {
		rInfo = newReservationInfoListItem(frameworkext.NewReservationInfoFromPod(newPod))
		cache.reservationInfos[newPod.UID] = rInfo
	} else {
		rInfo.info.UpdatePod(newPod)
	}
	if newPod.Spec.NodeName != "" {
		cache.updateReservationsOnNode(newPod.Spec.NodeName, newPod.UID)
	}
	if currentOwner != nil {
		rInfo.info.AddAssignedPod(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      currentOwner.Name,
				Namespace: currentOwner.Namespace,
				UID:       currentOwner.UID,
			},
		})
	}
}

func (cache *reservationCache) deleteReservationOperatingPod(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	delete(cache.reservationInfos, pod.UID)
	cache.removeReservationInfoFromList(pod.UID)
	cache.deleteReservationOnNode(pod.Spec.NodeName, pod.UID)
}

func (cache *reservationCache) assumePod(reservationUID types.UID, pod *corev1.Pod) error {
	return cache.addPod(reservationUID, pod)
}

func (cache *reservationCache) forgetPod(reservationUID types.UID, pod *corev1.Pod) {
	cache.deletePod(reservationUID, pod)
}

func (cache *reservationCache) addPod(reservationUID types.UID, pod *corev1.Pod) error {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo == nil {
		return fmt.Errorf("cannot find target reservation")
	}
	if rInfo.info.IsTerminating() {
		return fmt.Errorf("target reservation is terminating")
	}
	rInfo.info.AddAssignedPod(pod)
	cache.moveReservationInfoToHead(reservationUID)
	return nil
}

func (cache *reservationCache) updatePod(reservationUID types.UID, oldPod, newPod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		if oldPod != nil {
			rInfo.info.RemoveAssignedPod(oldPod)
		}
		if newPod != nil {
			rInfo.info.AddAssignedPod(newPod)
		}
	}
	cache.moveReservationInfoToHead(reservationUID)
}

func (cache *reservationCache) deletePod(reservationUID types.UID, pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		rInfo.info.RemoveAssignedPod(pod)
		cache.moveReservationInfoToHead(reservationUID)
	}
}

func (cache *reservationCache) getReservationInfo(name string) *frameworkext.ReservationInfo {
	reservation, err := cache.reservationLister.Get(name)
	if err != nil {
		return nil
	}
	return cache.getReservationInfoByUID(reservation.UID)
}

func (cache *reservationCache) getReservationInfoByUID(uid types.UID) *frameworkext.ReservationInfo {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rInfo := cache.reservationInfos[uid]
	if rInfo != nil {
		return rInfo.info.Clone()
	}
	return nil
}

func (cache *reservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	var target *frameworkext.ReservationInfo
	cache.snapshot.forEachAvailableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
		if _, ok := rInfo.AssignedPods[pod.UID]; ok {
			target = rInfo
			return false, nil
		}
		return true, nil
	})
	return target
}

func (cache *reservationCache) forEachAvailableReservationOnNode(nodeName string, fn func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status)) *framework.Status {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rOnNode := cache.reservationsOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	for uid := range rOnNode {
		rInfo := cache.reservationInfos[uid]
		if rInfo != nil && rInfo.info.IsAvailable() {
			beContinue, status := fn(rInfo.info)
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

func (cache *reservationCache) listAvailableReservationInfosOnNode(nodeName string) []*frameworkext.ReservationInfo {
	var result []*frameworkext.ReservationInfo
	cache.forEachAvailableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
		result = append(result, rInfo.Clone())
		return true, nil
	})
	return result
}

func (cache *reservationCache) listAllNodes() []string {
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

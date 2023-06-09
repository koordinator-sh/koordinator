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
	reservationLister  schedulinglister.ReservationLister
	lock               sync.Mutex
	reservationInfos   map[types.UID]*frameworkext.ReservationInfo
	reservationsOnNode map[string]map[types.UID]struct{}
}

func newReservationCache(reservationLister schedulinglister.ReservationLister) *reservationCache {
	cache := &reservationCache{
		reservationLister:  reservationLister,
		reservationInfos:   map[types.UID]*frameworkext.ReservationInfo{},
		reservationsOnNode: map[string]map[types.UID]struct{}{},
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
		rInfo = frameworkext.NewReservationInfo(newR)
		cache.reservationInfos[newR.UID] = rInfo
	} else {
		rInfo.UpdateReservation(newR)
	}
	if newR.Status.NodeName != "" {
		cache.updateReservationsOnNode(newR.Status.NodeName, newR.UID)
	}
}

func (cache *reservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[r.UID]
	delete(cache.reservationInfos, r.UID)
	cache.deleteReservationOnNode(r.Status.NodeName, r.UID)
	return rInfo
}

func (cache *reservationCache) updateReservationOperatingPod(newPod *corev1.Pod, currentOwner *corev1.ObjectReference) {
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
	}
}

func (cache *reservationCache) deleteReservationOperatingPod(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	delete(cache.reservationInfos, pod.UID)
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
	if rInfo.IsTerminating() {
		return fmt.Errorf("target reservation is terminating")
	}
	rInfo.AddAssignedPod(pod)
	return nil
}

func (cache *reservationCache) updatePod(reservationUID types.UID, oldPod, newPod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		if oldPod != nil {
			rInfo.RemoveAssignedPod(oldPod)
		}
		if newPod != nil {
			rInfo.AddAssignedPod(newPod)
		}
	}
}

func (cache *reservationCache) deletePod(reservationUID types.UID, pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		rInfo.RemoveAssignedPod(pod)
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
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[uid]
	if rInfo != nil {
		return rInfo.Clone()
	}
	return nil
}

func (cache *reservationCache) listAvailableReservationInfosOnNode(nodeName string) []*frameworkext.ReservationInfo {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rOnNode := cache.reservationsOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	result := make([]*frameworkext.ReservationInfo, 0, len(rOnNode))
	for uid := range rOnNode {
		rInfo := cache.reservationInfos[uid]
		if rInfo != nil && rInfo.IsAvailable() {
			result = append(result, rInfo.Clone())
		}
	}
	return result
}

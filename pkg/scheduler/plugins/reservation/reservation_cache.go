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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	corelister "k8s.io/client-go/listers/core/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulinglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
)

type reservationCache struct {
	podLister          corelister.PodLister
	reservationLister  schedulinglister.ReservationLister
	lock               sync.Mutex
	reservationInfos   map[types.UID]*reservationInfo
	reservationsOnNode map[string]map[types.UID]*schedulingv1alpha1.Reservation
}

func newReservationCache(sharedInformerFactory informers.SharedInformerFactory, koordinatorInformerFactory koordinatorinformers.SharedInformerFactory) *reservationCache {
	podLister := sharedInformerFactory.Core().V1().Pods().Lister()
	reservationInterface := koordinatorInformerFactory.Scheduling().V1alpha1().Reservations()
	reservationLister := reservationInterface.Lister()
	mgr := &reservationCache{
		podLister:          podLister,
		reservationLister:  reservationLister,
		reservationInfos:   map[types.UID]*reservationInfo{},
		reservationsOnNode: map[string]map[types.UID]*schedulingv1alpha1.Reservation{},
	}
	registerReservationEventHandler(mgr, koordinatorInformerFactory)
	registerPodEventHandler(mgr, sharedInformerFactory)
	return mgr
}

func (mgr *reservationCache) updateReservationsOnNode(nodeName string, r *schedulingv1alpha1.Reservation) {
	if nodeName == "" {
		return
	}

	reservations := mgr.reservationsOnNode[r.Status.NodeName]
	if reservations == nil {
		reservations = map[types.UID]*schedulingv1alpha1.Reservation{}
		mgr.reservationsOnNode[r.Status.NodeName] = reservations
	}
	reservations[r.UID] = r
}

func (mgr *reservationCache) deleteReservationOnNode(nodeName string, r *schedulingv1alpha1.Reservation) {
	if nodeName == "" {
		return
	}
	reservations := mgr.reservationsOnNode[nodeName]
	delete(reservations, r.UID)
	if len(reservations) == 0 {
		delete(mgr.reservationsOnNode, nodeName)
	}
}

func (mgr *reservationCache) addReservation(r *schedulingv1alpha1.Reservation) {
	mgr.updateReservation(r)
}

func (mgr *reservationCache) updateReservation(newR *schedulingv1alpha1.Reservation) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()
	rInfo := mgr.reservationInfos[newR.UID]
	if rInfo == nil {
		rInfo = newReservationInfo(newR)
		mgr.reservationInfos[newR.UID] = rInfo
	} else {
		rInfo.updateReservation(newR)
	}
	if newR.Status.NodeName != "" {
		mgr.updateReservationsOnNode(newR.Status.NodeName, newR)
	}
}

func (mgr *reservationCache) deleteReservation(r *schedulingv1alpha1.Reservation) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()
	delete(mgr.reservationInfos, r.UID)
	mgr.deleteReservationOnNode(r.Status.NodeName, r)
}

func (mgr *reservationCache) assumePod(reservationUID types.UID, pod *corev1.Pod) {
	mgr.addPod(reservationUID, pod)
}

func (mgr *reservationCache) forgetPod(reservationUID types.UID, pod *corev1.Pod) {
	mgr.deleteAllocatedPod(reservationUID, pod)
}

func (mgr *reservationCache) addPod(reservationUID types.UID, pod *corev1.Pod) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()

	rInfo := mgr.reservationInfos[reservationUID]
	if rInfo != nil {
		rInfo.addPod(pod)
	}
}

func (mgr *reservationCache) updatePod(reservationUID types.UID, oldPod, newPod *corev1.Pod) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()

	rInfo := mgr.reservationInfos[reservationUID]
	if rInfo != nil {
		rInfo.removePod(oldPod)
		rInfo.addPod(newPod)
	}
}

func (mgr *reservationCache) deleteAllocatedPod(reservationUID types.UID, pod *corev1.Pod) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()

	rInfo := mgr.reservationInfos[reservationUID]
	if rInfo != nil {
		rInfo.removePod(pod)
	}
}

func (mgr *reservationCache) getReservationInfo(name string) *reservationInfo {
	reservation, err := mgr.reservationLister.Get(name)
	if err != nil {
		return nil
	}
	return mgr.getReservationInfoByUID(reservation.UID)
}

func (mgr *reservationCache) getReservationInfoByUID(uid types.UID) *reservationInfo {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()
	rInfo := mgr.reservationInfos[uid]
	if rInfo != nil {
		return rInfo.Clone()
	}
	return nil
}

func (mgr *reservationCache) listReservationInfosOnNode(nodeName string) []*reservationInfo {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()
	rOnNode := mgr.reservationsOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	result := make([]*reservationInfo, 0, len(rOnNode))
	for _, v := range rOnNode {
		rInfo := mgr.reservationInfos[v.UID]
		if rInfo != nil {
			result = append(result, rInfo.Clone())
		}
	}
	return result
}

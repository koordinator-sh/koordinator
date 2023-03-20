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
	"context"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	defaultGCCheckInterval = 10 * time.Second
	defaultGCDuration      = 24 * time.Hour
)

func (p *Plugin) gcReservations() {
	rList, err := p.rLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list reservations, abort the GC turn, err: %s", err)
		return
	}
	for _, r := range rList {
		// expire reservations
		// the reserve pods of expired reservations would be dequeue or removed from cache by the scheduler handler.
		if isReservationNeedExpiration(r) {
			// marked as expired in cache even if the reservation is failed to set expired
			if err = p.expireReservation(r); err != nil {
				klog.Warningf("failed to update reservation %s as expired, err: %s", klog.KObj(r), err)
			}
		} else if reservationutil.IsReservationActive(r) {
			// sync active reservation for correct owner statuses
			p.syncActiveReservation(r)
		} else if reservationutil.IsReservationExpired(r) || reservationutil.IsReservationSucceeded(r) {
			// TBD: cleanup orphan reservations
			if !isReservationNeedCleanup(r) {
				continue
			}
			// cleanup expired reservations
			if err = p.client.Reservations().Delete(context.TODO(), r.Name, metav1.DeleteOptions{}); err != nil {
				klog.V(3).InfoS("failed to delete reservation", "reservation", klog.KObj(r), "err", err)
				continue
			}
		}
	}
}

func (p *Plugin) expireReservationOnNode(node *corev1.Node) {
	// assert node != nil
	rOnNode := p.reservationCache.listReservationInfosOnNode(node.Name)
	if len(rOnNode) == 0 {
		klog.V(5).InfoS("skip expire reservation on deleted node", "reason", "no active reservation", "node", node.Name)
		return
	}
	// for reservations scheduled on the deleted node, mark them as expired
	for _, r := range rOnNode {
		if err := p.expireReservation(r.reservation); err != nil {
			klog.Warningf("failed to update reservation %s as expired, err: %s", klog.KObj(r.reservation), err)
		}
	}
}

func (p *Plugin) expireReservation(r *schedulingv1alpha1.Reservation) error {
	// update reservation status
	return util.RetryOnConflictOrTooManyRequests(func() error {
		curR, err := p.rLister.Get(r.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(4).InfoS("reservation not found, abort the update",
					"reservation", klog.KObj(r))
				return nil
			}
			klog.V(3).InfoS("failed to get reservation",
				"reservation", klog.KObj(r), "err", err)
			return err
		}

		curR = curR.DeepCopy()
		setReservationExpired(curR)
		_, err = p.client.Reservations().UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
		return err
	})
}

func (p *Plugin) syncActiveReservation(r *schedulingv1alpha1.Reservation) {
	var actualOwners []corev1.ObjectReference
	var actualAllocated corev1.ResourceList
	rInfo := p.reservationCache.getReservationInfoByUID(r.UID)
	if rInfo == nil {
		return
	}
	for _, pod := range rInfo.pods {
		actualOwners = append(actualOwners, corev1.ObjectReference{
			Namespace: pod.namespace,
			Name:      pod.name,
			UID:       pod.uid,
		})
		actualAllocated = quotav1.Add(actualAllocated, pod.requests)
	}

	if reflect.DeepEqual(r.Status.CurrentOwners, actualOwners) && quotav1.Equals(actualAllocated, r.Status.Allocated) {
		return
	}

	newR := r.DeepCopy()
	actualAllocated = quotav1.Mask(actualAllocated, quotav1.ResourceNames(r.Status.Allocatable))
	newR.Status.Allocated = actualAllocated
	newR.Status.CurrentOwners = actualOwners
	// if failed to update, abort and let the next event reconcile
	_, err := p.client.Reservations().UpdateStatus(context.TODO(), newR, metav1.UpdateOptions{})
	if err != nil {
		klog.V(3).InfoS("failed to update status for reservation correction", "reservation", klog.KObj(r), "err", err)
	} else {
		klog.V(5).InfoS("update active reservation for status correction", "reservation", klog.KObj(r))
	}
}

func (p *Plugin) syncPodDeleted(pod *corev1.Pod) {
	reservationAllocated, err := apiext.GetReservationAllocated(pod)
	if reservationAllocated == nil {
		if err != nil {
			klog.Errorf("Failed to GetReservationAllocated, pod %v, err: %v", klog.KObj(pod), err)
		}
		return
	}

	rInfo := p.reservationCache.getReservationInfoByUID(reservationAllocated.UID)
	if rInfo == nil {
		return
	}
	reservation := rInfo.reservation

	// pod has allocated reservation, should remove allocation info in the reservation
	err = util.RetryOnConflictOrTooManyRequests(func() error {
		r, err := p.rLister.Get(reservation.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(5).InfoS("skip sync for reservation not found", "reservation", klog.KObj(r))
				return nil
			}
			klog.V(4).ErrorS(err, "failed to get reservation", "reservation", klog.KObj(reservation))
			return err
		}

		// check if the reservation is still scheduled; succeeded ones are ignored to update
		if !reservationutil.IsReservationAvailable(r) {
			klog.V(4).InfoS("skip sync for reservation no longer available or scheduled",
				"reservation", klog.KObj(r))
			return nil
		}
		// got different versions of the reservation; still check if the reservation was allocated by this pod
		if r.UID != reservation.UID {
			klog.V(4).InfoS("failed to get original reservation, got reservation with a different UID",
				"reservation", reservation.Name, "old UID", reservation.UID, "current UID", r.UID)
		}
		curR := r.DeepCopy()
		err = removeReservationAllocated(curR, pod)
		if err != nil {
			klog.V(5).InfoS("failed to remove reservation allocated",
				"reservation", klog.KObj(curR), "pod", klog.KObj(pod), "err", err)
			return nil
		}

		_, err = p.client.Reservations().UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		klog.Warningf("failed to sync pod deletion for reservation, pod %v, err: %v", klog.KObj(pod), err)
	} else {
		klog.V(5).InfoS("sync pod deletion for reservation successfully", "pod", klog.KObj(pod))
	}
}

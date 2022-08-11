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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	defaultGCCheckInterval = 60 * time.Second
	defaultGCDuration      = 24 * time.Hour
)

func (p *Plugin) gcReservations() {
	rList, err := p.lister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list reservations, abort the GC turn, err: %s", err)
		return
	}
	for _, r := range rList {
		// expire reservations
		// the reserve pods of expired reservations would be dequeue or removed from cache by the scheduler handler.
		if r.Status.Phase != schedulingv1alpha1.ReservationFailed && isReservationNeedExpiration(r) {
			// marked as expired in cache even if the reservation is failed to set expired
			if err = p.expireReservation(r); err != nil {
				klog.Warningf("failed to update reservation %s as expired, err: %s", klog.KObj(r), err)
			}
		} else if IsReservationExpired(r) {
			p.reservationCache.AddToFailed(r)
		}
	}

	expiredMap := p.reservationCache.GetAllFailed()
	// TBD: cleanup orphan reservations
	for _, r := range expiredMap {
		// cleanup expired reservations
		if isReservationNeedCleanup(r) {
			err = p.client.Reservations().Delete(context.TODO(), r.Name, metav1.DeleteOptions{})
			if err != nil {
				klog.V(3).InfoS("failed to delete reservation", "reservation", klog.KObj(r), "err", err)
				continue
			}

			p.reservationCache.Delete(r)
		}
	}
}

func (p *Plugin) expireReservationOnNode(node *corev1.Node) {
	// assert node != nil
	rOnNode, err := p.informer.GetIndexer().ByIndex(NodeNameIndex, node.Name)
	if err != nil {
		klog.V(4).InfoS("failed to list reservations for node deletion from indexer",
			"node", node.Name, "err", err)
		return
	}
	if len(rOnNode) <= 0 {
		klog.V(5).InfoS("skip expire reservation on deleted node",
			"reason", "no active reservation", "node", node.Name)
		return
	}
	// for reservations scheduled on the deleted node, mark them as expired
	for _, obj := range rOnNode {
		r, ok := obj.(*schedulingv1alpha1.Reservation)
		if !ok {
			klog.V(5).Infof("unable to convert to *schedulingv1alpha1.Reservation, obj %T", obj)
			continue
		}
		if err = p.expireReservation(r); err != nil {
			klog.Warningf("failed to update reservation %s as expired, err: %s", klog.KObj(r), err)
		}
	}
}

func (p *Plugin) expireReservation(r *schedulingv1alpha1.Reservation) error {
	// marked as expired in cache even if the reservation is failed to set expired
	p.reservationCache.AddToFailed(r)
	// update reservation status
	return retryOnConflictOrTooManyRequests(func() error {
		curR, err := p.lister.Get(r.Name)
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("reservation not found, abort the update",
				"reservation", klog.KObj(r))
			return nil
		} else if err != nil {
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

func (p *Plugin) syncPodDeleted(pod *corev1.Pod) {
	// assert pod != nil
	reservationAllocated, err := apiext.GetReservationAllocated(pod)
	if err != nil {
		klog.V(3).InfoS("failed to parse reservation allocation info of the pod",
			"pod", klog.KObj(pod), "err", err)
		return
	}
	// Most pods have no reservation allocated.
	if reservationAllocated == nil {
		return
	}

	// pod has allocated reservation, should remove allocation info in the reservation
	err = retryOnConflictOrTooManyRequests(func() error {
		r, err1 := p.lister.Get(reservationAllocated.Name)
		if errors.IsNotFound(err1) {
			klog.V(5).InfoS("skip sync for reservation not found", "reservation", klog.KObj(r))
			return nil
		} else if err1 != nil {
			klog.V(4).InfoS("failed to get reservation",
				"reservation", klog.KObj(r), "err", err1)
			return err1
		}

		// check if the reservation has been expired
		if !IsReservationScheduled(r) {
			klog.V(4).InfoS("abort sync for reservation is no longer available or scheduled",
				"reservation", klog.KObj(r))
			return nil
		}
		// got different versions of the reservation; still check if the reservation was allocated by this pod
		if r.UID != reservationAllocated.UID {
			klog.V(4).InfoS("failed to get original reservation, got reservation with a different UID",
				"reservation", reservationAllocated.Name, "old UID", reservationAllocated.UID, "current UID", r.UID)
		}
		curR := r.DeepCopy()
		err1 = removeReservationAllocated(curR, pod)
		if err1 != nil {
			klog.V(4).InfoS("failed to remove reservation allocated",
				"reservation", klog.KObj(curR), "pod", klog.KObj(pod), "err", err1)
			return err1
		}

		_, err1 = p.client.Reservations().UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
		return err1
	})
	if err != nil {
		klog.Warningf("failed to sync pod deletion for reservation, pod %v, err: %v", klog.KObj(pod), err)
	} else {
		klog.V(5).InfoS("sync pod deletion for reservation successfully", "pod", klog.KObj(pod))
	}
}

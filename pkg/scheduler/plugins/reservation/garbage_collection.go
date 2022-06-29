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

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	defaultGCCheckInterval = 30 * time.Second
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
		if r.Status.Phase != schedulingv1alpha1.ReservationExpired && isReservationNeedExpiration(r) {
			// marked as expired in cache even if the reservation is failed to set expired
			p.reservationCache.AddToExpired(r)

			err = retryOnConflictOrTooManyRequests(func() error {
				curR, err1 := p.lister.Reservations(r.Namespace).Get(r.Name)
				if errors.IsNotFound(err1) {
					klog.V(4).InfoS("reservation not found, abort the update",
						"reservation", klog.KObj(r))
					return nil
				} else if err1 != nil {
					klog.V(3).InfoS("failed to get reservation",
						"reservation", klog.KObj(r), "err", err1)
					return err1
				}

				curR = curR.DeepCopy()
				setReservationExpired(curR)
				_, err1 = p.client.Reservations(curR.Namespace).UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
				return err1
			})
			if err != nil {
				klog.Warningf("failed to update reservation %s as expired, err: %s", klog.KObj(r), err)
			}
		}
	}

	expiredMap := p.reservationCache.GetAllExpired()
	// TBD: cleanup orphan reservations
	for _, r := range expiredMap {
		// cleanup expired reservations
		if isReservationNeedCleanup(r) {
			err = p.client.Reservations(r.Namespace).Delete(context.TODO(), r.Name, metav1.DeleteOptions{})
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
		p.reservationCache.AddToExpired(r)
		err := retryOnConflictOrTooManyRequests(func() error {
			curR, err1 := p.lister.Reservations(r.Namespace).Get(r.Name)
			if errors.IsNotFound(err1) {
				klog.V(4).InfoS("reservation not found, abort the update",
					"reservation", klog.KObj(r))
				return nil
			} else if err1 != nil {
				klog.V(3).InfoS("failed to get reservation",
					"reservation", klog.KObj(r), "err", err1)
				return err1
			}

			curR = curR.DeepCopy()
			setReservationExpired(curR)
			_, err1 = p.client.Reservations(curR.Namespace).UpdateStatus(context.TODO(), curR, metav1.UpdateOptions{})
			return err1
		})
		if err != nil {
			klog.Warningf("failed to update reservation %s as expired, err: %s", klog.KObj(r), err)
		}
	}
}

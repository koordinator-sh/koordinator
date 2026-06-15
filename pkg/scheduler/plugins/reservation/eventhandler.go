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

	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type reservationEventHandler struct {
	cache       *reservationCache
	rrNominator *nominator
}

func registerReservationEventHandler(cache *reservationCache, koordinatorInformerFactory koordinatorinformers.SharedInformerFactory,
	rrNominator *nominator) {
	eventHandler := &reservationEventHandler{
		cache:       cache,
		rrNominator: rrNominator,
	}
	reservationInformer := koordinatorInformerFactory.Scheduling().V1alpha1().Reservations().Informer()
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), koordinatorInformerFactory, reservationInformer, eventHandler)
}

func (h *reservationEventHandler) OnAdd(obj interface{}, isInInitialList bool) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		return
	}
	if reservationutil.IsReservationActive(r) {
		h.cache.updateReservation(r)
		klog.V(4).InfoS("add reservation into reservationCache",
			"reservation", klog.KObj(r), "uid", r.UID, "node", reservationutil.GetReservationNodeName(r))
	}
	// On scheduler startup or informer re-sync, restore the in-memory nominator from the
	// persisted NominatedNodeName. This ensures that a Reservation that was previously
	// nominated (during preemption) is still tracked in the nominator after a restart.
	// Only applies to Reservations that are still pending (not yet fully scheduled).
	if k8sfeature.DefaultFeatureGate.Enabled(features.ReservationNominatedNodeName) &&
		r.Status.NominatedNodeName != "" &&
		!reservationutil.IsReservationActive(r) &&
		!reservationutil.IsReservationFailed(r) &&
		!reservationutil.IsReservationSucceeded(r) {
		podInfo, err := framework.NewPodInfo(reservationutil.NewReservePod(r))
		if err == nil {
			h.rrNominator.AddNominatedReservePod(podInfo, r.Status.NominatedNodeName)
			klog.V(4).InfoS("Restored NominatedNodeName into in-memory nominator from reservation status",
				"reservation", klog.KObj(r), "nominatedNodeName", r.Status.NominatedNodeName)
		}
	}
}

func (h *reservationEventHandler) OnUpdate(oldObj, newObj interface{}) {
	oldR, oldOK := oldObj.(*schedulingv1alpha1.Reservation)
	newR, newOK := newObj.(*schedulingv1alpha1.Reservation)
	if !oldOK || !newOK {
		return
	}
	if oldR == nil || newR == nil {
		return
	}

	if reservationutil.IsReservationActive(newR) {
		h.cache.updateReservation(newR)
		h.rrNominator.DeleteReservePod(reservationutil.NewReservePod(newR))
		klog.V(4).InfoS("update reservation into reservationCache",
			"reservation", klog.KObj(newR), "uid", newR.UID, "node", reservationutil.GetReservationNodeName(newR))
	} else if reservationutil.IsReservationFailed(newR) || reservationutil.IsReservationSucceeded(newR) {
		// Here it is only marked that ReservationInfo is unavailable,
		// and the real deletion operation is executed in deleteReservationFromCache(pkg/scheduler/frameworkext/eventhandlers/reservation_handler.go).
		// This ensures that the Reserve Pod and the resources it holds are deleted correctly.
		// NOTE: For the update event from available to terminated triggers the deleteReservationFromCache.
		h.cache.updateReservationIfExists(newR)
		klog.V(4).InfoS("update reservation into terminated so only update cache if exists",
			"reservation", klog.KObj(newR), "node", reservationutil.GetReservationNodeName(newR))
		h.rrNominator.DeleteReservePod(reservationutil.NewReservePod(newR))
	}
	// On status updates, re-sync the in-memory nominator from the persisted NominatedNodeName.
	// This handles the case where the failure handler has written the nomination to the API
	// server and the informer update arrives (including after a scheduler restart).
	// We only act on pending Reservations that are not yet fully scheduled or terminated.
	if k8sfeature.DefaultFeatureGate.Enabled(features.ReservationNominatedNodeName) &&
		newR.Status.NominatedNodeName != "" &&
		!reservationutil.IsReservationActive(newR) &&
		!reservationutil.IsReservationFailed(newR) &&
		!reservationutil.IsReservationSucceeded(newR) {
		podInfo, err := framework.NewPodInfo(reservationutil.NewReservePod(newR))
		if err == nil {
			h.rrNominator.AddNominatedReservePod(podInfo, newR.Status.NominatedNodeName)
			klog.V(4).InfoS("Re-synced NominatedNodeName into in-memory nominator on reservation update",
				"reservation", klog.KObj(newR), "nominatedNodeName", newR.Status.NominatedNodeName)
		}
	}
}

func (h *reservationEventHandler) OnDelete(obj interface{}) {
	var r *schedulingv1alpha1.Reservation
	switch t := obj.(type) {
	case *schedulingv1alpha1.Reservation:
		r = t
	case cache.DeletedFinalStateUnknown:
		deletedReservation, ok := t.Obj.(*schedulingv1alpha1.Reservation)
		if ok {
			r = deletedReservation
		}
	}
	if r == nil {
		klog.V(4).InfoS("reservation cache delete failed to parse, obj %T", obj)
		return
	}
	h.rrNominator.DeleteReservePod(reservationutil.NewReservePod(r))

	// Here it is only marked that ReservationInfo is unavailable,
	// and the real deletion operation is executed in deleteReservationFromCache(pkg/scheduler/frameworkext/eventhandlers/reservation_handler.go).
	// This ensures that the Reserve Pod and the resources it holds are deleted correctly.
	if reservationutil.IsReservationAvailable(r) {
		klog.V(4).InfoS("Reservation has been deleted but it's still available, mark it as Failed",
			"reservation", klog.KObj(r), "node", reservationutil.GetReservationNodeName(r))
		r = r.DeepCopy()
		r.Status.Phase = schedulingv1alpha1.ReservationFailed
	}
	h.cache.updateReservationIfExists(r)
	klog.V(4).InfoS("got delete reservation event but just update it if exists",
		"reservation", klog.KObj(r), "uid", r.UID, "node", reservationutil.GetReservationNodeName(r))
}

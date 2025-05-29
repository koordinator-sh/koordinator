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

package mutating

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/webhook/metrics"
)

const (
	ClusterColocationProfile = "ClusterColocationProfile"
)

// ReservationMutatingHandler handles Reservation.
type ReservationMutatingHandler struct {
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder
}

var _ admission.Handler = &ReservationMutatingHandler{}

func shouldIgnoreIfNotReservation(req admission.Request) bool {
	// Ignore all calls to sub resources or resources other than reservations.
	if len(req.AdmissionRequest.SubResource) != 0 ||
		req.AdmissionRequest.Resource.Resource != "reservations" {
		return true
	}
	return false
}

// Handle handles admission requests.
func (h *ReservationMutatingHandler) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	if shouldIgnoreIfNotReservation(req) {
		return admission.Allowed("")
	}

	obj := &schedulingv1alpha1.Reservation{}
	err := h.Decoder.Decode(req, obj)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	clone := obj.DeepCopy()

	switch req.Operation {
	case admissionv1.Create:
		err = h.handleCreate(ctx, req, obj)
	default:
		return admission.Allowed("")
	}

	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if reflect.DeepEqual(obj, clone) {
		return admission.Allowed("")
	}
	marshaled, err := json.Marshal(obj)
	if err != nil {
		klog.Errorf("Failed to marshal mutated Reservation %s/%s, err: %v", obj.Namespace, obj.Name, err)
		return admission.Errored(http.StatusInternalServerError, err)
	}
	original, err := json.Marshal(clone)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(original, marshaled)
}

func (h *ReservationMutatingHandler) handleCreate(ctx context.Context, req admission.Request, obj *schedulingv1alpha1.Reservation) error {
	start := time.Now()
	if err := h.clusterColocationProfileMutatingReservation(ctx, req, obj); err != nil {
		klog.Errorf("Failed to mutating Reservation %s/%s by ClusterColocationProfile, err: %v", obj.Namespace, obj.Name, err)
		metrics.RecordWebhookDurationMilliseconds(metrics.MutatingWebhook,
			metrics.Reservation, string(req.Operation), err, ClusterColocationProfile, time.Since(start).Seconds())
		return err
	}
	metrics.RecordWebhookDurationMilliseconds(metrics.MutatingWebhook,
		metrics.Reservation, string(req.Operation), nil, ClusterColocationProfile, time.Since(start).Seconds())

	return nil
}

// var _ inject.Client = &ReservationMutatingHandler{}

// InjectClient injects the client into the ReservationMutatingHandler
func (h *ReservationMutatingHandler) InjectClient(c client.Client) error {
	h.Client = c
	return nil
}

// var _ admission.DecoderInjector = &ReservationMutatingHandler{}

// InjectDecoder injects the decoder into the ReservationMutatingHandler
func (h *ReservationMutatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}

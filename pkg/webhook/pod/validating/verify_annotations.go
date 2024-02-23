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

package validating

import (
	"context"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func (h *PodValidatingHandler) clusterReservationValidatingPod(ctx context.Context, req admission.Request) (bool, string, error) {
	newPod := &corev1.Pod{}
	var allErrs field.ErrorList
	switch req.Operation {
	case admissionv1.Create:
		if err := h.Decoder.DecodeRaw(req.Object, newPod); err != nil {
			return false, "", err
		}
	case admissionv1.Update:
		oldPod := &corev1.Pod{}
		if err := h.Decoder.DecodeRaw(req.OldObject, oldPod); err != nil {
			return false, "", err
		}
		if err := h.Decoder.DecodeRaw(req.Object, newPod); err != nil {
			return false, "", err
		}

	}

	allErrs = append(allErrs, forbidSpecialAnnotations(newPod)...)
	err := allErrs.ToAggregate()
	allowed := true
	reason := ""
	if err != nil {
		allowed = false
		reason = err.Error()
	}
	return allowed, reason, err
}

var forbidAnnotations = []string{
	extension.AnnotationReservationAllocated,
	reservation.AnnotationReservePod,
}

func forbidSpecialAnnotations(pod *corev1.Pod) field.ErrorList {
	if pod.Annotations == nil {
		return nil
	}
	errorList := field.ErrorList{}

	for _, annotation := range forbidAnnotations {
		if _, ok := pod.Annotations[annotation]; ok {
			errorList = append(errorList, field.Forbidden(field.NewPath("annotations", extension.AnnotationReservationAllocated), "cannot set in annotations"))
		}
	}
	return errorList
}

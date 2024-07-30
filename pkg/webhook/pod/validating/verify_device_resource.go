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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func (h *PodValidatingHandler) deviceResourceValidatingPod(ctx context.Context, req admission.Request) (bool, string, error) {
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

	allErrs = append(allErrs, validateDeviceResource(newPod)...)
	err := allErrs.ToAggregate()
	allowed := true
	reason := ""
	if err != nil {
		allowed = false
		reason = err.Error()
	}

	return allowed, reason, err
}

func validateDeviceResource(pod *corev1.Pod) field.ErrorList {
	allErrs := field.ErrorList{}

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]

		// use gpu api first
		_, gpuExist := container.Resources.Requests[extension.ResourceGPU]
		_, gpuShareExist := container.Resources.Requests[extension.ResourceGPUShared]
		if gpuExist && gpuShareExist {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("pod.spec.containers[*].resources.requests"), "Forbidden declare GPU and GPU share at same time"))
			continue
		}

		// gpu resource will no longer exist
		if gpuExist {
			allErrs = append(allErrs, validateGPU(container)...)
		}

		if gpuShareExist {
			allErrs = append(allErrs, validateGPUShare(container)...)
		}
	}

	return allErrs
}

func validatePercentageResource(q resource.Quantity) bool {
	if q.Value() > 100 && q.Value()%100 != 0 {
		return false
	}

	return true
}

func validateZeroResource(q resource.Quantity) bool {
	if q.Value() != 0 {
		return false
	}

	return true
}

func validateMultiple(a, b resource.Quantity) bool {
	if a.Value()%b.Value() != 0 {
		return false
	}

	return true
}

func validateLess100(q resource.Quantity) bool {
	if q.Value() > 100 {
		return false
	}

	return true
}

func validateGPU(c *corev1.Container) field.ErrorList {
	allErrs := field.ErrorList{}
	gpuQuantity := c.Resources.Requests[extension.ResourceGPU]

	if validateZeroResource(gpuQuantity) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("pod.spec.containers[*].resources.requests"), gpuQuantity.String(), "the requested GPU must be greater than zero"))
	}

	if !validatePercentageResource(gpuQuantity) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("pod.spec.containers[*].resources.requests"), gpuQuantity.String(), "the requested GPU must be percentage of 100"))
	}

	return allErrs
}

func validateGPUShare(c *corev1.Container) field.ErrorList {
	allErrs := field.ErrorList{}
	gpuShareQuantity := c.Resources.Requests[extension.ResourceGPUShared]
	if validateZeroResource(gpuShareQuantity) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("pod.spec.containers[*].resources.requests"), gpuShareQuantity.String(), "the requested GPU must be greater than zero"))
	}

	gpuMemoryQuantity := c.Resources.Requests[extension.ResourceGPUMemory]
	gpuMemoryRatioQuantity := c.Resources.Requests[extension.ResourceGPUMemoryRatio]
	gpuCoreQuantity := c.Resources.Requests[extension.ResourceGPUCore]

	if validateZeroResource(gpuMemoryQuantity) && validateZeroResource(gpuMemoryRatioQuantity) {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("pod.spec.containers[*].resources.requests"), "GPU memory and GPU memory ratio all is zero"))
	}

	if !validateZeroResource(gpuMemoryQuantity) && !validateZeroResource(gpuMemoryRatioQuantity) {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("pod.spec.containers[*].resources.requests"), "declare GPU memory and GPU memory ratio at same time"))
	}

	if !validateZeroResource(gpuShareQuantity) {
		if !validateMultiple(gpuCoreQuantity, gpuShareQuantity) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("pod.spec.containers[*].resources.requests"), gpuCoreQuantity.String(), "the requested gpuCore must multiple of shared"))
		}
		if !validateMultiple(gpuMemoryRatioQuantity, gpuShareQuantity) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("pod.spec.containers[*].resources.requests"), gpuMemoryRatioQuantity.String(), "the requested gpuMemoryRatio must multiple of shared"))
		}
	}

	return allErrs
}

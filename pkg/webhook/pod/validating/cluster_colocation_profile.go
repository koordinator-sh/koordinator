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
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// +kubebuilder:rbac:groups=scheduling.k8s.io,resources=priorityclasses,verbs=get;list;watch

func (h *PodValidatingHandler) clusterColocationProfileValidatingPod(ctx context.Context, req admission.Request) (bool, string, error) {
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

		allErrs = append(allErrs, validateImmutableQoSClass(oldPod, newPod)...)
		allErrs = append(allErrs, validateImmutablePriorityClass(oldPod, newPod)...)
		allErrs = append(allErrs, validateImmutablePriority(oldPod, newPod)...)
	}

	allErrs = append(allErrs, validateRequiredQoSClass(newPod)...)
	allErrs = append(allErrs, forbidSpecialQoSClassAndPriorityClass(newPod, extension.QoSBE, extension.PriorityNone, extension.PriorityProd)...)
	allErrs = append(allErrs, forbidSpecialQoSClassAndPriorityClass(newPod, extension.QoSLSR, extension.PriorityNone, extension.PriorityMid, extension.PriorityBatch, extension.PriorityFree)...)
	allErrs = append(allErrs, validateResources(newPod)...)
	err := allErrs.ToAggregate()
	allowed := true
	reason := ""
	if err != nil {
		allowed = false
		reason = err.Error()
	}
	return allowed, reason, nil
}

func validateRequiredQoSClass(pod *corev1.Pod) field.ErrorList {
	request := util.GetPodRequest(pod)
	batchCPUQuantity := request[extension.BatchCPU]
	batchMemoryQuantity := request[extension.BatchMemory]

	if batchCPUQuantity.IsZero() && batchMemoryQuantity.IsZero() {
		return nil
	}
	qosClass := extension.GetPodQoSClassRaw(pod)
	if qosClass == extension.QoSBE {
		return nil
	}
	return field.ErrorList{field.Required(field.NewPath("labels", extension.LabelPodQoS), "must specify koordinator QoS BE with koordinator colocation resources")}
}

func validateImmutableQoSClass(oldPod, newPod *corev1.Pod) field.ErrorList {
	oldQoSClass := extension.GetPodQoSClassRaw(oldPod)
	newQoSClass := extension.GetPodQoSClassRaw(newPod)
	return validation.ValidateImmutableField(newQoSClass, oldQoSClass, field.NewPath("labels", extension.LabelPodQoS))
}

func validateImmutablePriorityClass(oldPod, newPod *corev1.Pod) field.ErrorList {
	oldPriorityClass := extension.GetPodPriorityClassRaw(oldPod)
	newPriorityClass := extension.GetPodPriorityClassRaw(newPod)
	return validation.ValidateImmutableField(newPriorityClass, oldPriorityClass, field.NewPath("spec.priority"))
}

func validateImmutablePriority(oldPod, newPod *corev1.Pod) field.ErrorList {
	oldPriority := oldPod.Labels[extension.LabelPodPriority]
	newPriority := newPod.Labels[extension.LabelPodPriority]
	return validation.ValidateImmutableField(newPriority, oldPriority, field.NewPath("labels", extension.LabelPodPriority))
}

func forbidSpecialQoSClassAndPriorityClass(pod *corev1.Pod, qoSClass extension.QoSClass, priorityClasses ...extension.PriorityClass) field.ErrorList {
	allErrs := field.ErrorList{}
	if extension.GetPodQoSClassRaw(pod) == qoSClass {
		priorityClass := extension.GetPodPriorityClassRaw(pod)
		found := false
		for _, v := range priorityClasses {
			if priorityClass == v {
				found = true
				break
			}
		}
		if found {
			errDetail := fmt.Sprintf("%s=%s and priorityClass=%s cannot be used in combination", extension.LabelPodQoS, qoSClass, priorityClass)
			allErrs = append(allErrs, field.Forbidden(field.NewPath("Pod"), errDetail))
		}
	}
	return allErrs
}

func validateResources(pod *corev1.Pod) field.ErrorList {
	allErrs := field.ErrorList{}
	qos := extension.GetPodQoSClassRaw(pod)
	if qos == extension.QoSLSR || qos == extension.QoSLSE {
		requests := util.GetPodRequest(pod)
		cpu := requests[corev1.ResourceCPU]
		if cpu.IsZero() {
			allErrs = append(allErrs, field.Required(field.NewPath("pod.spec.containers[*].resources.requests"), "LSR Pod must declare the requested CPUs"))
		} else if cpu.Value()*1000 != cpu.MilliValue() {
			allErrs = append(allErrs, field.Invalid(field.NewPath("pod.spec.containers[*].resources.requests"), cpu.String(), "the requested CPUs of LSR Pod must be integer"))
		}
	}
	return allErrs
}

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
	"net/http"

	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
)

type ElasticQuotaProfileValidatingHandler struct {
	Decoder admission.Decoder
}

var _ admission.Handler = &ElasticQuotaProfileValidatingHandler{}

func (h *ElasticQuotaProfileValidatingHandler) Handle(ctx context.Context, request admission.Request) admission.Response {
	if request.AdmissionRequest.Resource.Resource != "elasticquotaprofiles" {
		return admission.Allowed("")
	}

	obj := &quotav1alpha1.ElasticQuotaProfile{}

	var err error
	if request.Operation != v1.Delete {
		if err = h.Decoder.Decode(request, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	} else {
		return admission.Allowed("")
	}

	allErrs := h.ValidateElasticQuotaProfile(obj)
	if len(allErrs) > 0 {
		return admission.Denied(allErrs.ToAggregate().Error())
	}

	return admission.Allowed("")
}

func (h *ElasticQuotaProfileValidatingHandler) ValidateElasticQuotaProfile(profile *quotav1alpha1.ElasticQuotaProfile) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "quotaLabels")

	for k, v := range profile.Spec.QuotaLabels {
		for _, msg := range validation.IsQualifiedName(k) {
			allErrs = append(allErrs, field.Invalid(fldPath.Key(k), k, msg))
		}
		for _, msg := range validation.IsValidLabelValue(v) {
			allErrs = append(allErrs, field.Invalid(fldPath.Key(k), v, fmt.Sprintf("invalid label value: %s", msg)))
		}
	}

	return allErrs
}

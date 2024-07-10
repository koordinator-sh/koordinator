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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
	"github.com/koordinator-sh/koordinator/pkg/webhook/quotaevaluate"
)

func (h *PodValidatingHandler) evaluateQuota(ctx context.Context, req admission.Request) (bool, string, error) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.EnableQuotaAdmission) {
		return true, "", nil
	}

	newPod := &corev1.Pod{}
	switch req.Operation {
	case admissionv1.Create:
		if err := h.Decoder.DecodeRaw(req.Object, newPod); err != nil {
			return false, "", err
		}
	default:
		return true, "", nil
	}

	// quota is system quota or empty, skip it.
	quotaName := elasticquota.GetQuotaName(newPod, h.Client)
	if quotaName == "" || quotaName == extension.DefaultQuotaName ||
		quotaName == extension.SystemQuotaName || quotaName == extension.RootQuotaName {
		return true, "", nil
	}

	quotaList := &v1alpha1.ElasticQuotaList{}
	err := h.Client.List(context.TODO(), quotaList, client.MatchingFields{"metadata.name": quotaName})
	if err != nil {
		return false, "", err
	}

	if len(quotaList.Items) == 0 {
		err := fmt.Errorf("elastic quota %v not found", quotaName)
		return false, err.Error(), err
	} else if len(quotaList.Items) > 1 {
		err := fmt.Errorf("more than one elastic quota %v found", quotaName)
		return false, err.Error(), err
	}

	attribute := &quotaevaluate.Attributes{
		QuotaNamespace: quotaList.Items[0].Namespace,
		QuotaName:      quotaList.Items[0].Name,
		Operation:      req.Operation,
		Pod:            newPod,
	}

	err = h.QuotaEvaluator.Evaluate(attribute)
	if err != nil {
		return false, err.Error(), err
	}
	return true, "", nil
}

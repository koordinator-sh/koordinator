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

package extension

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiserver/pkg/quota/v1"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

// RootQuotaName means quotaTree's root\head.
const (
	SystemQuotaName           = "koordinator-system-quota"
	RootQuotaName             = "koordinator-root-quota"
	DefaultQuotaName          = "koordinator-default-quota"
	QuotaKoordinatorPrefix    = "quota.scheduling.koordinator.sh"
	LabelQuotaIsParent        = QuotaKoordinatorPrefix + "/is-parent"
	LabelQuotaParent          = QuotaKoordinatorPrefix + "/parent"
	LabelAllowLentResource    = QuotaKoordinatorPrefix + "/allow-lent-resource"
	LabelQuotaName            = QuotaKoordinatorPrefix + "/name"
	LabelQuotaProfile         = QuotaKoordinatorPrefix + "/profile"
	LabelQuotaIsRoot          = QuotaKoordinatorPrefix + "/is-root"
	LabelQuotaTreeID          = QuotaKoordinatorPrefix + "/tree-id"
	AnnotationSharedWeight    = QuotaKoordinatorPrefix + "/shared-weight"
	AnnotationRuntime         = QuotaKoordinatorPrefix + "/runtime"
	AnnotationRequest         = QuotaKoordinatorPrefix + "/request"
	AnnotationChildRequest    = QuotaKoordinatorPrefix + "/child-request"
	AnnotationResourceKeys    = QuotaKoordinatorPrefix + "/resource-keys"
	AnnotationTotalResource   = QuotaKoordinatorPrefix + "/total-resource"
	AnnotationQuotaNamespaces = QuotaKoordinatorPrefix + "/namespaces"
	AnnotationGuaranteed      = QuotaKoordinatorPrefix + "/guaranteed"
	AnnotationAllocated       = QuotaKoordinatorPrefix + "/allocated"
)

func GetParentQuotaName(quota *v1alpha1.ElasticQuota) string {
	parentName := quota.Labels[LabelQuotaParent]
	if parentName == "" && quota.Name != RootQuotaName {
		return RootQuotaName //default return RootQuotaName
	}
	return parentName
}

func IsParentQuota(quota *v1alpha1.ElasticQuota) bool {
	return quota.Labels[LabelQuotaIsParent] == "true"
}

func IsAllowLentResource(quota *v1alpha1.ElasticQuota) bool {
	return quota.Labels[LabelAllowLentResource] != "false"
}

func IsTreeRootQuota(quota *v1alpha1.ElasticQuota) bool {
	return quota.Labels[LabelQuotaIsRoot] == "true"
}

func GetQuotaTreeID(quota *v1alpha1.ElasticQuota) string {
	return quota.Labels[LabelQuotaTreeID]
}

func GetSharedWeight(quota *v1alpha1.ElasticQuota) corev1.ResourceList {
	value, exist := quota.Annotations[AnnotationSharedWeight]
	if exist {
		resList := corev1.ResourceList{}
		err := json.Unmarshal([]byte(value), &resList)
		if err == nil && !v1.IsZero(resList) {
			return resList
		}
	}
	return quota.Spec.Max.DeepCopy() //default equals to max
}

func IsForbiddenModify(quota *v1alpha1.ElasticQuota) (bool, error) {
	if quota.Name == SystemQuotaName || quota.Name == RootQuotaName {
		// can't modify SystemQuotaGroup
		return true, fmt.Errorf("invalid quota %s", quota.Name)
	}

	return false, nil
}

func GetQuotaName(pod *corev1.Pod) string {
	return pod.Labels[LabelQuotaName]
}

func GetAnnotationQuotaNamespaces(quota *v1alpha1.ElasticQuota) []string {
	if quota.Annotations == nil {
		return nil
	}
	if quota.Annotations[AnnotationQuotaNamespaces] == "" {
		return nil
	}

	var namespaces []string
	if err := json.Unmarshal([]byte(quota.Annotations[AnnotationQuotaNamespaces]), &namespaces); err != nil {
		return nil
	}
	return namespaces
}

func GetGuaranteed(quota *v1alpha1.ElasticQuota) (corev1.ResourceList, error) {
	guaranteed := corev1.ResourceList{}
	if quota.Annotations[AnnotationGuaranteed] != "" {
		if err := json.Unmarshal([]byte(quota.Annotations[AnnotationGuaranteed]), &guaranteed); err != nil {
			return guaranteed, err
		}
	}
	return guaranteed, nil
}

func GetAllocated(quota *v1alpha1.ElasticQuota) (corev1.ResourceList, error) {
	allocated := corev1.ResourceList{}
	if quota.Annotations[AnnotationAllocated] != "" {
		if err := json.Unmarshal([]byte(quota.Annotations[AnnotationAllocated]), &allocated); err != nil {
			return allocated, err
		}
	}
	return allocated, nil
}

func GetRuntime(quota *v1alpha1.ElasticQuota) (corev1.ResourceList, error) {
	runtime := corev1.ResourceList{}
	if quota.Annotations[AnnotationRuntime] != "" {
		if err := json.Unmarshal([]byte(quota.Annotations[AnnotationRuntime]), &runtime); err != nil {
			return runtime, err
		}
	}
	return runtime, nil
}

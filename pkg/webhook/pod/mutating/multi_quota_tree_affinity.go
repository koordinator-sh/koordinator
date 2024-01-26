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

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
)

func (h *PodMutatingHandler) addNodeAffinityForMultiQuotaTree(ctx context.Context, req admission.Request, pod *corev1.Pod) error {
	if req.Operation != admissionv1.Create {
		return nil
	}

	if !utilfeature.DefaultFeatureGate.Enabled(features.MultiQuotaTree) {
		return nil
	}

	plugin := elasticquota.NewPlugin(h.Decoder, h.Client)
	quotaName := extension.GetQuotaName(pod)
	if quotaName == "" {
		quotaName = pod.Namespace
	}

	info := plugin.GetQuotaInfo(quotaName)
	if info == nil {
		return nil
	}
	if info.TreeID == "" {
		return nil
	}

	profileList := &quotav1alpha1.ElasticQuotaProfileList{}
	err := h.Client.List(ctx, profileList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{extension.LabelQuotaTreeID: info.TreeID}),
	}, utilclient.DisableDeepCopy)
	if err != nil {
		return err
	}

	if len(profileList.Items) == 0 {
		return nil
	}

	nodeSelector := profileList.Items[0].Spec.NodeSelector
	if nodeSelector == nil {
		return nil
	}

	requirements := convertNodeSelectorToNodeSelectorRequirements(nodeSelector)

	affinity := pod.Spec.Affinity
	if affinity == nil {
		affinity = &corev1.Affinity{}
		pod.Spec.Affinity = affinity
	}

	nodeAffinity := affinity.NodeAffinity
	if nodeAffinity == nil {
		nodeAffinity = &corev1.NodeAffinity{}
		affinity.NodeAffinity = nodeAffinity
	}

	required := nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if required == nil {
		required = &corev1.NodeSelector{}
		nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = required
	}

	for i, term := range required.NodeSelectorTerms {
		term.MatchExpressions = append(term.MatchExpressions, requirements...)
		required.NodeSelectorTerms[i] = term
	}

	if len(required.NodeSelectorTerms) == 0 {
		required.NodeSelectorTerms = []corev1.NodeSelectorTerm{
			{
				MatchExpressions: requirements,
			},
		}
	}

	return nil
}

func convertNodeSelectorToNodeSelectorRequirements(nodeSelector *metav1.LabelSelector) []corev1.NodeSelectorRequirement {
	requirements := make([]corev1.NodeSelectorRequirement, 0, len(nodeSelector.MatchLabels)+len(nodeSelector.MatchExpressions))
	for k, v := range nodeSelector.MatchLabels {
		requirements = append(requirements, corev1.NodeSelectorRequirement{
			Key:      k,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{v},
		})
	}
	for _, expression := range nodeSelector.MatchExpressions {
		requirements = append(requirements, corev1.NodeSelectorRequirement{
			Key:      expression.Key,
			Operator: corev1.NodeSelectorOperator(string(expression.Operator)),
			Values:   expression.Values,
		})
	}

	return requirements
}

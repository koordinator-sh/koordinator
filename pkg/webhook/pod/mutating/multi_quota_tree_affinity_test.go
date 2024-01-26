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
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	schedulingv1alpha1 "sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func init() {
	_ = quotav1alpha1.AddToScheme(scheme.Scheme)
	_ = schedulingv1alpha1.AddToScheme(scheme.Scheme)
}

func TestAddNodeAffinityForMultiQuotaTree(t *testing.T) {
	testCases := []struct {
		name     string
		pod      *corev1.Pod
		quotas   []*schedulingv1alpha1.ElasticQuota
		profile  *quotav1alpha1.ElasticQuotaProfile
		expected *corev1.Pod
	}{
		{
			name: "no quota label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
				},
				Spec: corev1.PodSpec{},
			},
			quotas:  nil,
			profile: nil,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
				},
				Spec: corev1.PodSpec{},
			},
		},
		{
			name: "no quota profile",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota1",
					},
				},
				Spec: corev1.PodSpec{},
			},
			quotas: []*schedulingv1alpha1.ElasticQuota{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "kube-system",
						Name:      "quota1",
					},
				},
			},
			profile: nil,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota1",
					},
				},
				Spec: corev1.PodSpec{},
			},
		},
		{
			name: "add node affinity",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota1",
					},
				},
				Spec: corev1.PodSpec{},
			},
			quotas: []*schedulingv1alpha1.ElasticQuota{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "kube-system",
						Name:      "quota1",
						Labels: map[string]string{
							extension.LabelQuotaTreeID: "123456",
						},
					},
				},
			},
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kube-system",
					Name:      "quota1-profile",
					Labels: map[string]string{
						extension.LabelQuotaTreeID: "123456",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "root-quota",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"node-pool": "test",
						},
					},
				},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota1",
					},
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "node-pool",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"test"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "append node affinity",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota1",
					},
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "topology.kubernetes.io/zone",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"cn-hangzhou-a"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			quotas: []*schedulingv1alpha1.ElasticQuota{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "kube-system",
						Name:      "quota1",
						Labels: map[string]string{
							extension.LabelQuotaTreeID: "123456",
						},
					},
				},
			},
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kube-system",
					Name:      "quota1-profile",
					Labels: map[string]string{
						extension.LabelQuotaTreeID: "123456",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "root-quota",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"node-pool": "test",
						},
					},
				},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota1",
					},
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "topology.kubernetes.io/zone",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"cn-hangzhou-a"},
											},
											{
												Key:      "node-pool",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"test"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multi quotas",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota2",
					},
				},
				Spec: corev1.PodSpec{},
			},
			quotas: []*schedulingv1alpha1.ElasticQuota{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "kube-system",
						Name:      "quota1",
						Labels: map[string]string{
							extension.LabelQuotaTreeID: "123456",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "kube-system",
						Name:      "quota2",
						Labels: map[string]string{
							extension.LabelQuotaTreeID: "123456",
						},
					},
				},
			},
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kube-system",
					Name:      "quota1-profile",
					Labels: map[string]string{
						extension.LabelQuotaTreeID: "123456",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "root-quota",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"node-pool": "test",
						},
					},
				},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "quota2",
					},
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "node-pool",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"test"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "default quota",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
				},
				Spec: corev1.PodSpec{},
			},
			quotas: []*schedulingv1alpha1.ElasticQuota{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "default",
						Labels: map[string]string{
							extension.LabelQuotaTreeID: "123456",
						},
					},
				},
			},
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kube-system",
					Name:      "quota1-profile",
					Labels: map[string]string{
						extension.LabelQuotaTreeID: "123456",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "root-quota",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"node-pool": "test",
						},
					},
				},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "node-pool",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"test"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer feature.SetFeatureGateDuringTest(t, feature.DefaultMutableFeatureGate, features.MultiQuotaTree, true)()
			assert := assert.New(t)

			client := fake.NewClientBuilder().Build()
			decoder, _ := admission.NewDecoder(scheme.Scheme)
			handler := &PodMutatingHandler{
				Client:  client,
				Decoder: decoder,
			}

			if tc.profile != nil {
				err := client.Create(context.TODO(), tc.profile)
				assert.NoError(err)
			}

			for _, quota := range tc.quotas {
				err := client.Create(context.TODO(), quota)
				assert.NoError(err)
			}

			req := newAdmission(admissionv1.Create, runtime.RawExtension{}, runtime.RawExtension{}, "")
			err := handler.addNodeAffinityForMultiQuotaTree(context.TODO(), req, tc.pod)
			assert.NoError(err)

			assert.Equal(tc.expected, tc.pod)
		})
	}
}

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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

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
	handler, fakeInformers := makeTestHandler()
	quotaInformer, err := fakeInformers.FakeInformerFor(context.TODO(), &schedulingv1alpha1.ElasticQuota{})
	assert.NoError(t, err)

	profiles := []*quotav1alpha1.ElasticQuotaProfile{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "profileA",
				Labels: map[string]string{
					extension.LabelQuotaTreeID: "tree1",
				},
			},
			Spec: quotav1alpha1.ElasticQuotaProfileSpec{
				QuotaName: "root-quota-a",
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"node-pool": "nodePoolA",
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "profileB",
				Labels: map[string]string{
					extension.LabelQuotaTreeID: "tree2",
				},
			},
			Spec: quotav1alpha1.ElasticQuotaProfileSpec{
				QuotaName: "root-quota-b",
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"node-pool": "nodePoolB",
					},
				},
			},
		},
	}

	quotas := []*schedulingv1alpha1.ElasticQuota{
		// other-quota, no tree
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "other-quota",
			},
		},
		// root-quota-a
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "root-quota-a",
				Labels: map[string]string{
					extension.LabelQuotaTreeID:   "tree1",
					extension.LabelQuotaIsParent: "true",
				},
			},
		},
		// the children quotas of root-quota-a
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "root-quota-a-child1",
				Labels: map[string]string{
					extension.LabelQuotaTreeID: "tree1",
					extension.LabelQuotaParent: "root-quota-a",
				},
			},
		},
		// the namespace quota
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "namespace1",
				Name:      "namespace1",
				Labels: map[string]string{
					extension.LabelQuotaTreeID: "tree1",
					extension.LabelQuotaParent: "root-quota-a",
				},
			},
		},
		// root-quota-b
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "root-quota-b",
				Labels: map[string]string{
					extension.LabelQuotaTreeID:   "tree2",
					extension.LabelQuotaIsParent: "true",
				},
				Annotations: map[string]string{
					extension.AnnotationQuotaNamespaces: "[\"namespace2\"]",
				},
			},
		},
		// the children quotas of root-quota-b
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "root-quota-b-child1",
				Labels: map[string]string{
					extension.LabelQuotaTreeID: "tree2",
					extension.LabelQuotaParent: "root-quota-b",
				},
			},
		},
	}

	for _, profile := range profiles {
		err := handler.Client.Create(context.TODO(), profile)
		assert.NoError(t, err)
	}

	for _, quota := range quotas {
		err := handler.Client.Create(context.TODO(), quota)
		assert.NoError(t, err)
		quotaInformer.Add(quota)
	}

	quota := &schedulingv1alpha1.ElasticQuota{}
	err = handler.Client.Get(context.TODO(), types.NamespacedName{Namespace: "kube-system", Name: "root-quota-b-child1"}, quota)
	assert.NoError(t, err)

	testCases := []struct {
		name     string
		pod      *corev1.Pod
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
						extension.LabelQuotaName: "other-quota",
					},
				},
				Spec: corev1.PodSpec{},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "other-quota",
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
						extension.LabelQuotaName: "root-quota-a-child1",
					},
				},
				Spec: corev1.PodSpec{},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "root-quota-a-child1",
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
												Values:   []string{"nodePoolA"},
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
						extension.LabelQuotaName: "root-quota-a-child1",
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
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "root-quota-a-child1",
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
												Values:   []string{"nodePoolA"},
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
						extension.LabelQuotaName: "root-quota-b-child1",
					},
				},
				Spec: corev1.PodSpec{},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						extension.LabelQuotaName: "root-quota-b-child1",
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
												Values:   []string{"nodePoolB"},
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
					Namespace: "namespace1",
					Name:      "test-pod-1",
				},
				Spec: corev1.PodSpec{},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "namespace1",
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
												Values:   []string{"nodePoolA"},
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
			name: "default quota 2",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "namespace2",
					Name:      "test-pod-1",
				},
				Spec: corev1.PodSpec{},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "namespace2",
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
												Values:   []string{"nodePoolB"},
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

			req := newAdmission(admissionv1.Create, runtime.RawExtension{}, runtime.RawExtension{}, "")
			err := handler.addNodeAffinityForMultiQuotaTree(context.TODO(), req, tc.pod)
			assert.NoError(t, err)

			assert.Equal(t, tc.expected, tc.pod)
		})
	}
}

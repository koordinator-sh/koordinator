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

package quotaevaluate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
)

func TestEvaluate(t *testing.T) {
	testCases := []struct {
		name        string
		quota       *v1alpha1.ElasticQuota
		attribute   *Attributes
		expectError bool
		errMessage  string
		expectUsed  corev1.ResourceList
	}{
		{
			name: "normal case",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}).Obj(),
			},
			expectError: false,
			expectUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
		{
			name: "cpu exceed",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("19"),
					corev1.ResourceMemory: resource.MustParse("50Gi"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}).Obj(),
			},
			expectError: true,
			errMessage:  "exceeded quota: ns1/test1, requested: cpu=2, used: cpu=19, limited: cpu=20",
			expectUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
		{
			name: "admission allow",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("19"),
					corev1.ResourceMemory: resource.MustParse("50Gi"),
				}).Admission(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("30"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}).Obj(),
			},
			expectError: false,
			expectUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("21"),
				corev1.ResourceMemory: resource.MustParse("54Gi"),
			},
		},
		{
			name: "gpu normal case",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					extension.ResourceNvidiaGPU: resource.MustParse("2"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:          resource.MustParse("2"),
						corev1.ResourceMemory:       resource.MustParse("4Gi"),
						extension.ResourceNvidiaGPU: resource.MustParse("2"),
					}).Obj(),
			},
			expectError: false,
			expectUsed: corev1.ResourceList{
				extension.ResourceNvidiaGPU: resource.MustParse("2"),
			},
		},
		{
			name: "gpu exceed",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					extension.ResourceNvidiaGPU: resource.MustParse("2"),
				}).ChildRequest(
				corev1.ResourceList{
					extension.ResourceNvidiaGPU: resource.MustParse("2"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:          resource.MustParse("2"),
						corev1.ResourceMemory:       resource.MustParse("4Gi"),
						extension.ResourceNvidiaGPU: resource.MustParse("2"),
					}).Obj(),
			},
			expectError: true,
			errMessage:  "exceeded quota: ns1/test1, requested: nvidia.com/gpu=2, used: nvidia.com/gpu=2, limited: nvidia.com/gpu=2",
			expectUsed: corev1.ResourceList{
				extension.ResourceNvidiaGPU: resource.MustParse("2"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)

			client := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(tc.quota).Build()

			quotaAccessor := NewQuotaAccessor(client)
			evaluator := NewQuotaEvaluator(quotaAccessor, 16, make(chan struct{}))

			err := evaluator.Evaluate(tc.attribute)
			if tc.expectError {
				assert.Error(t, err)
				assert.Equal(t, tc.errMessage, err.Error())
				t.Logf("expect err: %v", err)
			} else {
				assert.NoError(t, err)

				newQuota := &v1alpha1.ElasticQuota{}
				err = client.Get(context.TODO(), types.NamespacedName{
					Namespace: tc.quota.Namespace,
					Name:      tc.quota.Name,
				}, newQuota)
				assert.NoError(t, err)
				newUsage, err := extension.GetChildRequest(newQuota)
				assert.NoError(t, err)
				t.Logf("expec %v, got %v", util.DumpJSON(tc.expectUsed), util.DumpJSON(newUsage))
				assert.Equal(t, tc.expectUsed, newUsage)
			}
		})
	}
}

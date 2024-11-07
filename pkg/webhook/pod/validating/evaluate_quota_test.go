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
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
	"github.com/koordinator-sh/koordinator/pkg/webhook/quotaevaluate"
)

func TestEvalucateQuota(t *testing.T) {
	testCases := []struct {
		name        string
		operation   admissionv1.Operation
		oldPod      *corev1.Pod
		newPod      *corev1.Pod
		quota       *v1alpha1.ElasticQuota
		wantAllowed bool
		wantReason  string
		wantErr     bool
		wantUsed    corev1.ResourceList
	}{
		{
			name:      "normal case 1",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).Obj(),
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
			wantUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
		{
			name:      "normal case 2",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}).Obj(),
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
			wantUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("6Gi"),
			},
		},
		{
			name:      "cpu exceeded max",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}).Obj(),
			wantAllowed: false,
			wantReason:  "exceeded quota: kube-system/quota1, requested: cpu=2, used: cpu=4, limited: cpu=4",
			wantErr:     true,
			wantUsed:    corev1.ResourceList{},
		},
		{
			name:      "cpu and memory exceeded max",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).Obj(),
			wantAllowed: false,
			wantReason:  "exceeded quota: kube-system/quota1, requested: cpu=2,memory=4Gi, used: cpu=4,memory=8Gi, limited: cpu=4,memory=8Gi",
			wantErr:     true,
			wantUsed:    corev1.ResourceList{},
		},
		{
			name:      "cpu exceeded max, but not admission",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}).Admission(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			}).Obj(),
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
			wantUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("6"),
				corev1.ResourceMemory: resource.MustParse("6Gi"),
			},
		},
		{
			name:      "cpu and memory exceeded max, but not admission",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).Admission(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			}).Obj(),
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
			wantUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("6"),
				corev1.ResourceMemory: resource.MustParse("12Gi"),
			},
		},
		{
			name:      "quota not found",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota2").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}).Obj(),
			wantAllowed: false,
			wantReason:  "elastic quota quota2 not found",
			wantErr:     true,
			wantUsed:    corev1.ResourceList{},
		},
		{
			name:      "quota has batch cpu, pod not",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				extension.BatchCPU:    resource.MustParse("4"),
				extension.BatchMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				extension.BatchCPU:    resource.MustParse("4"),
				extension.BatchMemory: resource.MustParse("8Gi"),
			}).Obj(),
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
			wantUsed: corev1.ResourceList{
				extension.BatchCPU:    resource.MustParse("4"),
				extension.BatchMemory: resource.MustParse("8Gi"),
			},
		},
		{
			name:      "quota has batch cpu, pod has and exceeded",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("1"),
					extension.BatchMemory: resource.MustParse("2Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				extension.BatchCPU:    resource.MustParse("4"),
				extension.BatchMemory: resource.MustParse("8Gi"),
			}).ChildRequest(corev1.ResourceList{
				extension.BatchCPU:    resource.MustParse("4"),
				extension.BatchMemory: resource.MustParse("8Gi"),
			}).Obj(),
			wantAllowed: false,
			wantReason:  "exceeded quota: kube-system/quota1, requested: kubernetes.io/batch-cpu=1,kubernetes.io/batch-memory=2Gi, used: kubernetes.io/batch-cpu=4,kubernetes.io/batch-memory=8Gi, limited: kubernetes.io/batch-cpu=4,kubernetes.io/batch-memory=8Gi",
			wantErr:     true,
			wantUsed:    corev1.ResourceList{},
		},
		{
			name:      "admission not set, use max by default",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}).Obj(),
			wantAllowed: false,
			wantReason:  "exceeded quota: kube-system/quota1, requested: cpu=2,memory=4Gi, used: , limited: cpu=1,memory=2Gi",
			wantErr:     true,
			wantUsed:    corev1.ResourceList{},
		},
		{
			name:      "admission set empty, use max by default",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}).Admission(corev1.ResourceList{}).Obj(),
			wantAllowed: false,
			wantReason:  "exceeded quota: kube-system/quota1, requested: cpu=2,memory=4Gi, used: , limited: cpu=1,memory=2Gi",
			wantErr:     true,
			wantUsed:    corev1.ResourceList{},
		},
		{
			name:      "admission set zero explicitly",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).Admission(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("0"),
				corev1.ResourceMemory: resource.MustParse("0Gi"),
			}).Obj(),
			wantAllowed: false,
			wantReason:  "exceeded quota: kube-system/quota1, requested: cpu=2,memory=4Gi, used: , limited: cpu=0,memory=0",
			wantErr:     true,
			wantUsed:    corev1.ResourceList{},
		},
		{
			name:      "admission set zero for non-related resource",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").Label("quota.scheduling.koordinator.sh/name", "quota1").
				Container(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}).Obj(),
			quota: elasticquota.MakeQuota("quota1").Namespace("kube-system").Max(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			}).Admission(corev1.ResourceList{
				extension.BatchCPU: resource.MustParse("0"),
			}).Obj(),
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
			wantUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate, features.EnableQuotaAdmission, true)()
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithIndex(&v1alpha1.ElasticQuota{},
				"metadata.name", func(object client.Object) []string {
					eq, ok := object.(*v1alpha1.ElasticQuota)
					if !ok {
						return []string{}
					}
					return []string{eq.Name}
				})

			if tc.quota != nil {
				clientBuilder.WithObjects(tc.quota)
			}
			client := clientBuilder.Build()

			decoder := admission.NewDecoder(scheme)
			h := &PodValidatingHandler{
				Client:  client,
				Decoder: decoder,
			}
			quotaAccessor := quotaevaluate.NewQuotaAccessor(h.Client)
			h.QuotaEvaluator = quotaevaluate.NewQuotaEvaluator(quotaAccessor, 16, make(chan struct{}))

			var objRawExt, oldObjRawExt runtime.RawExtension
			if tc.newPod != nil {
				objRawExt = runtime.RawExtension{
					Raw: []byte(util.DumpJSON(tc.newPod)),
				}
			}
			if tc.oldPod != nil {
				oldObjRawExt = runtime.RawExtension{
					Raw: []byte(util.DumpJSON(tc.oldPod)),
				}
			}

			req := newAdmissionRequest(tc.operation, objRawExt, oldObjRawExt, "pods")
			gotAllowed, gotReason, err := h.evaluateQuota(context.TODO(), admission.Request{AdmissionRequest: req})
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				newQuota := &v1alpha1.ElasticQuota{}
				err = client.Get(context.TODO(), types.NamespacedName{
					Namespace: tc.quota.Namespace,
					Name:      tc.quota.Name,
				}, newQuota)
				assert.NoError(t, err)
				newUsed, err := extension.GetChildRequest(newQuota)
				assert.NoError(t, err)
				t.Logf("want %v, got %v", util.DumpJSON(tc.wantUsed), util.DumpJSON(newUsed))
				assert.Equal(t, tc.wantUsed, newUsed)
			}
			if gotAllowed != tc.wantAllowed {
				t.Errorf("evaluateQuota gotAllowed = %v, want %v", gotAllowed, tc.wantAllowed)
			}
			if gotReason != tc.wantReason {
				t.Errorf("evaluateQuota:\n"+
					"gotReason = %v,\n"+
					"want = %v", gotReason, tc.wantReason)
				t.Errorf("got=%v, want=%v", len(gotReason), len(tc.wantReason))
			}
		})
	}

}

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
)

func TestPodEnhancedValidate(t *testing.T) {
	quotaLabelRequiredCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodEnhancedValidatorConfigName,
			Namespace: PodEnhancedValidatorConfigNamespace,
		},
		Data: map[string]string{
			ConfigKeyEnable: "true",
			ConfigKeyRules: `[
						{
							"name": "quota-label-required",
							"requiredLabels": ["quota.scheduling.koordinator.sh/name"],
							"namespaceWhitelist": ["kube-system", "koordinator-system"]
						}
					]`,
		},
	}
	multipleLabelsRequiredCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodEnhancedValidatorConfigName,
			Namespace: PodEnhancedValidatorConfigNamespace,
		},
		Data: map[string]string{
			ConfigKeyEnable: "true",
			ConfigKeyRules: `[
						{
							"name": "multiple-labels-required",
							"requiredLabels": ["product", "app"]
						}
					]`,
		},
	}
	testCases := []struct {
		name        string
		operation   admissionv1.Operation
		newPod      *corev1.Pod
		configMap   *corev1.ConfigMap
		wantAllowed bool
		wantReason  string
		wantErr     bool
	}{
		{
			name:        "no config map: should allow all pods",
			operation:   admissionv1.Create,
			newPod:      elasticquota.MakePod("ns1", "pod1").Obj(),
			configMap:   nil,
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "validation disabled: should allow all pods",
			operation: admissionv1.Create,
			newPod:    elasticquota.MakePod("ns1", "pod1").Obj(),
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PodEnhancedValidatorConfigName,
					Namespace: PodEnhancedValidatorConfigNamespace,
				},
				Data: map[string]string{
					ConfigKeyEnable: "false",
					ConfigKeyRules: `[
						{
							"name": "quota-label-required",
							"requiredLabels": ["quota.scheduling.koordinator.sh/name"]
						}
					]`,
				},
			},
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "empty rules: should allow all pods",
			operation: admissionv1.Create,
			newPod:    elasticquota.MakePod("ns1", "pod1").Obj(),
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PodEnhancedValidatorConfigName,
					Namespace: PodEnhancedValidatorConfigNamespace,
				},
				Data: map[string]string{
					ConfigKeyEnable: "true",
					ConfigKeyRules:  `[]`,
				},
			},
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "quota-label-required: create pod with quota label",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("quota.scheduling.koordinator.sh/name", "quota1").Obj(),
			configMap:   quotaLabelRequiredCM,
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "quota-label-required: update pod with quota label",
			operation: admissionv1.Update,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("quota.scheduling.koordinator.sh/name", "quota1").Obj(),
			configMap:   quotaLabelRequiredCM,
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:        "quota-label-required: pod without quota label",
			operation:   admissionv1.Create,
			newPod:      elasticquota.MakePod("ns1", "pod1").Obj(),
			configMap:   quotaLabelRequiredCM,
			wantAllowed: false,
			wantReason: "validation rule 'quota-label-required' failed: " +
				"required label 'quota.scheduling.koordinator.sh/name' is missing",
			wantErr: true,
		},
		{
			name:        "quota-label-required: update pod without quota label",
			operation:   admissionv1.Update,
			newPod:      elasticquota.MakePod("ns1", "pod1").Obj(),
			configMap:   quotaLabelRequiredCM,
			wantAllowed: false,
			wantReason: "validation rule 'quota-label-required' failed: " +
				"required label 'quota.scheduling.koordinator.sh/name' is missing",
			wantErr: true,
		},
		{
			name:        "quota-label-required: pod in whitelisted namespace",
			operation:   admissionv1.Create,
			newPod:      elasticquota.MakePod("kube-system", "pod1").Obj(),
			configMap:   quotaLabelRequiredCM,
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:        "quota-label-required: pod in another whitelisted namespace",
			operation:   admissionv1.Create,
			newPod:      elasticquota.MakePod("koordinator-system", "pod1").Obj(),
			configMap:   quotaLabelRequiredCM,
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "multiple-labels-required: pod with required labels",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("product", "product1").
				Label("app", "app1").Obj(),
			configMap:   multipleLabelsRequiredCM,
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "multiple-labels-required: pod missing app label",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("product", "product1").Obj(),
			configMap:   multipleLabelsRequiredCM,
			wantAllowed: false,
			wantReason:  "validation rule 'multiple-labels-required' failed: required label 'app' is missing",
			wantErr:     true,
		},
		{
			name:      "multiple-labels-required: pod missing product label",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("app", "app1").Obj(),
			configMap:   multipleLabelsRequiredCM,
			wantAllowed: false,
			wantReason:  "validation rule 'multiple-labels-required' failed: required label 'product' is missing",
			wantErr:     true,
		},
		{
			name:        "multiple-labels-required: pod without required labels",
			operation:   admissionv1.Create,
			newPod:      elasticquota.MakePod("ns1", "pod1").Obj(),
			configMap:   multipleLabelsRequiredCM,
			wantAllowed: false,
			wantReason:  "validation rule 'multiple-labels-required' failed: required labels are missing: [product app]",
			wantErr:     true,
		},
		{
			name:      "multiple rules: all rules pass",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("quota.scheduling.koordinator.sh/name", "quota1").
				Label("app", "myapp").Obj(),
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PodEnhancedValidatorConfigName,
					Namespace: PodEnhancedValidatorConfigNamespace,
				},
				Data: map[string]string{
					ConfigKeyEnable: "true",
					ConfigKeyRules: `[
						{
							"name": "quota-label-required",
							"requiredLabels": ["quota.scheduling.koordinator.sh/name"]
						},
						{
							"name": "app-label-required",
							"requiredLabels": ["app"]
						}
					]`,
				},
			},
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "multiple rules: first rule fails",
			operation: admissionv1.Create,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("app", "myapp").Obj(),
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PodEnhancedValidatorConfigName,
					Namespace: PodEnhancedValidatorConfigNamespace,
				},
				Data: map[string]string{
					ConfigKeyEnable: "true",
					ConfigKeyRules: `[
						{
							"name": "quota-label-required",
							"requiredLabels": ["quota.scheduling.koordinator.sh/name"]
						},
						{
							"name": "app-label-required",
							"requiredLabels": ["app"]
						}
					]`,
				},
			},
			wantAllowed: false,
			wantReason: "validation rule 'quota-label-required' failed: " +
				"required label 'quota.scheduling.koordinator.sh/name' is missing",
			wantErr: true,
		},
		{
			name:      "update operation: should validate",
			operation: admissionv1.Update,
			newPod: elasticquota.MakePod("ns1", "pod1").
				Label("quota.scheduling.koordinator.sh/name", "quota1").Obj(),
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PodEnhancedValidatorConfigName,
					Namespace: PodEnhancedValidatorConfigNamespace,
				},
				Data: map[string]string{
					ConfigKeyEnable: "true",
					ConfigKeyRules: `[
						{
							"name": "quota-label-required",
							"requiredLabels": ["quota.scheduling.koordinator.sh/name"]
						}
					]`,
				},
			},
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "delete operation: should allow",
			operation: admissionv1.Delete,
			newPod:    elasticquota.MakePod("ns1", "pod1").Obj(),
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PodEnhancedValidatorConfigName,
					Namespace: PodEnhancedValidatorConfigNamespace,
				},
				Data: map[string]string{
					ConfigKeyEnable: "true",
					ConfigKeyRules: `[
						{
							"name": "quota-label-required",
							"requiredLabels": ["quota.scheduling.koordinator.sh/name"]
						}
					]`,
				},
			},
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate,
				features.EnablePodEnhancedValidator, true)()
			scheme := runtime.NewScheme()
			_ = clientgoscheme.AddToScheme(scheme)
			decoder := admission.NewDecoder(scheme)
			var fakeClient client.Client
			if tc.configMap != nil {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.configMap).Build()
			} else {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			}
			h := &PodValidatingHandler{
				Client:  fakeClient,
				Decoder: decoder,
			}
			// create a validator to build the whitelist set
			h.PodEnhancedValidator = NewPodEnhancedValidator(fakeClient)
			err := h.PodEnhancedValidator.Start(context.Background())
			assert.NoError(t, err)

			var objRawExt, oldObjRawExt runtime.RawExtension
			if tc.newPod != nil {
				objRawExt = runtime.RawExtension{
					Raw: []byte(util.DumpJSON(tc.newPod)),
				}
			}

			req := newAdmissionRequest(tc.operation, objRawExt, oldObjRawExt, "pods")
			gotReason, err := h.podEnhancedValidate(context.TODO(), admission.Request{AdmissionRequest: req})
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			gotAllowed := err == nil
			if gotAllowed != tc.wantAllowed {
				t.Errorf("podEnhancedValidate gotAllowed = %v, want %v", gotAllowed, tc.wantAllowed)
			}
			if gotReason != tc.wantReason {
				t.Errorf("podEnhancedValidate:\n"+
					"gotReason = %v,\n"+
					"want = %v", gotReason, tc.wantReason)
			}
		})
	}
}

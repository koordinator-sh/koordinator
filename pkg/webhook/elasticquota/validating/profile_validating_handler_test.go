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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
)

func TestElasticQuotaProfileValidatingHandler(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = quotav1alpha1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	handler := &ElasticQuotaProfileValidatingHandler{
		Decoder: decoder,
	}

	tests := []struct {
		name            string
		req             admission.Request
		expectedAllowed bool
	}{
		{
			name: "valid labels",
			req: admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Resource:  metav1.GroupVersionResource{Resource: "elasticquotaprofiles"},
					Operation: v1.Create,
					Object: runtime.RawExtension{
						Raw: marshalObj(&quotav1alpha1.ElasticQuotaProfile{
							Spec: quotav1alpha1.ElasticQuotaProfileSpec{
								QuotaLabels: map[string]string{
									"valid-key":   "valid-value",
									"foo.bar/baz": "value123",
								},
							},
						}),
					},
				},
			},
			expectedAllowed: true,
		},
		{
			name: "invalid label keys",
			req: admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Resource:  metav1.GroupVersionResource{Resource: "elasticquotaprofiles"},
					Operation: v1.Create,
					Object: runtime.RawExtension{
						Raw: marshalObj(&quotav1alpha1.ElasticQuotaProfile{
							Spec: quotav1alpha1.ElasticQuotaProfileSpec{
								QuotaLabels: map[string]string{
									"invalid key with spaces": "value",
								},
							},
						}),
					},
				},
			},
			expectedAllowed: false,
		},
		{
			name: "invalid label values",
			req: admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Resource:  metav1.GroupVersionResource{Resource: "elasticquotaprofiles"},
					Operation: v1.Create,
					Object: runtime.RawExtension{
						Raw: marshalObj(&quotav1alpha1.ElasticQuotaProfile{
							Spec: quotav1alpha1.ElasticQuotaProfileSpec{
								QuotaLabels: map[string]string{
									"valid-key": "invalid value with spaces",
								},
							},
						}),
					},
				},
			},
			expectedAllowed: false,
		},
		{
			name: "ignore other resources",
			req: admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Resource:  metav1.GroupVersionResource{Resource: "pods"},
					Operation: v1.Create,
				},
			},
			expectedAllowed: true,
		},
		{
			name: "allow delete operation",
			req: admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Resource:  metav1.GroupVersionResource{Resource: "elasticquotaprofiles"},
					Operation: v1.Delete,
				},
			},
			expectedAllowed: true,
		},
		{
			name: "invalid object decode error",
			req: admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Resource:  metav1.GroupVersionResource{Resource: "elasticquotaprofiles"},
					Operation: v1.Create,
					Object: runtime.RawExtension{
						Raw: []byte("invalid json"),
					},
				},
			},
			expectedAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := handler.Handle(context.TODO(), tt.req)
			assert.Equal(t, tt.expectedAllowed, resp.Allowed)
		})
	}
}

func marshalObj(obj interface{}) []byte {
	b, _ := json.Marshal(obj)
	return b
}

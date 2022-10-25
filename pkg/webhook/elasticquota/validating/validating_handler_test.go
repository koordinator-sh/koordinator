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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgfake "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned/fake"
	"sigs.k8s.io/scheduler-plugins/pkg/generated/informers/externalversions"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
)

func makeTestHandler() *ElasticQuotaValidatingHandler {
	client := fake.NewClientBuilder().Build()
	sche := client.Scheme()
	v1alpha1.AddToScheme(sche)
	decoder, _ := admission.NewDecoder(sche)
	handler := &ElasticQuotaValidatingHandler{}
	handler.InjectClient(client)
	handler.InjectDecoder(decoder)

	cacheTmp := &informertest.FakeInformers{
		InformersByGVK: map[schema.GroupVersionKind]cache.SharedIndexInformer{},
		Scheme:         sche,
	}
	pgClient := pgfake.NewSimpleClientset()
	quotaSharedInformerFactory := externalversions.NewSharedInformerFactory(pgClient, 0)
	quotaInformer := quotaSharedInformerFactory.Scheduling().V1alpha1().ElasticQuotas().Informer()
	cacheTmp.InformersByGVK[elasticquotasKind] = quotaInformer
	handler.InjectCache(cacheTmp)
	return handler
}

var elasticquotasKind = schema.GroupVersionKind{Group: "scheduling.sigs.k8s.io", Version: "v1alpha1", Kind: "ElasticQuota"}

func gvr(resource string) metav1.GroupVersionResource {
	return metav1.GroupVersionResource{
		Group:    corev1.SchemeGroupVersion.Group,
		Version:  corev1.SchemeGroupVersion.Version,
		Resource: resource,
	}
}

func TestElasticQuotaValidatingHandler_Handle(t *testing.T) {
	handler := makeTestHandler()
	ctx := context.Background()

	testCases := []struct {
		name    string
		request admission.Request
		allowed bool
		code    int32
	}{
		{
			name: "not a elasticQuota",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("configmaps"),
					Operation: admissionv1.Create,
				},
			},
			allowed: true,
		},
		{
			name: "elasticQuota with subresource",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:    gvr("elasticquotas"),
					Operation:   admissionv1.Create,
					SubResource: "status",
				},
			},
			allowed: true,
		},
		{
			name: "elasticQuota with empty object",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("elasticquotas"),
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{},
				},
			},
			allowed: false,
			code:    http.StatusBadRequest,
		},
		{
			name: "elasticQuota with object",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("elasticquotas"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"quota1"}}`),
					},
				},
			},
			allowed: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			response := handler.Handle(ctx, tc.request)
			if tc.allowed && !response.Allowed {
				t.Errorf("unexpeced failed to handler %#v", response)
			}
			if !tc.allowed && response.AdmissionResponse.Result.Code != tc.code {
				t.Errorf("unexpected code, got %v expected %v", response.AdmissionResponse.Result.Code, tc.code)
			}
		})
	}
}

func TestElasticQuotaValidatingHandler_RegisterWebhookEndpoints(t *testing.T) {
	engine := gin.Default()
	handler := makeTestHandler()
	handler.RegisterServices(engine.Group("/"))
	w := httptest.NewRecorder()
	plugin := elasticquota.NewPlugin(handler.Decoder, handler.Client)
	qt := plugin.QuotaTopo

	sub1 := MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(false).Obj()
	qt.OnQuotaAdd(sub1)

	req, _ := http.NewRequest("GET", "/quotaTopologySummaries", nil)
	engine.ServeHTTP(w, req)
	quotaTopologyInfo := elasticquota.NewQuotaTopologyForMarshal()
	err := json.NewDecoder(w.Result().Body).Decode(quotaTopologyInfo)

	assert.NoError(t, err)
	assert.Equal(t, 1, len(quotaTopologyInfo.QuotaHierarchyInfo["temp"]))
}

type quotaWrapper struct {
	*v1alpha1.ElasticQuota
}

func MakeQuota(name string) *quotaWrapper {
	eq := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{Kind: "ElasticQuota", APIVersion: "scheduling.sigs.k8s.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}
	return &quotaWrapper{eq}
}

func (q *quotaWrapper) Min(min corev1.ResourceList) *quotaWrapper {
	q.ElasticQuota.Spec.Min = min
	return q
}

func (q *quotaWrapper) Max(max corev1.ResourceList) *quotaWrapper {
	q.ElasticQuota.Spec.Max = max
	return q
}

func (q *quotaWrapper) IsParent(isParent bool) *quotaWrapper {
	if isParent {
		q.Labels[extension.LabelQuotaIsParent] = "true"
	} else {
		q.Labels[extension.LabelQuotaIsParent] = "false"
	}
	return q
}

func (q *quotaWrapper) ParentName(parentName string) *quotaWrapper {
	q.Labels[extension.LabelQuotaParent] = parentName
	return q
}

func (q *quotaWrapper) Obj() *v1alpha1.ElasticQuota {
	return q.ElasticQuota
}

type resourceWrapper struct{ corev1.ResourceList }

func MakeResourceList() *resourceWrapper {
	return &resourceWrapper{corev1.ResourceList{}}
}

func (r *resourceWrapper) CPU(val int64) *resourceWrapper {
	r.ResourceList[corev1.ResourceCPU] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Mem(val int64) *resourceWrapper {
	r.ResourceList[corev1.ResourceMemory] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Obj() corev1.ResourceList {
	return r.ResourceList
}

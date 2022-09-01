package validating

import (
	"context"
	"net/http"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func makeTestHandler() *ElasticQuotaValidatingHandler {
	client := fake.NewClientBuilder().Build()
	decoder, _ := admission.NewDecoder(scheme.Scheme)
	handler := &ElasticQuotaValidatingHandler{}
	handler.InjectClient(client)
	handler.InjectDecoder(decoder)
	return handler
}

func gvr(resource string) metav1.GroupVersionResource {
	return metav1.GroupVersionResource{
		Group:    corev1.SchemeGroupVersion.Group,
		Version:  corev1.SchemeGroupVersion.Version,
		Resource: resource,
	}
}

func TestElasticQuotaMutatingHandler_Handle(t *testing.T) {
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
					Resource:    gvr("elasticQuotas"),
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
					Resource:  gvr("elasticQuotas"),
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{},
				},
			},
			allowed: false,
			code:    http.StatusBadRequest,
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

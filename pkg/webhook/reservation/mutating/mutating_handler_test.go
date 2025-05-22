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
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	clientcache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

func makeTestHandler() (*ReservationMutatingHandler, *informertest.FakeInformers) {
	schedulingv1alpha1.AddToScheme(scheme.Scheme)
	client := fake.NewClientBuilder().Build()
	sche := client.Scheme()
	sche.AddKnownTypes(schema.GroupVersion{
		Group:   "scheduling.sigs.k8s.io",
		Version: "v1alpha1",
	}, &v1alpha1.ElasticQuota{}, &v1alpha1.ElasticQuotaList{})
	decoder := admission.NewDecoder(sche)
	handler := &ReservationMutatingHandler{}
	handler.InjectClient(client)
	handler.InjectDecoder(decoder)

	cacheTmp := &informertest.FakeInformers{
		InformersByGVK: map[schema.GroupVersionKind]clientcache.SharedIndexInformer{},
		Scheme:         sche,
	}

	return handler, cacheTmp
}

func gvr(resource string) metav1.GroupVersionResource {
	return metav1.GroupVersionResource{
		Group:    schedulingv1alpha1.SchemeGroupVersion.Group,
		Version:  schedulingv1alpha1.SchemeGroupVersion.Version,
		Resource: resource,
	}
}

func TestMutatingHandler(t *testing.T) {
	handler, _ := makeTestHandler()
	ctx := context.Background()
	testProfile := &configv1alpha1.ClusterColocationProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-profile",
		},
		Spec: configv1alpha1.ClusterColocationProfileSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"koordinator-colocation-reservation": "true",
				},
			},
			Labels: map[string]string{
				"foo": "bar",
			},
		},
	}
	err := handler.Client.Create(ctx, testProfile)
	assert.NoError(t, err)
	testProfile1 := &configv1alpha1.ClusterColocationProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-profile-1",
		},
		Spec: configv1alpha1.ClusterColocationProfileSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"koordinator-colocation-reservation": "true",
				},
			},
			Labels: map[string]string{
				"foo-1": "bar-1",
			},
		},
	}
	err = handler.Client.Create(ctx, testProfile1)
	assert.NoError(t, err)

	testCases := []struct {
		name    string
		request admission.Request
		allowed bool
		code    int32
	}{
		{
			name: "not a reservation",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("configmaps"),
					Operation: admissionv1.Create,
				},
			},
			allowed: true,
		},
		{
			name: "reservation with subresource",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:    gvr("reservations"),
					Operation:   admissionv1.Create,
					SubResource: "status",
				},
			},
			allowed: true,
		},
		{
			name: "reservation with empty object",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("reservations"),
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{},
				},
			},
			allowed: false,
			code:    http.StatusBadRequest,
		},
		{
			name: "reservation not create",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("reservations"),
					Operation: admissionv1.Update,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"reservation1"}}`),
					},
				},
			},
			allowed: true,
			code:    http.StatusOK,
		},
		{
			name: "reservation with object unmatched",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("reservations"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"reservation1", "labels": {"koordinator-colocation-reservation": "false"}}}`),
					},
				},
			},
			allowed: true,
		},
		{
			name: "reservation with object matched",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("reservations"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"reservation1", "labels": {"koordinator-colocation-reservation": "true"}}}`),
					},
				},
			},
			allowed: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			response := handler.Handle(ctx, tc.request)
			assert.Equal(t, tc.allowed, response.Allowed, fmt.Sprintf("unexpeced failed to handler %#v", response))
			if !tc.allowed {
				assert.Equal(t, tc.code, response.AdmissionResponse.Result.Code, fmt.Sprintf("unexpected code, got %v expected %v", response.AdmissionResponse.Result.Code, tc.code))
			}
		})
	}
}

type fakeManager struct {
	ctrl.Manager
}

func (f *fakeManager) GetClient() client.Client {
	return nil
}

func (f *fakeManager) GetScheme() *runtime.Scheme {
	return runtime.NewScheme()
}

func Test_reservationMutateBuilder(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		b := &reservationMutateBuilder{}
		got := b.WithControllerManager(&fakeManager{}).Build()
		assert.NotNil(t, got)
	})
}

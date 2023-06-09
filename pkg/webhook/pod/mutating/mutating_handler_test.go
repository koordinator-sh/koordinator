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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func makeTestHandler() *PodMutatingHandler {
	client := fake.NewClientBuilder().Build()
	decoder, _ := admission.NewDecoder(scheme.Scheme)
	handler := &PodMutatingHandler{}
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

func TestMutatingHandler(t *testing.T) {
	handler := makeTestHandler()
	ctx := context.Background()

	testCases := []struct {
		name    string
		request admission.Request
		allowed bool
		code    int32
	}{
		{
			name: "not a pod",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("configmaps"),
					Operation: admissionv1.Create,
				},
			},
			allowed: true,
		},
		{
			name: "pod with subresource",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:    gvr("pods"),
					Operation:   admissionv1.Create,
					SubResource: "status",
				},
			},
			allowed: true,
		},
		{
			name: "pod with empty object",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("pods"),
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{},
				},
			},
			allowed: false,
			code:    http.StatusBadRequest,
		},
		{
			name: "pod with object",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("pods"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"pod1"}}`),
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

func TestCustomMutatingFunc(t *testing.T) {
	testCases := []struct {
		name             string
		request          admission.Request
		mutatingFunc     mutatingPodFunc
		expectedResponse admission.Response
	}{
		{
			name: "no mutating plugin, mutating pod",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("pods"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"pod1"}}`),
					},
				},
			},
			mutatingFunc: nil,
			expectedResponse: admission.Response{
				Patches: nil,
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: 200,
					},
				},
			},
		},
		{
			name: "mutating namespace, mutating pod",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("pods"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"pod1"},"spec":{"containers":[]},"status":{}}`),
					},
				},
			},
			mutatingFunc: func(h *PodMutatingHandler, ctx context.Context, req admission.Request, pod *corev1.Pod) error {
				pod.Namespace = "mutating-namespace"
				return nil
			},
			expectedResponse: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{Operation: "add", Path: "/metadata/namespace", Value: "mutating-namespace"},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: 200,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			if tc.mutatingFunc != nil {
				customMutatingPodFuncs["mutating-namespace"] = tc.mutatingFunc
			}

			client := fake.NewClientBuilder().Build()
			decoder, _ := admission.NewDecoder(scheme.Scheme)
			handler := &PodMutatingHandler{
				Client:  client,
				Decoder: decoder,
			}
			response := handler.Handle(context.TODO(), tc.request)
			if len(response.Patches) != 0 {
				response.Patches = []jsonpatch.JsonPatchOperation{response.Patches[0]}
			}
			assert.Equal(tc.expectedResponse.Patches, response.Patches)

		})

	}
}

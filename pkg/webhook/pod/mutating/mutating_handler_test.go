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
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientcache "k8s.io/client-go/tools/cache"
	sigcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
)

func makeTestHandler() (*PodMutatingHandler, *informertest.FakeInformers) {
	client := fake.NewClientBuilder().Build()
	sche := client.Scheme()
	sche.AddKnownTypes(schema.GroupVersion{
		Group:   "scheduling.sigs.k8s.io",
		Version: "v1alpha1",
	}, &v1alpha1.ElasticQuota{}, &v1alpha1.ElasticQuotaList{})
	decoder := admission.NewDecoder(sche)
	handler := &PodMutatingHandler{}
	handler.InjectClient(client)
	handler.InjectDecoder(decoder)

	cacheTmp := &informertest.FakeInformers{
		InformersByGVK: map[schema.GroupVersionKind]clientcache.SharedIndexInformer{},
		Scheme:         sche,
	}
	handler.InjectCache(cacheTmp)

	return handler, cacheTmp
}

func gvr(resource string) metav1.GroupVersionResource {
	return metav1.GroupVersionResource{
		Group:    corev1.SchemeGroupVersion.Group,
		Version:  corev1.SchemeGroupVersion.Version,
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
			name: "pod with object unmatched",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("pods"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"pod1", "labels": {"koordinator-colocation-reservation": "false"}}}`),
					},
				},
			},
			allowed: true,
		},
		{
			name: "pod with object matched",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Resource:  gvr("pods"),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"name":"pod1", "labels": {"koordinator-colocation-reservation": "true"}}}`),
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

// var _ inject.Cache = &PodMutatingHandler{}

func (h *PodMutatingHandler) InjectCache(cache sigcache.Cache) error {
	ctx := context.TODO()
	quotaInformer, err := cache.GetInformer(ctx, &v1alpha1.ElasticQuota{})
	if err != nil {
		return err
	}
	plugin := elasticquota.NewPlugin(h.Decoder, h.Client)
	qt := plugin.QuotaTopo
	quotaInformer.AddEventHandler(clientcache.ResourceEventHandlerFuncs{
		AddFunc:    qt.OnQuotaAdd,
		UpdateFunc: qt.OnQuotaUpdate,
		DeleteFunc: qt.OnQuotaDelete,
	})
	return nil
}

func TestHandleUpdate_SkippedWithoutLabel(t *testing.T) {
	handler, _ := makeTestHandler()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
	}
	// No LabelPodMutatingUpdate label → handler must be a no-op
	err := handler.handleUpdate(context.TODO(), admission.Request{}, pod)
	assert.NoError(t, err)
	// pod must be unmodified
	assert.Nil(t, pod.Labels)
}

func TestHandleUpdate_RunsPluginsWhenLabelSet(t *testing.T) {
	handler, _ := makeTestHandler()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodMutatingUpdate: "true",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							extension.BatchCPU: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							extension.BatchCPU: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
				},
			},
		},
	}

	err := handler.handleUpdate(context.TODO(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
		},
	}, pod)
	assert.NoError(t, err)
	// assert expected pod mutations were applied by ExtendedResourceSpec
	// The koordinator.sh/extended-resource-spec annotation should be set
	assert.NotEmpty(t, pod.Annotations[extension.AnnotationExtendedResourceSpec])
}

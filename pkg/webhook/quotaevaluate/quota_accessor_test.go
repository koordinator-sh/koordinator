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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

type fakeClientWrapper struct {
	client.Client
	updateErr error
}

func (c *fakeClientWrapper) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.updateErr != nil {
		return c.updateErr
	}
	return c.Client.Update(ctx, obj, opts...)
}

type fakeReaderWrapper struct {
	client.Reader
	getErr error
}

func (r *fakeReaderWrapper) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	if r.getErr != nil {
		return r.getErr
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func TestUpdateQuotaStatus(t *testing.T) {
	conflictError := apierrors.NewConflict(schema.GroupResource{Group: "scheduling.sigs.k8s.io", Resource: "elasticquotas"}, "", fmt.Errorf("the object has been modified"))

	testCases := []struct {
		name            string
		updateErr       error
		getErr          error
		expectError     bool
		expectCached    bool
		expectCachedCPU string
	}{
		{
			name:            "update success",
			expectError:     false,
			expectCached:    true,
			expectCachedCPU: "8",
		},
		{
			name:            "conflict refreshes cache from apiReader",
			updateErr:       conflictError,
			expectError:     true,
			expectCached:    true,
			expectCachedCPU: "4",
		},
		{
			name:         "conflict with apiReader failure skips cache",
			updateErr:    conflictError,
			getErr:       fmt.Errorf("api server unavailable"),
			expectError:  true,
			expectCached: false,
		},
		{
			name:         "non-conflict error skips fetch and cache",
			updateErr:    fmt.Errorf("internal server error"),
			expectError:  true,
			expectCached: false,
		},
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			quota := &v1alpha1.ElasticQuota{
				TypeMeta: metav1.TypeMeta{Kind: "ElasticQuota", APIVersion: "scheduling.sigs.k8s.io/v1alpha1"},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "quota1",
				},
				Status: v1alpha1.ElasticQuotaStatus{
					Used: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(quota).Build()
			cw := &fakeClientWrapper{Client: fakeClient, updateErr: tc.updateErr}
			rw := &fakeReaderWrapper{Reader: fakeClient, getErr: tc.getErr}
			accessor := NewQuotaAccessor(cw, rw)

			got := &v1alpha1.ElasticQuota{}
			err := fakeClient.Get(context.TODO(), types.NamespacedName{Namespace: "ns1", Name: "quota1"}, got)
			assert.NoError(t, err)

			got.Status.Used = corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("8"),
			}

			err = accessor.UpdateQuotaStatus(got)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			cached, ok := accessor.updatedQuotas.Get("ns1/quota1")
			assert.Equal(t, tc.expectCached, ok)
			if tc.expectCached {
				cachedQuota := cached.(*v1alpha1.ElasticQuota)
				assert.Equal(t, resource.MustParse(tc.expectCachedCPU), cachedQuota.Status.Used[corev1.ResourceCPU])
			}
		})
	}
}

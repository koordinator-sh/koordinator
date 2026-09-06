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

package util

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"
)

func TestEvictPodByVersion(t *testing.T) {
	tests := []struct {
		name         string
		evictVersion string
		wantErr      bool
	}{
		{
			name:         "evict with v1 succeeds",
			evictVersion: "v1",
			wantErr:      false,
		},
		{
			name:         "evict with unsupported version returns error",
			evictVersion: "v1beta1",
			wantErr:      true,
		},
		{
			name:         "evict with empty version returns error",
			evictVersion: "",
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			}
			fakeClient := kubefake.NewSimpleClientset(pod)
			err := EvictPodByVersion(context.TODO(), fakeClient, "default", "test-pod", metav1.DeleteOptions{}, tt.evictVersion)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestSupportEviction(t *testing.T) {
	tests := []struct {
		name        string
		setupFake   func(fake *coretesting.Fake)
		wantVersion string
		wantErr     bool
	}{
		{
			name: "policy group and pods/eviction resource found",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "policy/v1",
							APIResources: []metav1.APIResource{
								{Name: EvictionSubResourceName, Kind: EvictionKind},
							},
						},
					}
					return true, nil, nil
				})
				fake.AddReactor("get", "resource", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "v1",
							APIResources: []metav1.APIResource{
								{
									Name:    EvictionSubResourceName,
									Kind:    EvictionKind,
									Group:   EvictionGroupName,
									Version: "v1",
								},
							},
						},
					}
					return true, nil, nil
				})
			},
			wantVersion: "policy/v1",
			wantErr:     false,
		},
		{
			name: "policy group found but pods/eviction not in v1 resources falls back to preferred",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "policy/v1",
							APIResources: []metav1.APIResource{
								{Name: "policies", Kind: "Policy"},
							},
						},
					}
					return true, nil, nil
				})
				fake.AddReactor("get", "resource", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "v1",
							APIResources: []metav1.APIResource{
								{Name: "pods", Kind: "Pod"},
							},
						},
					}
					return true, nil, nil
				})
			},
			wantVersion: "policy/v1",
			wantErr:     false,
		},
		{
			name: "policy group not found returns empty version",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "apps/v1",
							APIResources: []metav1.APIResource{
								{Name: "deployments", Kind: "Deployment"},
							},
						},
					}
					return true, nil, nil
				})
			},
			wantVersion: "",
			wantErr:     false,
		},
		{
			name: "ServerGroups returns error",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("server groups error")
				})
			},
			wantVersion: "",
			wantErr:     true,
		},
		{
			name: "ServerResourcesForGroupVersion returns not-found is treated as no eviction support",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "policy/v1",
							APIResources: []metav1.APIResource{
								{Name: EvictionSubResourceName, Kind: EvictionKind},
							},
						},
					}
					return true, nil, nil
				})
				fake.AddReactor("get", "resource", func(action coretesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.NewNotFound(schema.GroupResource{Resource: "resource"}, "v1")
				})
			},
			wantVersion: "",
			wantErr:     false,
		},
		{
			name: "ServerResourcesForGroupVersion returns non-not-found error",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "policy/v1",
							APIResources: []metav1.APIResource{
								{Name: EvictionSubResourceName, Kind: EvictionKind},
							},
						},
					}
					return true, nil, nil
				})
				fake.AddReactor("get", "resource", func(action coretesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("internal server error")
				})
			},
			wantVersion: "",
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := kubefake.NewSimpleClientset()
			tt.setupFake(&fakeClient.Fake)
			version, err := SupportEviction(fakeClient)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantVersion, version)
		})
	}
}

func TestFindSupportedEvictVersion(t *testing.T) {
	tests := []struct {
		name        string
		setupFake   func(fake *coretesting.Fake)
		wantVersion string
		wantErr     bool
	}{
		{
			name: "returns version segment from policy/v1",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "policy/v1",
							APIResources: []metav1.APIResource{
								{Name: EvictionSubResourceName, Kind: EvictionKind},
							},
						},
					}
					return true, nil, nil
				})
				fake.AddReactor("get", "resource", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{
						{
							GroupVersion: "v1",
							APIResources: []metav1.APIResource{
								{
									Name:    EvictionSubResourceName,
									Kind:    EvictionKind,
									Group:   EvictionGroupName,
									Version: "v1",
								},
							},
						},
					}
					return true, nil, nil
				})
			},
			wantVersion: "v1",
			wantErr:     false,
		},
		{
			name: "returns empty version when policy group not found",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					fake.Resources = []*metav1.APIResourceList{}
					return true, nil, nil
				})
			},
			wantVersion: "",
			wantErr:     false,
		},
		{
			name: "propagates error from SupportEviction",
			setupFake: func(fake *coretesting.Fake) {
				fake.AddReactor("get", "group", func(action coretesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("discovery failed")
				})
			},
			wantVersion: "",
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := kubefake.NewSimpleClientset()
			tt.setupFake(&fakeClient.Fake)
			version, err := FindSupportedEvictVersion(fakeClient)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantVersion, version)
		})
	}
}

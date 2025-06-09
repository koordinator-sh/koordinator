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

package colocationprofile

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
)

func Test_listPodsForProfile(t *testing.T) {
	scheme := getTestScheme()
	testProfileForNothing := &configv1alpha1.ClusterColocationProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-profile",
			Labels: map[string]string{
				extension.LabelControllerManaged: "true",
			},
		},
		Spec: configv1alpha1.ClusterColocationProfileSpec{
			Selector: nil,
			Labels: map[string]string{
				"testLabelA": "valueA",
			},
			Annotations: map[string]string{
				"testAnnotationA": "valueA",
			},
			QoSClass:            string(extension.QoSBE),
			KoordinatorPriority: pointer.Int32(1111),
			Patch: runtime.RawExtension{
				Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
			},
		},
	}
	testProfile := &configv1alpha1.ClusterColocationProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-profile",
			Labels: map[string]string{
				extension.LabelControllerManaged: "true",
			},
		},
		Spec: configv1alpha1.ClusterColocationProfileSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test-selector-key": "test",
				},
			},
			Labels: map[string]string{
				"testLabelA": "valueA",
			},
			Annotations: map[string]string{
				"testAnnotationA": "valueA",
			},
			QoSClass:            string(extension.QoSBE),
			KoordinatorPriority: pointer.Int32(1111),
			Patch: runtime.RawExtension{
				Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
			},
		},
	}
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-selector-key": "test",
			},
		},
		Spec: corev1.PodSpec{
			Priority: pointer.Int32(0),
		},
	}
	testPod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "test-ns",
			Labels:    map[string]string{},
		},
		Spec: corev1.PodSpec{
			Priority: pointer.Int32(9000),
		},
	}
	type fields struct {
		client client.Client
	}
	tests := []struct {
		name    string
		fields  fields
		arg     *configv1alpha1.ClusterColocationProfile
		want    *corev1.PodList
		wantErr bool
	}{
		{
			name: "select no pod",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(testPod, testPod1).
					WithIndex(&corev1.Pod{}, "spec.nodeName", func(obj client.Object) []string {
						return []string{obj.(*corev1.Pod).Spec.NodeName}
					}).Build(),
			},
			arg:     testProfileForNothing,
			want:    nil,
			wantErr: false,
		},
		{
			name: "select matched pods",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(testPod, testPod1).
					WithIndex(&corev1.Pod{}, "spec.nodeName", func(obj client.Object) []string {
						return []string{obj.(*corev1.Pod).Spec.NodeName}
					}).Build(),
			},
			arg: testProfile,
			want: &corev1.PodList{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "PodList",
				},
				Items: []corev1.Pod{
					*testPod,
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Reconciler{
				Client: tt.fields.client,
			}

			got, gotErr := r.listPodsForProfile(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func Test_updatePodByClusterColocationProfile(t *testing.T) {
	testCases := []struct {
		name        string
		pod         *corev1.Pod
		profile     *configv1alpha1.ClusterColocationProfile
		podNotExist bool
		wantPod     *corev1.Pod
		want        bool
		wantErr     bool
	}{
		{
			name: "mutating pod",
			pod: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
					ResourceVersion: "1000",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
					Labels: map[string]string{
						extension.LabelControllerManaged: "true",
					},
				},
				Spec: configv1alpha1.ClusterColocationProfileSpec{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"enable-koordinator-colocation": "true",
						},
					},
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"koordinator-colocation-pod": "true",
						},
					},
					Labels: map[string]string{
						"testLabelA": "valueA",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					QoSClass:            string(extension.QoSBE),
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			wantPod: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
						"testLabelA":                 "valueA",
						"test-patch-label":           "patch-a",
						extension.LabelPodQoS:        string(extension.QoSBE),
						extension.LabelPodPriority:   "1111",
					},
					Annotations: map[string]string{
						"testAnnotationA":       "valueA",
						"test-patch-annotation": "patch-b",
					},
					ResourceVersion: "1001",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "mutate advanced attributes",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
						"label-key-to-load":          "test-label-value",
					},
					Annotations: map[string]string{
						"annotation-key-to-load": "test-annotation-value",
					},
					ResourceVersion: "1000",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
					Labels: map[string]string{
						extension.LabelControllerManaged: "true",
					},
				},
				Spec: configv1alpha1.ClusterColocationProfileSpec{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"enable-koordinator-colocation": "true",
						},
					},
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"koordinator-colocation-pod": "true",
						},
					},
					Labels: map[string]string{
						"testLabelA":                 "valueA",
						extension.LabelSchedulerName: "koordinator-scheduler",
					},
					LabelKeysMapping: map[string]string{
						"label-key-to-load":           "label-key-to-store",
						"label-key-to-load-not-exist": "label-key-to-store-not-exist",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					AnnotationKeysMapping: map[string]string{
						"annotation-key-to-load": "annotation-key-to-store",
					},
					QoSClass:            string(extension.QoSBE),
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			wantPod: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod":   "true",
						"testLabelA":                   "valueA",
						"test-patch-label":             "patch-a",
						"label-key-to-load":            "test-label-value",
						"label-key-to-store":           "test-label-value",
						"label-key-to-store-not-exist": "",
						extension.LabelPodQoS:          string(extension.QoSBE),
						extension.LabelPodPriority:     "1111",
						extension.LabelSchedulerName:   "koordinator-scheduler",
					},
					Annotations: map[string]string{
						"testAnnotationA":         "valueA",
						"test-patch-annotation":   "patch-b",
						"annotation-key-to-load":  "test-annotation-value",
						"annotation-key-to-store": "test-annotation-value",
					},
					ResourceVersion: "1001",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "mutate advanced attributes 1",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       "default",
					Name:            "test-pod-1",
					ResourceVersion: "1000",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",

					Labels: map[string]string{
						extension.LabelControllerManaged: "true",
					},
				},
				Spec: configv1alpha1.ClusterColocationProfileSpec{
					Selector: &metav1.LabelSelector{},
					Labels: map[string]string{
						"testLabelA":                 "valueA",
						extension.LabelSchedulerName: "koordinator-scheduler",
					},
					LabelKeysMapping: map[string]string{
						"label-key-to-load":           "label-key-to-store",
						"label-key-to-load-not-exist": "label-key-to-store-not-exist",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					AnnotationKeysMapping: map[string]string{
						"annotation-key-to-load": "annotation-key-to-store",
					},
					QoSClass:            string(extension.QoSBE),
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			wantPod: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"testLabelA":                   "valueA",
						"test-patch-label":             "patch-a",
						"label-key-to-store":           "",
						"label-key-to-store-not-exist": "",
						extension.LabelPodQoS:          string(extension.QoSBE),
						extension.LabelPodPriority:     "1111",
						extension.LabelSchedulerName:   "koordinator-scheduler",
					},
					Annotations: map[string]string{
						"testAnnotationA":         "valueA",
						"test-patch-annotation":   "patch-b",
						"annotation-key-to-store": "",
					},
					ResourceVersion: "1001",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "failed due to pod not found",
			pod: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
					ResourceVersion: "1000",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",

					Labels: map[string]string{
						extension.LabelControllerManaged: "true",
					},
				},
				Spec: configv1alpha1.ClusterColocationProfileSpec{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"enable-koordinator-colocation": "true",
						},
					},
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"koordinator-colocation-pod": "true",
						},
					},
					Labels: map[string]string{
						"testLabelA": "valueA",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					QoSClass:            string(extension.QoSBE),
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			podNotExist: true,
			want:        false,
			wantErr:     true,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			s := getTestScheme()
			r := &Reconciler{
				Client:      fake.NewClientBuilder().WithScheme(s).WithObjects(tt.pod, tt.profile).Build(),
				Scheme:      s,
				rateLimiter: rate.NewLimiter(rate.Limit(MaxUpdatePodQPS), MaxUpdatePodQPSBurst),
			}
			if tt.podNotExist {
				err := r.Client.Delete(context.TODO(), tt.pod)
				assert.NoError(t, err)
			}

			got, gotErr := r.updatePodByClusterColocationProfile(context.TODO(), tt.profile, tt.pod)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)

			if tt.wantPod != nil {
				gotPod := &corev1.Pod{}
				err := r.Client.Get(context.TODO(), types.NamespacedName{Name: tt.pod.Name, Namespace: tt.pod.Namespace}, gotPod)
				assert.NoError(t, err)
				assert.Equal(t, tt.wantPod, gotPod)
			}
		})

	}
}

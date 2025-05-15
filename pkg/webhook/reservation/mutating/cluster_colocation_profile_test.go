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
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func init() {
	_ = configv1alpha1.AddToScheme(scheme.Scheme)
}

func newAdmission(op admissionv1.Operation, object, oldObject runtime.RawExtension, subResource string) admission.Request {
	return admission.Request{
		AdmissionRequest: newAdmissionRequest(op, object, oldObject, subResource),
	}
}

func newAdmissionRequest(op admissionv1.Operation, object, oldObject runtime.RawExtension, subResource string) admissionv1.AdmissionRequest {
	return admissionv1.AdmissionRequest{
		Resource:    metav1.GroupVersionResource{Group: configv1alpha1.SchemeGroupVersion.Group, Version: configv1alpha1.SchemeGroupVersion.Version, Resource: "reservations"},
		Operation:   op,
		Object:      object,
		OldObject:   oldObject,
		SubResource: subResource,
	}
}

func TestClusterColocationProfileMutatingReservation(t *testing.T) {
	preemptionPolicy := corev1.PreemptionPolicy("fakePreemptionPolicy")
	testCases := []struct {
		name                      string
		reservation               *schedulingv1alpha1.Reservation
		profile                   *configv1alpha1.ClusterColocationProfile
		percent                   *int32
		randIntnFn                func(int) int
		defaultSkipUpdateResource bool
		expected                  *schedulingv1alpha1.Reservation
		expectErr                 bool
	}{
		{
			name: "mutating reservation",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
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
						"testLabelA": "valueA",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					SchedulerName:       "koordinator-scheduler",
					KoordinatorPriority: pointer.Int32(1111),
					PriorityClassName:   "koordinator-prod",
					QoSClass:            string(extension.QoSLS),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
						"testLabelA":                         "valueA",
						"test-patch-label":                   "patch-a",
						extension.LabelPodQoS:                string(extension.QoSLS),
						extension.LabelPodPriority:           "1111",
					},
					Annotations: map[string]string{
						"testAnnotationA":       "valueA",
						"test-patch-annotation": "patch-b",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName:     "koordinator-scheduler",
							PriorityClassName: "koordinator-prod",
							Priority:          pointer.Int32(extension.PriorityProdValueDefault),
							PreemptionPolicy:  &preemptionPolicy,
						},
					},
				},
			},
		},
		{
			name: "mutating reservation 1",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
				},
				Spec: configv1alpha1.ClusterColocationProfileSpec{
					Selector: &metav1.LabelSelector{},
					Labels: map[string]string{
						"testLabelA": "valueA",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					SchedulerName:       "koordinator-scheduler",
					KoordinatorPriority: pointer.Int32(1111),
					PriorityClassName:   "koordinator-prod",
					QoSClass:            string(extension.QoSLS),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"testLabelA":               "valueA",
						"test-patch-label":         "patch-a",
						extension.LabelPodQoS:      string(extension.QoSLS),
						extension.LabelPodPriority: "1111",
					},
					Annotations: map[string]string{
						"testAnnotationA":       "valueA",
						"test-patch-annotation": "patch-b",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName:     "koordinator-scheduler",
							PriorityClassName: "koordinator-prod",
							Priority:          pointer.Int32(extension.PriorityProdValueDefault),
							PreemptionPolicy:  &preemptionPolicy,
						},
					},
				},
			},
		},
		{
			name: "mutating reservation, percent 100",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
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
						"testLabelA": "valueA",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			percent: pointer.Int32(100),
			expected: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
						"testLabelA":                         "valueA",
						"test-patch-label":                   "patch-a",
						extension.LabelPodQoS:                string(extension.QoSBE),
						extension.LabelPodPriority:           "1111",
					},
					Annotations: map[string]string{
						"testAnnotationA":       "valueA",
						"test-patch-annotation": "patch-b",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "koordinator-scheduler",
						},
					},
				},
			},
		},
		{
			name: "mutating reservation, percent 0",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
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
						"testLabelA": "valueA",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			percent: pointer.Int32(0),
			expected: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
		},
		{
			name: "mutating reservation, percent 50, rand=30",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
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
						"testLabelA": "valueA",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			percent: pointer.Int32(50),
			randIntnFn: func(i int) int {
				return 30
			},
			expected: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
						"testLabelA":                         "valueA",
						extension.LabelPodQoS:                string(extension.QoSBE),
						extension.LabelPodPriority:           "1111",
						"test-patch-label":                   "patch-a",
					},
					Annotations: map[string]string{
						"testAnnotationA":       "valueA",
						"test-patch-annotation": "patch-b",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "koordinator-scheduler",
						},
					},
				},
			},
		},
		{
			name: "mutate advanced attributes",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
						"label-key-to-load":                  "test-label-value",
					},
					Annotations: map[string]string{
						"testAnnotationA":        "valueA",
						"annotation-key-to-load": "test-annotation-value",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
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
						"testLabelA":                 "valueA",
						extension.LabelSchedulerName: "koordinator-scheduler",
					},
					LabelKeysMapping: map[string]string{
						"label-key-to-load": "label-key-to-store",
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
			expected: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
						"testLabelA":                         "valueA",
						"test-patch-label":                   "patch-a",
						"label-key-to-load":                  "test-label-value",
						"label-key-to-store":                 "test-label-value",
						extension.LabelPodQoS:                string(extension.QoSBE),
						extension.LabelPodPriority:           "1111",
						extension.LabelSchedulerName:         "koordinator-scheduler",
					},
					Annotations: map[string]string{
						"testAnnotationA":         "valueA",
						"test-patch-annotation":   "patch-b",
						"annotation-key-to-load":  "test-annotation-value",
						"annotation-key-to-store": "test-annotation-value",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
		},
		{
			name: "mutating failed",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
				},
				Spec: configv1alpha1.ClusterColocationProfileSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"koordinator-colocation-reservation": "true",
						},
					},
					PriorityClassName: "unknown-priority-class",
				},
			},
			expected: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-reservation-1",
					Labels: map[string]string{
						"koordinator-colocation-reservation": "true",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
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
							SchedulerName: "nonExistSchedulerName",
						},
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			client := fake.NewClientBuilder().Build()
			decoder := admission.NewDecoder(scheme.Scheme)
			handler := &ReservationMutatingHandler{
				Client:  client,
				Decoder: decoder,
			}

			prodPriorityClass := &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "koordinator-prod",
				},
				Value:            extension.PriorityProdValueDefault,
				PreemptionPolicy: &preemptionPolicy,
			}
			err := client.Create(context.TODO(), prodPriorityClass)
			assert.NoError(err)

			if tc.percent != nil {
				s := intstr.FromInt32(*tc.percent)
				tc.profile.Spec.Probability = &s
			}
			if tc.randIntnFn != nil {
				originalFn := randIntnFn
				randIntnFn = tc.randIntnFn
				defer func() {
					randIntnFn = originalFn
				}()
			}
			err = client.Create(context.TODO(), tc.profile)
			assert.NoError(err)

			req := newAdmission(admissionv1.Create, runtime.RawExtension{}, runtime.RawExtension{}, "")
			err = handler.clusterColocationProfileMutatingReservation(context.TODO(), req, tc.reservation)
			assert.Equal(tc.expectErr, err != nil, err)

			assert.Equal(tc.expected, tc.reservation)
		})

	}
}

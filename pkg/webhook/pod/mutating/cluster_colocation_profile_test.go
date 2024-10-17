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
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func SetRandIntnFnWhenTest(updatedRandIntnFn func(int) int) func() {
	originalFn := randIntnFn
	randIntnFn = updatedRandIntnFn
	return func() {
		randIntnFn = originalFn
	}
}

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
		Resource:    metav1.GroupVersionResource{Group: corev1.SchemeGroupVersion.Group, Version: corev1.SchemeGroupVersion.Version, Resource: "pods"},
		Operation:   op,
		Object:      object,
		OldObject:   oldObject,
		SubResource: subResource,
	}
}

func TestClusterColocationProfileMutatingPod(t *testing.T) {
	preemptionPolicy := corev1.PreemptionPolicy("fakePreemptionPolicy")
	testCases := []struct {
		name                      string
		pod                       *corev1.Pod
		profile                   *configv1alpha1.ClusterColocationProfile
		percent                   *int
		randIntnFn                func(int) int
		defaultSkipUpdateResource bool
		expected                  *corev1.Pod
	}{
		{
			name: "mutating pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &corev1.Pod{
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
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "set default request to the limit",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
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
							},
						},
					},
					SchedulerName: "nonExistSchedulerName",
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &corev1.Pod{
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
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "keep limit unset",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					SchedulerName: "nonExistSchedulerName",
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &corev1.Pod{
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
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "mutating pod, percent 100",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			percent: pointer.Int(100),
			expected: &corev1.Pod{
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
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "mutating pod, percent 0",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			percent: pointer.Int(0),
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName: "nonExistSchedulerName",
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
		},
		{
			name: "mutating pod, percent 50, rand=70",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			percent: pointer.Int(50),
			randIntnFn: func(i int) int {
				return 70
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName: "nonExistSchedulerName",
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
		},
		{
			name: "mutating pod, percent 50, rand=30",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			percent: pointer.Int(50),
			randIntnFn: func(i int) int {
				return 30
			},
			expected: &corev1.Pod{
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
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "mutating pod, skip update resource by profile annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
					Annotations: map[string]string{
						extension.AnnotationSkipUpdateResource: "true",
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
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &corev1.Pod{
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
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "mutating pod, skip update resource by leveraging profile",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
						"testAnnotationA":                      "valueA",
						extension.AnnotationSkipUpdateResource: "true",
					},
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			defaultSkipUpdateResource: true,
			expected: &corev1.Pod{
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
						"testAnnotationA":                      "valueA",
						"test-patch-annotation":                "patch-b",
						extension.AnnotationSkipUpdateResource: "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "mutate pod labels according to labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
						"label-key-to-load":          "test-label-value",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					LabelKeysMapping: map[string]string{
						"label-key-to-load": "label-key-to-store",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
						"testLabelA":                 "valueA",
						"test-patch-label":           "patch-a",
						"label-key-to-load":          "test-label-value",
						"label-key-to-store":         "test-label-value",
						extension.LabelPodQoS:        string(extension.QoSBE),
						extension.LabelPodPriority:   "1111",
					},
					Annotations: map[string]string{
						"testAnnotationA":       "valueA",
						"test-patch-annotation": "patch-b",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "mutate pod labels according to labels 1",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
						"label-key-to-load":          "test-label-value",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					LabelKeysMapping: map[string]string{
						"label-key-to-load":           "label-key-to-store",
						"label-key-to-load-not-exist": "label-key-to-store-not-exist",
					},
					Annotations: map[string]string{
						"testAnnotationA": "valueA",
					},
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &corev1.Pod{
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
					},
					Annotations: map[string]string{
						"testAnnotationA":       "valueA",
						"test-patch-annotation": "patch-b",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
		{
			name: "mutate pod annotations according to annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod-1",
					Annotations: map[string]string{
						"annotation-key-to-load": "test-annotation-value",
					},
					Labels: map[string]string{
						"koordinator-colocation-pod": "true",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
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
					Priority:      pointer.Int32(extension.PriorityBatchValueMax),
				},
			},
			profile: &configv1alpha1.ClusterColocationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-profile",
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
					AnnotationKeysMapping: map[string]string{
						"annotation-key-to-load": "annotation-key-to-store",
					},
					SchedulerName:       "koordinator-scheduler",
					QoSClass:            string(extension.QoSBE),
					PriorityClassName:   "koordinator-batch",
					KoordinatorPriority: pointer.Int32(1111),
					Patch: runtime.RawExtension{
						Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
					},
				},
			},
			expected: &corev1.Pod{
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
						"testAnnotationA":         "valueA",
						"test-patch-annotation":   "patch-b",
						"annotation-key-to-load":  "test-annotation-value",
						"annotation-key-to-store": "test-annotation-value",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Overhead: map[corev1.ResourceName]resource.Quantity{
						extension.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
						extension.BatchMemory: resource.MustParse("2Gi"),
					},
					SchedulerName:     "koordinator-scheduler",
					Priority:          pointer.Int32(extension.PriorityBatchValueMax),
					PriorityClassName: "koordinator-batch",
					PreemptionPolicy:  &preemptionPolicy,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer feature.SetFeatureGateDuringTest(t, feature.DefaultMutableFeatureGate, features.ColocationProfileSkipMutatingResources, tc.defaultSkipUpdateResource)()
			assert := assert.New(t)

			client := fake.NewClientBuilder().Build()
			decoder := admission.NewDecoder(scheme.Scheme)
			handler := &PodMutatingHandler{
				Client:  client,
				Decoder: decoder,
			}

			namespaceObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					Labels: map[string]string{
						"enable-koordinator-colocation": "true",
					},
				},
			}
			err := client.Create(context.TODO(), namespaceObj)
			assert.NoError(err)

			batchPriorityClass := &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "koordinator-batch",
				},
				Value:            extension.PriorityBatchValueMax,
				PreemptionPolicy: &preemptionPolicy,
			}
			err = client.Create(context.TODO(), batchPriorityClass)
			assert.NoError(err)

			if tc.percent != nil {
				s := intstr.FromInt(*tc.percent)
				tc.profile.Spec.Probability = &s
			}
			if tc.randIntnFn != nil {
				defer SetRandIntnFnWhenTest(tc.randIntnFn)()
			}
			err = client.Create(context.TODO(), tc.profile)
			assert.NoError(err)

			req := newAdmission(admissionv1.Create, runtime.RawExtension{}, runtime.RawExtension{}, "")
			err = handler.clusterColocationProfileMutatingPod(context.TODO(), req, tc.pod)
			assert.NoError(err)

			assert.Equal(tc.expected, tc.pod)
		})

	}
}

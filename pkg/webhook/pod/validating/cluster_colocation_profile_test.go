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
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func init() {
	_ = configv1alpha1.AddToScheme(scheme.Scheme)
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

func TestClusterColocationProfileValidatingPod(t *testing.T) {
	tests := []struct {
		name        string
		operation   admissionv1.Operation
		oldPod      *corev1.Pod
		newPod      *corev1.Pod
		wantAllowed bool
		wantReason  string
		wantErr     bool
	}{
		{
			name:        "non-colocation empty pod",
			operation:   admissionv1.Create,
			newPod:      &corev1.Pod{},
			wantAllowed: true,
			wantReason:  "",
			wantErr:     false,
		},
		{
			name:      "validate immutable QoS",
			operation: admissionv1.Update,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
			},
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
			},
			wantAllowed: false,
			wantReason:  `labels.koordinator.sh/qosClass: Invalid value: "LS": field is immutable`,
		},
		{
			name:      "validate remove QoS",
			operation: admissionv1.Update,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
			},
			wantAllowed: false,
			wantReason:  `labels.koordinator.sh/qosClass: Invalid value: "": field is immutable`,
		},
		{
			name:      "validate immutable priorityClass",
			operation: admissionv1.Update,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
				},
			},
			oldPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
				},
			},
			wantAllowed: false,
			wantReason:  `spec.priority: Invalid value: "Prod": field is immutable`,
		},
		{
			name:      "validate remove priorityClass",
			operation: admissionv1.Update,
			newPod:    &corev1.Pod{},
			oldPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
				},
			},
			wantAllowed: false,
			wantReason:  `spec.priority: Invalid value: "": field is immutable`,
		},
		{
			name:      "validate koordinator priority",
			operation: admissionv1.Update,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodPriority: "8888",
					},
				},
			},
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodPriority: "9999",
					},
				},
			},
			wantAllowed: false,
			wantReason:  `labels.koordinator.sh/priority: Invalid value: "8888": field is immutable`,
		},
		{
			name:      "validate remove koordinator priority",
			operation: admissionv1.Update,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodPriority: "9999",
					},
				},
			},
			wantAllowed: false,
			wantReason:  `labels.koordinator.sh/priority: Invalid value: "": field is immutable`,
		},
		{
			name:      "allowed QoS and priorityClass combination: BE And NonProd",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
				},
			},
			wantAllowed: true,
		},
		{
			name:      "allowed QoS and priorityClass combination: LSR And Prod",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
				},
			},
			wantAllowed: true,
		},
		{
			name:      "forbidden QoS and priorityClass combination: BE And Prod",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=BE and koordinator.sh/priority=Prod cannot be used in combination`,
		},
		{
			name:      "forbidden QoS and priorityClass combination: LSR And Batch",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMax),
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=LSR and koordinator.sh/priority=Batch cannot be used in combination`,
		},
		{
			name:      "forbidden QoS and priorityClass combination: LSR And Mid",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityMidValueMax),
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=LSR and koordinator.sh/priority=Mid cannot be used in combination`,
		},
		{
			name:      "forbidden QoS and priorityClass combination: LSR And Free",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityFreeValueMax),
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=LSR and koordinator.sh/priority=Free cannot be used in combination`,
		},
		{
			name:      "validate resources - LSR And Prod",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
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
				},
			},
			wantAllowed: true,
		},
		{
			name:      "forbidden resources - LSR And Prod: negative resource requirements",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("-1"),
									corev1.ResourceMemory: resource.MustParse("-4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("-1"),
									corev1.ResourceMemory: resource.MustParse("-4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `[pod.spec.containers.test-container-a.resources.requests.cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.requests.memory: Invalid value: "-4Gi": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.memory: Invalid value: "-4Gi": quantity must be positive]`,
		},
		{
			name:      "forbidden resources - LSR And Prod: requests less than limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `pod.spec.containers.test-container-a.resources: Forbidden: resource memory of container test-container-a: quantity of request and limit should be equal`,
		},
		{
			name:      "forbidden resources - LSR And Prod: missing CPU/Memory",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
								Requests: corev1.ResourceList{},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `[pod.spec.containers.test-container-a.resources.requests.cpu: Not found: "null", pod.spec.containers.test-container-a.resources: Forbidden: resource cpu of container test-container-a: quantity of request and limit must be equal, pod.spec.containers.test-container-a.resources.requests.memory: Not found: "null", pod.spec.containers.test-container-a.resources: Forbidden: resource memory of container test-container-a: quantity of request and limit must be equal]`,
		},
		{
			name:      "validate resources - LS And Prod: requests equals limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
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
				},
			},
			wantAllowed: true,
		},
		{
			name:      "validate resources - LS And Prod: request cpu less than limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: true,
		},
		{
			name:      "forbidden resources - LS And Prod: negative resource requirements",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("-1"),
									corev1.ResourceMemory: resource.MustParse("-4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("-1"),
									corev1.ResourceMemory: resource.MustParse("-4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `[pod.spec.containers.test-container-a.resources.requests.cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.requests.memory: Invalid value: "-4Gi": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.memory: Invalid value: "-4Gi": quantity must be positive]`,
		},
		{
			name:      "forbidden resources - LS And Batch: negative resource requirements",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("-1"),
									extension.BatchMemory: resource.MustParse("-4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("-1"),
									extension.BatchMemory: resource.MustParse("-4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `[pod.spec.containers.test-container-a.resources.requests.koordinator.sh/batch-cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.requests.koordinator.sh/batch-memory: Invalid value: "-4Gi": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.koordinator.sh/batch-cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.koordinator.sh/batch-memory: Invalid value: "-4Gi": quantity must be positive]`,
		},
		{
			name:      "forbidden resources - LS And Prod: requests has cpu/memory and missing limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `pod.spec.containers.test-container-a.resources: Forbidden: container test-container-a: resource cpu quantity should satisify request <= limit`,
		},
		{
			name:      "forbidden resources - LS And Prod: forbidden missing requests but has limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `pod.spec.containers.test-container-a.resources.requests.cpu: Required value: request of container test-container-a does not have resource cpu`,
		},
		{
			name:      "forbidden resources - LS And Batch: forbidden missing requests but has limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `pod.spec.containers.test-container-a.resources.requests.koordinator.sh/batch-memory: Required value: request of container test-container-a does not have resource koordinator.sh/batch-memory`,
		},
		{
			name:      "forbidden resources - BE And Batch: negative resource requirements",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("-1"),
									extension.BatchMemory: resource.MustParse("-4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("-1"),
									extension.BatchMemory: resource.MustParse("-4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `[pod.spec.containers.test-container-a.resources.requests.koordinator.sh/batch-cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.requests.koordinator.sh/batch-memory: Invalid value: "-4Gi": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.koordinator.sh/batch-cpu: Invalid value: "-1": quantity must be positive, pod.spec.containers.test-container-a.resources.limits.koordinator.sh/batch-memory: Invalid value: "-4Gi": quantity must be positive]`,
		},
		{
			name:      "forbidden resources - BE And Batch: requests has cpu/memory and missing limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `[pod.spec.containers.test-container-a.resources.limits.koordinator.sh/batch-cpu: Required value: limit of container test-container-a does not have resource koordinator.sh/batch-cpu, pod.spec.containers.test-container-a.resources: Forbidden: container test-container-a: resource koordinator.sh/batch-cpu quantity should satisify request <= limit, pod.spec.containers.test-container-a.resources.limits.koordinator.sh/batch-memory: Not found: "null", pod.spec.containers.test-container-a.resources: Forbidden: resource koordinator.sh/batch-memory of container test-container-a: quantity of request and limit must be equal]`,
		},
		{
			name:      "forbidden resources - BE And Batch: limits has cpu/memory and missing requests",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `[pod.spec.containers.test-container-a.resources.requests.koordinator.sh/batch-cpu: Required value: request of container test-container-a does not have resource koordinator.sh/batch-cpu, pod.spec.containers.test-container-a.resources.requests.koordinator.sh/batch-memory: Not found: "null", pod.spec.containers.test-container-a.resources: Forbidden: resource koordinator.sh/batch-memory of container test-container-a: quantity of request and limit must be equal]`,
		},
		{
			name:      "validate resources - BE And Batch: request memory must equal limits and cpu less than limits",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32Ptr(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("2"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			decoder, _ := admission.NewDecoder(scheme.Scheme)
			h := &PodValidatingHandler{
				Client:  client,
				Decoder: decoder,
			}

			var objRawExt, oldObjRawExt runtime.RawExtension
			if tt.newPod != nil {
				objRawExt = runtime.RawExtension{
					Raw: []byte(util.DumpJSON(tt.newPod)),
				}
			}
			if tt.oldPod != nil {
				oldObjRawExt = runtime.RawExtension{
					Raw: []byte(util.DumpJSON(tt.oldPod)),
				}
			}

			req := newAdmissionRequest(tt.operation, objRawExt, oldObjRawExt, "pods")
			gotAllowed, gotReason, err := h.clusterColocationProfileValidatingPod(context.TODO(), admission.Request{AdmissionRequest: req})
			if (err != nil) != tt.wantErr {
				t.Errorf("clusterColocationProfileValidatingPod() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotAllowed != tt.wantAllowed {
				t.Errorf("clusterColocationProfileValidatingPod() gotAllowed = %v, want %v", gotAllowed, tt.wantAllowed)
			}
			if gotReason != tt.wantReason {
				t.Errorf("clusterColocationProfileValidatingPod() gotReason = %v, want %v", gotReason, tt.wantReason)
			}
		})
	}
}

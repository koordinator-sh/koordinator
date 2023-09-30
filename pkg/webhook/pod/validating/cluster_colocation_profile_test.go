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
	"fmt"
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
			name:      "validate defined QoS",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Priority: pointer.Int32(extension.PriorityBatchValueMin),
				},
			},
			wantAllowed: true,
		},
		{
			name:      "forbidden not defined QoS",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									extension.BatchCPU:    resource.MustParse("1"),
									extension.BatchMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Priority: pointer.Int32(6666),
				},
			},
			wantAllowed: false,
			wantReason:  `labels.koordinator.sh/qosClass: Required value: must specify koordinator QoS BE with koordinator colocation resources`,
		},
		{
			name:      "validate immutable priorityClass",
			operation: admissionv1.Update,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
				},
			},
			oldPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityBatchValueMin),
				},
			},
			wantAllowed: false,
			wantReason:  `spec.priority: Invalid value: "koord-prod": field is immutable`,
		},
		{
			name:      "validate remove priorityClass",
			operation: admissionv1.Update,
			newPod:    &corev1.Pod{},
			oldPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityBatchValueMin),
				},
			},
			wantAllowed: false,
			wantReason:  fmt.Sprintf(`spec.priority: Invalid value: %q: field is immutable`, extension.GetPodPriorityClassRaw(&corev1.Pod{})),
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
					Priority: pointer.Int32(extension.PriorityBatchValueMin),
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
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("1"),
								},
							},
						},
					},
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
					Priority: pointer.Int32(extension.PriorityProdValueMax),
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=BE and priorityClass=koord-prod cannot be used in combination`,
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
					Priority: pointer.Int32(extension.PriorityBatchValueMax),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=LSR and priorityClass=koord-batch cannot be used in combination`,
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
					Priority: pointer.Int32(extension.PriorityMidValueMax),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=LSR and priorityClass=koord-mid cannot be used in combination`,
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
					Priority: pointer.Int32(extension.PriorityFreeValueMax),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `Pod: Forbidden: koordinator.sh/qosClass=LSR and priorityClass=koord-free cannot be used in combination`,
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
					Priority: pointer.Int32(extension.PriorityProdValueMax),
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
			name:      "forbidden resources - LSR And Prod: unset CPUs",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-skip",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("0Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `pod.spec.containers[*].resources.requests: Required value: LSR Pod must declare the requested CPUs`,
		},
		{
			name:      "forbidden resources - LSR And Prod: non-integer CPUs",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "test-container-skip",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("0Gi"),
								},
							},
						},
					},
				},
			},
			wantAllowed: false,
			wantReason:  `pod.spec.containers[*].resources.requests: Invalid value: "100m": the requested CPUs of LSR Pod must be integer`,
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
				t.Errorf("clusterColocationProfileValidatingPod():\n"+
					"gotReason = %v,\n"+
					"want = %v", gotReason, tt.wantReason)
				t.Errorf("got=%v, want=%v", len(gotReason), len(tt.wantReason))
			}
		})
	}
}

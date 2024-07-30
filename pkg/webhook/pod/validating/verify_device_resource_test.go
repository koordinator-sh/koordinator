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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func init() {
	_ = configv1alpha1.AddToScheme(scheme.Scheme)
}

func TestDeviceResourceValidatingPod(t *testing.T) {
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
			name:      "validate gpu resource is percentage",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.ResourceGPU: *resource.NewQuantity(200, resource.DecimalSI),
								},
								Requests: corev1.ResourceList{
									extension.ResourceGPU: *resource.NewQuantity(200, resource.DecimalSI),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					PriorityClassName: "koordinator-batch",
				},
			},
			wantErr:     false,
			wantAllowed: true,
		},
		{
			name:      "validate gpu resource is not percentage",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.ResourceGPU: *resource.NewQuantity(70, resource.DecimalSI),
								},
								Requests: corev1.ResourceList{
									extension.ResourceGPU: *resource.NewQuantity(70, resource.DecimalSI),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					PriorityClassName: "koordinator-batch",
				},
			},
			wantErr:     false,
			wantAllowed: true,
		},
		{
			name:      "validate declain gpu memory and memoryRatio at same time",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.ResourceGPUMemory:      *resource.NewQuantity(4000, resource.DecimalSI),
									extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									extension.ResourceGPUShared:      *resource.NewQuantity(1, resource.DecimalSI),
								},
								Requests: corev1.ResourceList{
									extension.ResourceGPUMemory:      *resource.NewQuantity(4000, resource.DecimalSI),
									extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									extension.ResourceGPUShared:      *resource.NewQuantity(1, resource.DecimalSI),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					PriorityClassName: "koordinator-batch",
				},
			},
			wantErr:     true,
			wantAllowed: false,
			wantReason:  "pod.spec.containers[*].resources.requests: Forbidden: declare GPU memory and GPU memory ratio at same time",
		},
		{
			name:      "validate declain none gpu memory and memoryRatio",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.ResourceGPUShared: *resource.NewQuantity(2, resource.DecimalSI),
								},
								Requests: corev1.ResourceList{
									extension.ResourceGPUShared: *resource.NewQuantity(2, resource.DecimalSI),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					PriorityClassName: "koordinator-batch",
				},
			},
			wantErr:     true,
			wantAllowed: false,
			wantReason:  "pod.spec.containers[*].resources.requests: Forbidden: GPU memory and GPU memory ratio all is zero",
		},
		{
			name:      "validate gpu gpuCore greater than 100 multiple of shared",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.ResourceGPUCore:        *resource.NewQuantity(101, resource.DecimalSI),
									extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
								},
								Requests: corev1.ResourceList{
									extension.ResourceGPUCore:        *resource.NewQuantity(101, resource.DecimalSI),
									extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					PriorityClassName: "koordinator-batch",
				},
			},
			wantErr:     true,
			wantAllowed: false,
			wantReason:  "pod.spec.containers[*].resources.requests: Invalid value: \"101\": the requested gpuCore must multiple of shared",
		},
		{
			name:      "validate gpu memoryRatio greater than 100 multiple of shared",
			operation: admissionv1.Create,
			newPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.ResourceGPUMemoryRatio: *resource.NewQuantity(101, resource.DecimalSI),
									extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
								},
								Requests: corev1.ResourceList{
									extension.ResourceGPUMemoryRatio: *resource.NewQuantity(101, resource.DecimalSI),
									extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
								},
							},
						},
					},
					SchedulerName:     "koordinator-scheduler",
					PriorityClassName: "koordinator-batch",
				},
			},
			wantErr:     true,
			wantAllowed: false,
			wantReason:  "pod.spec.containers[*].resources.requests: Invalid value: \"101\": the requested gpuMemoryRatio must multiple of shared",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			decoder := admission.NewDecoder(scheme.Scheme)
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
			gotAllowed, gotReason, err := h.deviceResourceValidatingPod(context.TODO(), admission.Request{AdmissionRequest: req})
			if (err != nil) != tt.wantErr {
				t.Errorf("clusterReservationValidatingPod() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotAllowed != tt.wantAllowed {
				t.Errorf("clusterReservationValidatingPod() gotAllowed = %v, want %v", gotAllowed, tt.wantAllowed)
			}
			if gotReason != tt.wantReason {
				t.Errorf("clusterReservationValidatingPod():\n"+
					"gotReason = %v,\n"+
					"want = %v", gotReason, tt.wantReason)
				t.Errorf("got=%v, want=%v", len(gotReason), len(tt.wantReason))
			}
		})
	}
}

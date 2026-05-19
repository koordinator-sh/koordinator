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

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestMultiSchedulerDispatch(t *testing.T) {
	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		expectedSchedulerName string
	}{
		{
			name: "AI workload by label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelWorkloadType: WorkloadTypeAI,
					},
				},
			},
			expectedSchedulerName: SchedulerGPUShare,
		},
		{
			name: "AI workload by GPU resource request",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName("koordinator.sh/gpu"): resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			expectedSchedulerName: SchedulerGPUShare,
		},
		{
			name: "Latency Sensitive workload by QoS",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"koordinator.sh/qosClass": "LSR",
					},
				},
			},
			expectedSchedulerName: SchedulerLatencySens,
		},
		{
			name: "Latency Sensitive workload by label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelWorkloadType: WorkloadTypeLatencySensitive,
					},
				},
			},
			expectedSchedulerName: SchedulerLatencySens,
		},
		{
			name: "Batch workload by QoS",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"koordinator.sh/qosClass": "BE",
					},
				},
			},
			expectedSchedulerName: SchedulerBatch,
		},
		{
			name: "Batch workload by label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelWorkloadType: WorkloadTypeBatch,
					},
				},
			},
			expectedSchedulerName: SchedulerBatch,
		},
 		{
			name: "Default workload",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expectedSchedulerName: SchedulerDefault,
		},
		{
			name: "Explicit koord-scheduler workload",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					SchedulerName: SchedulerDefault,
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"koordinator.sh/qosClass": "BE",
					},
				},
			},
			expectedSchedulerName: SchedulerDefault,
		},
	}

	handler := &PodMutatingHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
				},
			}
			err := handler.multiSchedulerDispatchMutatingPod(context.Background(), req, tt.pod)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.pod.Spec.SchedulerName != tt.expectedSchedulerName {
				t.Errorf("expected schedulerName %s, got %s", tt.expectedSchedulerName, tt.pod.Spec.SchedulerName)
			}
		})
	}
}

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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestDeviceResourceSpecMutatingPod(t *testing.T) {
	assert := assert.New(t)

	client := fake.NewClientBuilder().Build()
	decoder := admission.NewDecoder(scheme.Scheme)
	handler := &PodMutatingHandler{
		Client:  client,
		Decoder: decoder,
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			Labels: map[string]string{
				"koordinator-colocation-pod": "true",
				extension.LabelPodQoS:        string(extension.QoSBE),
				extension.LabelPodPriority:   "1111",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-a",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							extension.ResourceGPUCore: *resource.NewQuantity(100, resource.DecimalSI),
							extension.BatchMemory:     resource.MustParse("4Gi"),
						},
						Requests: corev1.ResourceList{
							extension.ResourceGPUCore: *resource.NewQuantity(100, resource.DecimalSI),
							extension.BatchMemory:     resource.MustParse("4Gi"),
						},
					},
				},
			},
			SchedulerName:     "koordinator-scheduler",
			Priority:          pointer.Int32(extension.PriorityBatchValueMax),
			PriorityClassName: "koordinator-batch",
		},
	}

	testCases := []struct {
		name                         string
		resourceRequirements         corev1.ResourceRequirements
		expectedResourceRequirements corev1.ResourceRequirements
	}{
		{
			name: "mutating gpu share when gpuMemoryRatio small than 100",
			resourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(4000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
				},
				Limits: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(4000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
				},
			},
			expectedResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(4000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
					extension.ResourceGPUShared:      *resource.NewQuantity(1, resource.DecimalSI),
				},
				Limits: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(4000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
					extension.ResourceGPUShared:      *resource.NewQuantity(1, resource.DecimalSI),
				},
			},
		},
		{
			name: "mutating gpu share when gpuMemoryRatio is multiple of 100 ",
			resourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(8000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
				},
				Limits: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(8000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
				},
			},
			expectedResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(8000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
					extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
				},
				Limits: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					extension.ResourceGPUMemory:      *resource.NewQuantity(8000, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
					extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
		{
			name: "mutating gpu",
			resourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					extension.ResourceGPU: *resource.NewQuantity(200, resource.DecimalSI),
				},
				Limits: corev1.ResourceList{
					extension.ResourceGPU: *resource.NewQuantity(200, resource.DecimalSI),
				},
			},
			expectedResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(200, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
					extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
				},
				Limits: corev1.ResourceList{
					extension.ResourceGPUCore:        *resource.NewQuantity(200, resource.DecimalSI),
					extension.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
					extension.ResourceGPUShared:      *resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		},
	}

	req := newAdmission(admissionv1.Create, runtime.RawExtension{}, runtime.RawExtension{}, "")
	for i := range testCases {
		pod.Spec.Containers[0].Resources = testCases[i].resourceRequirements
		err := handler.deviceResourceSpecMutatingPod(context.TODO(), req, pod)

		assert.NoError(err)
		assert.Equal(pod.Spec.Containers[0].Resources, testCases[i].expectedResourceRequirements)
	}
}

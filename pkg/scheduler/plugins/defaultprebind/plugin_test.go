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

package defaultprebind

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
)

func TestApplyPatch(t *testing.T) {
	tests := []struct {
		name        string
		originalObj metav1.Object
		modifiedObj metav1.Object
		wantObj     metav1.Object
		wantStatus  *framework.Status
	}{
		{
			name: "patch pod",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			modifiedObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
								{
									Name:  "appendEnv",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "patch reservation",
			originalObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
			},
			modifiedObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
										{
											Name:  "appendEnv",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
											apiext.ResourceGPU: resource.MustParse("100"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
											apiext.ResourceGPU: resource.MustParse("100"),
										},
									},
								},
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := &Plugin{
				clientSet:      kubefake.NewSimpleClientset(),
				koordClientSet: koordfake.NewSimpleClientset(),
			}

			if pod, ok := tt.originalObj.(*corev1.Pod); ok {
				_, err := pl.clientSet.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			} else if reservation, ok := tt.originalObj.(*schedulingv1alpha1.Reservation); ok {
				_, err := pl.koordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			status := pl.ApplyPatch(context.TODO(), framework.NewCycleState(), tt.originalObj, tt.modifiedObj)
			assert.Equal(t, tt.wantStatus, status)

			if pod, ok := tt.originalObj.(*corev1.Pod); ok {
				got, err := pl.clientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, tt.modifiedObj.(*corev1.Pod), got)
			} else if reservation, ok := tt.originalObj.(*schedulingv1alpha1.Reservation); ok {
				got, err := pl.koordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, tt.modifiedObj.(*schedulingv1alpha1.Reservation), got)
			}
		})
	}
}

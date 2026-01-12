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
	"time"

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
	now := metav1.Now()
	tests := []struct {
		name        string
		originalObj metav1.Object
		modifiedObj metav1.Object
		wantStatus  *framework.Status
		wantError   bool
	}{
		{
			name: "patch pod successfully",
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
			wantError:  false,
		},
		{
			name: "pod deleted before patch - should fail",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-pod-deleted",
					Namespace:         "default",
					UID:               "deleted-uid",
					DeletionTimestamp: &now,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
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
					Name:              "test-pod-deleted",
					Namespace:         "default",
					UID:               "deleted-uid",
					DeletionTimestamp: &now,
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
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
			wantStatus: nil,
			wantError:  true,
		},
		{
			name: "skipped to patch pod - no changes",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "xxxxxx",
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
					UID:       "xxxxxx",
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
			wantStatus: nil,
			wantError:  false,
		},
		{
			name: "patch reservation successfully",
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
			wantError:  false,
		},
		{
			name: "reservation deleted before patch - should fail",
			originalObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-reservation-deleted",
					UID:               "deleted-reservation-uid",
					DeletionTimestamp: &now,
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
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
					Name:              "test-reservation-deleted",
					UID:               "deleted-reservation-uid",
					DeletionTimestamp: &now,
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
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
			wantStatus: nil,
			wantError:  true,
		},
		{
			name: "skipped to patch reservation - no changes",
			originalObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					UID:  "yyyyyy",
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
					UID:  "yyyyyy",
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
			wantStatus: nil,
			wantError:  false,
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
			if tt.wantError {
				assert.NotNil(t, status, "expected error status but got nil")
				assert.False(t, status.IsSuccess(), "expected error but got success")
			} else {
				assert.Equal(t, tt.wantStatus, status)
			}

			// Verify final state only if no error expected
			if !tt.wantError {
				if pod, ok := tt.originalObj.(*corev1.Pod); ok {
					got, err := pl.clientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
					assert.NoError(t, err)
					assert.Equal(t, tt.modifiedObj.(*corev1.Pod).Annotations, got.Annotations)
					assert.Equal(t, tt.modifiedObj.(*corev1.Pod).Labels, got.Labels)
				} else if reservation, ok := tt.originalObj.(*schedulingv1alpha1.Reservation); ok {
					got, err := pl.koordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
					assert.NoError(t, err)
					assert.Equal(t, tt.modifiedObj.(*schedulingv1alpha1.Reservation).Annotations, got.Annotations)
					assert.Equal(t, tt.modifiedObj.(*schedulingv1alpha1.Reservation).Labels, got.Labels)
				}
			}
		})
	}
}

// TestApplyPatchWithDeletionAfterPatch tests the scenario where an object is deleted
// after the patch request is sent but before it completes, resulting in the patched
// object having a DeletionTimestamp.
func TestApplyPatchWithDeletionAfterPatch(t *testing.T) {
	t.Run("pod deleted after patch - should fail", func(t *testing.T) {
		deleteTime := metav1.NewTime(time.Now())
		originalPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "main",
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
		}

		// Create pod with DeletionTimestamp to simulate a pod that was deleted after patch
		podWithDeletion := originalPod.DeepCopy()
		podWithDeletion.DeletionTimestamp = &deleteTime

		modifiedPod := originalPod.DeepCopy()
		modifiedPod.Annotations = map[string]string{
			"testAnnotation": "1",
		}
		modifiedPod.DeletionTimestamp = &deleteTime

		pl := &Plugin{
			clientSet:      kubefake.NewSimpleClientset(podWithDeletion),
			koordClientSet: koordfake.NewSimpleClientset(),
		}

		// Try to patch - this should fail because the returned pod has DeletionTimestamp
		status := pl.ApplyPatch(context.TODO(), framework.NewCycleState(), originalPod, modifiedPod)
		assert.NotNil(t, status, "expected error status but got nil")
		assert.False(t, status.IsSuccess(), "expected patch to fail for deleting pod")
		assert.Contains(t, status.Message(), "pod is being deleted")
	})

	t.Run("reservation deleted after patch - should fail", func(t *testing.T) {
		deleteTime := metav1.NewTime(time.Now())
		originalReservation := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-reservation",
				UID:  "test-reservation-uid",
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "main",
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
		}

		// Create reservation with DeletionTimestamp to simulate a reservation that was deleted after patch
		reservationWithDeletion := originalReservation.DeepCopy()
		reservationWithDeletion.DeletionTimestamp = &deleteTime

		modifiedReservation := originalReservation.DeepCopy()
		modifiedReservation.Annotations = map[string]string{
			"testAnnotation": "1",
		}
		modifiedReservation.DeletionTimestamp = &deleteTime

		pl := &Plugin{
			clientSet:      kubefake.NewSimpleClientset(),
			koordClientSet: koordfake.NewSimpleClientset(reservationWithDeletion),
		}

		// Try to patch - this should fail because the returned reservation has DeletionTimestamp
		status := pl.ApplyPatch(context.TODO(), framework.NewCycleState(), originalReservation, modifiedReservation)
		assert.NotNil(t, status, "expected error status but got nil")
		assert.False(t, status.IsSuccess(), "expected patch to fail for deleting reservation")
		assert.Contains(t, status.Message(), "pod is being deleted") // Note: error message says "pod" even for reservation
	})
}

// TestApplyPatchWithPatchFailure tests scenarios where the patch request itself fails
func TestApplyPatchWithPatchFailure(t *testing.T) {
	t.Run("pod not found - patch should fail", func(t *testing.T) {
		originalPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "non-existent-pod",
				Namespace: "default",
				UID:       "test-uid",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "main",
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
		}

		modifiedPod := originalPod.DeepCopy()
		modifiedPod.Annotations = map[string]string{
			"testAnnotation": "1",
		}

		// Create plugin with empty client (pod doesn't exist)
		pl := &Plugin{
			clientSet:      kubefake.NewSimpleClientset(),
			koordClientSet: koordfake.NewSimpleClientset(),
		}

		// Try to patch non-existent pod - should fail
		status := pl.ApplyPatch(context.TODO(), framework.NewCycleState(), originalPod, modifiedPod)
		assert.NotNil(t, status, "expected error status but got nil")
		assert.False(t, status.IsSuccess(), "expected patch to fail for non-existent pod")
		assert.Contains(t, status.Message(), "not found")
	})

	t.Run("reservation not found - patch should fail", func(t *testing.T) {
		originalReservation := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "non-existent-reservation",
				UID:  "test-reservation-uid",
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "main",
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
		}

		modifiedReservation := originalReservation.DeepCopy()
		modifiedReservation.Annotations = map[string]string{
			"testAnnotation": "1",
		}

		// Create plugin with empty client (reservation doesn't exist)
		pl := &Plugin{
			clientSet:      kubefake.NewSimpleClientset(),
			koordClientSet: koordfake.NewSimpleClientset(),
		}

		// Try to patch non-existent reservation - should fail
		status := pl.ApplyPatch(context.TODO(), framework.NewCycleState(), originalReservation, modifiedReservation)
		assert.NotNil(t, status, "expected error status but got nil")
		assert.False(t, status.IsSuccess(), "expected patch to fail for non-existent reservation")
		assert.Contains(t, status.Message(), "not found")
	})

	t.Run("pod with conflicting resourceVersion - patch should retry and succeed", func(t *testing.T) {
		originalPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-pod-conflict",
				Namespace:       "default",
				UID:             "test-uid",
				ResourceVersion: "1",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "main",
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
		}

		modifiedPod := originalPod.DeepCopy()
		modifiedPod.Annotations = map[string]string{
			"testAnnotation": "1",
		}

		pl := &Plugin{
			clientSet:      kubefake.NewSimpleClientset(originalPod),
			koordClientSet: koordfake.NewSimpleClientset(),
		}

		// Patch should succeed even with conflict retry logic
		status := pl.ApplyPatch(context.TODO(), framework.NewCycleState(), originalPod, modifiedPod)
		assert.Nil(t, status, "expected success but got error: %v", status)

		// Verify the pod was actually patched
		got, err := pl.clientSet.CoreV1().Pods(originalPod.Namespace).Get(context.TODO(), originalPod.Name, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, modifiedPod.Annotations, got.Annotations)
	})

	t.Run("reservation with conflicting resourceVersion - patch should retry and succeed", func(t *testing.T) {
		originalReservation := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-reservation-conflict",
				UID:             "test-reservation-uid",
				ResourceVersion: "1",
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "main",
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
		}

		modifiedReservation := originalReservation.DeepCopy()
		modifiedReservation.Annotations = map[string]string{
			"testAnnotation": "1",
		}

		pl := &Plugin{
			clientSet:      kubefake.NewSimpleClientset(),
			koordClientSet: koordfake.NewSimpleClientset(originalReservation),
		}

		// Patch should succeed even with conflict retry logic
		status := pl.ApplyPatch(context.TODO(), framework.NewCycleState(), originalReservation, modifiedReservation)
		assert.Nil(t, status, "expected success but got error: %v", status)

		// Verify the reservation was actually patched
		got, err := pl.koordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), originalReservation.Name, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, modifiedReservation.Annotations, got.Annotations)
	})
}

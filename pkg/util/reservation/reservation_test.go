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

package reservation

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestIsReservationActive(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		rPending := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "reserve-pod-0",
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template: &corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Name: "reserve-pod-0",
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node-0",
					},
				},
				Owners: []schedulingv1alpha1.ReservationOwner{
					{
						Object: &corev1.ObjectReference{
							Kind: "Pod",
							Name: "test-pod-0",
						},
					},
				},
				TTL: &metav1.Duration{Duration: 30 * time.Minute},
			},
		}
		assert.Equal(t, false, IsReservationActive(rPending))

		rActive := rPending.DeepCopy()
		rActive.Status = schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-0",
		}
		assert.Equal(t, true, IsReservationActive(rActive))
	})
}

func TestIsReservationAvailable(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{}
		got := IsReservationAvailable(r)
		assert.False(t, got)

		r = &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Phase:    schedulingv1alpha1.ReservationAvailable,
				NodeName: "test-node-0",
			},
		}
		got = IsReservationAvailable(r)
		assert.True(t, got)
	})
}

func TestIsReservationSucceeded(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{}
		got := IsReservationSucceeded(r)
		assert.False(t, got)

		r = &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Phase:    schedulingv1alpha1.ReservationAvailable,
				NodeName: "test-node-0",
			},
		}
		got = IsReservationSucceeded(r)
		assert.False(t, got)

		r = &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Phase:    schedulingv1alpha1.ReservationSucceeded,
				NodeName: "test-node-0",
			},
		}
		got = IsReservationSucceeded(r)
		assert.True(t, got)
	})
}

func TestIsReservationFailed(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{}
		got := IsReservationFailed(r)
		assert.False(t, got)

		r = &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Phase: schedulingv1alpha1.ReservationFailed,
			},
		}
		got = IsReservationFailed(r)
		assert.True(t, got)
	})
}

func TestIsReservationExpired(t *testing.T) {
	tests := []struct {
		name string
		arg  *schedulingv1alpha1.Reservation
		want bool
	}{
		{
			name: "not panic for nil",
			arg:  nil,
			want: false,
		},
		{
			name: "not panic for empty",
			arg:  &schedulingv1alpha1.Reservation{},
			want: false,
		},
		{
			name: "available reservation is not expired",
			arg: &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node-0",
				},
			},
			want: false,
		},
		{
			name: "scheduled failed reservation is not expired",
			arg: &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusFalse,
							Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
							Message:            "xxx",
							LastTransitionTime: metav1.Now(),
							LastProbeTime:      metav1.Now(),
						},
					},
				},
			},
			want: false,
		},
		{
			name: "check expired reservation",
			arg: &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationFailed,
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusFalse,
							Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
							Message:            "xxx",
							LastTransitionTime: metav1.Now(),
							LastProbeTime:      metav1.Now(),
						},
						{
							Type:               schedulingv1alpha1.ReservationConditionReady,
							Status:             schedulingv1alpha1.ConditionStatusFalse,
							Reason:             schedulingv1alpha1.ReasonReservationExpired,
							LastTransitionTime: metav1.Now(),
							LastProbeTime:      metav1.Now(),
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsReservationExpired(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetReservationSchedulerName(t *testing.T) {
	tests := []struct {
		name string
		arg  *schedulingv1alpha1.Reservation
		want string
	}{
		{
			name: "empty reservation",
			arg:  nil,
			want: corev1.DefaultSchedulerName,
		},
		{
			name: "empty template",
			arg:  &schedulingv1alpha1.Reservation{},
			want: corev1.DefaultSchedulerName,
		},
		{
			name: "empty scheduler name",
			arg: &schedulingv1alpha1.Reservation{
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
			},
			want: corev1.DefaultSchedulerName,
		},
		{
			name: "get scheduler name successfully",
			arg: &schedulingv1alpha1.Reservation{
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							SchedulerName: "test-scheduler",
						},
					},
				},
			},
			want: "test-scheduler",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetReservationSchedulerName(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatchReservationOwners(t *testing.T) {
	type args struct {
		pod *corev1.Pod
		r   *schedulingv1alpha1.Reservation
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "no owner to match",
			args: args{
				pod: &corev1.Pod{},
				r: &schedulingv1alpha1.Reservation{
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: nil,
					},
				},
			},
			want: false,
		},
		{
			name: "match objRef",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod-0",
						Namespace: "test",
					},
				},
				r: &schedulingv1alpha1.Reservation{
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Name:      "test-pod-0",
									Namespace: "test",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "match controllerRef",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sts-0-0",
						Namespace: "test",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "test-sts-0",
								Controller: ptr.To[bool](true),
								Kind:       "StatefulSet",
								APIVersion: "apps/v1",
							},
						},
					},
				},
				r: &schedulingv1alpha1.Reservation{
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Controller: &schedulingv1alpha1.ReservationControllerReference{
									OwnerReference: metav1.OwnerReference{
										Name:       "test-sts-0",
										Controller: ptr.To[bool](true),
									},
									Namespace: "test",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "match labels",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod-1",
						Namespace: "test",
						Labels: map[string]string{
							"aaa": "bbb",
							"ccc": "ddd",
						},
					},
				},
				r: &schedulingv1alpha1.Reservation{
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"aaa": "bbb",
									},
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "fail on one term of owner spec",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod-1",
						Namespace: "test",
						Labels: map[string]string{
							"aaa": "bbb",
							"ccc": "ddd",
						},
					},
				},
				r: &schedulingv1alpha1.Reservation{
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Name: "test-pod-2",
								},
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"aaa": "bbb",
										"xxx": "yyy",
									},
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "match one of owner specs",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod-2",
						Namespace: "test",
						Labels: map[string]string{
							"aaa": "bbb",
							"ccc": "ddd",
						},
					},
				},
				r: &schedulingv1alpha1.Reservation{
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Name:      "test-pod-0",
									Namespace: "test",
								},
							},
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"aaa": "bbb",
									},
								},
							},
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchers, err := ParseReservationOwnerMatchers(tt.args.r.Spec.Owners)
			assert.NoError(t, err)
			got := MatchReservationOwners(tt.args.pod, matchers)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetReservationUnschedulable(t *testing.T) {
	type args struct {
		r   *schedulingv1alpha1.Reservation
		msg string
	}
	tests := []struct {
		name string
		args args
		want *schedulingv1alpha1.Reservation
	}{
		{
			name: "add condition",
			args: args{
				r: &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "reserve-pod-0",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Kind: "Pod",
									Name: "test-pod-0",
								},
							},
						},
						TTL: &metav1.Duration{Duration: 30 * time.Minute},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						Phase: schedulingv1alpha1.ReservationPending,
					},
				},
				msg: "unschedule msg",
			},
			want: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "test-pod-0",
							},
						},
					},
					TTL: &metav1.Duration{Duration: 30 * time.Minute},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationPending,
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusFalse,
							Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
							Message:            "unschedule msg",
							LastProbeTime:      metav1.Now(),
							LastTransitionTime: metav1.Now(),
						},
					},
				},
			},
		},
		{
			name: "update condition",
			args: args{
				r: &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "reserve-pod-0",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Kind: "Pod",
									Name: "test-pod-0",
								},
							},
						},
						TTL: &metav1.Duration{Duration: 30 * time.Minute},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						Phase: schedulingv1alpha1.ReservationPending,
						Conditions: []schedulingv1alpha1.ReservationCondition{
							{
								Type:               schedulingv1alpha1.ReservationConditionScheduled,
								Status:             schedulingv1alpha1.ConditionStatusTrue,
								Reason:             schedulingv1alpha1.ReasonReservationScheduled,
								LastProbeTime:      metav1.Now(),
								LastTransitionTime: metav1.Now(),
							},
						},
					},
				},
				msg: "unschedule msg",
			},
			want: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "test-pod-0",
							},
						},
					},
					TTL: &metav1.Duration{Duration: 30 * time.Minute},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationPending,
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusTrue,
							Reason:             schedulingv1alpha1.ReasonReservationScheduled,
							LastProbeTime:      metav1.Now(),
							LastTransitionTime: metav1.Now(),
						},
					},
				},
			},
		},
		{
			name: "change condition",
			args: args{
				r: &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "reserve-pod-0",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Kind: "Pod",
									Name: "test-pod-0",
								},
							},
						},
						TTL: &metav1.Duration{Duration: 30 * time.Minute},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						Phase: schedulingv1alpha1.ReservationPending,
						Conditions: []schedulingv1alpha1.ReservationCondition{
							{
								Type:               schedulingv1alpha1.ReservationConditionScheduled,
								Status:             schedulingv1alpha1.ConditionStatusFalse,
								Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
								Message:            "old unschedule msg",
								LastProbeTime:      metav1.Now(),
								LastTransitionTime: metav1.Now(),
							},
						},
					},
				},
				msg: "unschedule msg",
			},
			want: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "test-pod-0",
							},
						},
					},
					TTL: &metav1.Duration{Duration: 30 * time.Minute},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationPending,
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusFalse,
							Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
							Message:            "unschedule msg",
							LastProbeTime:      metav1.Now(),
							LastTransitionTime: metav1.Now(),
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetReservationUnschedulable(tt.args.r, tt.args.msg)
			assertEqualReservationCondition(t, tt.want, tt.args.r)
		})
	}
}

func assertEqualReservationCondition(t *testing.T, expect, got *schedulingv1alpha1.Reservation) {
	if expect == nil && got == nil {
		return
	}
	if expect == nil || got == nil {
		if expect != got {
			t.Errorf("reservation condition not equal, expect %v, got %v", expect, got)
		}
		return
	}
	if len(expect.Status.Conditions) != len(got.Status.Conditions) {
		t.Errorf("reservation condition not equal, expect len %v, got len %v", len(expect.Status.Conditions), len(got.Status.Conditions))
		return
	}
	expectConditions := map[string]*schedulingv1alpha1.ReservationCondition{}
	for i, condition := range expect.Status.Conditions {
		expectConditions[string(condition.Type)] = &expect.Status.Conditions[i]
	}
	for _, condition := range got.Status.Conditions {
		e, ok := expectConditions[string(condition.Type)]
		if !ok {
			t.Errorf("reservation condition not equal, got unexpect condition type %v", condition.Type)
			continue
		}
		msg := "condition type " + string(condition.Type)
		assert.Equal(t, e.Status, condition.Status, msg)
		assert.Equal(t, e.Message, condition.Message, msg)
		assert.Equal(t, e.Reason, condition.Reason, msg)
	}
}

func TestSetReservationAvailable(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reserve-pod-0",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"test-resource": *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
		},
	}

	resizeAllocatable := corev1.ResourceList{
		"test-resource": *resource.NewQuantity(100, resource.DecimalSI),
	}

	availableReservation := reservation.DeepCopy()
	availableReservation.Status.Phase = schedulingv1alpha1.ReservationAvailable
	availableReservation.Status.Allocatable = resizeAllocatable
	availableReservation.Status.NodeName = "test-node"
	availableReservation.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{
			Type:               schedulingv1alpha1.ReservationConditionScheduled,
			Status:             schedulingv1alpha1.ConditionStatusTrue,
			Reason:             schedulingv1alpha1.ReasonReservationScheduled,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusTrue,
			Reason:             schedulingv1alpha1.ReasonReservationAvailable,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		},
	}

	resizedReservation := reservation.DeepCopy()
	assert.NoError(t, UpdateReservationResizeAllocatable(resizedReservation, resizeAllocatable))
	resizedAvailableReservation := availableReservation.DeepCopy()
	assert.NoError(t, UpdateReservationResizeAllocatable(resizedAvailableReservation, resizeAllocatable))
	resizedAvailableReservation.Status.Allocatable = resizeAllocatable

	tests := []struct {
		name            string
		reservation     *schedulingv1alpha1.Reservation
		wantReservation *schedulingv1alpha1.Reservation
	}{
		{
			name:            "pending reservation to available",
			reservation:     reservation,
			wantReservation: availableReservation,
		},
		{
			name:            "reservation with resize allocatable",
			reservation:     resizedReservation,
			wantReservation: resizedAvailableReservation,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, SetReservationAvailable(tt.reservation, "test-node"))
		})
	}
}

func TestReservePod(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reserve-pod-0",
			Labels: map[string]string{
				"test-label": "666",
			},
			Annotations: map[string]string{
				"test-annotation": "888",
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "reserve-pod-0",
					Namespace: "test",
					Labels: map[string]string{
						"test-label":  "111",
						"other-label": "1",
					},
					Annotations: map[string]string{
						"test-annotation":  "222",
						"other-annotation": "2",
					},
				},
				Spec: corev1.PodSpec{
					NodeName:      "test-node",
					SchedulerName: "koord-scheduler",
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"test-resource": *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"test-resource": *resource.NewQuantity(10, resource.DecimalSI),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						"test-resource": *resource.NewQuantity(0, resource.DecimalSI),
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				"test-resource": *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
	}

	expectReservePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       reservation.UID,
			Name:      GetReservationKey(reservation),
			Namespace: "test",
			Labels: map[string]string{
				"test-label":  "666",
				"other-label": "1",
			},
			Annotations: map[string]string{
				"test-annotation":         "888",
				"other-annotation":        "2",
				AnnotationReservePod:      "true",
				AnnotationReservationName: reservation.Name,
				AnnotationReservationNode: reservation.Status.NodeName,
			},
		},
		Spec: corev1.PodSpec{
			NodeName:      "test-node",
			SchedulerName: "koord-scheduler",
			Containers: []corev1.Container{
				{
					Name: "test-container",
				},
				{
					Name: "__internal_fake_container__",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"test-resource": *resource.NewQuantity(200, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							"test-resource": *resource.NewQuantity(200, resource.DecimalSI),
						},
					},
				},
			},
			Priority: ptr.To[int32](math.MaxInt32),
			InitContainers: []corev1.Container{
				{
					Name:      "test-init-container",
					Resources: corev1.ResourceRequirements{},
				},
			},
			Overhead: nil,
		},
	}

	tests := []struct {
		name           string
		reservation    *schedulingv1alpha1.Reservation
		wantReservePod *corev1.Pod
	}{
		{
			name:           "convert to reserve pod",
			reservation:    reservation,
			wantReservePod: expectReservePod,
		},
		{
			name: "custom priority",
			reservation: func() *schedulingv1alpha1.Reservation {
				r := reservation.DeepCopy()
				r.Labels[apiext.LabelPodPriority] = "1000"
				return r
			}(),
			wantReservePod: func() *corev1.Pod {
				p := expectReservePod.DeepCopy()
				p.Labels[apiext.LabelPodPriority] = "1000"
				p.Spec.Priority = ptr.To[int32](1000)
				return p
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservePod := NewReservePod(tt.reservation)
			assert.Equal(t, tt.wantReservePod, reservePod)
		})
	}
}

func TestReservationRequests(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reserve-pod-0",
			Labels: map[string]string{
				"test-label": "666",
			},
			Annotations: map[string]string{
				"test-annotation": "888",
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "reserve-pod-0",
					Namespace: "test",
					Labels: map[string]string{
						"test-label":  "111",
						"other-label": "1",
					},
					Annotations: map[string]string{
						"test-annotation":  "222",
						"other-annotation": "2",
					},
				},
				Spec: corev1.PodSpec{
					NodeName:      "test-node",
					SchedulerName: "koord-scheduler",
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"test-resource": *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "test-init-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"test-resource": *resource.NewQuantity(10, resource.DecimalSI),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						"test-resource": *resource.NewQuantity(10, resource.DecimalSI),
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationPending,
		},
	}

	resizeAllocatableReservation := reservation.DeepCopy()
	resizeAllocatableReservation.Status.NodeName = "test-node"
	resizeAllocatableReservation.Status.Phase = schedulingv1alpha1.ReservationAvailable
	resizeAllocatableReservation.Status.Allocatable = corev1.ResourceList{
		"test-resource": *resource.NewQuantity(200, resource.DecimalSI),
	}

	tests := []struct {
		name        string
		reservation *schedulingv1alpha1.Reservation
		want        corev1.ResourceList
	}{
		{
			name:        "request from PodTemplateSpec",
			reservation: reservation,
			want: corev1.ResourceList{
				"test-resource": *resource.NewQuantity(110, resource.DecimalSI),
			},
		},
		{
			name:        "request from status allocatable",
			reservation: resizeAllocatableReservation,
			want: corev1.ResourceList{
				"test-resource": *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReservationRequests(tt.reservation)
			assert.True(t, equality.Semantic.DeepEqual(tt.want, got))
		})
	}
}

func TestGetReservationRestrictedResources(t *testing.T) {
	tests := []struct {
		name          string
		resourceNames []corev1.ResourceName
		options       *apiext.ReservationRestrictedOptions
		want          []corev1.ResourceName
	}{
		{
			name:          "no options, got all allocatable resources",
			resourceNames: []corev1.ResourceName{"cpu", "memory"},
			options:       nil,
			want:          []corev1.ResourceName{"cpu", "memory"},
		},
		{
			name:          "has options and same as resourceNames",
			resourceNames: []corev1.ResourceName{"cpu", "memory"},
			options: &apiext.ReservationRestrictedOptions{
				Resources: []corev1.ResourceName{"cpu", "memory"},
			},
			want: []corev1.ResourceName{"cpu", "memory"},
		},
		{
			name:          "has options but different resourceNames",
			resourceNames: []corev1.ResourceName{"cpu", "memory"},
			options: &apiext.ReservationRestrictedOptions{
				Resources: []corev1.ResourceName{"cpu"},
			},
			want: []corev1.ResourceName{"cpu"},
		},
		{
			name:          "has options but no resourceNames",
			resourceNames: []corev1.ResourceName{"cpu", "memory"},
			options: &apiext.ReservationRestrictedOptions{
				Resources: []corev1.ResourceName{},
			},
			want: []corev1.ResourceName{"cpu", "memory"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetReservationRestrictedResources(tt.resourceNames, tt.options)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewReservationReason(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		reasonMsg := "node(s) didn't match the requested node name"
		got := NewReservationReason(reasonMsg)
		assert.True(t, IsReservationReason(got))
	})
}

func TestGetRequiredReservationAffinity(t *testing.T) {
	tests := []struct {
		name                      string
		pod                       *corev1.Pod
		wantNil                   bool
		wantErr                   bool
		wantTolerateUnschedulable bool
	}{
		{
			name: "pod without reservation affinity",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			wantNil: true,
		},
		{
			name: "pod with reservation affinity but no toleration",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Annotations: map[string]string{
						apiext.AnnotationReservationAffinity: `{"reservationSelector":{"test":"true"}}`,
					},
				},
			},
			wantNil:                   false,
			wantTolerateUnschedulable: false,
		},
		{
			name: "pod with reservation affinity and unschedulable toleration",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Annotations: map[string]string{
						apiext.AnnotationReservationAffinity: `{"reservationSelector":{"test":"true"},"tolerations":[{"key":"node.kubernetes.io/unschedulable","operator":"Exists","effect":"NoSchedule"}]}`,
					},
				},
			},
			wantNil:                   false,
			wantTolerateUnschedulable: true,
		},
		{
			name: "pod with reservation affinity and other toleration",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Annotations: map[string]string{
						apiext.AnnotationReservationAffinity: `{"reservationSelector":{"test":"true"},"tolerations":[{"key":"other-key","operator":"Exists","effect":"NoSchedule"}]}`,
					},
				},
			},
			wantNil:                   false,
			wantTolerateUnschedulable: false,
		},
		{
			name: "pod with reservation affinity and multiple tolerations including unschedulable",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Annotations: map[string]string{
						apiext.AnnotationReservationAffinity: `{"reservationSelector":{"test":"true"},"tolerations":[{"key":"other-key","operator":"Exists","effect":"NoSchedule"},{"key":"node.kubernetes.io/unschedulable","operator":"Exists","effect":"NoSchedule"}]}`,
					},
				},
			},
			wantNil:                   false,
			wantTolerateUnschedulable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetRequiredReservationAffinity(tt.pod)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
				assert.Equal(t, tt.wantTolerateUnschedulable, got.TolerateUnschedulable())
			}
		})
	}
}

func TestTolerateUnschedulable(t *testing.T) {
	tests := []struct {
		name     string
		affinity *RequiredReservationAffinity
		want     bool
	}{
		{
			name:     "nil affinity",
			affinity: nil,
			want:     false,
		},
		{
			name: "affinity without toleration",
			affinity: &RequiredReservationAffinity{
				tolerateUnschedulable: false,
			},
			want: false,
		},
		{
			name: "affinity with unschedulable toleration",
			affinity: &RequiredReservationAffinity{
				tolerateUnschedulable: true,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.affinity.TolerateUnschedulable()
			assert.Equal(t, tt.want, got)
		})
	}
}

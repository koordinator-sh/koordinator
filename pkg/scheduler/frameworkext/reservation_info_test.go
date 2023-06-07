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

package frameworkext

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestIsAvailable(t *testing.T) {
	tests := []struct {
		name string
		obj  metav1.Object
		want bool
	}{
		{
			name: "normal reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: true,
		},
		{
			name: "failed reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationFailed,
					NodeName: "test-node",
				},
			},
			want: false,
		},
		{
			name: "ready and running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "not ready and running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "not ready and not running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rInfo *ReservationInfo
			switch obj := tt.obj.(type) {
			case *schedulingv1alpha1.Reservation:
				rInfo = NewReservationInfo(obj)
			case *corev1.Pod:
				rInfo = NewReservationInfoFromPod(obj)
			}
			assert.NotNil(t, rInfo)
			assert.Equal(t, tt.want, rInfo.IsAvailable())
		})
	}
}

func TestIsTerminating(t *testing.T) {
	tests := []struct {
		name string
		obj  metav1.Object
		want bool
	}{
		{
			name: "normal reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: false,
		},
		{
			name: "deleting reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-r",
					UID:               "123456",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: true,
		},
		{
			name: "ready and running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "ready, running and deleting pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-p",
					Namespace:         "default",
					UID:               "123456",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rInfo *ReservationInfo
			switch obj := tt.obj.(type) {
			case *schedulingv1alpha1.Reservation:
				rInfo = NewReservationInfo(obj)
			case *corev1.Pod:
				rInfo = NewReservationInfoFromPod(obj)
			}
			assert.NotNil(t, rInfo)
			assert.Equal(t, tt.want, rInfo.IsTerminating())
		})
	}
}

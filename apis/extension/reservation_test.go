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

package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestIsReservationIgnored(t *testing.T) {
	tests := []struct {
		name string
		arg  *corev1.Pod
		want bool
	}{
		{
			name: "pod is nil",
			arg:  nil,
			want: false,
		},
		{
			name: "pod labels is missing",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			want: false,
		},
		{
			name: "pod label is empty",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-pod",
					Labels: map[string]string{},
				},
			},
			want: false,
		},
		{
			name: "pod set label to true",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						LabelReservationIgnored: "true",
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsReservationIgnored(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetReservationAllocated(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
	}
	pod := &corev1.Pod{}
	SetReservationAllocated(pod, reservation)
	reservationAllocated, err := GetReservationAllocated(pod)
	assert.NoError(t, err)
	expectReservationAllocated := &ReservationAllocated{
		UID:  reservation.UID,
		Name: reservation.Name,
	}
	assert.Equal(t, expectReservationAllocated, reservationAllocated)
}

func TestExactMatchReservation(t *testing.T) {
	tests := []struct {
		name                   string
		podRequests            corev1.ResourceList
		reservationAllocatable corev1.ResourceList
		spec                   *ExactMatchReservationSpec
		want                   bool
	}{
		{
			name: "exact matched cpu",
			spec: &ExactMatchReservationSpec{
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
			reservationAllocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
			want: true,
		},
		{
			name: "exact matched cpu",
			spec: &ExactMatchReservationSpec{
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			reservationAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
			want: true,
		},
		{
			name: "exact matched cpu, memory not exact matched",
			spec: &ExactMatchReservationSpec{
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
					corev1.ResourceMemory,
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			reservationAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
			want: false,
		},
		{
			name: "exact matched cpu, memory exact match spec doesn't matter",
			spec: &ExactMatchReservationSpec{
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
					corev1.ResourceMemory,
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
			reservationAllocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ExactMatchReservation(tt.podRequests, tt.reservationAllocatable, tt.spec))
		})
	}
}

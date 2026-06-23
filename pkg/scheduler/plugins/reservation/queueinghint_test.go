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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestIsSchedulableAfterPodDeletion(t *testing.T) {
	pl := &Plugin{}
	logger := klog.Background()

	tests := []struct {
		name       string
		deletedPod *corev1.Pod
		want       fwktype.QueueingHint
	}{
		{
			name: "deleted pod not assigned",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
			},
			want: fwktype.QueueSkip,
		},
		{
			name: "deleted pod assigned to node",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
				Spec:       corev1.PodSpec{NodeName: "node1"},
			},
			want: fwktype.Queue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := pl.isSchedulableAfterPodDeletion(logger, nil, tt.deletedPod, nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsSchedulableAfterReservationChanged(t *testing.T) {
	pl := &Plugin{}
	logger := klog.Background()

	tests := []struct {
		name   string
		oldRes *schedulingv1alpha1.Reservation
		newRes *schedulingv1alpha1.Reservation
		want   fwktype.QueueingHint
	}{
		{
			name:   "add reservation",
			oldRes: nil,
			newRes: &schedulingv1alpha1.Reservation{},
			want:   fwktype.Queue,
		},
		{
			name: "reservation became available",
			oldRes: &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationPending},
			},
			newRes: &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationAvailable},
			},
			want: fwktype.Queue,
		},
		{
			name: "allocated resources decreased",
			oldRes: &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{
					Allocated: corev1.ResourceList{
						corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
					},
				},
			},
			newRes: &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{
					Allocated: corev1.ResourceList{
						corev1.ResourceCPU: *resource.NewQuantity(5, resource.DecimalSI),
					},
				},
			},
			want: fwktype.Queue,
		},
		{
			name: "irrelevant update",
			oldRes: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": "b"}},
			},
			newRes: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": "c"}},
			},
			want: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := pl.isSchedulableAfterReservationChanged(logger, nil, tt.oldRes, tt.newRes)
			assert.Equal(t, tt.want, got)
		})
	}
}

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

package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestReservationStatusNodeNameIndexFunc(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		pod := &corev1.Pod{}
		got, err := reservationStatusNodeNameIndexFunc(pod)
		assert.NoError(t, err)
		assert.Equal(t, []string{}, got)

		rPending := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "reserve-pod-0",
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template: &corev1.PodTemplateSpec{},
			},
		}
		got, err = reservationStatusNodeNameIndexFunc(rPending)
		assert.NoError(t, err)
		assert.Equal(t, []string{}, got)

		rActive := rPending.DeepCopy()
		rActive.Status = schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-0",
		}
		got, err = reservationStatusNodeNameIndexFunc(rActive)
		assert.NoError(t, err)
		assert.Equal(t, []string{"test-node-0"}, got)
	})
}

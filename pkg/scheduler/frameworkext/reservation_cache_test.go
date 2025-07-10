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

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestReservationCache(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		oldRCache := GetReservationCache()
		defer func() {
			if oldRCache != nil {
				SetReservationCache(oldRCache)
			}
		}()
		testR := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-reservation",
				UID:  "test-reservation-uid",
			},
		}
		rc := &FakeReservationCache{
			RInfo: NewReservationInfo(testR),
		}
		SetReservationCache(rc)
		got := GetReservationCache()
		assert.Equal(t, rc, got)
		got1 := rc.GetReservationInfo(testR.UID)
		assert.Equal(t, NewReservationInfo(testR), got1)
		got2 := rc.GetReservationInfoByPod(&corev1.Pod{}, "test-node")
		assert.Equal(t, NewReservationInfo(testR), got2)
		rc.DeleteReservation(testR)
	})
}

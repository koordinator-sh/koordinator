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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func Test_stateData(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		s := &stateData{
			schedulingStateData: schedulingStateData{
				hasAffinity: true,
				podRequests: corev1.ResourceList{
					corev1.ResourceCPU:          resource.MustParse("1"),
					corev1.ResourceMemory:       resource.MustParse("4Gi"),
					extension.ResourceNvidiaGPU: resource.MustParse("1"),
				},
				podRequestsResources: &framework.Resource{
					MilliCPU: 1000,
					Memory:   4 * 1024 * 1024 * 1024,
					ScalarResources: map[corev1.ResourceName]int64{
						extension.ResourceNvidiaGPU: 1,
					},
				},
				preemptible:              map[string]corev1.ResourceList{},
				preemptibleInRRs:         map[string]map[types.UID]corev1.ResourceList{},
				nodeReservationStates:    map[string]*nodeReservationState{},
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{},
			},
		}
		sCopy := s.Clone()
		assert.Equal(t, s, sCopy)
		s.CleanSchedulingData()
		assert.Equal(t, &stateData{}, s)
	})
}

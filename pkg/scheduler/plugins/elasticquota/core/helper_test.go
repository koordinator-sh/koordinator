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

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"

	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestPodRequestsAndLimits(t *testing.T) {
	tests := []struct {
		name     string
		overhead corev1.ResourceList
		ignore   bool
		wantReqs corev1.ResourceList
	}{
		{
			name: "ElasticQuotaIgnorePodOverhead=false",
			overhead: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
			},
			ignore: false,
			wantReqs: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(5000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(9*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "ElasticQuotaIgnorePodOverhead=true",
			overhead: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
			},
			ignore: true,
			wantReqs: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.ElasticQuotaIgnorePodOverhead, tt.ignore)()
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(8000, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
								},
							},
						},
					},
					Overhead: tt.overhead,
				},
			}
			reqs := PodRequests(pod)
			assert.Equal(t, tt.wantReqs, reqs)
		})
	}

}

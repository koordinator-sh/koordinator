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

package impl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func TestFillPodDevicesAllocatedByKoord(t *testing.T) {
	tests := []struct {
		name           string
		response       *podresourcesapi.ListPodResourcesResponse
		podList        *corev1.PodList
		expectedResult *podresourcesapi.ListPodResourcesResponse
	}{
		{
			name: "MatchingPodWithDeviceAllocations",
			response: &podresourcesapi.ListPodResourcesResponse{
				PodResources: []*podresourcesapi.PodResources{
					{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Containers: []*podresourcesapi.ContainerResources{
							{
								Name: "test-container",
							},
						},
					},
				},
			},
			podList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod",
							Namespace: "test-namespace",
							Annotations: map[string]string{
								apiext.AnnotationDeviceAllocated: `{"gpu":[{"id":"0"}]}`,
							},
						},
					},
				},
			},
			expectedResult: &podresourcesapi.ListPodResourcesResponse{
				PodResources: []*podresourcesapi.PodResources{
					{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Containers: []*podresourcesapi.ContainerResources{
							{
								Name: "test-container",
								Devices: []*podresourcesapi.ContainerDevices{
									{
										ResourceName: "nvidia.com/gpu",
										DeviceIds:    []string{"0"},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "NoMatchingPod",
			response: &podresourcesapi.ListPodResourcesResponse{
				PodResources: []*podresourcesapi.PodResources{
					{
						Name:      "nonexistent-pod",
						Namespace: "nonexistent-namespace",
						Containers: []*podresourcesapi.ContainerResources{
							{
								Name: "nonexistent-container",
							},
						},
					},
				},
			},
			podList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod",
							Namespace: "test-namespace",
						},
					},
				},
			},
			expectedResult: &podresourcesapi.ListPodResourcesResponse{
				PodResources: []*podresourcesapi.PodResources{
					{
						Name:      "nonexistent-pod",
						Namespace: "nonexistent-namespace",
						Containers: []*podresourcesapi.ContainerResources{
							{
								Name: "nonexistent-container",
							},
						},
					},
				},
			},
		},
		{
			name: "DeviceAllocationsFetchFailure",
			response: &podresourcesapi.ListPodResourcesResponse{
				PodResources: []*podresourcesapi.PodResources{
					{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Containers: []*podresourcesapi.ContainerResources{
							{
								Name: "test-container",
							},
						},
					},
				},
			},
			podList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod",
							Namespace: "test-namespace",
							Annotations: map[string]string{
								"volcano.sh/device-allocations": `invalid-json`,
							},
						},
					},
				},
			},
			expectedResult: &podresourcesapi.ListPodResourcesResponse{
				PodResources: []*podresourcesapi.PodResources{
					{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Containers: []*podresourcesapi.ContainerResources{
							{
								Name: "test-container",
							},
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fillPodDevicesAllocatedByKoord(test.response, test.podList)
			assert.Equal(t, test.response, test.expectedResult)
		})
	}
}

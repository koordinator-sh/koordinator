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

package deviceshare

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestGetGPUPartitionIndexer(t *testing.T) {
	tests := []struct {
		name  string
		table apiext.GPUPartitionTable
		want  GPUPartitionIndexer
	}{
		{
			name: "normal flow ",
			table: apiext.GPUPartitionTable{
				1: {
					{
						Minors:          []int{0},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{1},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{2},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{3},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{4},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{5},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{6},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{7},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
				},
				2: {
					{
						Minors:          []int{0, 1},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{2, 3},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{4, 5},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{6, 7},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
				},
				4: {
					{
						Minors:          []int{0, 1, 2, 3},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
					{
						Minors:          []int{4, 5, 6, 7},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
				},
				8: {
					{
						Minors:          []int{0, 1, 2, 3, 4, 5, 6, 7},
						GPULinkType:     apiext.GPUNVLink,
						AllocationScore: 1,
					},
				},
			},
			want: GPUPartitionIndexOfNVIDIAHopper,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetGPUPartitionIndexer(tt.table))
		})
	}
}

func TestGetGPUTopologyScope(t *testing.T) {
	mockDeviceCR := fakeDeviceCR.DeepCopy()
	tests := []struct {
		name   string
		device *schedulingv1alpha1.Device
		want   *GPUTopologyScope
	}{
		{
			name:   "normal flow",
			device: mockDeviceCR,
			want: &GPUTopologyScope{
				scopeName:  apiext.DeviceTopologyScopeNode,
				scopeLevel: apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopeNode],
				minorsResources: map[int]corev1.ResourceList{
					0: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
					1: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
					2: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
					3: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
					4: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
					5: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
					6: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
					7: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
					},
				},
				minors:     []int{0, 1, 2, 3, 4, 5, 6, 7},
				minorsHash: hashMinors([]int{0, 1, 2, 3, 4, 5, 6, 7}),
				childScopes: []*GPUTopologyScope{
					{
						scopeName:  apiext.DeviceTopologyScopeNUMANode,
						scopeLevel: apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopeNUMANode],
						minorsResources: map[int]corev1.ResourceList{
							0: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
							1: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
							2: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
							3: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
						},
						minors:     []int{0, 1, 2, 3},
						minorsHash: hashMinors([]int{0, 1, 2, 3}),
						numaNodeID: 0,
						childScopes: []*GPUTopologyScope{
							{
								scopeName:  apiext.DeviceTopologyScopePCIe,
								scopeLevel: apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopePCIe],
								minorsResources: map[int]corev1.ResourceList{
									0: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
									1: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
								},
								minors:     []int{0, 1},
								minorsHash: hashMinors([]int{0, 1}),
								pcieID:     "0",
							},
							{
								scopeName:  apiext.DeviceTopologyScopePCIe,
								scopeLevel: apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopePCIe],
								minorsResources: map[int]corev1.ResourceList{
									2: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
									3: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
								},
								minors:     []int{2, 3},
								minorsHash: hashMinors([]int{2, 3}),
								pcieID:     "1",
							},
						},
					},
					{
						scopeName:  apiext.DeviceTopologyScopeNUMANode,
						scopeLevel: apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopeNUMANode],
						minorsResources: map[int]corev1.ResourceList{
							4: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
							5: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
							6: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
							7: {
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
							},
						},
						minors:     []int{4, 5, 6, 7},
						minorsHash: hashMinors([]int{4, 5, 6, 7}),
						numaNodeID: 1,
						childScopes: []*GPUTopologyScope{
							{
								scopeName:  apiext.DeviceTopologyScopePCIe,
								scopeLevel: apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopePCIe],
								minorsResources: map[int]corev1.ResourceList{
									4: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
									5: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
								},
								minors:     []int{4, 5},
								minorsHash: hashMinors([]int{4, 5}),
								pcieID:     "2",
							},
							{
								scopeName:  apiext.DeviceTopologyScopePCIe,
								scopeLevel: apiext.DeviceTopologyScopeLevel[apiext.DeviceTopologyScopePCIe],
								minorsResources: map[int]corev1.ResourceList{
									6: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
									7: {
										apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
										apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
									},
								},
								minors:     []int{6, 7},
								minorsHash: hashMinors([]int{6, 7}),
								pcieID:     "3",
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deviceInfos := map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{}
			for i := range tt.device.Spec.Devices {
				info := &tt.device.Spec.Devices[i]
				deviceInfos[info.Type] = append(deviceInfos[info.Type], info)
				info.Resources = corev1.ResourceList{
					apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
					apiext.ResourceGPUMemory:      *resource.NewQuantity(80*1024*10241024, resource.BinarySI),
				}
			}
			nodeDeviceResource := buildDeviceResources(tt.device)
			assert.Equal(t, tt.want, GetGPUTopologyScope(deviceInfos[schedulingv1alpha1.GPU], nodeDeviceResource[schedulingv1alpha1.GPU]))
		})
	}
}

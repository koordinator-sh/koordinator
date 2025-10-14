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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func Test_sortDeviceResourcesByMinor(t *testing.T) {
	tests := []struct {
		name         string
		resources    []deviceResourceMinorPair
		preferred    []int
		expectOrders []int
	}{
		{
			name: "without preferred",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
			},
			expectOrders: []int{0, 1, 2},
		},
		{
			name: "with preferred",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
				{minor: 5},
				{minor: 6},
				{minor: 7},
			},
			preferred:    []int{5, 6},
			expectOrders: []int{5, 6, 0, 1, 2, 7},
		},
		{
			name: "with preferred and score",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
				{minor: 5, score: 10},
				{minor: 6, score: 50},
				{minor: 7},
			},
			preferred:    []int{5, 6},
			expectOrders: []int{6, 5, 0, 1, 2, 7},
		},
		{
			name: "with preferred and same score",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
				{minor: 5, score: 10},
				{minor: 6, score: 10},
				{minor: 7},
			},
			preferred:    []int{5, 6},
			expectOrders: []int{5, 6, 0, 1, 2, 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortDeviceResourcesByMinor(tt.resources, sets.NewInt(tt.preferred...))
			var orders []int
			for _, v := range got {
				orders = append(orders, v.minor)
			}
			assert.Equal(t, tt.expectOrders, orders)
		})
	}
}

func Test_sortDeviceResourcesByPreferredPCIe(t *testing.T) {
	tests := []struct {
		name          string
		resources     []deviceResourceMinorPair
		preferredPCIe sets.String
		deviceInfos   map[int]*schedulingv1alpha1.DeviceInfo
		expectOrders  []int
	}{
		{
			name: "without preferred pcie",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
				{minor: 3},
			},
			expectOrders: []int{0, 1, 2, 3},
		},
		{
			name: "with preferred pcie",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
				{minor: 3},
				{minor: 4},
				{minor: 5},
				{minor: 6},
				{minor: 7},
			},
			preferredPCIe: sets.NewString("0", "1"),
			deviceInfos: map[int]*schedulingv1alpha1.DeviceInfo{
				1: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "0"}},
				2: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "0"}},
				5: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "1"}},
				6: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "1"}},
			},
			expectOrders: []int{1, 5, 2, 6, 0, 3, 4, 7},
		},
		{
			name: "with preferred pcie and score",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
				{minor: 3},
				{minor: 4},
				{minor: 5, score: 10},
				{minor: 6, score: 50},
				{minor: 7},
			},
			preferredPCIe: sets.NewString("0", "1"),
			deviceInfos: map[int]*schedulingv1alpha1.DeviceInfo{
				1: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "0"}},
				2: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "0"}},
				5: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "1"}},
				6: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "1"}},
			},
			expectOrders: []int{6, 1, 5, 2, 0, 3, 4, 7},
		},
		{
			name: "with preferred pcie and same score",
			resources: []deviceResourceMinorPair{
				{minor: 0},
				{minor: 1},
				{minor: 2},
				{minor: 3},
				{minor: 4},
				{minor: 5, score: 10},
				{minor: 6, score: 10},
				{minor: 7},
			},
			preferredPCIe: sets.NewString("0", "1"),
			deviceInfos: map[int]*schedulingv1alpha1.DeviceInfo{
				1: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "0"}},
				2: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "0"}},
				5: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "1"}},
				6: {Topology: &schedulingv1alpha1.DeviceTopology{PCIEID: "1"}},
			},
			expectOrders: []int{5, 1, 6, 2, 0, 3, 4, 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortDeviceResourcesByPreferredPCIe(tt.resources, tt.preferredPCIe, tt.deviceInfos)
			var orders []int
			for _, v := range got {
				orders = append(orders, v.minor)
			}
			assert.Equal(t, tt.expectOrders, orders)
		})
	}
}

func Test_appendAllocatedByHints(t *testing.T) {
	tests := []struct {
		name      string
		allocated map[schedulingv1alpha1.DeviceType]deviceResources
		hints     map[schedulingv1alpha1.DeviceType]sets.Int
		expect    map[schedulingv1alpha1.DeviceType]deviceResources
	}{
		{
			name: "without hints",
			allocated: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {},
					1: {},
					2: {},
				},
			},
			hints:  nil,
			expect: map[schedulingv1alpha1.DeviceType]deviceResources{},
		},
		{
			name: "with hints",
			allocated: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
					1: {
						apiext.ResourceGPUCore: resource.MustParse("50"),
					},
					2: {
						apiext.ResourceGPUCore: resource.MustParse("60"),
					},
				},
			},
			hints: map[schedulingv1alpha1.DeviceType]sets.Int{
				schedulingv1alpha1.GPU: sets.NewInt(0, 1),
			},
			expect: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
					1: {
						apiext.ResourceGPUCore: resource.MustParse("50"),
					},
				},
			},
		},
		{
			name: "with hints filter non-GPU devies",
			allocated: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
					1: {
						apiext.ResourceGPUCore: resource.MustParse("50"),
					},
					2: {
						apiext.ResourceGPUCore: resource.MustParse("60"),
					},
				},
				schedulingv1alpha1.RDMA: {
					0: {
						apiext.ResourceRDMA: resource.MustParse("100"),
					},
					1: {
						apiext.ResourceRDMA: resource.MustParse("50"),
					},
					2: {
						apiext.ResourceRDMA: resource.MustParse("60"),
					},
				},
			},
			hints: map[schedulingv1alpha1.DeviceType]sets.Int{
				schedulingv1alpha1.GPU: sets.NewInt(0, 1),
			},
			expect: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
					1: {
						apiext.ResourceGPUCore: resource.MustParse("50"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendAllocatedByHints(tt.hints, nil, tt.allocated)
			assert.Equal(t, tt.expect, got)
		})
	}
}

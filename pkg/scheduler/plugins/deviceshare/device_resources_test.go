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
		resources    deviceResources
		preferred    []int
		expectOrders []int
	}{
		{
			name: "without preferred",
			resources: deviceResources{
				0: {},
				1: {},
				2: {},
			},
			expectOrders: []int{0, 1, 2},
		},
		{
			name: "with preferred",
			resources: deviceResources{
				0: {},
				1: {},
				2: {},
				5: {},
				6: {},
				7: {},
			},
			preferred:    []int{5, 6},
			expectOrders: []int{5, 6, 0, 1, 2, 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r []deviceResourceMinorPair
			for minor, resources := range tt.resources {
				r = append(r, deviceResourceMinorPair{
					minor:     minor,
					resources: resources,
					score:     0,
				})
			}
			got := sortDeviceResourcesByMinor(r, sets.NewInt(tt.preferred...))
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

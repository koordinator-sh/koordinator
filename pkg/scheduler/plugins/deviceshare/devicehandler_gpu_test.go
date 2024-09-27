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

func Test_fillGPUTotalMem(t *testing.T) {
	tests := []struct {
		name            string
		allocations     apiext.DeviceAllocations
		nodeDeviceInfo  *nodeDevice
		wantAllocations apiext.DeviceAllocations
		wantErr         bool
	}{
		{
			name: "ratio to mem",
			allocations: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
						},
					},
				},
			},
			nodeDeviceInfo: &nodeDevice{
				deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: {
						0: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
							apiext.ResourceGPUMemory:      resource.MustParse("32Gi"),
						},
					},
				},
			},
			wantAllocations: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
						},
					},
				},
			},
		},
		{
			name: "mem to ratio",
			allocations: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("50"),
							apiext.ResourceGPUMemory: resource.MustParse("16Gi"),
						},
					},
				},
			},
			nodeDeviceInfo: &nodeDevice{
				deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: {
						0: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
							apiext.ResourceGPUMemory:      resource.MustParse("32Gi"),
						},
					},
				},
			},
			wantAllocations: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
							apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
						},
					},
				},
			},
		},
		{
			name: "missing total",
			allocations: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
						},
					},
				},
			},
			nodeDeviceInfo: &nodeDevice{
				deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: {
						0: corev1.ResourceList{},
					},
				},
			},
			wantAllocations: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
						},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fillGPUTotalMem(tt.allocations, tt.nodeDeviceInfo)
			if tt.wantErr != (err != nil) {
				t.Errorf("wantErr %v but got %v", tt.wantErr, err != nil)
			}
			assert.Equal(t, tt.wantAllocations, tt.allocations)
		})
	}
}

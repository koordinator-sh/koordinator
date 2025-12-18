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

package transformer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestTransformDevice(t *testing.T) {
	tests := []struct {
		name       string
		device     *schedulingv1alpha1.Device
		wantDevice *schedulingv1alpha1.Device
	}{
		{
			name: "normal device",
			device: &schedulingv1alpha1.Device{
				Spec: schedulingv1alpha1.DeviceSpec{
					Devices: []schedulingv1alpha1.DeviceInfo{
						{
							Minor:  ptr.To[int32](1),
							Type:   schedulingv1alpha1.GPU,
							Health: true,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore: resource.MustParse("100"),
							},
						},
					},
				},
			},
			wantDevice: &schedulingv1alpha1.Device{
				Spec: schedulingv1alpha1.DeviceSpec{
					Devices: []schedulingv1alpha1.DeviceInfo{
						{
							Minor:  ptr.To[int32](1),
							Type:   schedulingv1alpha1.GPU,
							Health: true,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore: resource.MustParse("100"),
							},
						},
					},
				},
			},
		},
		{
			name: "device with deprecated resources",
			device: &schedulingv1alpha1.Device{
				Spec: schedulingv1alpha1.DeviceSpec{
					Devices: []schedulingv1alpha1.DeviceInfo{
						{
							Minor:  ptr.To[int32](1),
							Type:   schedulingv1alpha1.GPU,
							Health: true,
							Resources: corev1.ResourceList{
								apiext.DeprecatedGPUCore:        resource.MustParse("100"),
								apiext.DeprecatedGPUMemoryRatio: resource.MustParse("100"),
								apiext.DeprecatedGPUMemory:      resource.MustParse("8Gi"),
							},
						},
					},
				},
			},
			wantDevice: &schedulingv1alpha1.Device{
				Spec: schedulingv1alpha1.DeviceSpec{
					Devices: []schedulingv1alpha1.DeviceInfo{
						{
							Minor:  ptr.To[int32](1),
							Type:   schedulingv1alpha1.GPU,
							Health: true,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := TransformDevice(tt.device)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantDevice, obj)
		})
	}
}

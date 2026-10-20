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

package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

func TestNewHYManager(t *testing.T) {
	manager := NewHYManager()
	assert.NotNil(t, manager)
	_, ok := manager.(*hyManager)
	assert.True(t, ok)
}

func TestHyManager_GetDeviceVendor(t *testing.T) {
	manager := &hyManager{}
	assert.Equal(t, extension.GPUVendorHygon, manager.GetDeviceVendor())
}

func TestHyManager_GetDriverVersion(t *testing.T) {
	manager := &hyManager{}
	version, err := manager.GetDriverVersion()
	assert.NoError(t, err)
	assert.Equal(t, "", version)
}

func TestHyDevice_GetDeviceInfo(t *testing.T) {
	tests := []struct {
		name string
		dev  hyDevice
		want koordletuti.XPUDeviceInfo
	}{
		{
			name: "full device info",
			dev: hyDevice{
				DeviceInfo: koordletuti.XPUDeviceInfo{
					Vendor: extension.GPUVendorHygon,
					Model:  "BW200-UBB-BW1000",
					UUID:   "HY-0",
					Minor:  "0",
					Resources: map[string]string{
						"koordinator.sh/gpu-memory": "65520Mi",
						"koordinator.sh/gpu-core":   "100",
					},
				},
			},
			want: koordletuti.XPUDeviceInfo{
				Vendor: extension.GPUVendorHygon,
				Model:  "BW200-UBB-BW1000",
				UUID:   "HY-0",
				Minor:  "0",
				Resources: map[string]string{
					"koordinator.sh/gpu-memory": "65520Mi",
					"koordinator.sh/gpu-core":   "100",
				},
			},
		},
		{
			name: "empty device info",
			dev:  hyDevice{},
			want: koordletuti.XPUDeviceInfo{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.dev.GetDeviceInfo()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

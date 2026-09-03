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

	"github.com/jaypipes/ghw/pkg/pci"
	pcidb "github.com/jaypipes/pcidb/types"
	"github.com/stretchr/testify/assert"
)

func TestIsNPUDevice(t *testing.T) {
	tests := []struct {
		name   string
		device *pci.Device
		want   bool
	}{
		{
			name: "HUAWEI vendor with non-excluded product",
			device: &pci.Device{
				Vendor:  &pcidb.Vendor{ID: HUAWEIVendorID},
				Product: &pcidb.Product{ID: "d801"},
			},
			want: true,
		},
		{
			name: "HUAWEI real vendor with non-excluded product",
			device: &pci.Device{
				Vendor:  &pcidb.Vendor{ID: HUAWEIRealVendorID},
				Product: &pcidb.Product{ID: "d801"},
			},
			want: true,
		},
		{
			name: "HUAWEI vendor with excluded product 1710",
			device: &pci.Device{
				Vendor:  &pcidb.Vendor{ID: HUAWEIVendorID},
				Product: &pcidb.Product{ID: "1710"},
			},
			want: false,
		},
		{
			name: "HUAWEI vendor with excluded product 1711",
			device: &pci.Device{
				Vendor:  &pcidb.Vendor{ID: HUAWEIVendorID},
				Product: &pcidb.Product{ID: "1711"},
			},
			want: false,
		},
		{
			name: "non-HUAWEI vendor",
			device: &pci.Device{
				Vendor:  &pcidb.Vendor{ID: "0x1234"},
				Product: &pcidb.Product{ID: "d801"},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsNPUDevice(tt.device))
		})
	}
}

func TestIsXPUDevice(t *testing.T) {
	tests := []struct {
		name   string
		device *pci.Device
		want   bool
	}{
		{
			name:   "KUNLUN vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: KUNLUNVendorID}},
			want:   true,
		},
		{
			name:   "KUNLUN real vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: KUNLUNRealVendorID}},
			want:   true,
		},
		{
			name:   "non-KUNLUN vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: "0x9999"}},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsXPUDevice(tt.device))
		})
	}
}

func TestIsMLUDevice(t *testing.T) {
	tests := []struct {
		name   string
		device *pci.Device
		want   bool
	}{
		{
			name:   "MLU vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: MLUVendorID}},
			want:   true,
		},
		{
			name:   "MLU real vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: MLURealVendorID}},
			want:   true,
		},
		{
			name:   "non-MLU vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: "0x1234"}},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsMLUDevice(tt.device))
		})
	}
}

func TestIsMXDevice(t *testing.T) {
	tests := []struct {
		name   string
		device *pci.Device
		want   bool
	}{
		{
			name:   "MX vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: MXVendorID}},
			want:   true,
		},
		{
			name:   "MX real vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: MXRealVendorID}},
			want:   true,
		},
		{
			name:   "non-MX vendor",
			device: &pci.Device{Vendor: &pcidb.Vendor{ID: "0x1234"}},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsMXDevice(tt.device))
		})
	}
}

func TestIsHYDevice(t *testing.T) {
	tests := []struct {
		name   string
		device *pci.Device
		want   bool
	}{
		{
			name: "HYGON vendor with Co-processor class",
			device: &pci.Device{
				Vendor: &pcidb.Vendor{ID: HYGONVendorID},
				Class:  &pcidb.Class{Name: "Co-processor"},
			},
			want: true,
		},
		{
			name: "HYGON real vendor with Processor class",
			device: &pci.Device{
				Vendor: &pcidb.Vendor{ID: HYGONRealVendorID},
				Class:  &pcidb.Class{Name: "Processor"},
			},
			want: true,
		},
		{
			name: "HYGON vendor with lowercase co-processor",
			device: &pci.Device{
				Vendor: &pcidb.Vendor{ID: HYGONVendorID},
				Class:  &pcidb.Class{Name: "co-processor"},
			},
			want: true,
		},
		{
			name: "HYGON vendor with Display controller class",
			device: &pci.Device{
				Vendor: &pcidb.Vendor{ID: HYGONVendorID},
				Class:  &pcidb.Class{Name: "Display controller"},
			},
			want: false,
		},
		{
			name: "non-HYGON vendor with Co-processor class",
			device: &pci.Device{
				Vendor: &pcidb.Vendor{ID: "0x1234"},
				Class:  &pcidb.Class{Name: "Co-processor"},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsHYDevice(tt.device))
		})
	}
}

func TestIsNumericRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "pure digits", input: "12345", want: true},
		{name: "single digit", input: "0", want: true},
		{name: "letters", input: "abc", want: false},
		{name: "mixed", input: "12a3", want: false},
		{name: "empty string", input: "", want: false},
		{name: "spaces", input: "1 2", want: false},
		{name: "negative number", input: "-1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNumericRegex(tt.input))
		})
	}
}

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

package util

type DeviceType string

const (
	GPUDeviceType  DeviceType = "GPU"
	RDMADeviceType DeviceType = "RDMA"
)

type Devices interface {
	Type() DeviceType
}

type GPUDevices []GPUDeviceInfo

func (g GPUDevices) Type() DeviceType {
	return GPUDeviceType
}

type GPUDeviceInfo struct {
	// UUID represents the UUID of device
	UUID string `json:"id,omitempty"`
	// Minor represents the Minor number of Devices, starting from 0
	Minor       int32  `json:"minor,omitempty"`
	MemoryTotal uint64 `json:"memory-total,omitempty"`
	NodeID      int32  `json:"nodeID"`
	PCIE        string `json:"pcie,omitempty"`
	BusID       string `json:"busID,omitempty"`
}

type RDMADevices []RDMADeviceInfo

func (r RDMADevices) Type() DeviceType {
	return RDMADeviceType
}

type RDMADeviceInfo struct {
	ID            string                      `json:"id,omitempty"`
	NetDev        string                      `json:"netDev,omitempty"`
	MasterNetDev  *string                     `json:"masterNetDev,omitempty"`
	RDMAResources []string                    `json:"rdmaResources"`
	DevicePaths   []string                    `json:"devicePaths,omitempty"`
	VFEnabled     bool                        `json:"vfEnabled,omitempty"`
	VFMap         map[string]*VirtualFunction `json:"vfMap,omitempty"` // busId:VirtualFunction
	Labels        map[string]string           `json:"labels,omitempty"`
	Minor         int32                       `json:"minor"`
	VendorCode    string                      `json:"vendorCode,omitempty"`
	DeviceCode    string                      `json:"deviceCode,omitempty"`
	NodeID        int32                       `json:"nodeID,omitempty"`
	PCIE          string                      `json:"pcie,omitempty"`
	BusID         string                      `json:"busID,omitempty"`
}

type VirtualFunction struct {
	ID          string            `json:"id,omitempty"`
	DevicePaths []string          `json:"devicePaths,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CustomInfo  interface{}       `json:"customInfo,omitempty"`
}

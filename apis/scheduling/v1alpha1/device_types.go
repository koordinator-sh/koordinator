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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DeviceType string

const (
	GPU  DeviceType = "gpu"
	FPGA DeviceType = "fpga"
	RDMA DeviceType = "rdma"
)

type DeviceSpec struct {
	Devices []DeviceInfo `json:"devices,omitempty"`
}

type DeviceInfo struct {
	// Type represents the type of device
	Type DeviceType `json:"type,omitempty"`
	// Labels represents the device properties that can be used to organize and categorize (scope and select) objects
	Labels map[string]string `json:"labels,omitempty"`
	// UUID represents the UUID of device
	UUID string `json:"id,omitempty"`
	// Minor represents the Minor number of Device, starting from 0
	Minor *int32 `json:"minor,omitempty"`
	// ModuleID represents the physical id of Device
	ModuleID *int32 `json:"moduleID,omitempty"`
	// Health indicates whether the device is normal
	Health bool `json:"health,omitempty"`
	// Resources is a set of (resource name, quantity) pairs
	Resources corev1.ResourceList `json:"resources,omitempty"`
	// Topology represents the topology information about the device
	Topology *DeviceTopology `json:"topology,omitempty"`
	// VFGroups represents the virtual function devices
	VFGroups []VirtualFunctionGroup `json:"vfGroups,omitempty"`
}

type DeviceTopology struct {
	SocketID int32  `json:"socketID"`
	NodeID   int32  `json:"nodeID"`
	PCIEID   int32  `json:"pcieID"`
	BusID    string `json:"busID,omitempty"`
}

type VirtualFunctionGroup struct {
	Labels map[string]string `json:"labels,omitempty"`
	VFs    []VirtualFunction `json:"vfs,omitempty"`
}

type VirtualFunction struct {
	Minor int32  `json:"minor"`
	BusID string `json:"busID,omitempty"`
}

type DeviceStatus struct {
	Allocations []DeviceAllocation `json:"allocations,omitempty"`
}

type DeviceAllocation struct {
	Type    DeviceType             `json:"type,omitempty"`
	Entries []DeviceAllocationItem `json:"entries,omitempty"`
}

type DeviceAllocationItem struct {
	Name      string  `json:"name,omitempty"`
	Namespace string  `json:"namespace,omitempty"`
	UUID      string  `json:"uuid,omitempty"`
	Minors    []int32 `json:"minors,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

type Device struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeviceSpec   `json:"spec,omitempty"`
	Status DeviceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type DeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Device `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Device{}, &DeviceList{})
}

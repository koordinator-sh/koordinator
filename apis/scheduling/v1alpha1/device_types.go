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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DeviceType string

const (
	GPU  DeviceType = "gpu"
	FPGA DeviceType = "fpga"
	RDMA DeviceType = "rdma"
)

type DeviceSpec struct {
	Devices []DeviceInfo `json:"devices"`
}

type DeviceInfo struct {
	// UUID represents the UUID of device
	UUID string `json:"id,omitempty"`
	// Minor represents the Minor number of Device, starting from 0
	Minor int32 `json:"minor,omitempty"`
	// Type represents the type of device
	Type DeviceType `json:"deviceType,omitempty"`
	// Health indicates whether the device is normal
	Health bool `json:"health,omitempty"`
	// Resources represents the total capacity of various resources of the device
	Resources map[string]resource.Quantity `json:"resource,omitempty"`
}

type DeviceStatus struct {
	Allocations []DeviceAllocation `json:"allocations"`
}

type DeviceAllocation struct {
	Type    DeviceType             `json:"type"`
	Entries []DeviceAllocationItem `json:"entries"`
}

type DeviceAllocationItem struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	UUID      string   `json:"uuid"`
	Devices   []string `json:"devices"`
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

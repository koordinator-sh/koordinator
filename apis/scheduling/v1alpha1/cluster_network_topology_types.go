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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TopologyLayer string

const (
	// ClusterTopologyLayer is the built-in layer which represents a K8s cluster.
	ClusterTopologyLayer TopologyLayer = "ClusterTopologyLayer"
	// NodeTopologyLayer is the built-in layer which represents a K8s Node.
	NodeTopologyLayer TopologyLayer = "NodeTopologyLayer"
)

type ClusterNetworkTopologySpec struct {
	NetworkTopologySpec []NetworkTopologySpec `json:"networkTopologySpec"`
}

type NetworkTopologySpec struct {
	// TopologyLayer is the type of the layer.
	TopologyLayer TopologyLayer `json:"topologyLayer"`
	// ParentTopologyLayer is the type of the parent layer of the layer.
	// +optional
	ParentTopologyLayer TopologyLayer `json:"parentTopologyLayer,omitempty"`
	// LabelKey is the key of label on the Node which represents the layer that the Node belongs to.
	// The matching policy is any of.
	// +optional
	LabelKey []string `json:"labelKey,omitempty"`
}

type ClusterNetworkTopologyStatus struct {
	// +optional
	DetailStatus []*ClusterNetworkTopologyDetailStatus `json:"detailStatus,omitempty"`
}

type ClusterNetworkTopologyDetailStatus struct {
	TopologyInfo TopologyInfo `json:"topologyInfo"`
	// +optional
	ParentTopologyInfo *TopologyInfo `json:"parentTopologyInfo,omitempty"`
	// +optional
	ChildTopologyLayer TopologyLayer `json:"childTopologyLayer,omitempty"`
	// +optional
	ChildTopologyNames []string `json:"childTopologyNames,omitempty"`
	NodeNum            int32    `json:"nodeNum"`
}

type TopologyInfo struct {
	TopologyLayer TopologyLayer `json:"topologyLayer"`
	TopologyName  string        `json:"topologyName"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

type ClusterNetworkTopology struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterNetworkTopologySpec   `json:"spec,omitempty"`
	Status ClusterNetworkTopologyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type ClusterNetworkTopologyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ClusterNetworkTopology `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterNetworkTopology{}, &ClusterNetworkTopologyList{})
}

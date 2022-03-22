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
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterColocationProfileSpec is a description of a ClusterColocationProfile.
type ClusterColocationProfileSpec struct {
	// NamespaceSelector decides whether to mutate/validate Pods on an object based
	// on whether the namespace for that object matches the selector.
	// Default to the empty LabelSelector, which matches everything.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ObjectSelector decides whether to mutate/validate Pods based on if the
	// Pod has matching labels.
	// Use the object selector only if the profile is opt-in, because end
	// users may skip the admission webhook by setting the labels.
	// Default to the empty LabelSelector, which matches everything.
	// +optional
	ObjectSelector *metav1.LabelSelector `json:"objectSelector,omitempty"`

	// Labels describes the k/v pair that needs to inject into Pod.Labels
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations describes the k/v pair that needs to inject into Pod.Annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// If specified, the pod will be dispatched by specified scheduler.
	// +optional
	SchedulerName string `json:"schedulerName,omitempty"`

	// Patch indicates patching podTemplate to the Pod.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Patch runtime.RawExtension `json:"patch,omitempty"`
}

// ClusterColocationProfileStatus represents information about the status of a ClusterColocationProfile.
type ClusterColocationProfileStatus struct {
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ClusterColocationProfile is the Schema for the ClusterColocationProfile API
type ClusterColocationProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterColocationProfileSpec   `json:"spec,omitempty"`
	Status            ClusterColocationProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterColocationProfileList contains a list of ClusterColocationProfile
type ClusterColocationProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterColocationProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterColocationProfile{}, &ClusterColocationProfileList{})
}

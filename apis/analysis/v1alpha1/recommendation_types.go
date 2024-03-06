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

// RecommendationTargetType defines the type of analysis target
type RecommendationTargetType string

const (
	// RecommendationTargetWorkload defines the k8s workload type
	RecommendationTargetWorkload RecommendationTargetType = "workload"
	// RecommendationPodSelector defines the pod selector type
	RecommendationPodSelector RecommendationTargetType = "podSelector"
)

// RecommendationTarget defines the target of analysis, which can be a k8s workload or a series of pods
type RecommendationTarget struct {
	// Type indicates the type of target
	Type RecommendationTargetType `json:"type"`
	// Workload indicates the target is a k8s workload, which is effective when Type is "workload"
	Workload *CrossVersionObjectReference `json:"workload,omitempty"`
	// PodSelector defines the reference of a series of pods, which is effective when Type is "podSelector"
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`
}

// CrossVersionObjectReference contains enough information to let you identify the referred resource.
type CrossVersionObjectReference struct {
	// Kind of the referent; More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds"
	Kind string `json:"kind"`
	// Name of the referent; More info: http://kubernetes.io/docs/user-guide/identifiers#names
	Name string `json:"name"`
	// API version of the referent
	APIVersion string `json:"apiVersion,omitempty"`
}

// RecommendationSpec is the specification of the client object.
type RecommendationSpec struct {
	// Target is the object to be analyzed, which can be a workload or a series of pods
	Target RecommendationTarget `json:"target"`
	// TODO add more tuning knobs about aggregation policy
}

// RecommendedPodStatus defines the observed state of pod
type RecommendedPodStatus struct {
	// ContainerStatuses records the most recently computed amount of resources recommended
	ContainerStatuses []RecommendedContainerStatus `json:"containerStatuses,omitempty"`
}

// RecommendedContainerStatus defines the observed state of container
type RecommendedContainerStatus struct {
	// Name of the container.
	ContainerName string `json:"containerName,omitempty"`
	// Recommended resources of container
	Resources corev1.ResourceList `json:"resources,omitempty"`
}

// RecommendationStatus defines the observed state of Recommendation
type RecommendationStatus struct {
	// PodStatus records the most recently computed amount of resources recommended
	PodStatus *RecommendedPodStatus `json:"podStatus,omitempty"`
	// UpdateTime is the update time of the distribution
	UpdateTime *metav1.Time `json:"updateTime,omitempty"`
	// Conditions is the list of conditions representing the status of the distribution
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=recom
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.target.type"
// +kubebuilder:printcolumn:name="Kind",type="string",JSONPath=".spec.target.workload.kind"
// +kubebuilder:printcolumn:name="Name",type="string",JSONPath=".spec.target.workload.name"
// +kubebuilder:printcolumn:name="LastUpdateTime",type="date",JSONPath=".status.updateTime"

// Recommendation is the Schema for the recommendations API
type Recommendation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RecommendationSpec   `json:"spec,omitempty"`
	Status RecommendationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RecommendationList contains a list of Recommendation
type RecommendationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Recommendation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Recommendation{}, &RecommendationList{})
}

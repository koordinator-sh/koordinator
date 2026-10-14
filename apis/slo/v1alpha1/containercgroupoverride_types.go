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

	"github.com/koordinator-sh/koordinator/apis/extension"
)

// ContainerCgroupTarget identifies the container to override.
// Shape mirrors NRC PodRef (name+UID) but keeps one-container ownership (not multi-container CR).
type ContainerCgroupTarget struct {
	// PodName is the target Pod name in the same namespace as this CR.
	PodName string `json:"podName"`
	// ContainerName is the target container name.
	ContainerName string `json:"containerName"`
	// PodUID when set must match the live Pod UID (recommended; prevents recreate races).
	PodUID string `json:"podUID,omitempty"`
}

// ContainerCgroupOverrideSpec defines the desired runtime cgroup hard limits.
// Resources are nested by controller (ACK Cgroups style) for extensibility;
// restore contract stays host-baseline writeback (not NRC PodSpec rollback).
type ContainerCgroupOverrideSpec struct {
	// Target selects the Pod/container. Required.
	Target ContainerCgroupTarget `json:"target"`

	// Resources is the desired hard-limit surface (memory/cpu/cpuset; blkio schema-ready).
	Resources extension.ContainerCgroupResources `json:"resources"`

	// WritebackOnDelete defaults to true: delete blocks on finalizer until host baseline restored.
	WritebackOnDelete *bool `json:"writebackOnDelete,omitempty"`
}

// ContainerCgroupOverrideStatus is the observed state.
type ContainerCgroupOverrideStatus struct {
	Phase   extension.CgroupOverridePhase `json:"phase,omitempty"`
	Message string                        `json:"message,omitempty"`

	// NodeName is filled by the aggregator from the scheduled Pod.
	NodeName string `json:"nodeName,omitempty"`

	// ObservedGeneration is the metadata.generation last successfully applied.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Baseline is the host cgroup snapshot captured before first apply (nested).
	Baseline *extension.ContainerCgroupResources `json:"baseline,omitempty"`

	ObservedDesiredHash string `json:"observedDesiredHash,omitempty"`

	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// Conditions optional NRC-style observability (Applied / Writeback / Failed).
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cco
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.spec.target.podName`
// +kubebuilder:printcolumn:name="Container",type=string,JSONPath=`.spec.target.containerName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.status.nodeName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ContainerCgroupOverride declares precise runtime cgroup hard limits for one container
// without mutating Pod Spec. Delete (default) writebacks host baseline.
type ContainerCgroupOverride struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerCgroupOverrideSpec   `json:"spec,omitempty"`
	Status ContainerCgroupOverrideStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ContainerCgroupOverrideList contains a list of ContainerCgroupOverride.
type ContainerCgroupOverrideList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerCgroupOverride `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ContainerCgroupOverride{}, &ContainerCgroupOverrideList{})
}

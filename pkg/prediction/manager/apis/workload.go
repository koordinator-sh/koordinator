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

package apis

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkloadTargetType defines the type of analysis target
type WorkloadTargetType string

const (
	// WorkloadTargetController defines the k8s workload type
	WorkloadTargetController WorkloadTargetType = "controller"
	// WorkloadTargetPodSelector defines the pod selector type
	WorkloadTargetPodSelector WorkloadTargetType = "podSelector"
)

// WorkloadTarget defines the target for prediction
type WorkloadTarget struct {
	// Type indicates the type of target
	Type WorkloadTargetType `json:"type"`
	// Controller indicates the target is a k8s workload, which is effective when Type is "workload"
	Controller *ControllerRef `json:"controller,omitempty"`
	// PodSelector indicates the target is a series of pods, which is effective when Type is "podSelector"
	PodSelector *PodSelectorRef `json:"podSelector,omitempty"`
}

// ControllerRef defines the reference of a k8s workload managed by controller
type ControllerRef struct {
	// Namespace of thte workload
	Namespace string `json:"namespace"`
	// Kind of the referent; More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds"
	Kind string `json:"kind"`
	// Name of the referent; More info: http://kubernetes.io/docs/user-guide/identifiers#names
	Name string `json:"name"`
	// API version of the referent
	APIVersion string `json:"apiVersion,omitempty"`
	// Hierarchy indicates the hierarchy of the target for profiling
	Hierarchy ProfileHierarchy `json:"hierarchy,omitempty"`
}

// PodSelectorRef defines the reference of a series of pods
type PodSelectorRef struct {
	// Namespace of pods
	Namespace string `json:"namespace"`
	// Alias of pods selector
	Alias string `json:"alias"`
	// Selector is a label query over pods
	Selector *metav1.LabelSelector `json:"selector"`
	// Hierarchy indicates the hierarchy of the target for profiling
	Hierarchy ProfileHierarchy `json:"hierarchy,omitempty"`
}

// ProfileHierarchy defines the hierarchy of the target for profiling
type ProfileHierarchy struct {
	// Level indicates the level of the profile target, which can be pod or container
	Level ProfileHierarchyLevel `json:"level,omitempty"`
}

// ProfileHierarchyLevel defines the level of the profile target
type ProfileHierarchyLevel string

const (
	// ProfileHierarchyLevelPod indicates the profiling target is a pod level
	ProfileHierarchyLevelPod ProfileHierarchyLevel = "pod"
	// ProfileHierarchyLevelContainer indicates the profiling target is a container level
	ProfileHierarchyLevelContainer ProfileHierarchyLevel = "container"
)

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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AnalysisTargetType defines the type of analysis target
type AnalysisTargetType string

const (
	// AnalysisTargetWorkload defines the k8s workload type
	AnalysisTargetWorkload AnalysisTargetType = "workload"
	// AnalysisTargetPodSelector defines the pod selector type
	AnalysisTargetPodSelector AnalysisTargetType = "podSelector"
)

// AnalysisTarget defines the target of analysis, which can be a k8s workload or a series of pods
type AnalysisTarget struct {
	// Type indicates the type of target
	Type AnalysisTargetType `json:"type"`
	// Workload indicates the target is a k8s workload, which is effective when Type is "workload"
	Workload *WorkloadRef `json:"workload,omitempty"`
	// PodSelector indicates the target is a series of pods, which is effective when Type is "podSelector"
	PodSelector *PodSelectorRef `json:"podSelector,omitempty"`
}

// WorkloadRef defines the reference of a k8s workload
type WorkloadRef struct {
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
	// ProfileHierarchyContainer indicates the profiling target is a container level
	ProfileHierarchyContainer ProfileHierarchyLevel = "container"
)

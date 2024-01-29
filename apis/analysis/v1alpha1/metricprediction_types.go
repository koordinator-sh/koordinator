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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MetricPredictionSpec defines the desired state of MetricPrediction
type MetricPredictionSpec struct {
	// Target is the object to be analyzed, which can be a workload or a series of pods
	Target AnalysisTarget `json:"target"`
	// Metric defines the source of metric, including resource name, metric name and how to collect
	Metric MetricSpec `json:"metric"`
	// Profilers define multiple analysis models for metric profiling
	Profilers []Profiler `json:"profilers"`
}

// MetricPredictionStatus defines the observed state of MetricPrediction
type MetricPredictionStatus struct {
	// Results is the list of results for all profilers
	Results []ProfileResult `json:"results,omitempty"`
}

// MetricPrediction is the Schema for the MetricPrediction API
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mp
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.target.type"
// +kubebuilder:printcolumn:name="Kind",type="string",JSONPath=".spec.target.workload.kind"
// +kubebuilder:printcolumn:name="Name",type="string",JSONPath=".spec.target.workload.name"
// +kubebuilder:printcolumn:name="Metric",type="string",JSONPath=".spec.metric.source"

// MetricPrediction is the Schema for the metricpredictions API
type MetricPrediction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MetricPredictionSpec   `json:"spec,omitempty"`
	Status            MetricPredictionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MetricPredictionList contains a list of MetricPrediction
type MetricPredictionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetricPrediction `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetricPrediction{}, &MetricPredictionList{})
}

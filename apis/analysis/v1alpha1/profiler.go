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

// ProfilerModel is the model of the profiler
type ProfilerModel string

const (
	// ProfilerTypeDistribution is the distribution model, collecting metrics continuously and calculate the distribution
	ProfilerTypeDistribution ProfilerModel = "distribution"
)

// Profiler defines an analysis model for metric profiling
type Profiler struct {
	// Name is the name of the profiler
	Name string `json:"name"`
	// Model is the model of the profiler
	Model ProfilerModel `json:"model,omitempty"`
	// Distribution is the setting of distribution model
	Distribution *DistributionModel `json:"distribution,omitempty"`
}

// DistributionModel defines the tuning knobs for distribution model
type DistributionModel struct{}

// ProfileResult is the result of metric profiling
type ProfileResult struct {
	// ProfilerName is the name of the profiler
	ProfilerName string `json:"profilerName,omitempty"`
	// Model is the analysis model of the profiler
	Model ProfilerModel `json:"model,omitempty"`
	// DistributionResult is the result of distribution model, which is effective only when Model is Distribution
	DistributionResult *DistributionResult `json:"distributionResult,omitempty"`
}

// DistributionResult is the result of all profilers
type DistributionResult struct {
	// Items is a list of distribution items of all profiler hierarchy
	Items []DistributionItem `json:"items,omitempty"`
}

// DistributionItem is the distribution of a profiler hierarchy
type DistributionItem struct {
	// ID is the identifier of the profiler hierarchy
	ID HierarchyIdentifier `json:"id,omitempty"`
	// Resources is a list of distribution items of all resources defined in MetricSpec
	Resources []ResourceDistribution `json:"resources,omitempty"`
}

// HierarchyIdentifier is the identifier of a profiler hierarchy
type HierarchyIdentifier struct {
	// Level is the level of the profiler hierarchy
	Level ProfileHierarchyLevel `json:"level,omitempty"`
	// Name is the name of the profiler hierarchy, such as pod name or container name
	Name string `json:"name,omitempty"`
}

// ResourceDistribution is the distribution result of a resource type
type ResourceDistribution struct {
	// Name is the identifier defined in MetricSpec
	Name string `json:"name,omitempty"`
	// Avg is the average value of the resource
	Avg resource.Quantity `json:"avg,omitempty"`
	// Quantiles is the quantiles of the resource
	Quantiles map[string]resource.Quantity `json:"quantiles,omitempty"`
	// StdDev is the standard deviation of the resource
	StdDev resource.Quantity `json:"stdDev,omitempty"`
	// LastSampleTime is the start time of the first sample
	FirstSampleTime metav1.Time `json:"firstSampleTime,omitempty"`
	// LastSampleTime is the start time of the last sample
	LastSampleTime metav1.Time `json:"lastSampleTime,omitempty"`
	// TotalSamplesCount is the total samples count
	TotalSamplesCount int64 `json:"totalSamplesCount,omitempty"`
	// UpdateTime is the update time of the distribution
	UpdateTime *metav1.Time `json:"updateTime,omitempty"`
	// Conditions is the list of conditions representing the status of the distribution
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

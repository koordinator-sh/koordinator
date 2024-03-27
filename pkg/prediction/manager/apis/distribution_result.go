/*
 Copyright 2024 The Koordinator Authors.

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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ ProfileResult = &DistributionProfilerResult{}

type DistributionProfilerResult struct {
	ProfileKey
	// Models is a list of model details
	Models []DistributionModelDetail `json:"models,omitempty"`
}

type DistributionModelDetail struct {
	// ID is the identifier of the profiler hierarchy
	ID HierarchyIdentifier `json:"id,omitempty"`
	// Distributions is a list of distribution items of all resources defined in MetricSpec
	Distributions []MetricDistribution `json:"distributions,omitempty"`
}

// HierarchyIdentifier is the identifier of a profiler hierarchy
type HierarchyIdentifier struct {
	// Level is the level of the profiler hierarchy
	Level ProfileHierarchyLevel `json:"level,omitempty"`
	// Name is the name of the profiler hierarchy, such as container name
	Name string `json:"name,omitempty"`
}

// MetricDistribution is the distribution result of a specified metric
type MetricDistribution struct {
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

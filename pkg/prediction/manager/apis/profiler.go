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

// ProfilerModel is the model of the profiler
type ProfilerModel string

const (
	// ProfilerTypeDistribution is the distribution model, collecting metrics continuously and calculate the distribution
	ProfilerTypeDistribution ProfilerModel = "distribution"
)

// Profiler defines an analysis model for metric profiling
type Profiler struct {
	// Model is the model of the profiler
	Model ProfilerModel `json:"model,omitempty"`
	// Distribution is the setting of distribution model
	Distribution *DistributionModel `json:"distribution,omitempty"`
}

// DistributionModel defines the tuning knobs for distribution model
type DistributionModel struct {
}

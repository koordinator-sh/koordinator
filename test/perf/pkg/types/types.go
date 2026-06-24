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

// Package types defines shared data structures used across all benchmark
// packages. It imports nothing from within test/perf, acting as a clean
// leaf node in the import graph.
package types

import "time"

// ScenarioConfig holds all parameters parsed from a configs/scenarios/*.yaml file.
type ScenarioConfig struct {
	Name             string                 `yaml:"name"`
	Description      string                 `yaml:"description"`
	SchedulerName    string                 `yaml:"schedulerName"`
	Namespace        string                 `yaml:"namespace"`
	NodeCount        int                    `yaml:"nodeCount"`
	PodCount         int                    `yaml:"podCount"`
	Concurrency      int                    `yaml:"concurrency"`
	ClientQPS        float32                `yaml:"clientQPS"`
	ClientBurst      int                    `yaml:"clientBurst"`
	QoSClass         string                 `yaml:"qosClass"`
	GangSize         int                    `yaml:"gangSize"`
	MinMember        int                    `yaml:"minMember"`
	ResourceRequests map[string]string      `yaml:"resourceRequests"`
	Labels           map[string]string      `yaml:"labels"`
	Annotations      map[string]string      `yaml:"annotations"`
	Thresholds       Thresholds             `yaml:"thresholds"`
	Extra            map[string]interface{} `yaml:"extra"`
}

// Thresholds defines regression detection limits used by CI comparison.
type Thresholds struct {
	ThroughputDropPct float64 `yaml:"throughputDropPct"`
	P99IncreasePct    float64 `yaml:"p99IncreasePct"`
}

// NodeSpec describes what simulated nodes should look like.
type NodeSpec struct {
	CPU     string
	Memory  string
	MaxPods int
	Labels  map[string]string
}

// BenchmarkResult is the structured output of one complete benchmark run.
type BenchmarkResult struct {
	Name                   string   `json:"name"`
	RunID                  string   `json:"runID"`
	Timestamp              string   `json:"timestamp"`
	KoordinatorVersion     string   `json:"koordinatorVersion"`
	NodeCount              int      `json:"nodeCount"`
	PodCount               int      `json:"podCount"`
	ThroughputPodsPerSec   float64  `json:"throughputPodsPerSec"`
	APICreationDurationSec float64  `json:"apiCreationDurationSec"`
	TotalDurationSec       float64  `json:"totalDurationSec"`
	LatencyP50Sec          float64  `json:"latencyP50Sec"`
	LatencyP90Sec          float64  `json:"latencyP90Sec"`
	LatencyP99Sec          float64  `json:"latencyP99Sec"`
	GangCompletionP50Sec   *float64 `json:"gangCompletionP50Sec"`
	GangCompletionP99Sec   *float64 `json:"gangCompletionP99Sec"`
	PProfCPUArtifact       string   `json:"pprofCPUArtifact,omitempty"`
	PProfHeapArtifact      string   `json:"pprofHeapArtifact,omitempty"`
	ThresholdBreached      bool     `json:"thresholdBreached"`
}

// PodLatency records the scheduling latency for a single pod.
type PodLatency struct {
	PodName string
	GangID  string
	Latency time.Duration
}

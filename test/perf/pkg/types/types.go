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

import (
	"fmt"
	"time"
)

// RunIDLabel is the pod and node label key used to associate all objects
// created by a single benchmark run. Defined here so every package uses
// the same string and a typo in one place cannot cause a silent mismatch.
const RunIDLabel = "benchmark.koordinator.sh/run-id"

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

	// NodeTemplateFile is an optional path to a YAML file containing a
	// complete corev1.Node object. When set, nodes are built from this
	// file at runtime so the node shape can change without rebuilding
	// the binary. Benchmark-required kwok labels, annotation, and taint
	// are always overlaid on top of whatever the file specifies.
	NodeTemplateFile string `yaml:"nodeTemplateFile"`

	// NodeCreationWorkers controls how many nodes are provisioned in
	// parallel. Defaults to 20 when unset or zero.
	NodeCreationWorkers int `yaml:"nodeCreationWorkers"`

	// Timeout bounds the total wall-clock duration of one benchmark run,
	// expressed as a Go duration string (e.g. "10m", "90s"). A run that
	// exceeds this is aborted and reported with TimedOut: true rather than
	// hanging indefinitely on a stuck scheduler. Defaults to DefaultRunTimeout
	// when unset.
	Timeout string `yaml:"timeout"`
}

// DefaultRunTimeout is used when ScenarioConfig.Timeout is unset.
const DefaultRunTimeout = 10 * time.Minute

// TimeoutDuration parses Timeout, falling back to DefaultRunTimeout when
// unset. Validate() already rejects an unparseable non-empty value, so this
// is safe to call unconditionally after Validate() has passed.
func (c ScenarioConfig) TimeoutDuration() time.Duration {
	if c.Timeout == "" {
		return DefaultRunTimeout
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return DefaultRunTimeout
	}
	return d
}

// Validate returns an error if any ScenarioConfig field contains an invalid value.
// Call this immediately after unmarshalling to catch bad YAML before the run starts.
func (c ScenarioConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if c.NodeCount <= 0 {
		return fmt.Errorf("nodeCount must be > 0, got %d", c.NodeCount)
	}
	if c.PodCount <= 0 {
		return fmt.Errorf("podCount must be > 0, got %d", c.PodCount)
	}
	if c.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be > 0, got %d", c.Concurrency)
	}
	if c.ClientQPS <= 0 {
		return fmt.Errorf("clientQPS must be > 0, got %g", c.ClientQPS)
	}
	if c.ClientBurst <= 0 {
		return fmt.Errorf("clientBurst must be > 0, got %d", c.ClientBurst)
	}
	if c.NodeCreationWorkers < 0 {
		return fmt.Errorf("nodeCreationWorkers must be >= 0, got %d", c.NodeCreationWorkers)
	}
	if c.Thresholds.ThroughputDropPct < 0 || c.Thresholds.ThroughputDropPct > 100 {
		return fmt.Errorf("thresholds.throughputDropPct must be in [0, 100], got %g", c.Thresholds.ThroughputDropPct)
	}
	if c.Thresholds.P99IncreasePct < 0 {
		return fmt.Errorf("thresholds.p99IncreasePct must be >= 0, got %g", c.Thresholds.P99IncreasePct)
	}
	if c.Timeout != "" {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return fmt.Errorf("timeout %q is not a valid duration: %w", c.Timeout, err)
		}
		if d <= 0 {
			return fmt.Errorf("timeout must be > 0, got %q", c.Timeout)
		}
	}
	return nil
}

// Thresholds defines regression detection limits used by CI comparison.
type Thresholds struct {
	ThroughputDropPct float64 `yaml:"throughputDropPct"`
	P99IncreasePct    float64 `yaml:"p99IncreasePct"`
}

// NodeSpec describes what simulated nodes should look like.
// When NodeTemplateFile is set, the node object is loaded from that YAML file
// at runtime so the template can be changed without rebuilding the binary.
type NodeSpec struct {
	CPU                 string            `yaml:"cpu"`
	Memory              string            `yaml:"memory"`
	MaxPods             int               `yaml:"maxPods"`
	Labels              map[string]string `yaml:"labels"`
	NodeTemplateFile    string            `yaml:"nodeTemplateFile"`
	NodeCreationWorkers int               `yaml:"nodeCreationWorkers"`
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

	// TimedOut is true when the run was aborted because it exceeded
	// ScenarioConfig.Timeout. Throughput/latency fields reflect whatever
	// was measured before the deadline and should be treated as partial.
	TimedOut bool `json:"timedOut"`

	// SchedulingFailureCount is the total number of FailedScheduling events
	// observed across all pods in the run (a single pod may retry multiple times).
	SchedulingFailureCount int `json:"schedulingFailureCount"`

	// SchedulingFailureRate is the fraction of pods (0.0–1.0) that received
	// at least one FailedScheduling event during the run.
	SchedulingFailureRate float64 `json:"schedulingFailureRate"`
}

// PodLatency records the scheduling latency for a single pod.
type PodLatency struct {
	PodName string
	GangID  string
	Latency time.Duration
}

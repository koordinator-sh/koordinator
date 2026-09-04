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

	"k8s.io/apimachinery/pkg/api/resource"
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
	QuotaCPU         string                 `yaml:"quotaCPU"`
	QuotaMemory      string                 `yaml:"quotaMemory"`
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

	// ExpectedScheduledPodCount, when set, tells Watcher how many of the PodCount
	// pods are actually expected to reach PodScheduled=True. The remaining pods are
	// expected to stay Pending for the run's duration (e.g. elasticquota's
	// deliberately-throttled pods). nil means "all PodCount pods are expected to
	// schedule" — the existing basic/gang behaviour, unchanged.
	ExpectedScheduledPodCount *int `yaml:"expectedScheduledPodCount"`

	// ReservationCount is the number of Reservation objects to pre-create in the
	// reservation scenario. Defaults to 0 (no Reservations created). When set,
	// the first ReservationCount pods returned by Pods() are labeled to bind to
	// a pre-created Reservation; the remaining PodCount-ReservationCount pods
	// schedule normally against raw node capacity.
	ReservationCount int `yaml:"reservationCount"`

	// HighUtilNodeCount is the number of nodes to seed with a high CPU
	// utilization NodeMetric in the loadaware scenario. Defaults to 0 (every
	// node is left at low/no seeded utilization).
	HighUtilNodeCount int `yaml:"highUtilNodeCount"`

	// HighUtilCPUPct is the CPU utilization percentage (0-100) written into the
	// high-utilization nodes' NodeMetric.status.nodeMetric.nodeUsage.cpu.
	// Defaults to 80 when HighUtilNodeCount > 0 and this is left unset (0).
	HighUtilCPUPct int `yaml:"highUtilCPUPct"`

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
	if c.ExpectedScheduledPodCount != nil {
		if *c.ExpectedScheduledPodCount <= 0 || *c.ExpectedScheduledPodCount > c.PodCount {
			return fmt.Errorf("expectedScheduledPodCount must be in (0, podCount], got %d (podCount=%d)",
				*c.ExpectedScheduledPodCount, c.PodCount)
		}
		// Cross-field consistency: expectedScheduledPodCount must match the number of
		// pods that actually fit inside the configured quota (min over all resource
		// dimensions). This catches silent drift when quotaCPU/quotaMemory or
		// resourceRequests are edited without updating expectedScheduledPodCount.
		// If any quantity is unparseable, the check is skipped — scenario Setup will
		// surface the parse error before the cluster is ever touched.
		if c.QuotaCPU != "" && c.QuotaMemory != "" {
			if implied, err := impliedScheduledCount(c.QuotaCPU, c.QuotaMemory, c.ResourceRequests); err == nil {
				if implied != *c.ExpectedScheduledPodCount {
					return fmt.Errorf("expectedScheduledPodCount (%d) does not match the value implied by "+
						"quotaCPU/quotaMemory/resourceRequests (%d) — the quota now admits a different "+
						"number of pods than configured; update expectedScheduledPodCount or the "+
						"quota/request sizing", *c.ExpectedScheduledPodCount, implied)
				}
			}
		}
	}
	if c.Thresholds.FailureRatePct != nil {
		if *c.Thresholds.FailureRatePct < 0 || *c.Thresholds.FailureRatePct > 100 {
			return fmt.Errorf("thresholds.failureRatePct must be in [0, 100], got %g", *c.Thresholds.FailureRatePct)
		}
	}
	if c.ReservationCount < 0 || c.ReservationCount > c.PodCount {
		return fmt.Errorf("reservationCount must be in [0, podCount], got %d (podCount=%d)",
			c.ReservationCount, c.PodCount)
	}
	if c.HighUtilNodeCount < 0 || c.HighUtilNodeCount > c.NodeCount {
		return fmt.Errorf("highUtilNodeCount must be in [0, nodeCount], got %d (nodeCount=%d)",
			c.HighUtilNodeCount, c.NodeCount)
	}
	if c.HighUtilCPUPct < 0 || c.HighUtilCPUPct > 100 {
		return fmt.Errorf("highUtilCPUPct must be in [0, 100], got %d", c.HighUtilCPUPct)
	}
	return nil
}

// impliedScheduledCount returns how many pods of the given resourceRequests
// fit inside a quota of quotaCPU/quotaMemory, taking the minimum across
// whichever of cpu/memory are present in resourceRequests. Returns an error
// when no comparable dimension is found or a quantity is unparseable.
func impliedScheduledCount(quotaCPU, quotaMemory string, requests map[string]string) (int, error) {
	dims := []struct{ quota, request string }{
		{quotaCPU, requests["cpu"]},
		{quotaMemory, requests["memory"]},
	}
	bound := -1
	for _, d := range dims {
		if d.quota == "" || d.request == "" {
			continue
		}
		q, err := resource.ParseQuantity(d.quota)
		if err != nil {
			return 0, err
		}
		r, err := resource.ParseQuantity(d.request)
		if err != nil {
			return 0, err
		}
		if r.MilliValue() == 0 {
			continue
		}
		n := int(q.MilliValue() / r.MilliValue())
		if bound == -1 || n < bound {
			bound = n
		}
	}
	if bound == -1 {
		return 0, fmt.Errorf("no comparable cpu/memory dimensions in quota and resourceRequests")
	}
	return bound, nil
}

// Thresholds defines regression detection limits used by CI comparison.
type Thresholds struct {
	ThroughputDropPct float64 `yaml:"throughputDropPct"`
	P99IncreasePct    float64 `yaml:"p99IncreasePct"`

	// FailureRatePct overrides CompareToBaseline's default 1% scheduling-failure-rate
	// gate, expressed as a percentage (e.g. 75 for 75%). nil means "use the default 1%"
	// — unchanged for basic/gang. Scenarios that deliberately produce a high, expected
	// failure rate (elasticquota) set this so the gate reflects intent rather than
	// treating expected throttling as a regression.
	FailureRatePct *float64 `yaml:"failureRatePct"`
}

// ShortID returns the first 8 characters of id (typically a UUID run-id) for
// use in resource names. Returns the full string when len(id) <= 8.
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// FailureStats carries the plain per-run failure-watcher numbers a Scenario may
// need to compute its own BenchmarkResult fields. Defined in pkg/types (not
// pkg/framework) so a Scenario implementation can consume it without importing
// pkg/framework — pkg/framework already imports pkg/scenarios, so the reverse
// import would be a cycle.
type FailureStats struct {
	FailedPodCount       int
	FailureEventCount    int
	QuotaBlockedPodCount int
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
	QuotaBlockedPodCount   *int     `json:"quotaBlockedPodCount"`
	// ReservationBindCount is the number of pods that successfully consumed a
	// Reservation during the run. nil for scenarios with no Reservations configured.
	ReservationBindCount *int `json:"reservationBindCount"`
	// LoadAwareRoutedPodCount is the number of pods scheduled onto
	// low-utilization nodes in the loadaware scenario. nil for scenarios that
	// don't seed NodeMetric utilization.
	LoadAwareRoutedPodCount *int    `json:"loadAwareRoutedPodCount"`
	PProfCPUArtifact        string  `json:"pprofCPUArtifact,omitempty"`
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

	// CreateFailureCount is the number of pods whose API Create failed after
	// all retries and were excluded from the run rather than aborting it entirely.
	// Usually 0; non-zero signals transient apiserver or network trouble.
	CreateFailureCount int `json:"createFailureCount"`
}

// PodLatency records the scheduling latency for a single pod, plus the two
// absolute server-side timestamps it was derived from (CreatedAt, ScheduledAt).
// The timestamps let ComputeThroughputWindow measure the actual scheduling
// window of the recorded pods, independent of client-side send pacing.
type PodLatency struct {
	PodName     string
	GangID      string
	Latency     time.Duration
	CreatedAt   time.Time
	ScheduledAt time.Time
}

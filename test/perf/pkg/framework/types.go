package framework

import (
	"context"
	"time"
)

// Thresholds defines regression detection limits for CI comparison.
type Thresholds struct {
	ThroughputDropPct float64 `yaml:"throughputDropPct"`
	P99IncreasePct    float64 `yaml:"p99IncreasePct"`
}

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

// NodeSpec describes the capacity of simulated kwok nodes.
type NodeSpec struct {
	CPU     string
	Memory  string
	MaxPods int
	Labels  map[string]string
}

// PodLatency holds the scheduling latency for a single pod.
type PodLatency struct {
	PodName string
	GangID  string
	Latency time.Duration
}

// NodeProvider creates and destroys simulated nodes.
type NodeProvider interface {
	CreateNodes(ctx context.Context, runID string, spec NodeSpec, count int) error
	DeleteNodes(ctx context.Context, runID string) error
	WaitReady(ctx context.Context, runID string, timeout time.Duration) error
}

// BenchmarkResult is the structured output of one benchmark run.
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

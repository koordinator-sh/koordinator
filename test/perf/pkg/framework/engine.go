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

package framework

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/nodeprovider"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// defaultNodeWaitTimeout bounds how long Run waits for simulated nodes to
// report Ready before giving up.
const defaultNodeWaitTimeout = 60 * time.Second

// defaultNamespace matches the fallback used in pkg/scenarios/basic when
// cfg.Namespace is unset, so the watcher and the scenario always agree on
// which namespace to observe.
const defaultNamespace = "benchmark"

// Engine orchestrates a full benchmark run.
// Import graph: framework → nodeprovider → types
//
//	→ scenarios   → types
//	→ types
//
// No cycles: nodeprovider and scenarios no longer import framework.
type Engine struct {
	client    kubernetes.Interface
	dynClient dynamic.Interface
	provider  nodeprovider.NodeProvider
}

// NewEngine creates an Engine connected to the cluster at kubeconfig.
// qps and burst come from ScenarioConfig.ClientQPS / ClientBurst so the
// scenario YAML controls client-side rate limits rather than hard-coding them.
// Pass "" for kubeconfig to use ~/.kube/config.
func NewEngine(kubeconfig string, qps float32, burst int, provider nodeprovider.NodeProvider) (*Engine, error) {
	var cfg *rest.Config
	var err error

	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("",
			filepath.Join(os.Getenv("HOME"), ".kube", "config"))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}
	cfg.QPS = qps
	cfg.Burst = burst

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &Engine{client: client, dynClient: dynClient, provider: provider}, nil
}

// Run executes one full benchmark scenario end-to-end and writes the result.
//
// Sequence:
//  1. provider.CreateNodes
//  2. provider.WaitReady
//  3. scenario.Setup
//  4. watcher.Start in a goroutine
//  5. record burstStart
//  6. bounded worker pool fires cfg.PodCount pods from scenario.Pods
//  7. g.Wait() -> apiCreationDuration
//  8. wait for watcher -> totalDuration
//  9. ComputeLatencyPercentiles
//  10. ComputeThroughput
//  11. scenario.Teardown (always, via defer)
//  12. provider.DeleteNodes (always, via defer)
//  13. WriteReport
//
// Teardown and DeleteNodes use context.Background() so cleanup still reaches
// the API server even if the run's ctx has been cancelled or timed out.
func (e *Engine) Run(ctx context.Context, cfg types.ScenarioConfig, outputPath string) error {
	scenario, ok := scenarios.Get(cfg.Name)
	if !ok {
		return fmt.Errorf("scenario %q not registered; available: %v",
			cfg.Name, scenarios.List())
	}

	runID := uuid.New().String()
	klog.InfoS("Starting benchmark", "scenario", cfg.Name, "runID", runID)

	if _, err := e.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		return fmt.Errorf("cannot reach API server: %w", err)
	}
	klog.InfoS("API server reachable", "scenario", scenario.Name())

	namespace := cfg.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	// Step 1: provision simulated nodes.
	nodeSpec := types.NodeSpec{
		NodeTemplateFile:    cfg.NodeTemplateFile,
		NodeCreationWorkers: cfg.NodeCreationWorkers,
	}
	klog.InfoS("Creating kwok nodes", "count", cfg.NodeCount, "workers", effectiveWorkers(cfg.NodeCreationWorkers))
	if err := e.provider.CreateNodes(ctx, runID, nodeSpec, cfg.NodeCount); err != nil {
		return fmt.Errorf("CreateNodes failed: %w", err)
	}
	// Step 12 (deferred): always attempt cleanup, even on a later failure.
	defer func() {
		if err := e.provider.DeleteNodes(context.Background(), runID); err != nil {
			klog.ErrorS(err, "DeleteNodes failed", "runID", runID)
		}
	}()

	// Step 2: block until nodes are Ready or the timeout fires.
	if err := e.provider.WaitReady(ctx, runID, defaultNodeWaitTimeout); err != nil {
		return fmt.Errorf("WaitReady failed: %w", err)
	}
	klog.InfoS("Nodes ready")

	// Step 3: scenario-specific prerequisites (e.g. namespace creation).
	if err := scenario.Setup(ctx, e.client, e.dynClient, cfg, runID); err != nil {
		return fmt.Errorf("scenario Setup failed: %w", err)
	}
	// Step 11 (deferred): always tear down scenario objects.
	defer func() {
		if err := scenario.Teardown(context.Background(), e.client, e.dynClient, runID); err != nil {
			klog.ErrorS(err, "scenario Teardown failed", "runID", runID)
		}
	}()

	pods, err := scenario.Pods(cfg, runID)
	if err != nil {
		return fmt.Errorf("scenario Pods failed: %w", err)
	}

	// Step 4: start the PodScheduled watcher before the burst so no event
	// is missed. Errors surface through watcherErrCh.
	watcher := NewWatcher(e.client, namespace, runID, cfg.PodCount)
	watcherErrCh := make(chan error, 1)
	go func() {
		watcherErrCh <- watcher.Start(ctx)
	}()

	klog.InfoS("Starting pod burst", "pods", cfg.PodCount, "concurrency", cfg.Concurrency)

	// Step 5: mark the start of the API creation phase.
	burstStart := time.Now()

	// Step 6: bounded worker pool creates all pods.
	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, cfg.Concurrency)
	for _, pod := range pods {
		pod := pod
		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()
			_, err := e.client.CoreV1().Pods(pod.Namespace).Create(gctx, pod, metav1.CreateOptions{})
			return err
		})
	}

	// Step 7: wait for all creates and record API creation time.
	if err := g.Wait(); err != nil {
		return fmt.Errorf("pod creation failed: %w", err)
	}
	apiCreationDuration := time.Since(burstStart)
	klog.InfoS("API creation phase complete", "duration", apiCreationDuration.Round(10*time.Millisecond))

	// Step 8: wait for watcher to observe every pod scheduled.
	klog.InfoS("Waiting for all pods to be scheduled")
	if err := <-watcherErrCh; err != nil {
		return fmt.Errorf("watcher failed: %w", err)
	}
	totalDuration := time.Since(burstStart)

	// Steps 9-10: compute percentiles and throughput from recorded latencies.
	p50, p90, p99 := ComputeLatencyPercentiles(watcher.Latencies())
	throughput := ComputeThroughput(cfg.PodCount, totalDuration)

	result := types.BenchmarkResult{
		Name:                   cfg.Name,
		RunID:                  runID,
		Timestamp:              time.Now().UTC().Format(time.RFC3339),
		NodeCount:              cfg.NodeCount,
		PodCount:               cfg.PodCount,
		ThroughputPodsPerSec:   throughput,
		APICreationDurationSec: apiCreationDuration.Seconds(),
		TotalDurationSec:       totalDuration.Seconds(),
		LatencyP50Sec:          p50.Seconds(),
		LatencyP90Sec:          p90.Seconds(),
		LatencyP99Sec:          p99.Seconds(),
		ThresholdBreached:      breachesThresholds(cfg.Thresholds, throughput, p99),
	}

	// Step 13: write JSON report + stdout summary.
	// Steps 11-12 run via defer after this returns.
	return WriteReport(result, outputPath)
}

// effectiveWorkers mirrors the default in pkg/nodeprovider/kwok for accurate
// log output before CreateNodes is called.
func effectiveWorkers(configured int) int {
	if configured <= 0 {
		return 20
	}
	return configured
}

// breachesThresholds is a placeholder for CI baseline comparison.
// Until baselines are wired in there is nothing to compare against,
// so this always returns false.
func breachesThresholds(_ types.Thresholds, _ float64, _ time.Duration) bool {
	return false
}

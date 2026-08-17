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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// Version is injected at build time via -ldflags by the Makefile.
// Falls back to "dev" when building outside of make (e.g. go run directly).
var Version = "dev"

// defaultNodeWaitTimeout bounds how long Run waits for simulated nodes to
// report Ready before giving up.
const defaultNodeWaitTimeout = 60 * time.Second

const (
	// createMaxRetries is the per-pod Create attempt limit. AlreadyExists is
	// treated as success (a dropped connection after the object was committed).
	createMaxRetries     = 3
	createRetryBaseDelay = 100 * time.Millisecond

	// settleQuietPeriod is how long waitForSettle waits without seeing a new
	// FailedScheduling event before declaring the run settled. Chosen to exceed
	// one scheduler requeue/backoff cycle without materially lengthening runs.
	settleQuietPeriod  = 5 * time.Second
	settlePollInterval = 250 * time.Millisecond
)

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

// Client returns the engine's k8s client so callers can share it with the
// node provider instead of building a second client from the same kubeconfig.
func (e *Engine) Client() kubernetes.Interface {
	return e.client
}

// SetProvider replaces the node provider. Call this after NewEngine when the
// provider needs the engine's client (avoids constructing a second client).
func (e *Engine) SetProvider(p nodeprovider.NodeProvider) {
	e.provider = p
}

// Run executes one full benchmark scenario end-to-end and writes the result.
//
// Sequence:
//  1. provider.CreateNodes
//  2. provider.WaitReady
//  3. scenario.Setup
//  4. watcher.Start + failureWatcher.Start in goroutines
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
// The whole run is bounded by cfg.TimeoutDuration(). On timeout a partial
// report marked TimedOut: true is written before returning an error.
func (e *Engine) Run(ctx context.Context, cfg types.ScenarioConfig, outputPath, baselinePath string) error {
	scenario, ok := scenarios.Get(cfg.Name)
	if !ok {
		return fmt.Errorf("scenario %q not registered; available: %v",
			cfg.Name, scenarios.List())
	}

	runID := uuid.New().String()
	timeout := cfg.TimeoutDuration()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	klog.InfoS("Starting benchmark", "scenario", cfg.Name, "runID", runID, "timeout", timeout)

	if _, err := e.client.CoreV1().Nodes().List(runCtx, metav1.ListOptions{Limit: 1}); err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, fmt.Errorf("cannot reach API server: %w", err), scenario, nil, nil, 0, 0)
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
	klog.InfoS("Creating nodes", "count", cfg.NodeCount, "workers", effectiveWorkers(cfg.NodeCreationWorkers))
	if err := e.provider.CreateNodes(runCtx, runID, nodeSpec, cfg.NodeCount); err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, fmt.Errorf("CreateNodes failed: %w", err), scenario, nil, nil, 0, 0)
	}
	defer func() {
		if err := e.provider.DeleteNodes(context.Background(), runID); err != nil {
			klog.ErrorS(err, "DeleteNodes failed", "runID", runID)
		}
	}()

	// Step 2: block until nodes are Ready or the timeout fires.
	if err := e.provider.WaitReady(runCtx, runID, defaultNodeWaitTimeout); err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, fmt.Errorf("WaitReady failed: %w", err), scenario, nil, nil, 0, 0)
	}
	klog.InfoS("Nodes ready")

	// Step 3: scenario-specific prerequisites (e.g. namespace creation).
	if err := scenario.Setup(runCtx, e.client, e.dynClient, cfg, runID); err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, fmt.Errorf("scenario Setup failed: %w", err), scenario, nil, nil, 0, 0)
	}
	defer func() {
		if err := scenario.Teardown(context.Background(), e.client, e.dynClient, runID); err != nil {
			klog.ErrorS(err, "scenario Teardown failed", "runID", runID)
		}
	}()

	pods, err := scenario.Pods(cfg, runID)
	if err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, fmt.Errorf("scenario Pods failed: %w", err), scenario, nil, nil, 0, 0)
	}
	if len(pods) != cfg.PodCount {
		return fmt.Errorf("scenario %q returned %d pods, but config podCount is %d", scenario.Name(), len(pods), cfg.PodCount)
	}
	podNames := make([]string, len(pods))
	for i, pod := range pods {
		podNames[i] = pod.Name
	}

	// Step 4: start both watchers before the burst so no event is missed.
	expectedScheduled := cfg.PodCount
	if cfg.ExpectedScheduledPodCount != nil {
		expectedScheduled = *cfg.ExpectedScheduledPodCount
	}
	watcher := NewWatcher(e.client, namespace, runID, cfg.PodCount, expectedScheduled)
	watcherErrCh := make(chan error, 1)
	go func() { watcherErrCh <- watcher.Start(runCtx) }()

	// FailureWatcher has no natural end — cancel it explicitly once the
	// main watcher finishes rather than relying on the run timeout.
	failureCtx, failureCancel := context.WithCancel(runCtx)
	defer failureCancel()
	failureWatcher := NewFailureWatcher(e.client, namespace, podNames)
	failureWatcherErrCh := make(chan error, 1)
	go func() { failureWatcherErrCh <- failureWatcher.Start(failureCtx) }()

	// Block until both streams are established before starting the burst.
	select {
	case <-watcher.Ready():
	case <-runCtx.Done():
		return e.timeoutAwareReport(cfg, runID, outputPath, runCtx.Err(), scenario, failureWatcher, nil, 0, 0)
	}
	select {
	case <-failureWatcher.Ready():
	case <-runCtx.Done():
		return e.timeoutAwareReport(cfg, runID, outputPath, runCtx.Err(), scenario, failureWatcher, nil, 0, 0)
	}

	klog.InfoS("Starting pod burst", "pods", cfg.PodCount, "concurrency", cfg.Concurrency)

	// Step 5: mark the start of the API creation phase.
	burstStart := time.Now()

	// Step 6: bounded worker pool creates all pods. Individual create failures
	// are retried (createPodWithRetry) and counted into createFailureCount
	// rather than propagated as group errors — one transient 500 must not
	// abort the entire run and discard all collected latency samples. Only a
	// real ctx cancellation (timeout) stops the burst.
	var createFailureCount int32
	g, gctx := errgroup.WithContext(runCtx)
	sem := make(chan struct{}, cfg.Concurrency)
	for _, pod := range pods {
		pod := pod
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			if err := createPodWithRetry(gctx, e.client, pod); err != nil {
				atomic.AddInt32(&createFailureCount, 1)
				klog.ErrorS(err, "pod create failed after retries — continuing burst", "pod", pod.Name)
			}
			return nil
		})
	}

	// Step 7: wait for all creates and record API creation time.
	if err := g.Wait(); err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, fmt.Errorf("pod creation failed: %w", err), scenario, failureWatcher, watcher, time.Since(burstStart), 0)
	}
	apiCreationDuration := time.Since(burstStart)
	if n := atomic.LoadInt32(&createFailureCount); n > 0 {
		klog.ErrorS(nil, "some pods failed to create after retries", "count", n, "podCount", cfg.PodCount)
	}
	klog.InfoS("API creation phase complete", "duration", apiCreationDuration.Round(10*time.Millisecond))

	// Step 8: wait for the expected count to be reached …
	klog.InfoS("Waiting for expected pod count to be scheduled", "expected", expectedScheduled)
	if err := <-watcherErrCh; err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, fmt.Errorf("watcher failed: %w", err), scenario, failureWatcher, watcher, apiCreationDuration, time.Since(burstStart))
	}

	// … then keep the failure watcher running until every pod is accounted for
	// (scheduled + failed >= podCount) or nothing new has happened for
	// settleQuietPeriod. Without this, pods 301–1000 in an elasticquota run are
	// never attempted before the run ends, leaving quotaBlockedPodCount ~0.
	// For basic/gang (expectedScheduled == podCount) the first condition is
	// already satisfied, so waitForSettle returns immediately.
	if err := waitForSettle(runCtx, watcher, failureWatcher, cfg.PodCount); err != nil {
		return e.timeoutAwareReport(cfg, runID, outputPath, err, scenario, failureWatcher, watcher, apiCreationDuration, time.Since(burstStart))
	}
	totalDuration := time.Since(burstStart)

	// Stop the failure watcher and collect its results.
	failureCancel()
	if err := <-failureWatcherErrCh; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		klog.ErrorS(err, "failure watcher failed", "runID", runID)
	}
	failedPodCount, failureEventCount := failureWatcher.Stats()

	// Steps 9-10: compute percentiles and throughput. ComputeThroughputWindow
	// uses the server-side timestamps already in each PodLatency entry so the
	// denominator spans the same population as the numerator — correct when
	// expectedScheduled < podCount (elasticquota). For basic/gang the window
	// coincides with totalDuration, so numbers are unchanged.
	p50, p90, p99 := ComputeLatencyPercentiles(watcher.Latencies())
	throughput := ComputeThroughputWindow(watcher.PodLatencies())

	// Gang completion percentiles are only populated when pods carry
	// types.PodGroupLabel (gang scenario); nil for basic and others.
	var gangP50Sec, gangP99Sec *float64
	if gp50, gp99, ok := ComputeGangCompletionPercentiles(watcher.PodLatencies()); ok {
		v50, v99 := gp50.Seconds(), gp99.Seconds()
		gangP50Sec, gangP99Sec = &v50, &v99
	}

	failureRate := schedulingFailureRate(failedPodCount, cfg.PodCount)
	breached, err := CompareToBaseline(types.BenchmarkResult{
		NodeCount:              cfg.NodeCount,
		PodCount:               cfg.PodCount,
		ThroughputPodsPerSec:   throughput,
		LatencyP99Sec:          p99.Seconds(),
		SchedulingFailureCount: failedPodCount,
		SchedulingFailureRate:  failureRate,
	}, baselinePath, cfg.Thresholds)
	if err != nil {
		klog.ErrorS(err, "baseline comparison failed — ThresholdBreached will be false")
	}

	result := types.BenchmarkResult{
		Name:                   cfg.Name,
		RunID:                  runID,
		Timestamp:              time.Now().UTC().Format(time.RFC3339),
		KoordinatorVersion:     Version,
		NodeCount:              cfg.NodeCount,
		PodCount:               cfg.PodCount,
		ThroughputPodsPerSec:   throughput,
		APICreationDurationSec: apiCreationDuration.Seconds(),
		TotalDurationSec:       totalDuration.Seconds(),
		LatencyP50Sec:          p50.Seconds(),
		LatencyP90Sec:          p90.Seconds(),
		LatencyP99Sec:          p99.Seconds(),
		GangCompletionP50Sec:   gangP50Sec,
		GangCompletionP99Sec:   gangP99Sec,
		ThresholdBreached:      breached,
		SchedulingFailureCount: failureEventCount,
		SchedulingFailureRate:  failureRate,
		CreateFailureCount:     int(atomic.LoadInt32(&createFailureCount)),
	}

	// Let the scenario contribute its own result fields without the engine
	// needing to know which config fields drove them. See scenarios.ResultAugmenter.
	if augmenter, ok := scenario.(scenarios.ResultAugmenter); ok {
		augmenter.Augment(types.FailureStats{
			FailedPodCount:       failedPodCount,
			FailureEventCount:    failureEventCount,
			QuotaBlockedPodCount: failureWatcher.QuotaBlockedPodCount(),
		}, &result)
	}

	// Step 13: write JSON report. Steps 11-12 run via defer after this returns.
	return WriteReport(result, outputPath)
}

// timeoutAwareReport handles errors that abort Run early. On DeadlineExceeded
// it writes a partial report with TimedOut: true so partial numbers are not
// lost. For any other error it returns immediately without writing a report.
// scenario and failureWatcher may be nil for early failures before those are
// constructed; when non-nil, failure/quota stats are populated in the report.
func (e *Engine) timeoutAwareReport(
	cfg types.ScenarioConfig,
	runID, outputPath string,
	runErr error,
	scenario scenarios.Scenario,
	failureWatcher *FailureWatcher,
	watcher *Watcher,
	apiCreationDuration, totalDuration time.Duration,
) error {
	timedOut := errors.Is(runErr, context.DeadlineExceeded)
	klog.ErrorS(runErr, "Benchmark run aborted; writing partial report", "runID", runID, "timedOut", timedOut)

	result := types.BenchmarkResult{
		Name:                   cfg.Name,
		RunID:                  runID,
		Timestamp:              time.Now().UTC().Format(time.RFC3339),
		KoordinatorVersion:     Version,
		NodeCount:              cfg.NodeCount,
		PodCount:               cfg.PodCount,
		APICreationDurationSec: apiCreationDuration.Seconds(),
		TotalDurationSec:       totalDuration.Seconds(),
		TimedOut:               timedOut,
	}
	if watcher != nil {
		p50, p90, p99 := ComputeLatencyPercentiles(watcher.Latencies())
		result.LatencyP50Sec = p50.Seconds()
		result.LatencyP90Sec = p90.Seconds()
		result.LatencyP99Sec = p99.Seconds()
		result.ThroughputPodsPerSec = ComputeThroughputWindow(watcher.PodLatencies())
	}
	// Populate failure and scenario-specific stats on the aborted path too —
	// without this, quotaBlockedPodCount is null in every non-success result.
	if failureWatcher != nil {
		failedPodCount, failureEventCount := failureWatcher.Stats()
		result.SchedulingFailureCount = failureEventCount
		result.SchedulingFailureRate = schedulingFailureRate(failedPodCount, cfg.PodCount)
		if scenario != nil {
			if augmenter, ok := scenario.(scenarios.ResultAugmenter); ok {
				augmenter.Augment(types.FailureStats{
					FailedPodCount:       failedPodCount,
					FailureEventCount:    failureEventCount,
					QuotaBlockedPodCount: failureWatcher.QuotaBlockedPodCount(),
				}, &result)
			}
		}
	}
	if writeErr := WriteReport(result, outputPath); writeErr != nil {
		klog.ErrorS(writeErr, "failed to write partial report", "runID", runID)
	}
	return fmt.Errorf("benchmark run aborted: %w", runErr)
}

// createPodWithRetry retries transient Create failures with backoff and treats
// AlreadyExists as success: a dropped connection after the object was committed
// server-side means an earlier attempt succeeded, not that the pod is missing.
func createPodWithRetry(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod) error {
	var lastErr error
	for attempt := 0; attempt < createMaxRetries; attempt++ {
		if attempt > 0 {
			delay := createRetryBaseDelay << uint(attempt-1)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		_, err := client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err == nil || apierrors.IsAlreadyExists(err) {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("create failed after %d attempts: %w", createMaxRetries, lastErr)
}

// waitForSettle blocks until every pod is accounted for
// (scheduled + failedPodCount >= podCount), or no new FailedScheduling event
// has been observed for settleQuietPeriod, or ctx is done.
//
// For scenarios where ExpectedScheduledPodCount == PodCount (basic/gang),
// the first condition is already true when this is called — Watcher.Start
// already waited for all podCount pods — so this returns immediately and
// existing behaviour is unchanged.
func waitForSettle(ctx context.Context, watcher *Watcher, failureWatcher *FailureWatcher, podCount int) error {
	ticker := time.NewTicker(settlePollInterval)
	defer ticker.Stop()
	for {
		scheduled := len(watcher.Latencies())
		failedPodCount, _ := failureWatcher.Stats()
		if scheduled+failedPodCount >= podCount {
			return nil
		}
		if time.Since(failureWatcher.LastEventTime()) >= settleQuietPeriod {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// schedulingFailureRate returns the fraction (0.0–1.0) of pods that received
// at least one FailedScheduling event.
func schedulingFailureRate(failedPodCount, podCount int) float64 {
	if podCount <= 0 {
		return 0
	}
	return float64(failedPodCount) / float64(podCount)
}

// effectiveWorkers mirrors the default in pkg/nodeprovider/kwok for accurate
// log output before CreateNodes is called.
func effectiveWorkers(configured int) int {
	if configured <= 0 {
		return 20
	}
	return configured
}

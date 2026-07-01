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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/nodeprovider"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// Engine orchestrates a full benchmark run.
// Import graph: framework → nodeprovider → types
//                         → scenarios   → types
//                         → types
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

// Run executes one benchmark scenario and writes the result.
// Week 1 stub: verifies connectivity and scenario registration.
// Week 2: implements the full worker-pool + watcher + metrics loop.
func (e *Engine) Run(ctx context.Context, cfg types.ScenarioConfig, outputPath string) error {
	scenario, ok := scenarios.Get(cfg.Name)
	if !ok {
		return fmt.Errorf("scenario %q not registered; available: %v",
			cfg.Name, scenarios.List())
	}

	runID := uuid.New().String()
	fmt.Printf("Starting benchmark: scenario=%s runID=%s\n", cfg.Name, runID)

	if _, err := e.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		return fmt.Errorf("cannot reach API server: %w", err)
	}

	fmt.Printf("API server reachable. Scenario %q is registered.\n", scenario.Name())

	result := types.BenchmarkResult{
		Name:      cfg.Name,
		RunID:     runID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		NodeCount: cfg.NodeCount,
		PodCount:  cfg.PodCount,
	}
	return WriteReport(result, outputPath)
}

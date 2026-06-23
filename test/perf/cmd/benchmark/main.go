package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/framework"
	kwokprovider "github.com/koordinator-sh/koordinator/test/perf/pkg/nodeprovider/kwok"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"

	// Blank imports trigger each scenario's init() registration.
	_ "github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios/basic"
)

func main() {
	var (
		configPath = flag.String("config", "", "Path to scenario YAML config (required)")
		outputPath = flag.String("output", "results/result.json", "Path to write JSON result")
		kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig (default: ~/.kube/config)")
	)
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: --config is required")
		flag.Usage()
		os.Exit(1)
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to read config %q: %v", *configPath, err)
	}

	var cfg framework.ScenarioConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Failed to parse config %q: %v", *configPath, err)
	}

	// nil client is intentional: the engine owns the k8s client; the provider
	// receives it during Week 2 when CreateNodes is wired into engine.Run.
	provider := kwokprovider.New(nil)

	engine, err := framework.NewEngine(*kubeconfig, provider)
	if err != nil {
		log.Fatalf("Failed to create engine: %v", err)
	}

	scenario, ok := scenarios.Get(cfg.Name)
	if !ok {
		log.Fatalf("Scenario %q not registered; available: %v", cfg.Name, scenarios.List())
	}

	ctx := context.Background()
	if err := engine.Run(ctx, scenario, cfg, *outputPath); err != nil {
		log.Fatalf("Benchmark failed: %v", err)
	}
}

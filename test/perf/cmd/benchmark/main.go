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
	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"

	// Blank imports trigger each scenario's init() registration.
	// Add a new line here for each new scenario package.
	_ "github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios/basic"
	_ "github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios/gang"
)

func main() {
	configPath  := flag.String("config", "", "Path to scenario YAML config (required)")
	outputPath  := flag.String("output", "results/result.json", "Path for JSON result output")
	kubeconfig  := flag.String("kubeconfig", "", "Path to kubeconfig (default: ~/.kube/config)")
	baselinePath := flag.String("baseline", "", "Path to baseline JSON for regression detection (optional)")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: --config is required")
		fmt.Fprintln(os.Stderr, "Example: --config test/perf/configs/scenarios/basic-1k.yaml")
		flag.Usage()
		os.Exit(1)
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to read config %q: %v", *configPath, err)
	}

	var cfg types.ScenarioConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Failed to parse config %q: %v", *configPath, err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid config %q: %v", *configPath, err)
	}

	// Engine builds and owns the k8s client. The provider then reuses that
	// same client so only one connection pool exists per run.
	engine, err := framework.NewEngine(*kubeconfig, cfg.ClientQPS, cfg.ClientBurst, nil)
	if err != nil {
		log.Fatalf("Failed to create engine: %v", err)
	}
	engine.SetProvider(kwokprovider.New(engine.Client()))

	if err := engine.Run(context.Background(), cfg, *outputPath, *baselinePath); err != nil {
		log.Fatalf("Benchmark failed: %v", err)
	}
}

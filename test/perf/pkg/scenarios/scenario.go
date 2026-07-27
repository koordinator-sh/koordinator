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

// Package scenarios defines the Scenario interface and the global registry.
// Importing only pkg/types keeps this package free of import cycles
// with pkg/framework.
package scenarios

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// Scenario is the interface every benchmark scenario must implement.
// The engine calls: Setup → Pods → (fires burst) → Teardown.
// Scenario packages must not import pkg/framework to avoid cycles.
type Scenario interface {
	// Name returns a stable identifier used in reports and baselines.
	// Must match the "name" field in the scenario YAML config.
	Name() string

	// Setup creates prerequisites before the pod burst starts.
	// For basic scenarios this is a no-op beyond namespace creation.
	Setup(
		ctx       context.Context,
		client    kubernetes.Interface,
		dynClient dynamic.Interface,
		cfg       types.ScenarioConfig,
		runID     string,
	) error

	// Pods returns the pod specs to submit, or an error if cfg contains
	// invalid values (e.g. malformed resource quantity strings).
	// Every pod must carry: benchmark.koordinator.sh/run-id=<runID>
	Pods(cfg types.ScenarioConfig, runID string) ([]*corev1.Pod, error)

	// Teardown removes all objects created by Setup and the pod burst.
	// Always called — even if the benchmark failed.
	Teardown(
		ctx       context.Context,
		client    kubernetes.Interface,
		dynClient dynamic.Interface,
		runID     string,
	) error
}

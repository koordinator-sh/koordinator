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

// Package framework provides the benchmark engine, watcher, metrics,
// and report formatter. Shared struct types live in pkg/types to keep
// the import graph acyclic.
package framework

import "github.com/koordinator-sh/koordinator/test/perf/pkg/types"

// Type aliases expose pkg/types under the framework package so callers
// can use either import path without duplication.
type ScenarioConfig = types.ScenarioConfig
type Thresholds = types.Thresholds
type NodeSpec = types.NodeSpec
type BenchmarkResult = types.BenchmarkResult
type PodLatency = types.PodLatency

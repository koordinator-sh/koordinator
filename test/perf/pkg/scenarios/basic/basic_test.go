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

package basic

import (
	"testing"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// TestPods_LabelMergeOrder is the regression guard for the cfg.Labels-first
// merge-order fix. A config-supplied RunIDLabel must not override the
// engine-set run ID — a clobbered RunIDLabel causes Watcher/FailureWatcher to
// select no pods and hang the run until timeout.
//
// Setup is deliberately not called: BasicScenario.Pods reads s.namespace
// (populated by Setup), not cfg.Namespace, so namespace resolution is not
// under test here. Pods falls back to "benchmark" when s.namespace is empty.
func TestPods_LabelMergeOrder(t *testing.T) {
	cfg := types.ScenarioConfig{
		PodCount:      2,
		SchedulerName: "koord-scheduler",
		Labels: map[string]string{
			types.RunIDLabel: "attacker-supplied-run-id", // must NOT override the built-in
			"custom-label":   "preserved",
		},
	}
	s := &BasicScenario{}

	pods, err := s.Pods(cfg, "real-run-id")
	if err != nil {
		t.Fatalf("Pods() error = %v", err)
	}
	if len(pods) != cfg.PodCount {
		t.Fatalf("len(pods) = %d, want %d", len(pods), cfg.PodCount)
	}
	for _, p := range pods {
		if got := p.Labels[types.RunIDLabel]; got != "real-run-id" {
			t.Errorf("pod %s: RunIDLabel = %q, want %q (cfg.Labels must not override built-in)",
				p.Name, got, "real-run-id")
		}
		if got := p.Labels["custom-label"]; got != "preserved" {
			t.Errorf("pod %s: custom-label = %q, want %q", p.Name, got, "preserved")
		}
		if got := p.Labels["app"]; got != "kwok-bench" {
			t.Errorf("pod %s: app = %q, want kwok-bench", p.Name, got)
		}
	}
}

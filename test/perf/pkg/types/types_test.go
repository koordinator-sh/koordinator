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

package types

import "testing"

// validBaseConfig returns a ScenarioConfig that passes Validate() unchanged.
func validBaseConfig() ScenarioConfig {
	return ScenarioConfig{
		Name:        "basic",
		NodeCount:   10,
		PodCount:    100,
		Concurrency: 10,
		ClientQPS:   50,
		ClientBurst: 100,
	}
}

func TestValidate_ExpectedScheduledPodCountOutOfRange(t *testing.T) {
	n := 1500
	cfg := validBaseConfig()
	cfg.ExpectedScheduledPodCount = &n // > cfg.PodCount
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() = nil, want error for expectedScheduledPodCount > podCount")
	}
}

func TestValidate_FailureRatePctOutOfRange(t *testing.T) {
	pct := 150.0
	cfg := validBaseConfig()
	cfg.Thresholds.FailureRatePct = &pct
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() = nil, want error for failureRatePct > 100")
	}
}

func TestValidate_ImpliedScheduledCountMismatch(t *testing.T) {
	n := 200 // wrong: 150CPU / 500m = 300, not 200
	cfg := validBaseConfig()
	cfg.PodCount = 1000
	cfg.QuotaCPU = "150"
	cfg.QuotaMemory = "300Gi"
	cfg.ResourceRequests = map[string]string{"cpu": "500m", "memory": "512Mi"}
	cfg.ExpectedScheduledPodCount = &n
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() = nil, want error for expectedScheduledPodCount mismatch with quota/request sizing")
	}
}

func TestValidate_ImpliedScheduledCountMatch(t *testing.T) {
	n := 300 // 150CPU / 500m = 300 (CPU binds before memory: 300Gi / 512Mi = 600)
	cfg := validBaseConfig()
	cfg.PodCount = 1000
	cfg.QuotaCPU = "150"
	cfg.QuotaMemory = "300Gi"
	cfg.ResourceRequests = map[string]string{"cpu": "500m", "memory": "512Mi"}
	cfg.ExpectedScheduledPodCount = &n
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for correct expectedScheduledPodCount", err)
	}
}

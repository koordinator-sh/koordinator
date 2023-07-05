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

package prediction

import (
	"flag"
	"testing"
	"time"
)

func TestInitFlags(t *testing.T) {
	// Create a new Config instance
	config := NewDefaultConfig()

	// Create a new FlagSet
	fs := flag.NewFlagSet("test", flag.ContinueOnError)

	// Call InitFlags to set the flags
	config.InitFlags(fs)

	// Test case 1: Check if the PredictionCheckpointFilepath flag is set correctly
	expectedCheckpointFilepath := "/prediction-checkpoints"
	fs.Set("prediction-checkpoint-filepath", expectedCheckpointFilepath)
	if config.CheckpointFilepath != expectedCheckpointFilepath {
		t.Errorf("Expected PredictionCheckpointFilepath: %s, but got: %s", expectedCheckpointFilepath, config.CheckpointFilepath)
	}

	// Test case 2: Check if the PredictionColdStartDuration flag is set correctly
	expectedColdStartDuration := 24 * time.Hour
	fs.Set("prediction-cold-start-duration", "24h")
	if config.ColdStartDuration != expectedColdStartDuration {
		t.Errorf("Expected PredictionColdStartDuration: %s, but got: %s", expectedColdStartDuration, config.ColdStartDuration)
	}
}

func TestNewDefaultConfig(t *testing.T) {
	// Create a new default Config instance
	config := NewDefaultConfig()

	// Test if the PredictionCheckpointFilepath is set to the default value
	expectedCheckpointFilepath := "/prediction-checkpoints"
	if config.CheckpointFilepath != expectedCheckpointFilepath {
		t.Errorf("Expected default PredictionCheckpointFilepath: %s, but got: %s", expectedCheckpointFilepath, config.CheckpointFilepath)
	}

	// Test if the PredictionColdStartDuration is set to the default value
	expectedColdStartDuration := 24 * time.Hour
	if config.ColdStartDuration != expectedColdStartDuration {
		t.Errorf("Expected default PredictionColdStartDuration: %s, but got: %s", expectedColdStartDuration, config.ColdStartDuration)
	}
}

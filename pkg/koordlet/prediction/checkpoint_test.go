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
	"os"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/util/histogram"
)

func TestSaveAndRestore(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("/tmp", "checkpoints")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a fileCheckpointer instance
	checkpointer := NewFileCheckpointer(tempDir)

	// Create a sample model
	uid := UIDType("sample_uid")
	cpuCheckpoint := &histogram.HistogramCheckpoint{
		TotalWeight: 2,
	}
	memoryCheckpoint := &histogram.HistogramCheckpoint{
		TotalWeight: 3,
	}
	lastUpdated := metav1.Now()
	model := ModelCheckpoint{
		UID:         uid,
		CPU:         cpuCheckpoint,
		Memory:      memoryCheckpoint,
		LastUpdated: lastUpdated,
	}

	// Save the model as a checkpoint
	err = checkpointer.Save(model)
	if err != nil {
		t.Fatalf("Failed to save checkpoint: %v", err)
	}

	// Restore the checkpoints
	restoredCheckpoints, err := checkpointer.Restore()
	if err != nil {
		t.Fatalf("Failed to restore checkpoints: %v", err)
	}

	// Verify the restored checkpoints
	if len(restoredCheckpoints) != 1 {
		t.Fatalf("Expected 1 restored checkpoint, got %d", len(restoredCheckpoints))
	}

	restoredCheckpoint := restoredCheckpoints[0]
	if restoredCheckpoint.UID != uid {
		t.Errorf("Expected UID to be %s, got %s", uid, restoredCheckpoint.UID)
	}
	if restoredCheckpoint.CPU.TotalWeight != cpuCheckpoint.TotalWeight {
		t.Errorf("Expected CPU checkpoint to be equal")
	}
	if restoredCheckpoint.Memory.TotalWeight != memoryCheckpoint.TotalWeight {
		t.Errorf("Expected memory checkpoint to be equal")
	}
}

func TestSaveError(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("/tmp", "checkpoints")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a fileCheckpointer instance with invalid directory path
	checkpointer := &fileCheckpointer{
		path: "/invalid/directory",
	}

	// Create a sample model
	uid := UIDType("sample_uid")
	cpuCheckpoint := &histogram.HistogramCheckpoint{}
	memoryCheckpoint := &histogram.HistogramCheckpoint{}
	lastUpdated := metav1.Now()
	model := ModelCheckpoint{
		UID:         uid,
		CPU:         cpuCheckpoint,
		Memory:      memoryCheckpoint,
		LastUpdated: lastUpdated,
	}

	// Save the model as a checkpoint
	err = checkpointer.Save(model)
	if err == nil {
		t.Error("Expected error when saving checkpoint, got nil")
	}
}

func TestRestoreError(t *testing.T) {
	// Create a fileCheckpointer instance with an invalid directory path
	checkpointer := &fileCheckpointer{
		path: "/invalid/directory",
	}

	// Attempt to restore the checkpoints
	checkpoints, err := checkpointer.Restore()
	if err == nil {
		t.Error("Expected error when restoring checkpoints, got nil")
	}

	if len(checkpoints) != 0 {
		t.Errorf("Expected nil checkpoints, got %+v", checkpoints)
	}
}

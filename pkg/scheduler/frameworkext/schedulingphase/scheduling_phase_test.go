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

package schedulingphase

import (
	"testing"

	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func TestRecordAndGetExtensionPointBeingExecuted(t *testing.T) {
	tests := []struct {
		name           string
		extensionPoint string
		want           string
	}{
		{
			name:           "record PostFilter phase",
			extensionPoint: PostFilter,
			want:           PostFilter,
		},
		{
			name:           "record Reserve phase",
			extensionPoint: Reserve,
			want:           Reserve,
		},
		{
			name:           "record empty string clears phase",
			extensionPoint: "",
			want:           "",
		},
		{
			name:           "record arbitrary extension point name",
			extensionPoint: "CustomPhase",
			want:           "CustomPhase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cycleState := framework.NewCycleState()
			RecordPhase(cycleState, tt.extensionPoint)
			got := GetExtensionPointBeingExecuted(cycleState)
			if got != tt.want {
				t.Errorf("GetExtensionPointBeingExecuted() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetExtensionPointBeingExecuted_NotRecorded(t *testing.T) {
	cycleState := framework.NewCycleState()
	got := GetExtensionPointBeingExecuted(cycleState)
	if got != "" {
		t.Errorf("GetExtensionPointBeingExecuted() on fresh CycleState = %q, want empty string", got)
	}
}

func TestRecordPhase_OverwritePrevious(t *testing.T) {
	cycleState := framework.NewCycleState()

	RecordPhase(cycleState, PostFilter)
	if got := GetExtensionPointBeingExecuted(cycleState); got != PostFilter {
		t.Fatalf("expected %q, got %q", PostFilter, got)
	}

	RecordPhase(cycleState, Reserve)
	if got := GetExtensionPointBeingExecuted(cycleState); got != Reserve {
		t.Errorf("after overwrite: expected %q, got %q", Reserve, got)
	}

	RecordPhase(cycleState, "")
	if got := GetExtensionPointBeingExecuted(cycleState); got != "" {
		t.Errorf("after clear: expected empty string, got %q", got)
	}
}

func TestSchedulingPhase_Clone(t *testing.T) {
	phase := &SchedulingPhase{extensionPoint: PostFilter}
	cloned := phase.Clone()

	clonedPhase, ok := cloned.(*SchedulingPhase)
	if !ok {
		t.Fatalf("Clone() returned %T, want *SchedulingPhase", cloned)
	}
	if clonedPhase.extensionPoint != phase.extensionPoint {
		t.Errorf("cloned.extensionPoint = %q, want %q", clonedPhase.extensionPoint, phase.extensionPoint)
	}
	if clonedPhase != phase {
		t.Errorf("Clone() should return the same pointer (shallow identity), got different objects")
	}
}

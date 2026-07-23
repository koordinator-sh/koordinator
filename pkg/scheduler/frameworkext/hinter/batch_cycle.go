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

package hinter

import fwktype "k8s.io/kube-scheduler/framework"

const batchSchedulingCycleStateKey = "BatchSchedulingCycle"

var _ fwktype.StateData = batchSchedulingCycleMarker{}

type batchSchedulingCycleMarker struct{}

func (batchSchedulingCycleMarker) Clone() fwktype.StateData { return batchSchedulingCycleMarker{} }

// MarkBatchSchedulingCycle marks the cycle state as being driven by the batch/arbitrating engine.
// The engine sets this on every per-pod cycle state before running the scheduling cycle, so that the
// top-level inline batch trigger in RunPreFilterPlugins is not entered recursively.
func MarkBatchSchedulingCycle(cycleState fwktype.CycleState) {
	cycleState.Write(batchSchedulingCycleStateKey, batchSchedulingCycleMarker{})
}

// IsBatchSchedulingCycle reports whether the cycle state is driven by the batch/arbitrating engine.
// It is a dedicated re-entrancy signal and is intentionally decoupled from the scheduling hint state,
// which may be set from pod annotations (e.g. by the SchedulingHint plugin) on a top-level cycle.
func IsBatchSchedulingCycle(cycleState fwktype.CycleState) bool {
	_, err := cycleState.Read(batchSchedulingCycleStateKey)
	return err == nil
}

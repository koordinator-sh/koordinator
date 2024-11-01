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
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	phaseStateKey = extension.SchedulingDomainPrefix + "/scheduling-phase"
)

const (
	PostFilter = "PostFilter"
	Reserve    = "Reserve"
)

type SchedulingPhase struct {
	extensionPoint string
}

func (s *SchedulingPhase) Clone() framework.StateData {
	return s
}

func RecordPhase(cycleState *framework.CycleState, extensionPoint string) {
	cycleState.Write(phaseStateKey, &SchedulingPhase{
		extensionPoint: extensionPoint,
	})
}

func GetExtensionPointBeingExecuted(cycleState *framework.CycleState) string {
	s, err := cycleState.Read(phaseStateKey)
	if err != nil || s == nil {
		return ""
	}
	return s.(*SchedulingPhase).extensionPoint
}

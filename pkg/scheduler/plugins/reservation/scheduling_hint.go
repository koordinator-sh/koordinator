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

package reservation

import (
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
)

type HintState struct {
	// Selected nodes to pre-filter.
	PreFilterNodeInfos []string
	HintExtensions
}

type HintExtensions struct {
	// Whether to skip the nodeInfo restoration in this cycle.
	// e.g. the ownership for reservations does not change.
	SkipRestoreNodeInfo bool
}

func getSchedulingHint(cycleState *framework.CycleState) (*HintState, bool) {
	schedulingHint := hinter.GetSchedulingHintState(cycleState)
	if schedulingHint == nil || schedulingHint.Extensions == nil {
		return nil, false
	}
	if schedulingHint.Extensions[Name] == nil {
		return nil, false
	}
	extensions, ok := schedulingHint.Extensions[Name].(HintExtensions)
	if !ok {
		return nil, false
	}
	return &HintState{
		PreFilterNodeInfos: schedulingHint.PreFilterNodes,
		HintExtensions:     extensions,
	}, true
}

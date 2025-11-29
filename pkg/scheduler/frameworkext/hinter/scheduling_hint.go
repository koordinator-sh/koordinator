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

import "k8s.io/kubernetes/pkg/scheduler/framework"

const SchedulingHintStateKey = "SchedulingHintState"

var _ framework.StateData = &SchedulingHintStateData{}

type SchedulingHintStateData struct {
	PreFilterNodes []string // hint to preFilter node
	Extensions     map[string]interface{}
}

func (a *SchedulingHintStateData) Clone() framework.StateData {
	return &SchedulingHintStateData{
		PreFilterNodes: a.PreFilterNodes,
		Extensions:     a.Extensions,
	}
}

func GetSchedulingHintState(cycleState *framework.CycleState) *SchedulingHintStateData {
	stateData, err := cycleState.Read(SchedulingHintStateKey)
	if err != nil {
		return nil
	}
	return stateData.(*SchedulingHintStateData)
}

func SetSchedulingHintState(cycleState *framework.CycleState, data *SchedulingHintStateData) {
	cycleState.Write(SchedulingHintStateKey, data)
}

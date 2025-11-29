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
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
)

func Test_getSchedulingHint(t *testing.T) {
	// Create a mock cycle state
	cycleState := framework.NewCycleState()

	tests := []struct {
		name           string
		setupState     func(*framework.CycleState)
		expectedResult *HintState
		expectedOK     bool
	}{
		{
			name:           "no scheduling hint state",
			setupState:     func(state *framework.CycleState) {},
			expectedResult: nil,
			expectedOK:     false,
		},
		{
			name: "scheduling hint state without extensions",
			setupState: func(state *framework.CycleState) {
				hintState := &hinter.SchedulingHintStateData{}
				state.Write(hinter.SchedulingHintStateKey, hintState)
			},
			expectedResult: nil,
			expectedOK:     false,
		},
		{
			name: "scheduling hint state with extensions but not for reservation plugin",
			setupState: func(state *framework.CycleState) {
				hintState := &hinter.SchedulingHintStateData{
					Extensions: map[string]interface{}{
						"other-plugin": HintExtensions{SkipRestoreNodeInfo: true},
					},
				}
				state.Write(hinter.SchedulingHintStateKey, hintState)
			},
			expectedResult: nil,
			expectedOK:     false,
		},
		{
			name: "scheduling hint state with reservation plugin extensions - wrong type",
			setupState: func(state *framework.CycleState) {
				hintState := &hinter.SchedulingHintStateData{
					PreFilterNodes: []string{"node1", "node2"},
					Extensions: map[string]interface{}{
						Name: "wrong-type",
					},
				}
				state.Write(hinter.SchedulingHintStateKey, hintState)
			},
			expectedResult: nil,
			expectedOK:     false,
		},
		{
			name: "scheduling hint state with reservation plugin extensions - correct type",
			setupState: func(state *framework.CycleState) {
				hintState := &hinter.SchedulingHintStateData{
					PreFilterNodes: []string{"node1", "node2"},
					Extensions: map[string]interface{}{
						Name: HintExtensions{SkipRestoreNodeInfo: true},
					},
				}
				state.Write(hinter.SchedulingHintStateKey, hintState)
			},
			expectedResult: &HintState{
				PreFilterNodeInfos: []string{"node1", "node2"},
				HintExtensions:     HintExtensions{SkipRestoreNodeInfo: true},
			},
			expectedOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup the cycle state
			tt.setupState(cycleState)

			// Call the function
			result, ok := getSchedulingHint(cycleState)

			// Verify results
			assert.Equal(t, tt.expectedOK, ok, "Expected ok to be %v, but got %v", tt.expectedOK, ok)

			if tt.expectedOK {
				assert.NotNil(t, result, "Expected result to be non-nil")
				assert.Equal(t, tt.expectedResult.PreFilterNodeInfos, result.PreFilterNodeInfos,
					"Expected PreFilterNodeInfos %v, but got %v",
					tt.expectedResult.PreFilterNodeInfos, result.PreFilterNodeInfos)
				assert.Equal(t, tt.expectedResult.SkipRestoreNodeInfo, result.SkipRestoreNodeInfo,
					"Expected SkipRestoreNodeInfo %v, but got %v",
					tt.expectedResult.SkipRestoreNodeInfo, result.SkipRestoreNodeInfo)
			} else {
				assert.Nil(t, result, "Expected result to be nil, but got %v", result)
			}
		})
	}
}

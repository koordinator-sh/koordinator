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

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func TestSchedulingHintStateData_Clone(t *testing.T) {
	originalNodeInfo := ""
	preferredNodes := []string{"node-1", "node-2"}
	extensions := map[string]interface{}{
		"key1": "value1",
		"key2": 123,
	}

	original := &SchedulingHintStateData{
		PreFilterNodes: []string{originalNodeInfo},
		PreferredNodes: preferredNodes,
		Extensions:     extensions,
	}

	cloned := original.Clone().(*SchedulingHintStateData)

	assert.NotNil(t, cloned)
	assert.Equal(t, original.PreFilterNodes, cloned.PreFilterNodes)
	assert.Equal(t, original.PreferredNodes, cloned.PreferredNodes)
	assert.Equal(t, original.Extensions, cloned.Extensions)

	assert.NotSame(t, original.Extensions, cloned.Extensions)
}

func TestGetSchedulingHintState(t *testing.T) {
	cycleState := framework.NewCycleState()

	result := GetSchedulingHintState(cycleState)
	assert.Nil(t, result)

	stateData := &SchedulingHintStateData{
		PreFilterNodes: []string{},
		Extensions:     make(map[string]interface{}),
	}

	SetSchedulingHintState(cycleState, stateData)

	result = GetSchedulingHintState(cycleState)
	assert.NotNil(t, result)
	assert.Equal(t, stateData, result)
}

func TestSetSchedulingHintStateData(t *testing.T) {
	cycleState := framework.NewCycleState()

	stateData := &SchedulingHintStateData{
		PreFilterNodes: []string{},
		Extensions:     make(map[string]interface{}),
	}

	SetSchedulingHintState(cycleState, stateData)

	retrievedData, err := cycleState.Read(SchedulingHintStateKey)
	assert.NoError(t, err)
	assert.Equal(t, stateData, retrievedData)
}

func TestSchedulingHintStateData_Integration(t *testing.T) {
	cycleState := framework.NewCycleState()

	nodeInfo := "test-node"
	preferredNodes := []string{"preferred-node-1", "preferred-node-2"}
	extensions := map[string]interface{}{
		"testKey": "testValue",
	}

	stateData := &SchedulingHintStateData{
		PreFilterNodes: []string{nodeInfo},
		PreferredNodes: preferredNodes,
		Extensions:     extensions,
	}

	SetSchedulingHintState(cycleState, stateData)

	retrieved := GetSchedulingHintState(cycleState)
	assert.NotNil(t, retrieved)
	assert.Equal(t, []string{nodeInfo}, retrieved.PreFilterNodes)
	assert.Equal(t, preferredNodes, retrieved.PreferredNodes)
	assert.Equal(t, extensions, retrieved.Extensions)

	cloned := retrieved.Clone().(*SchedulingHintStateData)
	assert.Equal(t, retrieved, cloned)
}

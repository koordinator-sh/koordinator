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

package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	schetesting "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func TestInitHookPlugins(t *testing.T) {
	// Register a mock hook plugin factory
	testFactoryKey := "mockFactory"
	RegisterHookPluginFactory(testFactoryKey,
		func(qiProvider *QuotaInfoReader, key, args string) (QuotaHookPlugin, error) {
			return &MockHookPlugin{key: key}, nil
		})

	// Define test cases
	tests := []struct {
		name          string
		args          *config.ElasticQuotaArgs
		expectedError bool
		expectedErr   error
		expectedCount int
	}{
		{
			name: "valid plugin configuration",
			args: &config.ElasticQuotaArgs{
				HookPlugins: []config.HookPluginConf{
					{Key: "mockPlugin1", FactoryKey: testFactoryKey},
				},
			},
			expectedCount: 1,
		},
		{
			name: "valid plugin configuration with 2 plugins",
			args: &config.ElasticQuotaArgs{
				HookPlugins: []config.HookPluginConf{
					{Key: "mockPlugin1", FactoryKey: testFactoryKey},
					{Key: "mockPlugin2", FactoryKey: testFactoryKey},
				},
			},
			expectedCount: 2,
		},
		{
			name: "invalid plugin configuration with empty plugin key",
			args: &config.ElasticQuotaArgs{
				HookPlugins: []config.HookPluginConf{
					{Key: ""},
				},
			},
			expectedCount: 0,
			expectedErr:   fmt.Errorf("failed to initialize index-0 hook plugin: key is empty"),
		},
		{
			name: "invalid plugin configuration with valid and empty factory key",
			args: &config.ElasticQuotaArgs{
				HookPlugins: []config.HookPluginConf{
					{Key: "mockPlugin1", FactoryKey: testFactoryKey},
					{Key: "mockPlugin2", FactoryKey: ""},
				},
			},
			expectedCount: 0,
			expectedErr:   fmt.Errorf("failed to initialize hook plugin mockPlugin2: factory key is empty"),
		},
		{
			name: "invalid plugin configuration with unknown factory key",
			args: &config.ElasticQuotaArgs{
				HookPlugins: []config.HookPluginConf{
					{Key: "mockPlugin1", FactoryKey: "unknownFactory"},
				},
			},
			expectedCount: 0,
			expectedErr:   fmt.Errorf("failed to initialize hook plugin mockPlugin1: factory unknownFactory not found"),
		},
		{
			name: "invalid plugin configuration with valid and unknown factory keys",
			args: &config.ElasticQuotaArgs{
				HookPlugins: []config.HookPluginConf{
					{Key: "mockPlugin1", FactoryKey: testFactoryKey},
					{Key: "mockPlugin2", FactoryKey: "unknownFactory"},
				},
			},
			expectedCount: 0,
			expectedErr:   fmt.Errorf("failed to initialize hook plugin mockPlugin2: factory unknownFactory not found"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gqm := NewGroupQuotaManager("", nil, nil)
			err := gqm.InitHookPlugins(tt.args)
			if tt.expectedErr != nil {
				assert.ErrorContains(t, err, tt.expectedErr.Error(), "Error message should match")
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedCount, len(gqm.GetHookPlugins()))
		})
	}
}

// TestPrePostQuotaUpdateHooks tests the IsQuotaUpdated, PreQuotaUpdate and PostQuotaUpdate hooks
func TestPrePostQuotaUpdateHooks(t *testing.T) {
	gqm := initGQMForTesting(t)
	hookPlugins := gqm.GetHookPlugins()
	assert.Equal(t, 1, len(hookPlugins))
	mockHook := hookPlugins[0].(*MetricsWrapper).GetPlugin().(*MockHookPlugin)

	// validate Create operation
	parentQuota := CreateQuota("test-parent", extension.RootQuotaName, 10, 10, 0, 0, false, false)
	err := gqm.UpdateQuota(parentQuota)
	assert.NoError(t, err, "UpdateQuota should succeed")
	q1 := CreateQuota("q1", parentQuota.Name, 40, 40, 20, 20, false, false)
	validateInputParametersFn := func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		assert.Nil(t, oldQuotaInfo, "Old quota info should be nil for create operation")
		assert.NotNil(t, newQuotaInfo, "New quota info should not be nil")
		assert.True(t, q1 == quota, "Quota should match")
		assert.NotNil(t, state, "State should not be nil")
	}
	updateStateFn := func(state *QuotaUpdateState) {
		assert.NotNil(t, state, "State should not be nil")
		state.Storage.Store("mockHookValue", "testValue")
	}
	validateStateUpdated := func(state *QuotaUpdateState) {
		assert.NotNil(t, state, "State should not be nil")
		value, ok := state.Storage.Load("mockHookValue")
		assert.True(t, ok, "Storage should contain mockHookValue")
		assert.Equal(t, "testValue", value, "Storage value should match")
	}
	mockHook.PreQuotaUpdateValidateFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		updateStateFn(state)
		// validate QuotaInfoReader
		assert.NotNil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(parentQuota.Name), "parent should not be nil")
		assert.Nil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(q1.Name), "q1 should be nil")
		assert.Equal(t, 0, len(gqm.GetQuotaInfoReader().GetChildQuotaInfosNoLock(parentQuota.Name)))
	}
	mockHook.PostQuotaUpdateValidateFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		validateStateUpdated(state)
		// validate QuotaInfoReader
		assert.NotNil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(parentQuota.Name),
			"parent should not be nil")
		assert.NotNil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(q1.Name), "q1 should not be nil")
		assert.Equal(t, 1, len(gqm.GetQuotaInfoReader().GetChildQuotaInfosNoLock(parentQuota.Name)))
	}
	err = gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.True(t, mockHook.PreQuotaUpdateCalled, "PreQuotaUpdate should be called")
	assert.True(t, mockHook.PostQuotaUpdateCalled, "PostQuotaUpdate should be called")

	// validate Update operation: PreQuotaUpdate and PostQuotaUpdate hooks should not be called when quota has not been updated
	mockHook.Reset()
	err = gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.False(t, mockHook.PreQuotaUpdateCalled, "PreQuotaUpdate should not be called")
	assert.False(t, mockHook.PostQuotaUpdateCalled, "PostQuotaUpdate should not be called")

	// validate Update operation: PreQuotaUpdate and PostQuotaUpdate hooks should be called
	// (decision is made by hook plugin) even when quota has not been updated.
	mockHook.Reset()
	mockHook.IsQuotaUpdatedFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
		return true
	}
	err = gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.True(t, mockHook.PreQuotaUpdateCalled, "PreQuotaUpdate should be called")
	assert.True(t, mockHook.PostQuotaUpdateCalled, "PostQuotaUpdate should be called")

	// validate Update operation: spec.max is updated
	mockHook.Reset()
	q1Info := gqm.GetQuotaInfoByName("q1")
	updatedQ1 := q1.DeepCopy()
	updatedQ1.Spec.Max = v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("50"),
		v1.ResourceMemory: resource.MustParse("50"),
	}
	validateInputParametersFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		assert.True(t, oldQuotaInfo == q1Info, "Old quota info should be nil for create operation")
		assert.NotNil(t, newQuotaInfo, "New quota info should not be nil")
		assert.True(t, quotav1.Equals(newQuotaInfo.CalculateInfo.Max, updatedQ1.Spec.Max),
			"Max should updated for new quota info")
		assert.True(t, updatedQ1 == quota, "should be new quota")
		assert.NotNil(t, state, "State should not be nil")
	}
	mockHook.PreQuotaUpdateValidateFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		updateStateFn(state)
	}
	mockHook.PostQuotaUpdateValidateFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		validateStateUpdated(state)
	}
	err = gqm.UpdateQuota(updatedQ1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.True(t, mockHook.PreQuotaUpdateCalled, "PreQuotaUpdate should be called")
	assert.True(t, mockHook.PostQuotaUpdateCalled, "PostQuotaUpdate should be called")

	// validate Delete operation
	mockHook.Reset()
	err = gqm.DeleteQuota(updatedQ1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	validateInputParametersFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		assert.NotNil(t, oldQuotaInfo, "Old quota info should not be nil")
		assert.Nil(t, newQuotaInfo, "New quota info should be nil for delete operation")
		assert.True(t, updatedQ1 == quota, "Quota should match")
		assert.NotNil(t, state, "State should not be nil")
	}
	mockHook.PreQuotaUpdateValidateFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		updateStateFn(state)
	}
	mockHook.PostQuotaUpdateValidateFn = func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		validateStateUpdated(state)
	}
}

// TestOnPodUpdateHook tests the OnPodUpdateHook method
func TestOnPodUpdateHook(t *testing.T) {
	gqm := initGQMForTesting(t)
	hookPlugins := gqm.GetHookPlugins()
	assert.Equal(t, 1, len(hookPlugins))
	mockHook := hookPlugins[0].(*MetricsWrapper).GetPlugin().(*MockHookPlugin)

	// create test quota
	q1 := CreateQuota("q1", "", 10, 10, 0, 0, false, false)
	err := gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")

	// validate Pod Create operation: add pod with assigned node
	pod1 := schetesting.MakePod().Name("pod1").Label(extension.LabelQuotaName, q1.Name).Containers(
		[]v1.Container{schetesting.MakeContainer().Name("0").Resources(map[v1.ResourceName]string{
			v1.ResourceCPU: "2"}).Obj()}).Node("node0").Obj()
	mockHook.OnPodUpdatedValidateFn = func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.Nil(t, oldPod, "Old pod should be nil for create operation")
		assert.NotNil(t, newPod, "New pod should not be nil")
		assert.True(t, pod1 == newPod, "New pod should match")
	}
	gqm.OnPodAdd(q1.Name, pod1)
	assert.True(t, mockHook.OnPodUpdatedCalled, "OnPodUpdated should be called")

	// validate Pod Update operation
	mockHook.Reset()
	updatedPod1 := pod1.DeepCopy()
	mockHook.OnPodUpdatedValidateFn = func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.NotNil(t, oldPod, "Old pod should not be nil")
		assert.True(t, pod1 == oldPod, "Old pod should match")
		assert.NotNil(t, newPod, "New pod should not be nil")
		assert.True(t, updatedPod1 == newPod, "New pod should match")
	}
	gqm.OnPodUpdate(q1.Name, q1.Name, updatedPod1, pod1)
	assert.True(t, mockHook.OnPodUpdatedCalled, "OnPodUpdated should be called")

	// validate Pod Delete operation
	mockHook.Reset()
	mockHook.OnPodUpdatedValidateFn = func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.NotNil(t, oldPod, "Old pod should not be nil")
		assert.True(t, updatedPod1 == oldPod, "Old pod should match")
		assert.Nil(t, newPod, "New pod should be nil for delete operation")
	}
	gqm.OnPodDelete(q1.Name, updatedPod1)
	assert.True(t, mockHook.OnPodUpdatedCalled, "OnPodUpdated should be called")

	// validate Pod Reserve operation
	unassignedPod := pod1.DeepCopy()
	unassignedPod.Spec.NodeName = ""
	gqm.OnPodAdd(q1.Name, unassignedPod)

	mockHook.Reset()
	mockHook.OnPodUpdatedValidateFn = func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.Nil(t, oldPod, "Old pod should be nil for reserve operation")
		assert.NotNil(t, newPod, "New pod should not be nil")
		assert.True(t, unassignedPod == newPod, "New pod should match")
	}
	gqm.ReservePod(q1.Name, unassignedPod)
	assert.True(t, mockHook.OnPodUpdatedCalled, "OnPodUpdated should be called")

	// validate Pod UnReserve operation
	mockHook.Reset()
	mockHook.OnPodUpdatedValidateFn = func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.NotNil(t, oldPod, "Old pod should not be nil")
		assert.True(t, unassignedPod == oldPod, "Old pod should match")
		assert.Nil(t, newPod, "New pod should be nil for unreserve operation")
	}
	gqm.UnreservePod(q1.Name, unassignedPod)
	assert.True(t, mockHook.OnPodUpdatedCalled, "OnPodUpdated should be called")
}

func initGQMForTesting(t *testing.T) *GroupQuotaManager {
	RegisterHookPluginFactory("mockFactory",
		func(qiReader *QuotaInfoReader, key, args string) (QuotaHookPlugin, error) {
			return &MockHookPlugin{key: key}, nil
		})
	pluginArgs := &config.ElasticQuotaArgs{
		HookPlugins: []config.HookPluginConf{
			{
				Key:        "mockPlugin",
				FactoryKey: "mockFactory",
			},
		},
	}
	// create a GroupQuotaManager with a mock hook
	gqm := NewGroupQuotaManager("", nil, nil)
	err := gqm.InitHookPlugins(pluginArgs)
	assert.NoError(t, err)
	return gqm
}

var _ QuotaHookPlugin = &MockHookPlugin{}

// MockHookPlugin is a mock implementation of QuotaHookPlugin
type MockHookPlugin struct {
	key                      string
	PreQuotaUpdateCalled     bool
	PreQuotaUpdateValidateFn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)

	IsQuotaUpdatedFn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool

	PostQuotaUpdateCalled     bool
	PostQuotaUpdateValidateFn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)

	OnPodUpdatedCalled     bool
	OnPodUpdatedValidateFn func(quotaName string, oldPod, newPod *v1.Pod)
}

func (m *MockHookPlugin) Reset() {
	m.PreQuotaUpdateCalled = false
	m.PreQuotaUpdateValidateFn = nil
	m.IsQuotaUpdatedFn = nil
	m.PostQuotaUpdateCalled = false
	m.PostQuotaUpdateValidateFn = nil
	m.OnPodUpdatedCalled = false
	m.OnPodUpdatedValidateFn = nil
}

func (m *MockHookPlugin) GetKey() string {
	return m.key
}

func (m *MockHookPlugin) IsQuotaUpdated(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
	if m.IsQuotaUpdatedFn != nil {
		return m.IsQuotaUpdatedFn(oldQuotaInfo, newQuotaInfo, newQuota)
	}
	return false
}

func (m *MockHookPlugin) PreQuotaUpdate(oldQuotaInfo *QuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
	m.PreQuotaUpdateCalled = true
	if m.PreQuotaUpdateValidateFn != nil {
		m.PreQuotaUpdateValidateFn(oldQuotaInfo, newQuotaInfo, quota, state)
	}
}

func (m *MockHookPlugin) PostQuotaUpdate(oldQuotaInfo *QuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
	m.PostQuotaUpdateCalled = true
	if m.PostQuotaUpdateValidateFn != nil {
		m.PostQuotaUpdateValidateFn(oldQuotaInfo, newQuotaInfo, quota, state)
	}
}

func (m *MockHookPlugin) OnPodUpdated(quotaName string, oldPod, newPod *v1.Pod) {
	m.OnPodUpdatedCalled = true
	if m.OnPodUpdatedValidateFn != nil {
		m.OnPodUpdatedValidateFn(quotaName, oldPod, newPod)
	}
}

// The following methods of MockHookPlugin are tested in the plugin package instead of this file

func (m *MockHookPlugin) UpdateQuotaStatus(_, _ *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota {
	return nil
}

func (m *MockHookPlugin) CheckPod(quotaName string, pod *v1.Pod) error {
	return nil
}

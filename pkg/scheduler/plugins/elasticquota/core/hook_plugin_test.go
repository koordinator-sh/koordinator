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
	"sync"
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
			gqm := NewGroupQuotaManager("", true, nil, nil)
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
	mockHook.SetPreQuotaUpdateValidateFn(func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		updateStateFn(state)
		// validate QuotaInfoReader
		assert.NotNil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(parentQuota.Name), "parent should not be nil")
		assert.Nil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(q1.Name), "q1 should be nil")
		assert.Equal(t, 0, len(gqm.GetQuotaInfoReader().GetChildQuotaInfosNoLock(parentQuota.Name)))
	})
	mockHook.SetPostQuotaUpdateValidateFn(func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		validateStateUpdated(state)
		// validate QuotaInfoReader
		assert.NotNil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(parentQuota.Name),
			"parent should not be nil")
		assert.NotNil(t, gqm.GetQuotaInfoReader().GetQuotaInfoNoLock(q1.Name), "q1 should not be nil")
		assert.Equal(t, 1, len(gqm.GetQuotaInfoReader().GetChildQuotaInfosNoLock(parentQuota.Name)))
	})
	err = gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.True(t, mockHook.IsPreQuotaUpdateCalled(), "PreQuotaUpdate should be called")
	assert.True(t, mockHook.IsPostQuotaUpdateCalled(), "PostQuotaUpdate should be called")

	// validate Update operation: PreQuotaUpdate and PostQuotaUpdate hooks should not be called when quota has not been updated
	mockHook.Reset()
	err = gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.False(t, mockHook.IsPreQuotaUpdateCalled(), "PreQuotaUpdate should not be called")
	assert.False(t, mockHook.IsPostQuotaUpdateCalled(), "PostQuotaUpdate should not be called")

	// validate Update operation: PreQuotaUpdate and PostQuotaUpdate hooks should be called
	// (decision is made by hook plugin) even when quota has not been updated.
	mockHook.Reset()
	mockHook.SetIsQuotaUpdatedFn(func(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
		return true
	})
	err = gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.True(t, mockHook.IsPreQuotaUpdateCalled(), "PreQuotaUpdate should be called")
	assert.True(t, mockHook.IsPostQuotaUpdateCalled(), "PostQuotaUpdate should be called")

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
	mockHook.SetPreQuotaUpdateValidateFn(func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		updateStateFn(state)
	})
	mockHook.SetPostQuotaUpdateValidateFn(func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		validateStateUpdated(state)
	})
	err = gqm.UpdateQuota(updatedQ1)
	assert.NoError(t, err, "UpdateQuota should succeed")
	assert.True(t, mockHook.IsPreQuotaUpdateCalled(), "PreQuotaUpdate should be called")
	assert.True(t, mockHook.IsPostQuotaUpdateCalled(), "PostQuotaUpdate should be called")

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
	mockHook.SetPreQuotaUpdateValidateFn(func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		updateStateFn(state)
	})
	mockHook.SetPostQuotaUpdateValidateFn(func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota,
		state *QuotaUpdateState) {
		validateInputParametersFn(oldQuotaInfo, newQuotaInfo, quota, state)
		validateStateUpdated(state)
	})
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
	mockHook.SetOnPodUpdatedValidateFn(func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.Nil(t, oldPod, "Old pod should be nil for create operation")
		assert.NotNil(t, newPod, "New pod should not be nil")
		assert.True(t, pod1 == newPod, "New pod should match")
	})
	gqm.OnPodAdd(q1.Name, pod1)
	assert.True(t, mockHook.IsOnPodUpdatedCalled(), "OnPodUpdated should be called")

	// validate Pod Update operation
	mockHook.Reset()
	updatedPod1 := pod1.DeepCopy()
	mockHook.SetOnPodUpdatedValidateFn(func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.NotNil(t, oldPod, "Old pod should not be nil")
		assert.True(t, pod1 == oldPod, "Old pod should match")
		assert.NotNil(t, newPod, "New pod should not be nil")
		assert.True(t, updatedPod1 == newPod, "New pod should match")
	})
	gqm.OnPodUpdate(q1.Name, q1.Name, updatedPod1, pod1)
	assert.True(t, mockHook.IsOnPodUpdatedCalled(), "OnPodUpdated should be called")

	// validate Pod Delete operation
	mockHook.Reset()
	mockHook.SetOnPodUpdatedValidateFn(func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.NotNil(t, oldPod, "Old pod should not be nil")
		assert.True(t, updatedPod1 == oldPod, "Old pod should match")
		assert.Nil(t, newPod, "New pod should be nil for delete operation")
	})
	gqm.OnPodDelete(q1.Name, updatedPod1)
	assert.True(t, mockHook.IsOnPodUpdatedCalled(), "OnPodUpdated should be called")

	// validate Pod Reserve operation
	unassignedPod := pod1.DeepCopy()
	unassignedPod.Spec.NodeName = ""
	gqm.OnPodAdd(q1.Name, unassignedPod)

	mockHook.Reset()
	mockHook.SetOnPodUpdatedValidateFn(func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.Nil(t, oldPod, "Old pod should be nil for reserve operation")
		assert.NotNil(t, newPod, "New pod should not be nil")
		assert.True(t, unassignedPod == newPod, "New pod should match")
	})
	gqm.ReservePod(q1.Name, unassignedPod)
	assert.True(t, mockHook.IsOnPodUpdatedCalled(), "OnPodUpdated should be called")

	// validate Pod UnReserve operation
	mockHook.Reset()
	mockHook.SetOnPodUpdatedValidateFn(func(quotaName string, oldPod, newPod *v1.Pod) {
		assert.Equal(t, q1.Name, quotaName, "Quota name should match")
		assert.NotNil(t, oldPod, "Old pod should not be nil")
		assert.True(t, unassignedPod == oldPod, "Old pod should match")
		assert.Nil(t, newPod, "New pod should be nil for unreserve operation")
	})
	gqm.UnreservePod(q1.Name, unassignedPod)
	assert.True(t, mockHook.IsOnPodUpdatedCalled(), "OnPodUpdated should be called")
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
	gqm := NewGroupQuotaManager("", true, nil, nil)
	err := gqm.InitHookPlugins(pluginArgs)
	assert.NoError(t, err)
	return gqm
}

var _ QuotaHookPlugin = &MockHookPlugin{}

// MockHookPlugin is a mock implementation of QuotaHookPlugin
type MockHookPlugin struct {
	key                      string
	preQuotaUpdateQuotas     []string
	preQuotaUpdateValidateFn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)

	isQuotaUpdatedFn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool

	postQuotaUpdateQuotas     []string
	postQuotaUpdateValidateFn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)

	onPodUpdatedCalled     bool
	onPodUpdatedValidateFn func(quotaName string, oldPod, newPod *v1.Pod)

	sync.RWMutex
}

func (m *MockHookPlugin) Reset() {
	m.Lock()
	defer m.Unlock()

	m.preQuotaUpdateQuotas = nil
	m.preQuotaUpdateValidateFn = nil
	m.isQuotaUpdatedFn = nil
	m.postQuotaUpdateQuotas = nil
	m.postQuotaUpdateValidateFn = nil
	m.onPodUpdatedCalled = false
	m.onPodUpdatedValidateFn = nil
}

// Helper methods to safely set function fields with write lock protection
func (m *MockHookPlugin) SetPreQuotaUpdateValidateFn(fn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)) {
	m.Lock()
	defer m.Unlock()
	m.preQuotaUpdateValidateFn = fn
}

func (m *MockHookPlugin) SetIsQuotaUpdatedFn(fn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool) {
	m.Lock()
	defer m.Unlock()
	m.isQuotaUpdatedFn = fn
}

func (m *MockHookPlugin) SetPostQuotaUpdateValidateFn(fn func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)) {
	m.Lock()
	defer m.Unlock()
	m.postQuotaUpdateValidateFn = fn
}

func (m *MockHookPlugin) SetOnPodUpdatedValidateFn(fn func(quotaName string, oldPod, newPod *v1.Pod)) {
	m.Lock()
	defer m.Unlock()
	m.onPodUpdatedValidateFn = fn
}

func (m *MockHookPlugin) GetKey() string {
	m.RLock()
	defer m.RUnlock()
	return m.key
}

func (m *MockHookPlugin) IsQuotaUpdated(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
	m.RLock()
	isQuotaUpdatedFn := m.isQuotaUpdatedFn
	m.RUnlock()

	if isQuotaUpdatedFn != nil {
		return isQuotaUpdatedFn(oldQuotaInfo, newQuotaInfo, newQuota)
	}
	return false
}

func (m *MockHookPlugin) PreQuotaUpdate(oldQuotaInfo *QuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
	m.Lock()
	m.preQuotaUpdateQuotas = append(m.preQuotaUpdateQuotas, quota.Name)
	validateFn := m.preQuotaUpdateValidateFn
	m.Unlock()

	if validateFn != nil {
		validateFn(oldQuotaInfo, newQuotaInfo, quota, state)
	}
}

func (m *MockHookPlugin) PostQuotaUpdate(oldQuotaInfo *QuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
	m.Lock()
	m.postQuotaUpdateQuotas = append(m.postQuotaUpdateQuotas, quota.Name)
	validateFn := m.postQuotaUpdateValidateFn
	m.Unlock()

	if validateFn != nil {
		validateFn(oldQuotaInfo, newQuotaInfo, quota, state)
	}
}

func (m *MockHookPlugin) OnPodUpdated(quotaName string, oldPod, newPod *v1.Pod) {
	m.Lock()
	m.onPodUpdatedCalled = true
	validateFn := m.onPodUpdatedValidateFn
	m.Unlock()

	if validateFn != nil {
		validateFn(quotaName, oldPod, newPod)
	}
}

// Safe getter methods for boolean fields
func (m *MockHookPlugin) IsPreQuotaUpdateCalled() bool {
	m.RLock()
	defer m.RUnlock()
	return len(m.preQuotaUpdateQuotas) > 0
}

func (m *MockHookPlugin) IsPostQuotaUpdateCalled() bool {
	m.RLock()
	defer m.RUnlock()
	return len(m.postQuotaUpdateQuotas) > 0
}

func (m *MockHookPlugin) IsOnPodUpdatedCalled() bool {
	m.RLock()
	defer m.RUnlock()
	return m.onPodUpdatedCalled
}

// Safe getter methods for slice fields
func (m *MockHookPlugin) GetPreQuotaUpdateQuotas() []string {
	m.RLock()
	defer m.RUnlock()
	// Return a copy to prevent external modification
	result := make([]string, len(m.preQuotaUpdateQuotas))
	copy(result, m.preQuotaUpdateQuotas)
	return result
}

func (m *MockHookPlugin) GetPostQuotaUpdateQuotas() []string {
	m.RLock()
	defer m.RUnlock()
	// Return a copy to prevent external modification
	result := make([]string, len(m.postQuotaUpdateQuotas))
	copy(result, m.postQuotaUpdateQuotas)
	return result
}

// The following methods of MockHookPlugin are tested in the plugin package instead of this file

func (m *MockHookPlugin) UpdateQuotaStatus(_, _ *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota {
	return nil
}

func (m *MockHookPlugin) CheckPod(quotaName string, pod *v1.Pod) error {
	return nil
}

func TestResetQuotasForHookPlugins(t *testing.T) {
	gqm := initGQMForTesting(t)
	hookPlugins := gqm.GetHookPlugins()
	assert.Equal(t, 1, len(hookPlugins))
	mockHook := hookPlugins[0].(*MetricsWrapper).GetPlugin().(*MockHookPlugin)

	// create test quotas and add them to GQM
	parentQuota := CreateQuota("test-parent", extension.RootQuotaName, 100, 100, 0, 0, false, false)
	err := gqm.UpdateQuota(parentQuota)
	assert.NoError(t, err, "UpdateQuota should succeed")

	q1 := CreateQuota("q1", parentQuota.Name, 40, 40, 20, 20, false, false)
	err = gqm.UpdateQuota(q1)
	assert.NoError(t, err, "UpdateQuota should succeed")

	q2 := CreateQuota("q2", parentQuota.Name, 30, 30, 10, 10, false, false)
	err = gqm.UpdateQuota(q2)
	assert.NoError(t, err, "UpdateQuota should succeed")

	validateHookParameters := func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
		assert.Nil(t, oldQuotaInfo, "Old quota info should be nil for reset operation")
		assert.NotNil(t, newQuotaInfo, "New quota info should not be nil")
		assert.NotNil(t, quota, "Quota should not be nil")
		assert.NotNil(t, state, "State should not be nil")
	}
	quotaUpdateValidateFn := func(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
		validateHookParameters(oldQuotaInfo, newQuotaInfo, quota, state)
	}
	resetHookFn := func() {
		mockHook.Reset()
		mockHook.SetPreQuotaUpdateValidateFn(quotaUpdateValidateFn)
		mockHook.SetPostQuotaUpdateValidateFn(quotaUpdateValidateFn)
	}

	// Test case 1: Reset with all quotas present
	resetHookFn()
	quotas := map[string]*v1alpha1.ElasticQuota{
		parentQuota.Name: parentQuota,
		q1.Name:          q1,
		q2.Name:          q2,
	}

	gqm.ResetQuotasForHookPlugins(quotas)

	// validate that hooks were called for all quotas
	preUpdateQuotas := mockHook.GetPreQuotaUpdateQuotas()
	postUpdateQuotas := mockHook.GetPostQuotaUpdateQuotas()
	assert.Equal(t, 3, len(preUpdateQuotas), "PreQuotaUpdate should be called for all 3 quotas")
	assert.Equal(t, 3, len(postUpdateQuotas), "PostQuotaUpdate should be called for all 3 quotas")

	// validate that the correct quotas were processed
	expectedQuotas := []string{parentQuota.Name, q1.Name, q2.Name}
	for _, quota := range expectedQuotas {
		assert.Contains(t, preUpdateQuotas, quota, "Quota %s should be in preUpdateQuotas", quota)
		assert.Contains(t, postUpdateQuotas, quota, "Quota %s should be in postUpdateQuotas", quota)
	}

	// Test case 2: Reset with missing quota (should skip inconsistent quota)
	resetHookFn()

	// Remove one quota from the input map
	quotasWithMissing := map[string]*v1alpha1.ElasticQuota{
		parentQuota.Name: parentQuota,
		q1.Name:          q1,
		// q2 is missing
	}

	gqm.ResetQuotasForHookPlugins(quotasWithMissing)

	// validate that hooks were called only for present quotas
	preUpdateQuotas = mockHook.GetPreQuotaUpdateQuotas()
	postUpdateQuotas = mockHook.GetPostQuotaUpdateQuotas()
	assert.Equal(t, 2, len(preUpdateQuotas), "PreQuotaUpdate should be called for 2 present quotas")
	assert.Equal(t, 2, len(postUpdateQuotas), "PostQuotaUpdate should be called for 2 present quotas")

	// validate that only present quotas were processed
	expectedQuotas = []string{parentQuota.Name, q1.Name}
	for _, quota := range expectedQuotas {
		assert.Contains(t, preUpdateQuotas, quota, "Quota %s should be in preUpdateQuotas", quota)
		assert.Contains(t, postUpdateQuotas, quota, "Quota %s should be in postUpdateQuotas", quota)
	}

	// Test case 3: Reset with empty quotas map
	resetHookFn()

	emptyQuotas := map[string]*v1alpha1.ElasticQuota{}
	gqm.ResetQuotasForHookPlugins(emptyQuotas)

	// validate that no hooks were called
	preUpdateQuotas = mockHook.GetPreQuotaUpdateQuotas()
	postUpdateQuotas = mockHook.GetPostQuotaUpdateQuotas()
	assert.Equal(t, 0, len(preUpdateQuotas), "No PreQuotaUpdate quotas expected")
	assert.Equal(t, 0, len(postUpdateQuotas), "No PostQuotaUpdate quotas expected")
}

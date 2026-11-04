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
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestReadQuotaChain verifies traversal, callback errors, and the hierarchy read-lock lifetime.
// Each case builds a quota hierarchy, captures its path while probing the callback's lock scope,
// then checks the path, callback result, and lock release.
func TestReadQuotaChain(t *testing.T) {
	failure := fmt.Errorf("capture failed")
	for _, tt := range []struct {
		name          string
		quotaName     string
		missingParent bool
		callbackError error
		wantNames     []string
	}{
		{name: "leaf", quotaName: "leaf", wantNames: []string{"leaf", "parent", extension.RootQuotaName}},
		{name: "root", quotaName: extension.RootQuotaName, wantNames: []string{extension.RootQuotaName}},
		{name: "missing", quotaName: "missing"},
		{name: "empty name"},
		{name: "missing parent", quotaName: "leaf", missingParent: true, wantNames: []string{"leaf"}},
		{name: "callback error", quotaName: "leaf", callbackError: failure, wantNames: []string{"leaf", "parent", extension.RootQuotaName}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Build leaf -> parent -> root, then apply the case's missing-parent condition.
			gqm := NewGroupQuotaManager("", true, nil, nil)
			require.NoError(t, gqm.UpdateQuota(CreateQuota("parent", extension.RootQuotaName, 100, 100, 10, 10, true, false)))
			require.NoError(t, gqm.UpdateQuota(CreateQuota("leaf", "parent", 50, 50, 5, 5, false, false)))
			if tt.missingParent {
				// Keep the leaf's parent reference to exercise traversal of an incomplete hierarchy.
				delete(gqm.quotaInfoMap, "parent")
			}
			reader := gqm.GetQuotaInfoReader()
			var names []string
			calls := 0
			err := reader.ReadQuotaChain(tt.quotaName, func(chain []*QuotaInfo) error {
				calls++
				// Together, these probes distinguish a held read lock from a write lock or no lock.
				locked := gqm.hierarchyUpdateLock.TryLock()
				if locked {
					gqm.hierarchyUpdateLock.Unlock()
				}
				require.False(t, locked, "topology must stay stable during capture")
				readLocked := gqm.hierarchyUpdateLock.TryRLock()
				if readLocked {
					gqm.hierarchyUpdateLock.RUnlock()
				}
				require.True(t, readLocked, "reader must allow concurrent readers")
				for _, qi := range chain {
					names = append(names, qi.Name)
					require.Same(t, qi, reader.GetQuotaInfoNoLock(qi.Name))
				}
				return tt.callbackError
			})
			// Check the captured path and callback result after the reader returns.
			require.Equal(t, 1, calls)
			if tt.callbackError != nil {
				require.Same(t, tt.callbackError, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantNames, names)
			// Callback errors must not leave the hierarchy locked either.
			locked := gqm.hierarchyUpdateLock.TryLock()
			if locked {
				gqm.hierarchyUpdateLock.Unlock()
			}
			require.True(t, locked, "reader must release the hierarchy lock")
		})
	}
}

// TestReadQuotaChainWithHookLocks verifies that quota updates wait for capture and subsequent reads observe the updated state and path.
// Pause a reader holding hierarchy and plugin read locks, start a quota update, and confirm its write lock is waiting.
// Release the reader to check the old state, wait for the update, then read again to check the new state and path.
func TestReadQuotaChainWithHookLocks(t *testing.T) {
	for _, tt := range []struct {
		name   string
		parent string
		cpu    int64
	}{
		{name: "max update", parent: "old-parent", cpu: 60},
		{name: "parent update", parent: "new-parent", cpu: 50},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Prepare both parent quotas and plugin state before starting concurrent operations.
			gqm := NewGroupQuotaManager("", true, nil, nil)
			require.NoError(t, gqm.UpdateQuota(CreateQuota("old-parent", extension.RootQuotaName, 100, 100, 10, 10, true, false)))
			require.NoError(t, gqm.UpdateQuota(CreateQuota("new-parent", extension.RootQuotaName, 100, 100, 10, 10, true, false)))
			require.NoError(t, gqm.UpdateQuota(CreateQuota("leaf", "old-parent", 50, 50, 5, 5, false, false)))
			var pluginLock sync.RWMutex
			var revision int
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			// Release the paused callback even if a synchronization assertion fails.
			defer unblock()
			hook := &MockHookPlugin{key: "chain-reader"}
			hook.SetPreQuotaUpdateValidateFn(func(_, newQuotaInfo *QuotaInfo, _ *v1alpha1.ElasticQuota, _ *QuotaUpdateState) {
				// UpdateQuota holds the hierarchy write lock before invoking this hook.
				pluginLock.Lock()
				defer pluginLock.Unlock()
				// Reparenting invokes delete and create hooks; count only the new quota state.
				if newQuotaInfo != nil {
					revision++
				}
			})
			gqm.SetHookPlugins([]QuotaHookPlugin{hook})
			read := func(wantRevision int, wantCPU int64, wantParent string, pause bool) error {
				return gqm.GetQuotaInfoReader().ReadQuotaChain("leaf", func(chain []*QuotaInfo) error {
					// Match the hook's lock order: hierarchy first, plugin second.
					pluginLock.RLock()
					defer pluginLock.RUnlock()
					if pause {
						// Signal that both locks are held, then keep them until the writer is waiting.
						close(entered)
						<-release
					}
					// Check node order and parent links to reject a path mixing topology versions.
					if len(chain) != 3 || chain[0].Name != "leaf" || chain[1].Name != wantParent || chain[2].Name != extension.RootQuotaName {
						return fmt.Errorf("unexpected quota chain: %v", chain)
					}
					if chain[0].ParentName != wantParent || chain[1].ParentName != extension.RootQuotaName {
						return fmt.Errorf("unexpected parent links in quota chain")
					}
					max := chain[0].GetMax()
					if revision != wantRevision || max.Cpu().Value() != wantCPU {
						return fmt.Errorf("captured revision %d and CPU %d, want %d and %d", revision, max.Cpu().Value(), wantRevision, wantCPU)
					}
					return nil
				})
			}
			readDone := make(chan error, 1)
			updateDone := make(chan error, 1)
			// Hold both read locks until the quota update is waiting on the hierarchy lock.
			go func() { readDone <- read(0, 50, "old-parent", true) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not acquire hierarchy and plugin locks")
			}
			go func() {
				updateDone <- gqm.UpdateQuota(CreateQuota("leaf", tt.parent, tt.cpu, 50, 5, 5, false, false))
			}()
			// A waiting writer blocks new readers. Since the callback still holds RLock,
			// TryRLock failing proves UpdateQuota is waiting rather than merely started.
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for gqm.hierarchyUpdateLock.TryRLock() {
				gqm.hierarchyUpdateLock.RUnlock()
				select {
				case <-deadline.C:
					t.Fatal("quota update did not wait for the hierarchy lock")
				default:
					// Let the updater run while polling the lock state without a fixed sleep.
					runtime.Gosched()
				}
			}
			// Let the callback validate the old state, then allow the waiting update to complete.
			unblock()
			for _, done := range []<-chan error{readDone, updateDone} {
				select {
				case err := <-done:
					require.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Fatal("quota reader and update hook did not complete")
				}
			}
			// A fresh read must observe the updated plugin state, configuration, and complete path.
			go func() { readDone <- read(1, tt.cpu, tt.parent, false) }()
			select {
			case err := <-readDone:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not complete after the quota update")
			}
		})
	}
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

// batchEventLog records the sequence of hook callbacks across one or more
// plugins so tests can assert ordering of OnBatchResetBegin/End relative to
// PreQuotaUpdate/PostQuotaUpdate.
type batchEventLog struct {
	sync.Mutex
	events []string
}

func (l *batchEventLog) record(ev string) {
	l.Lock()
	defer l.Unlock()
	l.events = append(l.events, ev)
}

func (l *batchEventLog) snapshot() []string {
	l.Lock()
	defer l.Unlock()
	out := make([]string, len(l.events))
	copy(out, l.events)
	return out
}

// batchAwareMockHook is a MockHookPlugin that additionally implements
// BatchResetAware. It records events into a shared log to verify ordering.
type batchAwareMockHook struct {
	*MockHookPlugin
	log *batchEventLog
}

var _ BatchResetAware = &batchAwareMockHook{}

func (b *batchAwareMockHook) OnBatchResetBegin() {
	b.log.record(b.GetKey() + ":begin")
}

func (b *batchAwareMockHook) OnBatchResetEnd() {
	b.log.record(b.GetKey() + ":end")
}

func (b *batchAwareMockHook) PreQuotaUpdate(oldQ, newQ *QuotaInfo, q *v1alpha1.ElasticQuota,
	state *QuotaUpdateState) {
	b.log.record(b.GetKey() + ":pre")
	b.MockHookPlugin.PreQuotaUpdate(oldQ, newQ, q, state)
}

func (b *batchAwareMockHook) PostQuotaUpdate(oldQ, newQ *QuotaInfo, q *v1alpha1.ElasticQuota,
	state *QuotaUpdateState) {
	b.log.record(b.GetKey() + ":post")
	b.MockHookPlugin.PostQuotaUpdate(oldQ, newQ, q, state)
}

// loggingMockHook is a MockHookPlugin that does NOT implement BatchResetAware,
// used to verify collectBatchResetAware correctly skips non-aware plugins.
type loggingMockHook struct {
	*MockHookPlugin
	log *batchEventLog
}

func (l *loggingMockHook) PreQuotaUpdate(oldQ, newQ *QuotaInfo, q *v1alpha1.ElasticQuota,
	state *QuotaUpdateState) {
	l.log.record(l.GetKey() + ":pre")
	l.MockHookPlugin.PreQuotaUpdate(oldQ, newQ, q, state)
}

func (l *loggingMockHook) PostQuotaUpdate(oldQ, newQ *QuotaInfo, q *v1alpha1.ElasticQuota,
	state *QuotaUpdateState) {
	l.log.record(l.GetKey() + ":post")
	l.MockHookPlugin.PostQuotaUpdate(oldQ, newQ, q, state)
}

// TestResetQuotasForHookPlugins_BatchResetAware asserts that
// ResetQuotasForHookPlugins:
//  1. Calls OnBatchResetBegin on every BatchResetAware plugin BEFORE the loop.
//  2. Calls OnBatchResetEnd on every BatchResetAware plugin AFTER the loop.
//  3. Does not invoke Begin/End on plugins that don't implement BatchResetAware.
func TestResetQuotasForHookPlugins_BatchResetAware(t *testing.T) {
	log := &batchEventLog{}
	const (
		awareKey = "awareHook"
		plainKey = "plainHook"
		awareFK  = "awareFactory"
		plainFK  = "plainFactory"
	)
	RegisterHookPluginFactory(awareFK,
		func(_ *QuotaInfoReader, key, _ string) (QuotaHookPlugin, error) {
			return &batchAwareMockHook{MockHookPlugin: &MockHookPlugin{key: key}, log: log}, nil
		})
	RegisterHookPluginFactory(plainFK,
		func(_ *QuotaInfoReader, key, _ string) (QuotaHookPlugin, error) {
			return &loggingMockHook{MockHookPlugin: &MockHookPlugin{key: key}, log: log}, nil
		})

	gqm := NewGroupQuotaManager("", true, nil, nil)
	err := gqm.InitHookPlugins(&config.ElasticQuotaArgs{
		HookPlugins: []config.HookPluginConf{
			{Key: awareKey, FactoryKey: awareFK},
			{Key: plainKey, FactoryKey: plainFK},
		},
	})
	assert.NoError(t, err)

	// register two quotas so the loop body fires more than once
	parent := CreateQuota("p", extension.RootQuotaName, 100, 100, 0, 0, false, false)
	assert.NoError(t, gqm.UpdateQuota(parent))
	c1 := CreateQuota("c1", parent.Name, 40, 40, 10, 10, false, false)
	assert.NoError(t, gqm.UpdateQuota(c1))

	// reset log to ignore events from the registration phase above
	log.Lock()
	log.events = nil
	log.Unlock()

	quotas := map[string]*v1alpha1.ElasticQuota{
		parent.Name: parent,
		c1.Name:     c1,
	}
	gqm.ResetQuotasForHookPlugins(quotas)

	events := log.snapshot()

	// extract events for the aware hook and verify ordering
	var awareEvents []string
	for _, ev := range events {
		if len(ev) >= len(awareKey) && ev[:len(awareKey)] == awareKey {
			awareEvents = append(awareEvents, ev[len(awareKey)+1:])
		}
	}
	assert.GreaterOrEqual(t, len(awareEvents), 5, "aware hook should record begin + (pre+post)*N + end")
	assert.Equal(t, "begin", awareEvents[0], "first aware event must be begin")
	assert.Equal(t, "end", awareEvents[len(awareEvents)-1], "last aware event must be end")
	for _, ev := range awareEvents[1 : len(awareEvents)-1] {
		assert.Contains(t, []string{"pre", "post"}, ev,
			"events between begin and end must be pre/post only")
	}

	// plain hook should NOT receive begin/end (it doesn't implement BatchResetAware)
	for _, ev := range events {
		if len(ev) >= len(plainKey) && ev[:len(plainKey)] == plainKey {
			suffix := ev[len(plainKey)+1:]
			assert.NotEqual(t, "begin", suffix, "plain hook must not receive begin")
			assert.NotEqual(t, "end", suffix, "plain hook must not receive end")
		}
	}
}

// TestCollectBatchResetAware_UnwrapsMetricsWrapper verifies that
// collectBatchResetAware correctly unwraps MetricsWrapper before checking
// for the BatchResetAware capability.
func TestCollectBatchResetAware_UnwrapsMetricsWrapper(t *testing.T) {
	log := &batchEventLog{}
	aware := &batchAwareMockHook{MockHookPlugin: &MockHookPlugin{key: "aware"}, log: log}
	plain := &MockHookPlugin{key: "plain"}
	wrapped := []QuotaHookPlugin{
		NewMetricsWrapper(aware),
		NewMetricsWrapper(plain),
	}

	got := collectBatchResetAware(wrapped)
	assert.Equal(t, 1, len(got), "only the aware plugin should be collected")

	// invoke to confirm the right plugin was collected
	got[0].OnBatchResetBegin()
	got[0].OnBatchResetEnd()
	assert.Equal(t, []string{"aware:begin", "aware:end"}, log.snapshot())
}

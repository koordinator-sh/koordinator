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

package elasticquota

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schetesting "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

// TestOnQuotaStatusUpdatedHook tests the OnQuotaStatusUpdatedHook method
func TestUpdateQuotaStatusHook(t *testing.T) {
	// init plugin and gqm
	suit, plugin := initPluginSuit(t)
	gqm := plugin.groupQuotaManager
	assert.Equal(t, 1, len(gqm.GetHookPlugins()))
	ctrl := NewElasticQuotaController(plugin)

	// init quota
	q1 := CreateQuota2("q1", extension.RootQuotaName, 10, 10, 0, 0, 0, 0, false, "")
	plugin.OnQuotaAdd(q1)
	gotQ1, err := suit.client.SchedulingV1alpha1().ElasticQuotas(q1.Namespace).Create(
		context.TODO(), q1, metav1.CreateOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, gotQ1)

	// wait for the new quota can be seen
	retryNum := 0
	for retryNum < 100 {
		elasticQuotas, err := ctrl.plugin.quotaLister.List(labels.Everything())
		assert.NoError(t, err)
		if len(elasticQuotas) == 4 {
			break
		}
		retryNum++
		time.Sleep(time.Millisecond * 10)
	}

	// validate OnQuotaStatusUpdated
	mockHook := gqm.GetHookPlugins()[0].(*core.MetricsWrapper).GetPlugin().(*MockHookPlugin)
	mockHook.SetUpdateQuotaStatusValidateFn(func(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota {
		assert.NotNil(t, oldQuota, "Old quota should not be nil")
		if oldQuota.Name == q1.Name {
			// update quota
			updatedQuota := oldQuota.DeepCopy()
			assert.Equal(t, "", updatedQuota.GetAnnotations()["k1"])
			updatedQuota.SetAnnotations(map[string]string{"k1": "v1"})
			return updatedQuota
		}
		return nil
	})
	ctrl.syncElasticQuotaStatusWorker()
	assert.True(t, mockHook.IsUpdateQuotaStatusCalled(), "UpdateQuotaStatus should be called")

	eq, err := suit.client.SchedulingV1alpha1().ElasticQuotas(q1.Namespace).Get(
		context.TODO(), q1.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, eq)
	assert.Equal(t, "v1", eq.GetAnnotations()["k1"])
}

// TestCheckPodHook tests the CheckPodHook method
func TestCheckPodHook(t *testing.T) {
	// init plugin and gqm
	_, plugin := initPluginSuit(t)
	gqm := plugin.groupQuotaManager
	assert.Equal(t, 1, len(gqm.GetHookPlugins()))

	// init quota
	q1 := CreateQuota2("q1", extension.RootQuotaName, 10, 10, 0, 0, 0, 0, false, "")
	plugin.OnQuotaAdd(q1)

	// validate CheckPod
	mockHook := gqm.GetHookPlugins()[0].(*core.MetricsWrapper).GetPlugin().(*MockHookPlugin)
	mockHook.SetCheckPodFn(func(quotaName string, pod *v1.Pod) error {
		return fmt.Errorf("test error")
	})

	// validate Pod Create operation: add pod with assigned node
	pod1 := schetesting.MakePod().Name("pod1").Label(extension.LabelQuotaName, q1.Name).Containers(
		[]v1.Container{schetesting.MakeContainer().Name("0").Resources(map[v1.ResourceName]string{
			v1.ResourceCPU: "2"}).Obj()}).Obj()
	_, status := plugin.PreFilter(context.TODO(), framework.NewCycleState(), pod1)
	assert.True(t, status.IsUnschedulable(), "Pod should be unschedulable")
	assert.Equal(t, "CheckPod failed for hook plugin mockPlugin, err: test error", status.Message())
}

// TestUpdateQuota_IsQuotaUpdated tests the IsQuotaUpdated condition in UpdateQuota method
func TestUpdateQuota_IsQuotaUpdated(t *testing.T) {
	// init plugin and gqm
	_, plugin := initPluginSuit(t)
	gqm := plugin.groupQuotaManager
	assert.Equal(t, 1, len(gqm.GetHookPlugins()))

	// init quota
	q1 := CreateQuota2("q1", extension.RootQuotaName, 10, 10, 0, 0, 0, 0, false, "")
	plugin.OnQuotaAdd(q1)

	// get mock hook plugin
	mockHook := gqm.GetHookPlugins()[0].(*core.MetricsWrapper).GetPlugin().(*MockHookPlugin)

	// test case 1: quota has no changes and IsQuotaUpdated returns false
	// PreQuotaUpdate and PostQuotaUpdate should not be called
	mockHook.Reset()
	mockHook.SetIsQuotaUpdatedFn(func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
		return false
	})

	// update with the same quota (no changes)
	plugin.OnQuotaUpdate(q1, q1)
	assert.Equal(t, 0, len(mockHook.GetPreQuotaUpdateQuotas()), "PreQuotaUpdate should not be called when quota unchanged and IsQuotaUpdated returns false")
	assert.Equal(t, 0, len(mockHook.GetPostQuotaUpdateQuotas()), "PostQuotaUpdate should not be called when quota unchanged and IsQuotaUpdated returns false")

	// test case 2: quota has no changes but IsQuotaUpdated returns true
	// PreQuotaUpdate and PostQuotaUpdate should be called
	mockHook.Reset()
	mockHook.SetIsQuotaUpdatedFn(func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
		// verify that the hook receives correct parameters
		assert.NotNil(t, oldQuotaInfo, "oldQuotaInfo should not be nil")
		assert.NotNil(t, newQuotaInfo, "newQuotaInfo should not be nil")
		assert.NotNil(t, newQuota, "newQuota should not be nil")
		assert.Equal(t, oldQuotaInfo.Name, newQuotaInfo.Name, "newQuotaInfo name should match")
		assert.Equal(t, newQuotaInfo.Name, newQuota.Name, "newQuota name should match")
		return true
	})

	// validation functions for PreQuotaUpdate and PostQuotaUpdate
	validateHookParameters := func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, quota *v1alpha1.ElasticQuota, state *core.QuotaUpdateState) {
		assert.NotNil(t, oldQuotaInfo, "oldQuotaInfo should not be nil")
		assert.NotNil(t, newQuotaInfo, "newQuotaInfo should not be nil")
		assert.NotNil(t, quota, "quota should not be nil")
		assert.NotNil(t, state, "state should not be nil")
	}

	mockHook.SetPreQuotaUpdateValidateFn(validateHookParameters)
	mockHook.SetPostQuotaUpdateValidateFn(validateHookParameters)

	// update with the same quota (no changes), but IsQuotaUpdated returns true
	plugin.OnQuotaUpdate(q1, q1)
	assert.True(t, len(mockHook.GetPreQuotaUpdateQuotas()) > 0,
		"PreQuotaUpdate should be called when IsQuotaUpdated returns true")
	assert.True(t, len(mockHook.GetPostQuotaUpdateQuotas()) > 0,
		"PostQuotaUpdate should be called when IsQuotaUpdated returns true")

	// test case 3: quota has changes, IsQuotaUpdated should not affect the result
	// PreQuotaUpdate and PostQuotaUpdate should be called regardless of IsQuotaUpdated return value
	mockHook.Reset()
	mockHook.SetIsQuotaUpdatedFn(func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
		return false // Even if this returns false, hooks should still be called because quota has changes
	})

	// create a modified quota
	q1Modified := q1.DeepCopy()
	q1Modified.Spec.Max = v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("20"),
		v1.ResourceMemory: resource.MustParse("20Gi"),
	}

	validateHookParameters = func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo,
		quota *v1alpha1.ElasticQuota, state *core.QuotaUpdateState) {
		assert.NotNil(t, oldQuotaInfo, "oldQuotaInfo should not be nil")
		assert.NotNil(t, newQuotaInfo, "newQuotaInfo should not be nil")
		assert.NotNil(t, quota, "quota should not be nil")
		assert.NotNil(t, state, "state should not be nil")
		if quota.Name != q1Modified.Name {
			return
		}
		assert.Equal(t, q1Modified.Name, quota.Name, "quota name should match")
		// verify that the new quota has the updated max values
		assert.True(t, quotav1.Equals(newQuotaInfo.CalculateInfo.Max, q1Modified.Spec.Max),
			"newQuotaInfo should have updated max values")
	}
	mockHook.SetPreQuotaUpdateValidateFn(validateHookParameters)
	mockHook.SetPostQuotaUpdateValidateFn(validateHookParameters)

	// update with modified quota
	plugin.OnQuotaUpdate(q1, q1Modified)
	assert.True(t, len(mockHook.GetPreQuotaUpdateQuotas()) > 0, "PreQuotaUpdate should be called when quota has changes")
	assert.True(t, len(mockHook.GetPostQuotaUpdateQuotas()) > 0, "PostQuotaUpdate should be called when quota has changes")
}

func TestReplaceQuotasWithHookPlugins(t *testing.T) {
	// register a mock hook plugin factory for testing
	core.RegisterHookPluginFactory("mockFactory",
		func(qiProvider *core.QuotaInfoReader, key, args string) (core.QuotaHookPlugin, error) {
			return &MockHookPlugin{key: key}, nil
		})

	// create test suit with hook plugin configuration
	suit := newPluginTestSuit(t, nil,
		func(elasticQuotaArgs *config.ElasticQuotaArgs) {
			elasticQuotaArgs.HookPlugins = []config.HookPluginConf{
				{
					Key:        "mockPlugin",
					FactoryKey: "mockFactory",
				},
			}
		})
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	// prepare test quotas
	quotas := []interface{}{
		CreateQuota2("test1", extension.RootQuotaName, 100, 200, 40, 80, 1, 1, true, ""),
		CreateQuota2("test2", extension.RootQuotaName, 200, 400, 80, 160, 1, 1, true, ""),
		CreateQuota2("test11", "test1", 100, 200, 40, 80, 1, 1, false, ""),
	}

	// ReplaceQuotas will conflict with QuotaEventHandler. sleep 1 seconds to avoid it.
	time.Sleep(time.Second)
	// call ReplaceQuotas which should trigger ResetQuotasForHookPlugins
	err = plugin.ReplaceQuotas(quotas)
	assert.Nil(t, err)

	// verify hook plugins were called
	hookPlugins := plugin.groupQuotaManager.GetHookPlugins()
	assert.Equal(t, 1, len(hookPlugins))

	mockHook := hookPlugins[0].(*core.MetricsWrapper).GetPlugin().(*MockHookPlugin)

	// verify that the quotas were processed correctly
	expectedQuotaNames := []string{"test1", "test2", "test11"}
	preQuotas := mockHook.GetPreQuotaUpdateQuotas()
	postQuotas := mockHook.GetPostQuotaUpdateQuotas()
	for _, expectedName := range expectedQuotaNames {
		assert.Contains(t, preQuotas, expectedName,
			"Quota %s should be processed in PreQuotaUpdate", expectedName)
		assert.Contains(t, postQuotas, expectedName,
			"Quota %s should be processed in PostQuotaUpdate", expectedName)
	}
}

func initPluginSuit(t *testing.T) (*pluginTestSuit, *Plugin) {
	core.RegisterHookPluginFactory("mockFactory",
		func(qiProvider *core.QuotaInfoReader, key, args string) (core.QuotaHookPlugin, error) {
			return &MockHookPlugin{key: key}, nil
		})
	suit := newPluginTestSuit(t, nil,
		func(elasticQuotaArgs *config.ElasticQuotaArgs) {
			elasticQuotaArgs.EnableRuntimeQuota = false
			elasticQuotaArgs.HookPlugins = []config.HookPluginConf{
				{
					Key:        "mockPlugin",
					FactoryKey: "mockFactory",
				},
			}
		})
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)
	return suit, plugin
}

var _ core.QuotaHookPlugin = &MockHookPlugin{}

// MockHookPlugin is a mock implementation of QuotaHookPlugin
type MockHookPlugin struct {
	key string

	updateQuotaStatusCalled     bool
	updateQuotaStatusValidateFn func(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota

	checkPodCalled bool
	checkPodFn     func(quotaName string, pod *v1.Pod) error

	preQuotaUpdateQuotas     []string
	preQuotaUpdateValidateFn func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, quota *v1alpha1.ElasticQuota, state *core.QuotaUpdateState)

	postQuotaUpdateQuotas     []string
	postQuotaUpdateValidateFn func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, quota *v1alpha1.ElasticQuota, state *core.QuotaUpdateState)

	isQuotaUpdatedFn func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool

	sync.RWMutex
}

func (m *MockHookPlugin) Reset() {
	m.Lock()
	defer m.Unlock()

	m.updateQuotaStatusCalled = false
	m.checkPodCalled = false
	m.preQuotaUpdateQuotas = nil
	m.postQuotaUpdateQuotas = nil
	m.preQuotaUpdateValidateFn = nil
	m.postQuotaUpdateValidateFn = nil
	m.isQuotaUpdatedFn = nil
}

// Helper methods to safely set function fields with write lock protection
func (m *MockHookPlugin) SetUpdateQuotaStatusValidateFn(fn func(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota) {
	m.Lock()
	defer m.Unlock()
	m.updateQuotaStatusValidateFn = fn
}

func (m *MockHookPlugin) SetCheckPodFn(fn func(quotaName string, pod *v1.Pod) error) {
	m.Lock()
	defer m.Unlock()
	m.checkPodFn = fn
}

func (m *MockHookPlugin) SetIsQuotaUpdatedFn(fn func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool) {
	m.Lock()
	defer m.Unlock()
	m.isQuotaUpdatedFn = fn
}

func (m *MockHookPlugin) SetPreQuotaUpdateValidateFn(fn func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, quota *v1alpha1.ElasticQuota, state *core.QuotaUpdateState)) {
	m.Lock()
	defer m.Unlock()
	m.preQuotaUpdateValidateFn = fn
}

func (m *MockHookPlugin) SetPostQuotaUpdateValidateFn(fn func(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, quota *v1alpha1.ElasticQuota, state *core.QuotaUpdateState)) {
	m.Lock()
	defer m.Unlock()
	m.postQuotaUpdateValidateFn = fn
}

func (m *MockHookPlugin) GetKey() string {
	m.RLock()
	defer m.RUnlock()
	return m.key
}

func (m *MockHookPlugin) UpdateQuotaStatus(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota {
	m.Lock()
	m.updateQuotaStatusCalled = true
	validateFn := m.updateQuotaStatusValidateFn
	m.Unlock()

	if validateFn != nil {
		return validateFn(oldQuota, newQuota)
	}
	return nil
}

func (m *MockHookPlugin) CheckPod(quotaName string, pod *v1.Pod) error {
	m.Lock()
	m.checkPodCalled = true
	checkFn := m.checkPodFn
	m.Unlock()

	if checkFn != nil {
		return checkFn(quotaName, pod)
	}
	return nil
}

// The following methods of MockHookPlugin are tested in the core package instead of this file

func (m *MockHookPlugin) IsQuotaUpdated(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
	m.RLock()
	isQuotaUpdatedFn := m.isQuotaUpdatedFn
	m.RUnlock()

	if isQuotaUpdatedFn != nil {
		return isQuotaUpdatedFn(oldQuotaInfo, newQuotaInfo, newQuota)
	}
	return false
}

func (m *MockHookPlugin) PreQuotaUpdate(oldQuotaInfo, newQuotaInfo *core.QuotaInfo,
	quota *v1alpha1.ElasticQuota, state *core.QuotaUpdateState) {
	m.Lock()
	m.preQuotaUpdateQuotas = append(m.preQuotaUpdateQuotas, quota.Name)
	validateFn := m.preQuotaUpdateValidateFn
	m.Unlock()

	if validateFn != nil {
		validateFn(oldQuotaInfo, newQuotaInfo, quota, state)
	}
}

func (m *MockHookPlugin) PostQuotaUpdate(oldQuotaInfo, newQuotaInfo *core.QuotaInfo, quota *v1alpha1.ElasticQuota,
	state *core.QuotaUpdateState) {
	m.Lock()
	m.postQuotaUpdateQuotas = append(m.postQuotaUpdateQuotas, quota.Name)
	validateFn := m.postQuotaUpdateValidateFn
	m.Unlock()

	if validateFn != nil {
		validateFn(oldQuotaInfo, newQuotaInfo, quota, state)
	}
}

func (m *MockHookPlugin) OnPodUpdated(_ string, _, _ *v1.Pod) {
}

func (m *MockHookPlugin) IsUpdateQuotaStatusCalled() bool {
	m.RLock()
	defer m.RUnlock()
	return m.updateQuotaStatusCalled
}

func (m *MockHookPlugin) IsCheckPodCalled() bool {
	m.RLock()
	defer m.RUnlock()
	return m.checkPodCalled
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

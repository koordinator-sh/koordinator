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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	mockHook.UpdateQuotaStatusValidateFn = func(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota {
		assert.NotNil(t, oldQuota, "Old quota should not be nil")
		if oldQuota.Name == q1.Name {
			// update quota
			updatedQuota := oldQuota.DeepCopy()
			assert.Equal(t, "", updatedQuota.GetAnnotations()["k1"])
			updatedQuota.SetAnnotations(map[string]string{"k1": "v1"})
			return updatedQuota
		}
		return nil
	}
	ctrl.syncElasticQuotaStatusWorker()
	assert.True(t, mockHook.UpdateQuotaStatusCalled, "UpdateQuotaStatus should be called")

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
	mockHook.CheckPodFn = func(quotaName string, pod *v1.Pod) error {
		return fmt.Errorf("test error")
	}

	// validate Pod Create operation: add pod with assigned node
	pod1 := schetesting.MakePod().Name("pod1").Label(extension.LabelQuotaName, q1.Name).Containers(
		[]v1.Container{schetesting.MakeContainer().Name("0").Resources(map[v1.ResourceName]string{
			v1.ResourceCPU: "2"}).Obj()}).Obj()
	_, status := plugin.PreFilter(context.TODO(), framework.NewCycleState(), pod1)
	assert.True(t, status.IsUnschedulable(), "Pod should be unschedulable")
	assert.Equal(t, "test error", status.Message())
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

	UpdateQuotaStatusCalled     bool
	UpdateQuotaStatusValidateFn func(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota

	CheckPodCalled bool
	CheckPodFn     func(quotaName string, pod *v1.Pod) error
}

func (m *MockHookPlugin) Reset() {
	m.CheckPodCalled = false
}

func (m *MockHookPlugin) GetKey() string {
	return m.key
}

func (m *MockHookPlugin) UpdateQuotaStatus(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota {
	m.UpdateQuotaStatusCalled = true
	if m.UpdateQuotaStatusValidateFn != nil {
		return m.UpdateQuotaStatusValidateFn(oldQuota, newQuota)
	}
	return nil
}

func (m *MockHookPlugin) CheckPod(quotaName string, pod *v1.Pod) error {
	return m.CheckPodFn(quotaName, pod)
}

// The following methods of MockHookPlugin are tested in the core package instead of this file

func (m *MockHookPlugin) IsQuotaUpdated(_, _ *core.QuotaInfo, _ *v1alpha1.ElasticQuota) bool {
	return false
}

func (m *MockHookPlugin) PreQuotaUpdate(_ *core.QuotaInfo, _ *core.QuotaInfo, _ *v1alpha1.ElasticQuota,
	_ *core.QuotaUpdateState) {
}

func (m *MockHookPlugin) PostQuotaUpdate(_ *core.QuotaInfo, _ *core.QuotaInfo, _ *v1alpha1.ElasticQuota,
	_ *core.QuotaUpdateState) {
}

func (m *MockHookPlugin) OnPodUpdated(_ string, _, _ *v1.Pod) {
}

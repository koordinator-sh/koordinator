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

package defaultprofile

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwktype "k8s.io/kube-scheduler/framework"
	kubeschedulerconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	pluginNames "k8s.io/kubernetes/pkg/scheduler/framework/plugins/names"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	kubeschedmetrics "k8s.io/kubernetes/pkg/scheduler/metrics"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/defaultprebind"
)

func init() {
	kubeschedmetrics.Register()
}

func TestAppendDefaultPlugins(t *testing.T) {
	tests := []struct {
		name         string
		profiles     []kubeschedulerconfig.KubeSchedulerProfile
		wantProfiles []kubeschedulerconfig.KubeSchedulerProfile
	}{
		{
			name: "empty profile",
			profiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{},
			},
			wantProfiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{},
			},
		},
		{
			name: "append defaultPreBindExtension plugin",
			profiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{
					Plugins: &kubeschedulerconfig.Plugins{},
				},
			},
			wantProfiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{
					Plugins: &kubeschedulerconfig.Plugins{
						PreBind: kubeschedulerconfig.PluginSet{
							Enabled: []kubeschedulerconfig.Plugin{
								{
									Name: defaultprebind.Name,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "disable defaultPreBindExtension plugin",
			profiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{
					Plugins: &kubeschedulerconfig.Plugins{
						PreBind: kubeschedulerconfig.PluginSet{
							Disabled: []kubeschedulerconfig.Plugin{
								{
									Name: defaultprebind.Name,
								},
							},
						},
					},
				},
			},
			wantProfiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{
					Plugins: &kubeschedulerconfig.Plugins{
						PreBind: kubeschedulerconfig.PluginSet{
							Disabled: []kubeschedulerconfig.Plugin{
								{
									Name: defaultprebind.Name,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "disable all prebind plugins",
			profiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{
					Plugins: &kubeschedulerconfig.Plugins{
						PreBind: kubeschedulerconfig.PluginSet{
							Disabled: []kubeschedulerconfig.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
			wantProfiles: []kubeschedulerconfig.KubeSchedulerProfile{
				{
					Plugins: &kubeschedulerconfig.Plugins{
						PreBind: kubeschedulerconfig.PluginSet{
							Disabled: []kubeschedulerconfig.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			AppendDefaultPlugins(tt.profiles)
			assert.Equal(t, tt.wantProfiles, tt.profiles)
		})
	}
}

func TestAppendSandboxProfile(t *testing.T) {
	base := kubeschedulerconfig.KubeSchedulerProfile{
		SchedulerName: "koord-scheduler",
		Plugins: &kubeschedulerconfig.Plugins{
			MultiPoint: kubeschedulerconfig.PluginSet{
				Enabled: []kubeschedulerconfig.Plugin{
					{Name: "NodeResourcesFit"},
					{Name: pluginNames.PodTopologySpread},
					{Name: pluginNames.InterPodAffinity},
				},
			},
			PreFilter: kubeschedulerconfig.PluginSet{
				Enabled: []kubeschedulerconfig.Plugin{
					{Name: "NodeResourcesFit"},
					{Name: pluginNames.PodTopologySpread},
				},
				Disabled: []kubeschedulerconfig.Plugin{
					{Name: "ExistingPreFilterPlugin"},
				},
			},
			Filter: kubeschedulerconfig.PluginSet{
				Enabled: []kubeschedulerconfig.Plugin{
					{Name: "NodeResourcesFit"},
					{Name: pluginNames.InterPodAffinity},
				},
			},
			PreScore: kubeschedulerconfig.PluginSet{
				Enabled: []kubeschedulerconfig.Plugin{{Name: "Reservation"}},
			},
			Score: kubeschedulerconfig.PluginSet{
				Enabled: []kubeschedulerconfig.Plugin{{Name: "NodeResourcesFit", Weight: 1}},
			},
			Reserve: kubeschedulerconfig.PluginSet{
				Enabled: []kubeschedulerconfig.Plugin{{Name: "Reservation"}},
			},
		},
		PluginConfig: []kubeschedulerconfig.PluginConfig{
			{Name: "NodeResourcesFit"},
			{Name: "Reservation"},
		},
	}
	original := base.DeepCopy()

	profiles := AppendSandboxProfile([]kubeschedulerconfig.KubeSchedulerProfile{base})

	assert.Len(t, profiles, 2)
	assert.Equal(t, original, &profiles[0])

	sandbox := profiles[1]
	assert.Equal(t, apiext.SandboxSchedulerName, sandbox.SchedulerName)
	assert.Equal(t, original.Plugins.MultiPoint, sandbox.Plugins.MultiPoint)
	assert.Equal(t, original.Plugins.PreFilter, sandbox.Plugins.PreFilter)
	assert.Equal(t, original.Plugins.Filter, sandbox.Plugins.Filter)
	assert.Equal(t, original.Plugins.Reserve, sandbox.Plugins.Reserve)
	assert.Empty(t, sandbox.Plugins.PreScore.Enabled)
	assert.Equal(t, []kubeschedulerconfig.Plugin{{Name: "*"}}, sandbox.Plugins.PreScore.Disabled)
	assert.Empty(t, sandbox.Plugins.Score.Enabled)
	assert.Equal(t, []kubeschedulerconfig.Plugin{{Name: "*"}}, sandbox.Plugins.Score.Disabled)
	assert.Equal(t, original.PluginConfig, sandbox.PluginConfig)
}

func TestAppendSandboxProfileWithoutExplicitPlugins(t *testing.T) {
	profiles := AppendSandboxProfile([]kubeschedulerconfig.KubeSchedulerProfile{
		{SchedulerName: "koord-scheduler"},
	})

	assert.Len(t, profiles, 2)
	assert.Equal(t, apiext.SandboxSchedulerName, profiles[1].SchedulerName)
	assert.NotNil(t, profiles[1].Plugins)
	assert.Empty(t, profiles[1].Plugins.MultiPoint.Enabled)
	assert.Empty(t, profiles[1].Plugins.PreFilter.Enabled)
	assert.Empty(t, profiles[1].Plugins.PreFilter.Disabled)
	assert.Empty(t, profiles[1].Plugins.Filter.Enabled)
	assert.Empty(t, profiles[1].Plugins.Filter.Disabled)
	assert.Equal(t, []kubeschedulerconfig.Plugin{{Name: "*"}}, profiles[1].Plugins.PreScore.Disabled)
	assert.Equal(t, []kubeschedulerconfig.Plugin{{Name: "*"}}, profiles[1].Plugins.Score.Disabled)
}

func TestAppendSandboxProfileDoesNotDuplicateExplicitProfile(t *testing.T) {
	profiles := []kubeschedulerconfig.KubeSchedulerProfile{
		{
			SchedulerName: "koord-scheduler",
			Plugins:       &kubeschedulerconfig.Plugins{},
		},
		{
			SchedulerName: apiext.SandboxSchedulerName,
			Plugins:       &kubeschedulerconfig.Plugins{},
		},
	}

	got := AppendSandboxProfile(profiles)

	assert.Len(t, got, 2)
	assert.Same(t, &profiles[0], &got[0])
	assert.Same(t, &profiles[1], &got[1])
}

type testSandboxProfilePlugin struct{}

func (p *testSandboxProfilePlugin) Name() string {
	return "test-sandbox-profile-plugin"
}

func (p *testSandboxProfilePlugin) Less(_, _ fwktype.QueuedPodInfo) bool {
	return false
}

func (p *testSandboxProfilePlugin) PreScore(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) *fwktype.Status {
	return nil
}

func (p *testSandboxProfilePlugin) Score(context.Context, fwktype.CycleState, *corev1.Pod, fwktype.NodeInfo) (int64, *fwktype.Status) {
	return 0, nil
}

func (p *testSandboxProfilePlugin) ScoreExtensions() fwktype.ScoreExtensions {
	return nil
}

func (p *testSandboxProfilePlugin) PreFilter(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return nil, nil
}

func (p *testSandboxProfilePlugin) PreFilterExtensions() fwktype.PreFilterExtensions {
	return nil
}

func (p *testSandboxProfilePlugin) Filter(context.Context, fwktype.CycleState, *corev1.Pod, fwktype.NodeInfo) *fwktype.Status {
	return nil
}

func (p *testSandboxProfilePlugin) Bind(context.Context, fwktype.CycleState, *corev1.Pod, string) *fwktype.Status {
	return nil
}

type testSandboxFilterPlugin struct {
	name string
}

func (p *testSandboxFilterPlugin) Name() string {
	return p.name
}

func (p *testSandboxFilterPlugin) PreFilter(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return nil, nil
}

func (p *testSandboxFilterPlugin) PreFilterExtensions() fwktype.PreFilterExtensions {
	return nil
}

func (p *testSandboxFilterPlugin) Filter(context.Context, fwktype.CycleState, *corev1.Pod, fwktype.NodeInfo) *fwktype.Status {
	return nil
}

func (p *testSandboxFilterPlugin) PreScore(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) *fwktype.Status {
	return nil
}

func (p *testSandboxFilterPlugin) Score(context.Context, fwktype.CycleState, *corev1.Pod, fwktype.NodeInfo) (int64, *fwktype.Status) {
	return 0, nil
}

func (p *testSandboxFilterPlugin) ScoreExtensions() fwktype.ScoreExtensions {
	return nil
}

func TestAppendSandboxProfilePreservesFilterPluginsInFramework(t *testing.T) {
	factoryCalls := make(map[string]int)
	profiles := AppendSandboxProfile([]kubeschedulerconfig.KubeSchedulerProfile{
		{
			SchedulerName: "koord-scheduler",
			Plugins: &kubeschedulerconfig.Plugins{
				MultiPoint: kubeschedulerconfig.PluginSet{
					Enabled: []kubeschedulerconfig.Plugin{
						{Name: "test-sandbox-filter-plugin"},
						{Name: pluginNames.PodTopologySpread},
						{Name: pluginNames.InterPodAffinity},
					},
				},
				QueueSort: kubeschedulerconfig.PluginSet{
					Enabled: []kubeschedulerconfig.Plugin{{Name: "test-sandbox-profile-plugin"}},
				},
				Bind: kubeschedulerconfig.PluginSet{
					Enabled: []kubeschedulerconfig.Plugin{{Name: "test-sandbox-profile-plugin"}},
				},
			},
		},
	})
	registry := frameworkruntime.Registry{
		"test-sandbox-profile-plugin": func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testSandboxProfilePlugin{}, nil
		},
		"test-sandbox-filter-plugin": func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &testSandboxFilterPlugin{name: "test-sandbox-filter-plugin"}, nil
		},
	}
	for _, name := range []string{pluginNames.PodTopologySpread, pluginNames.InterPodAffinity} {
		name := name
		registry[name] = func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			factoryCalls[name]++
			return &testSandboxFilterPlugin{name: name}, nil
		}
	}

	baseFramework, err := frameworkruntime.NewFramework(context.Background(), registry, &profiles[0])
	assert.NoError(t, err)
	assert.Contains(t, baseFramework.ListPlugins().PreFilter.Enabled, kubeschedulerconfig.Plugin{Name: pluginNames.PodTopologySpread})
	assert.Contains(t, baseFramework.ListPlugins().Filter.Enabled, kubeschedulerconfig.Plugin{Name: pluginNames.InterPodAffinity})
	factoryCalls = make(map[string]int)

	sandboxFramework, err := frameworkruntime.NewFramework(context.Background(), registry, &profiles[1])
	assert.NoError(t, err)

	assert.True(t, baseFramework.HasScorePlugins())
	assert.False(t, sandboxFramework.HasScorePlugins())
	assert.Contains(t, sandboxFramework.ListPlugins().PreFilter.Enabled, kubeschedulerconfig.Plugin{Name: "test-sandbox-filter-plugin"})
	assert.Contains(t, sandboxFramework.ListPlugins().Filter.Enabled, kubeschedulerconfig.Plugin{Name: "test-sandbox-filter-plugin"})
	assert.Contains(t, sandboxFramework.ListPlugins().PreFilter.Enabled, kubeschedulerconfig.Plugin{Name: pluginNames.PodTopologySpread})
	assert.Contains(t, sandboxFramework.ListPlugins().Filter.Enabled, kubeschedulerconfig.Plugin{Name: pluginNames.InterPodAffinity})
	assert.Empty(t, sandboxFramework.ListPlugins().PreScore.Enabled)
	assert.Empty(t, sandboxFramework.ListPlugins().Score.Enabled)
	assert.Equal(t, map[string]int{
		pluginNames.PodTopologySpread: 1,
		pluginNames.InterPodAffinity:  1,
	}, factoryCalls)
}

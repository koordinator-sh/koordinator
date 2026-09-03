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
	corev1 "k8s.io/api/core/v1"
	kubeschedulerconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	pluginNames "k8s.io/kubernetes/pkg/scheduler/framework/plugins/names"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/defaultprebind"
)

const defaultSandboxPercentageOfNodesToScore int32 = 5

func AppendDefaultPlugins(profiles []kubeschedulerconfig.KubeSchedulerProfile) {
	for i := range profiles {
		p := &profiles[i]

		if p.Plugins == nil {
			continue
		}

		hasDisabled := false
		for _, disabled := range p.Plugins.PreBind.Disabled {
			if disabled.Name == "*" || disabled.Name == defaultprebind.Name {
				hasDisabled = true
				break
			}
		}

		found := false
		for _, enabled := range p.Plugins.PreBind.Enabled {
			if enabled.Name == defaultprebind.Name {
				found = true
				break
			}
		}

		if !found && !hasDisabled {
			p.Plugins.PreBind.Enabled = append(p.Plugins.PreBind.Enabled, kubeschedulerconfig.Plugin{
				Name: defaultprebind.Name,
			})
		}
	}
}

// AppendSandboxProfile adds a lightweight scoring profile derived from the first configured profile.
// A user-defined profile with the sandbox scheduler name takes precedence.
func AppendSandboxProfile(profiles []kubeschedulerconfig.KubeSchedulerProfile) []kubeschedulerconfig.KubeSchedulerProfile {
	for _, profile := range profiles {
		if profile.SchedulerName == apiext.SandboxSchedulerName {
			return profiles
		}
	}
	if len(profiles) == 0 {
		return profiles
	}

	sandboxProfile := profiles[0].DeepCopy()
	if sandboxProfile.Plugins == nil {
		sandboxProfile.Plugins = &kubeschedulerconfig.Plugins{}
	}
	sandboxProfile.SchedulerName = apiext.SandboxSchedulerName
	sandboxProfile.PercentageOfNodesToScore = ptr.To(defaultSandboxPercentageOfNodesToScore)
	sandboxProfile.Plugins.PreScore = kubeschedulerconfig.PluginSet{
		Enabled:  []kubeschedulerconfig.Plugin{{Name: pluginNames.NodeResourcesFit}},
		Disabled: []kubeschedulerconfig.Plugin{{Name: "*"}},
	}
	sandboxProfile.Plugins.Score = kubeschedulerconfig.PluginSet{
		Enabled:  []kubeschedulerconfig.Plugin{{Name: pluginNames.NodeResourcesFit, Weight: 1}},
		Disabled: []kubeschedulerconfig.Plugin{{Name: "*"}},
	}
	sandboxProfile.PluginConfig = withSandboxNodeResourcesFitConfig(sandboxProfile.PluginConfig)

	return append(profiles, *sandboxProfile)
}

func withSandboxNodeResourcesFitConfig(pluginConfigs []kubeschedulerconfig.PluginConfig) []kubeschedulerconfig.PluginConfig {
	sandboxPluginConfig := kubeschedulerconfig.PluginConfig{
		Name: pluginNames.NodeResourcesFit,
		Args: &kubeschedulerconfig.NodeResourcesFitArgs{
			ScoringStrategy: &kubeschedulerconfig.ScoringStrategy{
				Type: kubeschedulerconfig.LeastAllocated,
				Resources: []kubeschedulerconfig.ResourceSpec{
					{Name: string(corev1.ResourceCPU), Weight: 1},
					{Name: string(corev1.ResourceMemory), Weight: 1},
					{Name: string(apiext.BatchCPU), Weight: 1},
					{Name: string(apiext.BatchMemory), Weight: 1},
					{Name: string(apiext.MidCPU), Weight: 1},
					{Name: string(apiext.MidMemory), Weight: 1},
				},
			},
		},
	}
	result := make([]kubeschedulerconfig.PluginConfig, 0, len(pluginConfigs)+1)
	replaced := false
	for _, pluginConfig := range pluginConfigs {
		if pluginConfig.Name != pluginNames.NodeResourcesFit {
			result = append(result, pluginConfig)
			continue
		}
		if !replaced {
			result = append(result, sandboxPluginConfig)
			replaced = true
		}
	}
	if !replaced {
		result = append(result, sandboxPluginConfig)
	}
	return result
}

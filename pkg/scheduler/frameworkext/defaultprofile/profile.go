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
	kubeschedulerconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/defaultprebind"
)

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

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
	"testing"

	"github.com/stretchr/testify/assert"
	kubeschedulerconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/defaultprebind"
)

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

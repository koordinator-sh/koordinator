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

package frameworkext

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakePlugin implements framework.Plugin interface for testing
type fakePlugin struct {
	name string
}

func (f *fakePlugin) Name() string {
	return f.name
}

// fakeControllerPlugin implements both framework.Plugin and ControllerProvider for testing
type fakeControllerPlugin struct {
	name        string
	controllers []Controller
	err         error
}

func (f *fakeControllerPlugin) Name() string {
	return f.name
}

func (f *fakeControllerPlugin) NewControllers() ([]Controller, error) {
	return f.controllers, f.err
}

// fakeController implements Controller interface for testing
type fakeController struct {
	name    string
	started bool
}

func (f *fakeController) Name() string {
	return f.name
}

func (f *fakeController) Start() {
	f.started = true
}

func TestControllersMap_RegisterControllers(t *testing.T) {
	tests := []struct {
		name                       string
		plugins                    []*fakeControllerPlugin
		profileNames               []string
		expectedControllerCounts   []int // expected number of controllers for each plugin
		expectedTotalRegistrations int   // total number of unique plugins registered
	}{
		{
			name: "register single plugin with controllers",
			plugins: []*fakeControllerPlugin{
				{
					name: "plugin1",
					controllers: []Controller{
						&fakeController{name: "controller1"},
						&fakeController{name: "controller2"},
					},
				},
			},
			profileNames:               []string{"profile1"},
			expectedControllerCounts:   []int{2},
			expectedTotalRegistrations: 1,
		},
		{
			name: "register duplicate plugin should skip second registration",
			plugins: []*fakeControllerPlugin{
				{
					name: "plugin1",
					controllers: []Controller{
						&fakeController{name: "controller1"},
					},
				},
				{
					name: "plugin1", // duplicate
					controllers: []Controller{
						&fakeController{name: "controller2"},
					},
				},
			},
			profileNames:               []string{"profile1", "profile2"},
			expectedControllerCounts:   []int{1, 1}, // second registration should be skipped
			expectedTotalRegistrations: 1,
		},
		{
			name: "register different plugins should both succeed",
			plugins: []*fakeControllerPlugin{
				{
					name: "plugin1",
					controllers: []Controller{
						&fakeController{name: "controller1"},
					},
				},
				{
					name: "plugin2",
					controllers: []Controller{
						&fakeController{name: "controller2"},
					},
				},
			},
			profileNames:               []string{"profile1", "profile2"},
			expectedControllerCounts:   []int{1, 1},
			expectedTotalRegistrations: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewControllersMap()

			for i, plugin := range tt.plugins {
				cm.RegisterControllers(plugin, tt.profileNames[i])

				// Verify the controller count for this plugin
				pluginControllers := cm.controllers[plugin.Name()]
				assert.Equal(t, tt.expectedControllerCounts[i], len(pluginControllers),
					"plugin %s should have %d controllers", plugin.Name(), tt.expectedControllerCounts[i])
			}

			// Verify total number of unique plugins registered
			assert.Equal(t, tt.expectedTotalRegistrations, len(cm.controllers),
				"should have %d unique plugins registered", tt.expectedTotalRegistrations)
		})
	}
}

func TestControllersMap_RegisterControllers_NonControllerProvider(t *testing.T) {
	// Test that plugins not implementing ControllerProvider are ignored
	cm := NewControllersMap()

	plugin := &fakePlugin{name: "non-controller-plugin"}
	cm.RegisterControllers(plugin, "profile1")

	assert.Equal(t, 0, len(cm.controllers),
		"non-ControllerProvider plugin should not be registered")
}

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

package services

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// fakePlugin implements framework.Plugin interface for testing
type fakePlugin struct {
	name string
}

func (f *fakePlugin) Name() string {
	return f.name
}

// fakeServiceProvider implements both framework.Plugin and APIServiceProvider
type fakeServiceProvider struct {
	fakePlugin
	endpointsRegistered bool
}

func (f *fakeServiceProvider) RegisterEndpoints(group *gin.RouterGroup) {
	f.endpointsRegistered = true
	group.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"test": "ok"})
	})
}

func TestEngine_RegisterPluginService(t *testing.T) {
	tests := []struct {
		name                       string
		plugins                    []*fakeServiceProvider
		profileNames               []string
		expectedRegistrationCounts []bool // whether each registration should succeed
	}{
		{
			name: "register single plugin service",
			plugins: []*fakeServiceProvider{
				{fakePlugin: fakePlugin{name: "test-plugin-1"}},
			},
			profileNames:               []string{"default-scheduler"},
			expectedRegistrationCounts: []bool{true},
		},
		{
			name: "register duplicate plugin service should skip",
			plugins: []*fakeServiceProvider{
				{fakePlugin: fakePlugin{name: "test-plugin-1"}},
				{fakePlugin: fakePlugin{name: "test-plugin-1"}}, // duplicate
			},
			profileNames:               []string{"default-scheduler", "secondary-scheduler"},
			expectedRegistrationCounts: []bool{true, false}, // second should be skipped
		},
		{
			name: "register different plugins should both succeed",
			plugins: []*fakeServiceProvider{
				{fakePlugin: fakePlugin{name: "test-plugin-1"}},
				{fakePlugin: fakePlugin{name: "test-plugin-2"}},
			},
			profileNames:               []string{"default-scheduler", "default-scheduler"},
			expectedRegistrationCounts: []bool{true, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ginEngine := gin.New()
			engine := NewEngine(ginEngine)

			for i, plugin := range tt.plugins {
				engine.RegisterPluginService(plugin, tt.profileNames[i])

				if tt.expectedRegistrationCounts[i] {
					// Should be registered
					assert.True(t, plugin.endpointsRegistered, "plugin %s should have endpoints registered", plugin.Name())
					_, exists := engine.registeredProviders[plugin.Name()]
					assert.True(t, exists, "plugin %s should be in registeredProviders", plugin.Name())
				}
			}

			// Verify final state: each unique plugin name should be registered exactly once
			uniquePlugins := make(map[string]bool)
			for _, plugin := range tt.plugins {
				uniquePlugins[plugin.Name()] = true
			}
			assert.Equal(t, len(uniquePlugins), len(engine.registeredProviders),
				"registeredProviders should contain exactly %d unique plugins", len(uniquePlugins))
		})
	}
}

func TestEngine_RegisterPluginService_NonServiceProvider(t *testing.T) {
	// Test that plugins not implementing APIServiceProvider are ignored
	ginEngine := gin.New()
	engine := NewEngine(ginEngine)

	plugin := &fakePlugin{name: "non-service-plugin"}
	engine.RegisterPluginService(plugin, "default-scheduler")

	assert.Equal(t, 0, len(engine.registeredProviders),
		"non-APIServiceProvider plugin should not be registered")
}

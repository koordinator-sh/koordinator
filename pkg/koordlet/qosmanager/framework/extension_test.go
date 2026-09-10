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

package framework

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/component-base/featuregate"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type dynAwareTestPlugin struct {
	setupCalls int
	dynCalls   int
	dyn        dynamic.Interface
}

func (f *dynAwareTestPlugin) InitFlags(fs *flag.FlagSet) {}
func (f *dynAwareTestPlugin) Setup(client clientset.Interface, metricCache metriccache.MetricCache, statesInformer statesinformer.StatesInformer) {
	f.setupCalls++
}
func (f *dynAwareTestPlugin) Run(stopCh <-chan struct{}) {}
func (f *dynAwareTestPlugin) SetupDynamicClient(dyn dynamic.Interface) {
	f.dyn = dyn
	f.dynCalls++
}

type plainTestPlugin struct {
}

func (f *plainTestPlugin) InitFlags(fs *flag.FlagSet)                                {}
func (f *plainTestPlugin) Setup(client clientset.Interface, metricCache metriccache.MetricCache, statesInformer statesinformer.StatesInformer) {
}
func (f *plainTestPlugin) Run(stopCh <-chan struct{}) {}

func isolateExtensionGlobals(t *testing.T) {
	savedPlugins := globalExtensionPlugins
	savedFG := defaultQOSExtPluginsFG
	savedGate := DefaultMutableQOSExtPluginFG
	globalExtensionPlugins = map[featuregate.Feature]ExtensionPlugin{}
	defaultQOSExtPluginsFG = map[featuregate.Feature]featuregate.FeatureSpec{}
	DefaultMutableQOSExtPluginFG = featuregate.NewFeatureGate()
	DefaultQOSExtPluginsFG = DefaultMutableQOSExtPluginFG
	t.Cleanup(func() {
		globalExtensionPlugins = savedPlugins
		defaultQOSExtPluginsFG = savedFG
		DefaultMutableQOSExtPluginFG = savedGate
		DefaultQOSExtPluginsFG = savedGate
	})
}

func TestSetupPluginsWithDynamic(t *testing.T) {
	isolateExtensionGlobals(t)

	dynAware := &dynAwareTestPlugin{}
	plain := &plainTestPlugin{}
	assert.NoError(t, RegisterQOSExtPlugin(featuregate.Feature("test-dyn-aware"), featuregate.FeatureSpec{Default: false}, dynAware))
	assert.NoError(t, RegisterQOSExtPlugin(featuregate.Feature("test-plain"), featuregate.FeatureSpec{Default: false}, plain))

	// dyn == nil: SetupDynamicClient is never invoked; only Setup() runs.
	SetupPluginsWithDynamic(nil, nil, nil, nil)
	assert.Equal(t, 1, dynAware.setupCalls)
	assert.Equal(t, 0, dynAware.dynCalls)
	assert.Nil(t, dynAware.dyn)

	// with a real dynamic client: DynamicClientAware plugin gets the client.
	fakeDyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	SetupPluginsWithDynamic(nil, fakeDyn, nil, nil)
	assert.Equal(t, 2, dynAware.setupCalls)
	assert.Equal(t, 1, dynAware.dynCalls)
	assert.Equal(t, fakeDyn, dynAware.dyn)
}

func TestSetupPluginsNilDynamic(t *testing.T) {
	isolateExtensionGlobals(t)

	dynAware := &dynAwareTestPlugin{}
	assert.NoError(t, RegisterQOSExtPlugin(featuregate.Feature("test-dyn-nil"), featuregate.FeatureSpec{Default: false}, dynAware))

	// SetupPlugins delegates to SetupPluginsWithDynamic with nil dyn.
	SetupPlugins(nil, nil, nil)
	assert.Equal(t, 1, dynAware.setupCalls)
	assert.Nil(t, dynAware.dyn)
}
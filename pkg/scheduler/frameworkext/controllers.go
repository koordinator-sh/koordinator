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
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

var (
	ControllerPlugins = []string{"*"}
)

func isControllerPluginEnabled(pluginName string) bool {
	hasStar := false
	for _, p := range ControllerPlugins {
		if p == pluginName {
			return true
		}
		if p == "-"+pluginName {
			return false
		}
		if p == "*" {
			hasStar = true
		}
	}
	return hasStar
}

type ControllerProvider interface {
	NewControllers() ([]Controller, error)
}

type Controller interface {
	Start()
	Name() string
}

type ControllersMap struct {
	controllers map[string]map[string]Controller
}

func NewControllersMap() *ControllersMap {
	return &ControllersMap{
		controllers: make(map[string]map[string]Controller),
	}
}

func (cm *ControllersMap) RegisterControllers(plugin framework.Plugin) {
	controllerProvider, ok := plugin.(ControllerProvider)
	if !ok {
		return
	}
	pluginControllers := cm.controllers[plugin.Name()]
	if len(pluginControllers) > 0 {
		klog.Warningf("Plugin %s already build controllers, skip it", plugin.Name())
		return
	}

	pluginControllers = make(map[string]Controller)
	if controllers, err := controllerProvider.NewControllers(); err == nil {
		for _, controller := range controllers {
			if _, exist := pluginControllers[controller.Name()]; exist {
				klog.Warningf("controller: %v already registered", controller.Name())
				continue
			}
			pluginControllers[controller.Name()] = controller
			klog.V(4).Infof("register plugin:%v controller:%v", plugin.Name(), controller.Name())
		}
		cm.controllers[plugin.Name()] = pluginControllers
	}
}

func (cm *ControllersMap) Start() {
	for pluginName, plugin := range cm.controllers {
		if !isControllerPluginEnabled(pluginName) {
			klog.V(0).Infof("controller plugin %v is disabled, controllers %v are skipped", pluginName, plugin)
			continue
		}
		for _, controller := range plugin {
			controller.Start()
		}
	}
}

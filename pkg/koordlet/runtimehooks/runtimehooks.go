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

package runtimehooks

import (
	"reflect"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/server"
)

type HookPlugin interface {
	Register()
	SystemSupported() bool
}

type RuntimeHook interface {
	Run(stopCh <-chan struct{}) error
}

type runtimeHook struct {
	statesInformer statesinformer.StatesInformer
	server         server.Server
}

func (r *runtimeHook) Run(stopCh <-chan struct{}) error {
	registerPlugins()
	if err := r.server.Start(); err != nil {
		return err
	}
	<-stopCh
	klog.Infof("runtime hook is stopped")
	return nil
}

func NewRuntimeHook(si statesinformer.StatesInformer, cfg *Config) (RuntimeHook, error) {
	s, err := server.NewServer(server.Options{Network: cfg.RuntimeHooksNetwork, Address: cfg.RuntimeHooksAddr})
	if err != nil {
		return nil, err
	}
	r := &runtimeHook{
		statesInformer: si,
		server:         s,
	}
	si.RegisterCallbacks(reflect.TypeOf(&slov1alpha1.NodeSLO{}), "runtime-hooks-rule",
		"Update hooks rule can run callbacks if NodeSLO spec update",
		rule.UpdateRules)
	if err := s.Setup(); err != nil {
		klog.Fatal("failed to setup runtime hook server, error %v", err)
		return nil, err
	}
	return r, nil
}

func registerPlugins() {
	for hookFeature, hookPlugin := range runtimeHookPlugins {
		if DefaultRuntimeHooksFG.Enabled(hookFeature) {
			hookPlugin.Register()
			klog.Infof("runtime hook plugin %s has registered", hookFeature)
		}
	}
}

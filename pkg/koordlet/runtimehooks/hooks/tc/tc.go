//go:build !linux
// +build !linux

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

package tc

import (
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
)

const (
	name        = "TCInjection"
	description = "set tc rules for nodes"
)

type tcPlugin struct{}

func Object() *tcPlugin {
	return nil
}

func (n *tcPlugin) Reconcile() {
	klog.V(5).Info("net qos plugin start to reconcile in !linux os")
	return
}

func (n *tcPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", name)
}

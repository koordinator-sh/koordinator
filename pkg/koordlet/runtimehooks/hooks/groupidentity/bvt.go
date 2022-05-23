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

package groupidentity

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtime-manager/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	name        = "GroupIdentity"
	description = "set bvt value by priority and qos class"
)

type bvtPlugin struct {
	rule         *bvtRule
	ruleRWMutex  sync.RWMutex
	sysSupported *bool
}

func (b *bvtPlugin) Register() {
	klog.V(5).Infof("register hook %v", name)
	hooks.Register(rmconfig.PreRunPodSandbox, name, description, b.PreRunPodSandbox)
	rule.Register(name, description,
		rule.WithParseFunc(b.parseRule),
		rule.WithUpdateCallback(b.ruleUpdateCb),
		rule.WithSystemSupported(b.SystemSupported))
}

func (b *bvtPlugin) SystemSupported() bool {
	if b.sysSupported == nil {
		bvtFilePath := sysutil.GetCgroupFilePath(
			util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed), sysutil.CPUBVTWarpNs)
		b.sysSupported = pointer.BoolPtr(sysutil.FileExists(bvtFilePath))
		klog.Infof("update system supported info to %v for plugin %v", *b.sysSupported, name)
	}
	return *b.sysSupported
}

var singleton *bvtPlugin

func Object() *bvtPlugin {
	if singleton == nil {
		singleton = &bvtPlugin{rule: &bvtRule{}}
	}
	return singleton
}

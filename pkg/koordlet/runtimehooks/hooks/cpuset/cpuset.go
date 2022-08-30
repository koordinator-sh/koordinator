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

package cpuset

import (
	"fmt"
	"sync"

	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	sysutil "github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	name        = "CPUSetAllocator"
	description = "set cpuset value by pod allocation"
)

type cpusetPlugin struct {
	rule        *cpusetRule
	ruleRWMutex sync.RWMutex
}

func (p *cpusetPlugin) Register() {
	klog.V(5).Infof("register hook %v", name)
	hooks.Register(rmconfig.PreCreateContainer, name, description, p.SetContainerCPUSet)
	hooks.Register(rmconfig.PreUpdateContainerResources, name, description, p.SetContainerCPUSet)
	rule.Register(name, description,
		rule.WithParseFunc(statesinformer.RegisterTypeNodeTopology, p.parseRule),
		rule.WithUpdateCallback(p.ruleUpdateCb))
	reconciler.RegisterCgroupReconciler(reconciler.ContainerLevel, sysutil.CPUSet, p.SetContainerCPUSet,
		"set container cpuset")
}

var singleton *cpusetPlugin

func Object() *cpusetPlugin {
	if singleton == nil {
		singleton = &cpusetPlugin{}
	}
	return singleton
}

func (p *cpusetPlugin) SetContainerCPUSet(proto protocol.HooksProtocol) error {
	// TODO maybe consider support cpu-static policy for kubelet by refreshing cpuset of kubepods-burstable dir
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin %v", name)
	}
	containerReq := containerCtx.Request

	// cpuset from pod annotation (LSE, LSR)
	if cpusetVal, err := getCPUSetFromPod(containerReq.PodAnnotations); err != nil {
		return err
	} else if cpusetVal != "" {
		containerCtx.Response.Resources.CPUSet = pointer.StringPtr(cpusetVal)
		return nil
	}

	// use cpushare pool for pod
	r := p.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}
	cpusetValue, err := r.getContainerCPUSet(&containerReq)
	if err != nil {
		return err
	}
	containerCtx.Response.Resources.CPUSet = cpusetValue
	return nil
}

func getCPUSetFromPod(podAnnotations map[string]string) (string, error) {
	podAlloc, err := ext.GetResourceStatus(podAnnotations)
	if err != nil {
		return "", err
	}
	return podAlloc.CPUSet, nil
}

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

package hooks

import (
	"fmt"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	proxyconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

var globalStageHooks map[proxyconfig.RuntimeHookType][]*Hook

type Options struct {
	Executor resourceexecutor.ResourceUpdateExecutor
}

type Hook struct {
	name        string
	stage       proxyconfig.RuntimeHookType
	description string
	fn          HookFn
}

type HookFn func(protocol.HooksProtocol) error

func init() {
	globalStageHooks = map[proxyconfig.RuntimeHookType][]*Hook{
		proxyconfig.PreRunPodSandbox:            make([]*Hook, 0),
		proxyconfig.PreCreateContainer:          make([]*Hook, 0),
		proxyconfig.PreStartContainer:           make([]*Hook, 0),
		proxyconfig.PostStartContainer:          make([]*Hook, 0),
		proxyconfig.PostStopContainer:           make([]*Hook, 0),
		proxyconfig.PostStopPodSandbox:          make([]*Hook, 0),
		proxyconfig.PreUpdateContainerResources: make([]*Hook, 0),
	}
}

func Register(stage proxyconfig.RuntimeHookType, name, description string, hookFn HookFn) error {
	stageHooks, exist := globalStageHooks[stage]
	if !exist {
		return fmt.Errorf("stage %s is invalid", stage)
	}

	for _, hook := range stageHooks {
		if hook.name == name {
			return fmt.Errorf("a hook named %v already exists", name)
		}
	}

	h := &Hook{
		name:        name,
		stage:       stage,
		description: description,
		fn:          hookFn,
	}

	globalStageHooks[stage] = append(globalStageHooks[stage], h)
	return nil
}

func getHooksByStage(stage proxyconfig.RuntimeHookType) []*Hook {
	hooks, exist := globalStageHooks[stage]
	if !exist {
		return nil
	}
	return hooks
}

func RunHooks(failPolicy proxyconfig.FailurePolicyType, stage proxyconfig.RuntimeHookType, protocol protocol.HooksProtocol) error {
	hooks := getHooksByStage(stage)
	klog.Infof("start run %v hooks at %s", len(hooks), stage)
	for _, hook := range hooks {
		klog.V(5).Infof("start calling hook %v at %v stage.", hook.name, stage)
		if err := hook.fn(protocol); err != nil {
			klog.Errorf("failed to run hook %s at stage %s, reason: %v", hook.name, stage, err)
			if failPolicy == proxyconfig.PolicyFail {
				return err
			}
		}
	}
	return nil
}

func GetStages(disabled map[string]struct{}) []proxyconfig.RuntimeHookType {
	var stages []proxyconfig.RuntimeHookType
	for stage, hooks := range globalStageHooks {
		if _, ok := disabled[string(stage)]; ok {
			continue
		}
		if len(hooks) > 0 {
			stages = append(stages, stage)
		}
	}
	return stages
}
